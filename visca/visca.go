package visca

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Default VISCA-over-IP port used by most NDI PTZ cameras
const DefaultPort = 52381

// Direction constants for pan/tilt commands.
// These map directly to VISCA protocol direction bytes.
const (
	DirLeft  = 0x01
	DirRight = 0x02
	DirUp    = 0x01
	DirDown  = 0x02
	DirStop  = 0x03 // neutral axis — no movement on this axis
)

// Client manages a VISCA-over-IP UDP connection to a single camera.
// All commands are framed with an 8-byte header (type, length, sequence number)
// before the raw VISCA payload, per the VISCA-over-IP specification.
type Client struct {
	conn      net.Conn
	addr      string // "host:port" of the camera
	seqNum    atomic.Uint32
	mu        sync.Mutex // serializes writes to the UDP socket
	connected bool
}

// NewClient creates a Client targeting the given camera address.
// It does NOT open a connection — call Connect() separately so the
// caller can handle connection errors at an appropriate time.
func NewClient(host string, port int) *Client {
	return &Client{
		addr: fmt.Sprintf("%s:%d", host, port),
	}
}

// Connect opens a UDP socket to the camera and verifies it responds
// by sending an IF_Clear command and waiting for an ACK.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := net.Dial("udp", c.addr)
	if err != nil {
		return fmt.Errorf("visca: dial %s: %w", c.addr, err)
	}
	c.conn = conn
	c.seqNum.Store(0) // reset sequence on new connection

	// Verify the camera is reachable by sending IF_Clear and waiting for a reply.
	// Without this check, UDP "connects" always succeed even if the camera is off.
	ifClear := c.buildFrame([]byte{0x81, 0x01, 0x00, 0x01, 0xFF})
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		conn.Close()
		return fmt.Errorf("visca: %w", err)
	}
	if _, err := conn.Write(ifClear); err != nil {
		conn.Close()
		return fmt.Errorf("visca: send IF_Clear to %s: %w", c.addr, err)
	}

	// Wait for any response — an ACK proves the camera is alive.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		conn.Close()
		return fmt.Errorf("visca: %w", err)
	}
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err != nil {
		conn.Close()
		return fmt.Errorf("visca: no response from %s (camera may be offline)", c.addr)
	}

	// Clear deadlines for normal operation.
	_ = conn.SetWriteDeadline(time.Time{})
	_ = conn.SetReadDeadline(time.Time{})

	c.connected = true
	return nil
}

// Close tears down the UDP socket.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected reports whether the client has an active connection.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Address returns the camera target address.
func (c *Client) Address() string {
	return c.addr
}

// --- VISCA-over-IP framing ---

// buildFrame wraps a raw VISCA payload in the 8-byte VISCA-over-IP header.
//
//	Bytes 0-1: Message type (0x0100 = command)
//	Bytes 2-3: Payload length (big-endian)
//	Bytes 4-7: Sequence number (big-endian, monotonically increasing)
func (c *Client) buildFrame(payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	// Message type: VISCA command
	frame[0] = 0x01
	frame[1] = 0x00
	// Payload length
	binary.BigEndian.PutUint16(frame[2:4], uint16(len(payload)))
	// Sequence number — atomically increment for each send
	seq := c.seqNum.Add(1) - 1
	binary.BigEndian.PutUint32(frame[4:8], seq)
	copy(frame[8:], payload)
	return frame
}

// send frames and transmits a raw VISCA command.
func (c *Client) send(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return fmt.Errorf("visca: not connected")
	}
	frame := c.buildFrame(payload)
	_, err := c.conn.Write(frame)
	return err
}

// --- Camera commands ---

// PanTilt sends a combined pan/tilt movement command.
//
//	panDir / tiltDir: use DirLeft, DirRight, DirUp, DirDown, DirStop
//	panSpeed:  0x01–0x18  (slowest to fastest)
//	tiltSpeed: 0x01–0x17
//
// Payload: 81 01 06 01 <panSpeed> <tiltSpeed> <panDir> <tiltDir> FF
func (c *Client) PanTilt(panDir, tiltDir, panSpeed, tiltSpeed byte) error {
	payload := []byte{0x81, 0x01, 0x06, 0x01, panSpeed, tiltSpeed, panDir, tiltDir, 0xFF}
	return c.send(payload)
}

// Stop halts all pan/tilt movement by sending neutral directions on both axes.
func (c *Client) Stop() error {
	return c.PanTilt(DirStop, DirStop, 0x08, 0x08)
}

// Home returns the camera to its factory-default center position.
// Payload: 81 01 06 04 FF
func (c *Client) Home() error {
	return c.send([]byte{0x81, 0x01, 0x06, 0x04, 0xFF})
}

// ZoomIn starts zooming toward telephoto at the given speed (0–7).
// Payload: 81 01 04 07 2p FF  where p = speed
func (c *Client) ZoomIn(speed byte) error {
	if speed > 7 {
		speed = 7
	}
	return c.send([]byte{0x81, 0x01, 0x04, 0x07, 0x20 | speed, 0xFF})
}

// ZoomOut starts zooming toward wide-angle at the given speed (0–7).
// Payload: 81 01 04 07 3p FF  where p = speed
func (c *Client) ZoomOut(speed byte) error {
	if speed > 7 {
		speed = 7
	}
	return c.send([]byte{0x81, 0x01, 0x04, 0x07, 0x30 | speed, 0xFF})
}

// ZoomStop halts any in-progress zoom movement.
// Payload: 81 01 04 07 00 FF
func (c *Client) ZoomStop() error {
	return c.send([]byte{0x81, 0x01, 0x04, 0x07, 0x00, 0xFF})
}

// PresetSet stores the current camera position into the given preset slot (0–127).
// Payload: 81 01 04 3F 01 pp FF
func (c *Client) PresetSet(num byte) error {
	return c.send([]byte{0x81, 0x01, 0x04, 0x3F, 0x01, num, 0xFF})
}

// PresetRecall moves the camera to a previously stored preset position (0–127).
// Payload: 81 01 04 3F 02 pp FF
func (c *Client) PresetRecall(num byte) error {
	return c.send([]byte{0x81, 0x01, 0x04, 0x3F, 0x02, num, 0xFF})
}
