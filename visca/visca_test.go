package visca

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// newPipedClient returns a Client wired to one end of a net.Pipe, plus the
// far end so tests can observe exactly what bytes hit the wire. We construct
// the Client directly (in-package test) instead of going through Connect(),
// which needs a live UDP peer — the framing and command layers only care
// that c.conn satisfies net.Conn.
func newPipedClient(t *testing.T) (*Client, net.Conn) {
	t.Helper()
	clientEnd, serverEnd := net.Pipe()
	t.Cleanup(func() {
		clientEnd.Close()
		serverEnd.Close()
	})
	c := &Client{addr: "test:0", conn: clientEnd, connected: true}
	return c, serverEnd
}

// captureFrame reads one frame from the far end of the pipe in the background.
// net.Pipe writes are synchronous (Write blocks until Read consumes it), so
// the reader must already be running before the command under test is sent.
// A single Write maps to a single Read here because our buffer exceeds any
// VISCA frame size (max payload 16 bytes + 8 header).
func captureFrame(serverEnd net.Conn) <-chan []byte {
	ch := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := serverEnd.Read(buf)
		if err != nil {
			close(ch)
			return
		}
		ch <- buf[:n]
	}()
	return ch
}

func TestBuildFrameHeader(t *testing.T) {
	c := &Client{}
	payload := []byte{0x81, 0x01, 0x06, 0x04, 0xFF} // Home command

	frame := c.buildFrame(payload)

	if len(frame) != 8+len(payload) {
		t.Fatalf("frame length = %d, want %d", len(frame), 8+len(payload))
	}
	// Message type 0x0100 = VISCA command
	if frame[0] != 0x01 || frame[1] != 0x00 {
		t.Errorf("message type = % X, want 01 00", frame[0:2])
	}
	if gotLen := binary.BigEndian.Uint16(frame[2:4]); gotLen != uint16(len(payload)) {
		t.Errorf("payload length field = %d, want %d", gotLen, len(payload))
	}
	if !bytes.Equal(frame[8:], payload) {
		t.Errorf("payload = % X, want % X", frame[8:], payload)
	}
}

func TestBuildFrameSequenceIncrements(t *testing.T) {
	c := &Client{}
	// Sequence numbers must be monotonically increasing from 0 — cameras use
	// them to detect duplicated/reordered UDP datagrams.
	for want := range uint32(3) {
		frame := c.buildFrame([]byte{0xFF})
		if got := binary.BigEndian.Uint32(frame[4:8]); got != want {
			t.Fatalf("sequence = %d, want %d", got, want)
		}
	}
}

// TestCommandPayloads checks the exact VISCA bytes each command puts on the
// wire, per the VISCA specification. Catching a wrong byte here is far cheaper
// than debugging a camera that silently ignores malformed commands.
func TestCommandPayloads(t *testing.T) {
	cases := []struct {
		name string
		send func(c *Client) error
		want []byte
	}{
		{
			name: "PanTilt left-up",
			send: func(c *Client) error { return c.PanTilt(DirLeft, DirUp, 0x0A, 0x09) },
			want: []byte{0x81, 0x01, 0x06, 0x01, 0x0A, 0x09, DirLeft, DirUp, 0xFF},
		},
		{
			name: "Stop is neutral pan/tilt",
			send: func(c *Client) error { return c.Stop() },
			want: []byte{0x81, 0x01, 0x06, 0x01, 0x08, 0x08, DirStop, DirStop, 0xFF},
		},
		{
			name: "Home",
			send: func(c *Client) error { return c.Home() },
			want: []byte{0x81, 0x01, 0x06, 0x04, 0xFF},
		},
		{
			name: "ZoomIn speed 3",
			send: func(c *Client) error { return c.ZoomIn(3) },
			want: []byte{0x81, 0x01, 0x04, 0x07, 0x23, 0xFF},
		},
		{
			name: "ZoomIn clamps speed to 7",
			send: func(c *Client) error { return c.ZoomIn(99) },
			want: []byte{0x81, 0x01, 0x04, 0x07, 0x27, 0xFF},
		},
		{
			name: "ZoomOut speed 5",
			send: func(c *Client) error { return c.ZoomOut(5) },
			want: []byte{0x81, 0x01, 0x04, 0x07, 0x35, 0xFF},
		},
		{
			name: "ZoomOut clamps speed to 7",
			send: func(c *Client) error { return c.ZoomOut(200) },
			want: []byte{0x81, 0x01, 0x04, 0x07, 0x37, 0xFF},
		},
		{
			name: "ZoomStop",
			send: func(c *Client) error { return c.ZoomStop() },
			want: []byte{0x81, 0x01, 0x04, 0x07, 0x00, 0xFF},
		},
		{
			name: "PresetSet slot 4",
			send: func(c *Client) error { return c.PresetSet(4) },
			want: []byte{0x81, 0x01, 0x04, 0x3F, 0x01, 0x04, 0xFF},
		},
		{
			name: "PresetRecall slot 9",
			send: func(c *Client) error { return c.PresetRecall(9) },
			want: []byte{0x81, 0x01, 0x04, 0x3F, 0x02, 0x09, 0xFF},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, serverEnd := newPipedClient(t)
			frames := captureFrame(serverEnd)

			if err := tc.send(c); err != nil {
				t.Fatalf("send: %v", err)
			}

			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatal("pipe closed before frame arrived")
				}
				if len(frame) < 8 {
					t.Fatalf("frame too short: % X", frame)
				}
				if got := frame[8:]; !bytes.Equal(got, tc.want) {
					t.Errorf("payload = % X, want % X", got, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for frame")
			}
		})
	}
}

func TestSendWhenNotConnected(t *testing.T) {
	c := NewClient("192.0.2.1", DefaultPort) // never connected
	if err := c.Home(); err == nil {
		t.Fatal("expected error sending on a disconnected client, got nil")
	}
}

// TestConnect exercises the real UDP handshake against a fake camera:
// Connect must send IF_Clear and only report success after a reply, since a
// bare UDP dial succeeds even when nothing is listening.
func TestConnect(t *testing.T) {
	fakeCam, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer fakeCam.Close()

	received := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, addr, err := fakeCam.ReadFrom(buf)
		if err != nil {
			close(received)
			return
		}
		received <- append([]byte(nil), buf[:n]...)
		// Any datagram back satisfies the liveness check — reply with an ACK
		// (90 4x FF) framed as a VISCA-over-IP reply message.
		ack := []byte{0x01, 0x11, 0x00, 0x03, 0x00, 0x00, 0x00, 0x00, 0x90, 0x41, 0xFF}
		_, _ = fakeCam.WriteTo(ack, addr)
	}()

	port := fakeCam.LocalAddr().(*net.UDPAddr).Port
	c := NewClient("127.0.0.1", port)
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	if !c.IsConnected() {
		t.Error("IsConnected = false after successful Connect")
	}

	frame, ok := <-received
	if !ok {
		t.Fatal("fake camera read failed")
	}
	wantIFClear := []byte{0x81, 0x01, 0x00, 0x01, 0xFF}
	if len(frame) != 8+len(wantIFClear) || !bytes.Equal(frame[8:], wantIFClear) {
		t.Errorf("handshake frame = % X, want IF_Clear payload % X", frame, wantIFClear)
	}
	// Sequence must reset to 0 on (re)connect so the camera's dedup window
	// doesn't drop the first commands of a new session.
	if got := binary.BigEndian.Uint32(frame[4:8]); got != 0 {
		t.Errorf("handshake sequence = %d, want 0", got)
	}
}
