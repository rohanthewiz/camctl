// Package ndi provides a live camera preview. It tries four strategies
// in order: NDI direct, OBS WebSocket, HTTP snapshots, and RTSP via ffmpeg.
package ndi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/rohanthewiz/rweb"
)

// Previewer captures video frames from a camera for live preview.
type Previewer interface {
	// Start begins capturing preview frames.
	// cameraIP is the VISCA camera address; obsHost is the OBS WebSocket
	// address (e.g. "192.168.1.17:4455"). Either may be empty.
	Start(cameraIP, obsHost string) error
	Stop()
	Frame() []byte
	Available() bool
}

type previewer struct {
	mu      sync.RWMutex
	frame   []byte
	running bool
	stopCh  chan struct{}
	cancel  context.CancelFunc
}

// NewPreviewer returns a camera previewer.
func NewPreviewer() Previewer { return &previewer{} }

func (p *previewer) Available() bool { return true }

func (p *previewer) Frame() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.frame) == 0 {
		return nil
	}
	out := make([]byte, len(p.frame))
	copy(out, p.frame)
	return out
}

func (p *previewer) Stop() {
	p.mu.Lock()
	if p.running {
		close(p.stopCh)
		if p.cancel != nil {
			p.cancel()
			p.cancel = nil
		}
		p.running = false
		p.frame = nil
	}
	p.mu.Unlock()
}

func (p *previewer) start() (context.Context, context.CancelFunc) {
	p.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.stopCh = make(chan struct{})
	p.cancel = cancel
	p.running = true
	p.mu.Unlock()
	return ctx, cancel
}

func (p *previewer) Start(cameraIP, obsHost string) error {
	// Strategy 1: NDI direct from camera.
	if cameraIP != "" && p.tryNDI(cameraIP) {
		return nil
	}

	// Strategy 2: OBS WebSocket.
	// Build candidate list: configured host first, then fallbacks.
	var obsHosts []string
	if obsHost != "" {
		obsHosts = append(obsHosts, obsHost)
	}
	if h := os.Getenv("OBS_WS_HOST"); h != "" && h != obsHost {
		obsHosts = append(obsHosts, h)
	}
	obsHosts = append(obsHosts, "localhost:4455")
	if cameraIP != "" {
		obsHosts = append(obsHosts, cameraIP+":4455")
	}

	for _, host := range obsHosts {
		ws, scene, err := connectOBS(host)
		if err != nil {
			log.Printf("preview: OBS at %s: %v", host, err)
			continue
		}
		log.Printf("preview: connected to OBS at %s, scene=%q", host, scene)
		ctx, _ := p.start()
		go p.pollOBS(ctx, ws, scene)
		return nil
	}
	log.Printf("preview: OBS WebSocket unavailable")

	// Strategy 3: HTTP snapshot on camera IP.
	if cameraIP != "" {
		if url, ok := probeSnapshot(cameraIP); ok {
			log.Printf("preview: found snapshot at %s", url)
			ctx, _ := p.start()
			go p.pollSnapshot(ctx, url)
			return nil
		}
	}

	// Strategy 4: RTSP via ffmpeg.
	if cameraIP != "" {
		if _, err := exec.LookPath("ffmpeg"); err == nil {
			if rtspURL, err := probeRTSP(cameraIP); err == nil {
				log.Printf("preview: using RTSP at %s", rtspURL)
				ctx, _ := p.start()
				go p.captureRTSP(ctx, rtspURL)
				return nil
			}
		}
	}

	return fmt.Errorf("no preview source found (tried NDI, OBS WebSocket, HTTP snapshots, RTSP)")
}

// ---------------------------------------------------------------------------
// NDI direct capture
// ---------------------------------------------------------------------------

// tryNDI attempts to discover and connect to an NDI source at the given camera IP.
func (p *previewer) tryNDI(cameraIP string) bool {
	ndiInitOnce.Do(func() {
		ndiInitErr = initNDI()
		if ndiInitErr != nil {
			log.Printf("preview: NDI library init failed: %v", ndiInitErr)
		}
	})
	if ndiInitErr != nil {
		return false
	}

	sources, destroyFinder := ndiFindSources(cameraIP)
	defer destroyFinder()

	// Match source by camera IP.
	var matched *ndiSource
	for i := range sources {
		if strings.Contains(sources[i].Address(), cameraIP) {
			matched = &sources[i]
			break
		}
	}
	if matched == nil {
		log.Printf("preview: no NDI source found at %s (found %d sources)", cameraIP, len(sources))
		return false
	}

	recv := ndiCreateRecv(matched)
	if recv == 0 {
		log.Printf("preview: NDI receiver creation failed")
		return false
	}

	log.Printf("preview: connected to NDI source %q at %s", matched.Name(), matched.Address())
	ctx, _ := p.start()
	go p.captureNDI(ctx, recv)
	return true
}

// captureNDI runs the NDI frame capture loop.
func (p *previewer) captureNDI(ctx context.Context, recv uintptr) {
	defer ndilib_recv_destroy(recv)

	var vf ndiVideoFrame
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ft := ndilib_recv_capture_v2(recv, uintptr(unsafe.Pointer(&vf)), 0, 0, 500)
		switch ft {
		case ndiFrameTypeVideo:
			frame := encodeRGBAFrame(vf.Data, int(vf.Xres), int(vf.Yres), int(vf.LineStride))
			ndilib_recv_free_video_v2(recv, uintptr(unsafe.Pointer(&vf)))
			if frame != nil {
				p.mu.Lock()
				p.frame = frame
				p.mu.Unlock()
			}
		case ndiFrameTypeError:
			log.Printf("preview: NDI capture error, stopping")
			return
		}
	}
}

// encodeRGBAFrame converts raw RGBA pixel data to JPEG.
func encodeRGBAFrame(data *byte, width, height, stride int) []byte {
	if data == nil || width <= 0 || height <= 0 || stride <= 0 {
		return nil
	}
	size := stride * height
	raw := unsafe.Slice(data, size)

	img := &image.NRGBA{
		Pix:    raw,
		Stride: stride,
		Rect:   image.Rect(0, 0, width, height),
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// OBS WebSocket (v5, port 4455)
// ---------------------------------------------------------------------------

// OBS WebSocket v5 opcodes.
const (
	obsOpHello    = 0
	obsOpIdentify = 1
	obsOpRequest  = 6
	obsOpResponse = 7
)

type obsMsg struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

type obsHello struct {
	RpcVersion int `json:"rpcVersion"`
	Auth       *struct {
		Challenge string `json:"challenge"`
		Salt      string `json:"salt"`
	} `json:"authentication"`
}

type obsIdentify struct {
	RpcVersion int    `json:"rpcVersion"`
	Auth       string `json:"authentication,omitempty"`
}

type obsReq struct {
	RequestType string      `json:"requestType"`
	RequestId   string      `json:"requestId"`
	RequestData interface{} `json:"requestData,omitempty"`
}

type obsResp struct {
	RequestType string `json:"requestType"`
	RequestId   string `json:"requestId"`
	Status      struct {
		Result bool `json:"result"`
		Code   int  `json:"code"`
	} `json:"requestStatus"`
	Data json.RawMessage `json:"responseData"`
}

type screenshotReq struct {
	SourceName  string `json:"sourceName"`
	ImageFormat string `json:"imageFormat"`
	ImageWidth  int    `json:"imageWidth,omitempty"`
	ImageHeight int    `json:"imageHeight,omitempty"`
	Quality     int    `json:"imageCompressionQuality,omitempty"`
}

// bufferedConn wraps a net.Conn so reads go through a bufio.Reader,
// ensuring data buffered during the HTTP upgrade isn't lost.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// connectOBS dials the OBS WebSocket, completes the handshake,
// and returns the connection plus the current program scene name.
func connectOBS(host string) (*rweb.WSConn, string, error) {
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		return nil, "", fmt.Errorf("dial: %w", err)
	}

	// WebSocket HTTP upgrade.
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := "GET / HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, "", err
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, "", fmt.Errorf("upgrade failed: %s", strings.TrimSpace(statusLine))
	}
	// Drain response headers.
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}

	ws := rweb.NewWSConn(&bufferedConn{Conn: conn, r: reader}, false)

	// OBS Hello/Identify handshake.
	if err := obsHandshake(ws); err != nil {
		ws.Close(1000, "handshake failed")
		return nil, "", fmt.Errorf("handshake: %w", err)
	}

	// Get current program scene.
	scene, err := obsGetScene(ws)
	if err != nil {
		ws.Close(1000, "scene query failed")
		return nil, "", fmt.Errorf("get scene: %w", err)
	}

	return ws, scene, nil
}

func obsHandshake(ws *rweb.WSConn) error {
	// Read Hello.
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err := ws.ReadMessage()
	if err != nil {
		return err
	}

	var envelope obsMsg
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		return err
	}
	if envelope.Op != obsOpHello {
		return fmt.Errorf("expected Hello (op 0), got op %d", envelope.Op)
	}

	var hello obsHello
	json.Unmarshal(envelope.D, &hello)

	// Build Identify message.
	identify := obsIdentify{RpcVersion: 1}

	if hello.Auth != nil {
		pw := os.Getenv("OBS_WS_PASSWORD")
		if pw == "" {
			return fmt.Errorf("OBS requires authentication; set OBS_WS_PASSWORD env var")
		}
		identify.Auth = obsAuthString(pw, hello.Auth.Salt, hello.Auth.Challenge)
	}

	identData, _ := json.Marshal(identify)
	outMsg, _ := json.Marshal(obsMsg{Op: obsOpIdentify, D: identData})
	if err := ws.WriteMessage(rweb.TextMessage, outMsg); err != nil {
		return err
	}

	// Read Identified response.
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err = ws.ReadMessage()
	if err != nil {
		return err
	}
	json.Unmarshal(msg.Data, &envelope)
	if envelope.Op != 2 {
		return fmt.Errorf("identify failed (op %d): %s", envelope.Op, string(envelope.D))
	}

	ws.SetReadDeadline(time.Time{}) // clear deadline
	return nil
}

func obsAuthString(password, salt, challenge string) string {
	// secret = base64(sha256(password + salt))
	h := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(h[:])
	// auth = base64(sha256(secret + challenge))
	h2 := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(h2[:])
}

func obsSendRequest(ws *rweb.WSConn, reqType, reqId string, data interface{}) (*obsResp, error) {
	r := obsReq{RequestType: reqType, RequestId: reqId, RequestData: data}
	rd, _ := json.Marshal(r)
	out, _ := json.Marshal(obsMsg{Op: obsOpRequest, D: rd})

	if err := ws.WriteMessage(rweb.TextMessage, out); err != nil {
		return nil, err
	}

	// Read responses, skipping events, until we get our response.
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer ws.SetReadDeadline(time.Time{})

	for i := 0; i < 50; i++ { // safety limit
		msg, err := ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		var env obsMsg
		json.Unmarshal(msg.Data, &env)
		if env.Op != obsOpResponse {
			continue // skip events
		}
		var resp obsResp
		json.Unmarshal(env.D, &resp)
		if resp.RequestId == reqId {
			return &resp, nil
		}
	}
	return nil, fmt.Errorf("no response for %s", reqType)
}

func obsGetScene(ws *rweb.WSConn) (string, error) {
	resp, err := obsSendRequest(ws, "GetCurrentProgramScene", "scene", nil)
	if err != nil {
		return "", err
	}
	var d struct {
		Name string `json:"currentProgramSceneName"`
	}
	json.Unmarshal(resp.Data, &d)
	if d.Name == "" {
		return "", fmt.Errorf("empty scene name")
	}
	return d.Name, nil
}

func (p *previewer) pollOBS(ctx context.Context, ws *rweb.WSConn, scene string) {
	defer ws.Close(1000, "done")
	ticker := time.NewTicker(200 * time.Millisecond) // ~5fps
	defer ticker.Stop()

	reqID := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		reqID++
		resp, err := obsSendRequest(ws, "GetSourceScreenshot",
			fmt.Sprintf("ss%d", reqID),
			screenshotReq{
				SourceName:  scene,
				ImageFormat: "jpg",
				ImageWidth:  480,
				ImageHeight: 270,
				Quality:     60,
			})
		if err != nil {
			log.Printf("preview: OBS screenshot error: %v", err)
			return // connection likely dead; Start will be called again on reconnect
		}
		if !resp.Status.Result {
			continue
		}

		var d struct {
			ImageData string `json:"imageData"`
		}
		json.Unmarshal(resp.Data, &d)

		// Strip data URI prefix: "data:image/jpg;base64,..."
		idx := strings.Index(d.ImageData, ",")
		if idx < 0 {
			continue
		}
		frame, err := base64.StdEncoding.DecodeString(d.ImageData[idx+1:])
		if err != nil {
			continue
		}

		p.mu.Lock()
		p.frame = frame
		p.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// HTTP snapshot fallback
// ---------------------------------------------------------------------------

var snapshotPaths = []string{
	"/cgi-bin/snapshot.cgi", "/snapshot", "/shot.jpg", "/snap.jpg",
	"/tmpfs/snap.jpg", "/image/jpeg.cgi", "/cgi-bin/hi3510/snap.cgi",
	"/onvif-http/snapshot", "/cgi-bin/snapshot.cgi?stream=0",
}

func probeSnapshot(ip string) (string, bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range snapshotPaths {
		url := fmt.Sprintf("http://%s%s", ip, path)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		ct := resp.Header.Get("Content-Type")
		ok := strings.Contains(ct, "image/jpeg") || strings.Contains(ct, "image/jpg")
		resp.Body.Close()
		if resp.StatusCode == 200 && ok {
			return url, true
		}
	}
	return "", false
}

func (p *previewer) pollSnapshot(ctx context.Context, url string) {
	client := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			continue
		}
		p.mu.Lock()
		p.frame = data
		p.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// RTSP via ffmpeg fallback
// ---------------------------------------------------------------------------

var rtspPaths = []string{
	"/11", "/1", "/12", "/stream1", "/live/ch00_0",
	"/h264Preview_01_sub", "/cam/realmonitor?channel=1&subtype=1",
}

var jpegSOI = []byte{0xFF, 0xD8}
var jpegEOI = []byte{0xFF, 0xD9}

func probeRTSP(ip string) (string, error) {
	for _, path := range rtspPaths {
		url := fmt.Sprintf("rtsp://%s:554%s", ip, path)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, "ffmpeg",
			"-rtsp_transport", "tcp", "-i", url,
			"-vframes", "1", "-f", "image2pipe", "-vcodec", "mjpeg",
			"-q:v", "8", "-loglevel", "error", "pipe:1",
		).Output()
		cancel()
		if err == nil && len(out) > 100 {
			return url, nil
		}
	}
	return "", fmt.Errorf("no RTSP stream found on %s", ip)
}

func (p *previewer) captureRTSP(ctx context.Context, rtspURL string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		p.runFFmpeg(ctx, rtspURL)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (p *previewer) runFFmpeg(ctx context.Context, rtspURL string) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-rtsp_transport", "tcp", "-i", rtspURL,
		"-f", "image2pipe", "-vcodec", "mjpeg",
		"-q:v", "8", "-r", "5", "-loglevel", "error", "pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	reader := bufio.NewReaderSize(stdout, 512*1024)
	p.readFrames(ctx, reader)
	_ = cmd.Wait()
}

func (p *previewer) readFrames(ctx context.Context, reader *bufio.Reader) {
	var buf bytes.Buffer
	inFrame := false
	tmp := make([]byte, 32*1024)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := reader.Read(tmp)
		if n == 0 && err != nil {
			return
		}
		chunk := tmp[:n]
		for len(chunk) > 0 {
			if !inFrame {
				idx := bytes.Index(chunk, jpegSOI)
				if idx < 0 {
					break
				}
				buf.Reset()
				buf.Write(chunk[idx:])
				chunk = nil
				inFrame = true
			} else {
				idx := bytes.Index(chunk, jpegEOI)
				if idx < 0 {
					buf.Write(chunk)
					chunk = nil
				} else {
					buf.Write(chunk[:idx+2])
					chunk = chunk[idx+2:]
					inFrame = false
					frame := make([]byte, buf.Len())
					copy(frame, buf.Bytes())
					p.mu.Lock()
					p.frame = frame
					p.mu.Unlock()
				}
			}
		}
	}
}
