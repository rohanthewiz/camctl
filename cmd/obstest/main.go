// obstest connects to an OBS WebSocket server, completes the handshake,
// fetches the current scene, and grabs a single screenshot to verify connectivity.
//
// Usage:
//
//	go run ./cmd/obstest              # defaults to localhost:4455
//	go run ./cmd/obstest 192.168.1.17:4455
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/rohanthewiz/rweb"
)

type obsMsg struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

type obsHello struct {
	ObsWebSocketVersion string `json:"obsWebSocketVersion"`
	RpcVersion          int    `json:"rpcVersion"`
	Auth                *struct {
		Challenge string `json:"challenge"`
		Salt      string `json:"salt"`
	} `json:"authentication"`
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func main() {
	host := "localhost:4455"
	if len(os.Args) > 1 {
		host = os.Args[1]
	}

	fmt.Printf("Connecting to OBS WebSocket at %s ...\n", host)

	// TCP connect
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		fmt.Printf("FAIL: TCP connect: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("OK: TCP connected")

	// WebSocket upgrade
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
		fmt.Printf("FAIL: send upgrade: %v\n", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "101") {
		fmt.Printf("FAIL: upgrade response: %s\n", strings.TrimSpace(statusLine))
		os.Exit(1)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}
	fmt.Println("OK: WebSocket upgraded")

	ws := rweb.NewWSConn(&bufferedConn{Conn: conn, r: reader}, false)
	defer ws.Close(1000, "done")

	// Read Hello
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err := ws.ReadMessage()
	if err != nil {
		fmt.Printf("FAIL: read Hello: %v\n", err)
		os.Exit(1)
	}

	var env obsMsg
	json.Unmarshal(msg.Data, &env)
	if env.Op != 0 {
		fmt.Printf("FAIL: expected Hello (op 0), got op %d\n", env.Op)
		os.Exit(1)
	}

	var hello obsHello
	json.Unmarshal(env.D, &hello)
	fmt.Printf("OK: Hello received (OBS %s, rpc v%d)\n", hello.ObsWebSocketVersion, hello.RpcVersion)

	if hello.Auth != nil {
		fmt.Println("FAIL: OBS requires authentication — disable it in OBS → Tools → WebSocket Server Settings")
		os.Exit(1)
	}

	// Send Identify
	identData, _ := json.Marshal(map[string]int{"rpcVersion": 1})
	identMsg, _ := json.Marshal(obsMsg{Op: 1, D: identData})
	if err := ws.WriteMessage(rweb.TextMessage, identMsg); err != nil {
		fmt.Printf("FAIL: send Identify: %v\n", err)
		os.Exit(1)
	}

	// Read Identified
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err = ws.ReadMessage()
	if err != nil {
		fmt.Printf("FAIL: read Identified: %v\n", err)
		os.Exit(1)
	}
	json.Unmarshal(msg.Data, &env)
	if env.Op != 2 {
		fmt.Printf("FAIL: identify rejected (op %d): %s\n", env.Op, string(env.D))
		os.Exit(1)
	}
	fmt.Println("OK: Identified (authenticated)")

	// Get current program scene
	sendRequest(ws, "GetCurrentProgramScene", "scene", nil)
	resp := readResponse(ws, "scene")

	var sceneData struct {
		Name string `json:"currentProgramSceneName"`
	}
	json.Unmarshal(resp, &sceneData)
	if sceneData.Name == "" {
		fmt.Println("FAIL: empty scene name")
		os.Exit(1)
	}
	fmt.Printf("OK: Current scene: %q\n", sceneData.Name)

	// Get a screenshot
	sendRequest(ws, "GetSourceScreenshot", "ss", map[string]interface{}{
		"sourceName":              sceneData.Name,
		"imageFormat":             "jpg",
		"imageWidth":              480,
		"imageHeight":             270,
		"imageCompressionQuality": 60,
	})
	resp = readResponse(ws, "ss")

	var ssData struct {
		ImageData string `json:"imageData"`
	}
	json.Unmarshal(resp, &ssData)

	if ssData.ImageData == "" {
		fmt.Println("FAIL: empty screenshot data")
		os.Exit(1)
	}

	idx := strings.Index(ssData.ImageData, ",")
	if idx < 0 {
		fmt.Println("FAIL: invalid data URI")
		os.Exit(1)
	}

	imgBytes, err := base64.StdEncoding.DecodeString(ssData.ImageData[idx+1:])
	if err != nil {
		fmt.Printf("FAIL: decode base64: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("OK: Screenshot received (%d bytes JPEG)\n", len(imgBytes))

	// Save to file for visual verification
	os.WriteFile("obs_test_screenshot.jpg", imgBytes, 0644)
	fmt.Println("OK: Saved to obs_test_screenshot.jpg")
	fmt.Println("\nAll checks passed!")
}

func sendRequest(ws *rweb.WSConn, reqType, reqId string, data interface{}) {
	r := map[string]interface{}{
		"requestType": reqType,
		"requestId":   reqId,
	}
	if data != nil {
		r["requestData"] = data
	}
	rd, _ := json.Marshal(r)
	out, _ := json.Marshal(obsMsg{Op: 6, D: rd})
	if err := ws.WriteMessage(rweb.TextMessage, out); err != nil {
		fmt.Printf("FAIL: send %s: %v\n", reqType, err)
		os.Exit(1)
	}
}

func readResponse(ws *rweb.WSConn, requestId string) json.RawMessage {
	ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < 50; i++ {
		msg, err := ws.ReadMessage()
		if err != nil {
			fmt.Printf("FAIL: read response: %v\n", err)
			os.Exit(1)
		}
		var env obsMsg
		json.Unmarshal(msg.Data, &env)
		if env.Op != 7 {
			continue // skip events
		}
		var resp struct {
			RequestId string `json:"requestId"`
			Status    struct {
				Result bool `json:"result"`
				Code   int  `json:"code"`
			} `json:"requestStatus"`
			Data json.RawMessage `json:"responseData"`
		}
		json.Unmarshal(env.D, &resp)
		if resp.RequestId != requestId {
			continue
		}
		if !resp.Status.Result {
			fmt.Printf("FAIL: %s request failed (code %d)\n", requestId, resp.Status.Code)
			os.Exit(1)
		}
		return resp.Data
	}
	fmt.Printf("FAIL: no response for %s\n", requestId)
	os.Exit(1)
	return nil
}
