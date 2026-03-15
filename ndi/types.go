// types.go defines the Previewer interface and PreviewOptions used by both
// the real implementation (preview.go, build tag ndi) and the stub (stub.go, !ndi).
package ndi

// PreviewOptions controls which preview strategies are tried and
// provides OBS WebSocket credentials so they don't have to come from env vars.
type PreviewOptions struct {
	EnableNDI  bool
	EnableOBS  bool
	EnableHTTP bool
	EnableRTSP bool
	OBSHost    string
	OBSPassword string
}

// Previewer captures video frames from a camera for live preview.
type Previewer interface {
	// Start begins capturing preview frames using enabled strategies.
	// cameraIP is the VISCA camera address (e.g. "192.168.1.100").
	Start(cameraIP string, opts PreviewOptions) error
	Stop()
	Frame() []byte
	Available() bool
}
