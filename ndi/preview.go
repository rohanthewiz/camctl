package ndi

// Previewer captures video frames from an NDI source for live preview.
type Previewer interface {
	// Start begins receiving NDI video from the camera at the given IP.
	Start(ip string) error
	// Stop stops the NDI receiver and releases resources.
	Stop()
	// Frame returns the latest JPEG-encoded frame, or nil if unavailable.
	Frame() []byte
	// Available reports whether NDI preview support is compiled in.
	Available() bool
}
