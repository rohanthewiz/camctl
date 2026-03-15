//go:build !ndi

package ndi

import "fmt"

type stub struct{}

// NewPreviewer returns a stub previewer when built without the ndi tag.
func NewPreviewer() Previewer { return &stub{} }

func (s *stub) Start(cameraIP string, opts PreviewOptions) error {
	return fmt.Errorf("NDI preview not available (build with -tags ndi)")
}
func (s *stub) Stop()           {}
func (s *stub) Frame() []byte   { return nil }
func (s *stub) Available() bool { return false }
