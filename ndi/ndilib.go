// Minimal NDI bindings via purego — receive-only, no send/routing symbols required.
package ndi

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// NDI function pointers — only find + recv subset.
var (
	ndilib_initialize              func() bool
	ndilib_find_create_v2          func(settings uintptr) uintptr
	ndilib_find_destroy            func(instance uintptr)
	ndilib_find_get_current_sources func(instance uintptr, numSources uintptr) uintptr
	ndilib_find_wait_for_sources   func(instance uintptr, timeout uint32) bool
	ndilib_recv_create_v3          func(settings uintptr) uintptr
	ndilib_recv_destroy            func(instance uintptr)
	ndilib_recv_capture_v2         func(instance uintptr, vf uintptr, af uintptr, mf uintptr, timeout uint32) int32
	ndilib_recv_free_video_v2      func(instance uintptr, frame uintptr)
)

var (
	ndiInitOnce sync.Once
	ndiInitErr  error
)

func ndiLibraryPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/usr/local/lib/libndi.dylib"
	case "linux":
		return "libndi.so"
	default:
		return ""
	}
}

func initNDI() error {
	path := ndiLibraryPath()
	if path == "" {
		return fmt.Errorf("NDI not supported on %s", runtime.GOOS)
	}

	lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("dlopen %s: %w", path, err)
	}

	purego.RegisterLibFunc(&ndilib_initialize, lib, "NDIlib_initialize")
	purego.RegisterLibFunc(&ndilib_find_create_v2, lib, "NDIlib_find_create_v2")
	purego.RegisterLibFunc(&ndilib_find_destroy, lib, "NDIlib_find_destroy")
	purego.RegisterLibFunc(&ndilib_find_get_current_sources, lib, "NDIlib_find_get_current_sources")
	purego.RegisterLibFunc(&ndilib_find_wait_for_sources, lib, "NDIlib_find_wait_for_sources")
	purego.RegisterLibFunc(&ndilib_recv_create_v3, lib, "NDIlib_recv_create_v3")
	purego.RegisterLibFunc(&ndilib_recv_destroy, lib, "NDIlib_recv_destroy")
	purego.RegisterLibFunc(&ndilib_recv_capture_v2, lib, "NDIlib_recv_capture_v2")
	purego.RegisterLibFunc(&ndilib_recv_free_video_v2, lib, "NDIlib_recv_free_video_v2")

	if !ndilib_initialize() {
		return errors.New("NDIlib_initialize returned false")
	}
	return nil
}

// ---------------------------------------------------------------------------
// C-compatible structs (must match NDI SDK memory layout)
// ---------------------------------------------------------------------------

type ndiSource struct {
	name    *byte
	address *byte
}

func (s *ndiSource) Name() string    { return goStr(s.name) }
func (s *ndiSource) Address() string { return goStr(s.address) }

type ndiFindSettings struct {
	showLocalSources bool
	groups           *byte
	extraIPs         *byte
}

type ndiRecvSettings struct {
	source           ndiSource
	colorFormat      int32
	bandwidth        int32
	allowVideoFields bool
	name             *byte
}

type ndiVideoFrame struct {
	Xres, Yres         int32
	FourCC              [4]byte
	FrameRateN, FrameRateD int32
	PictureAspectRatio  float32
	FrameFormatType     int32
	Timecode            int64
	Data                *byte
	LineStride          int32
	Metadata            *byte
	Timestamp           int64
}

// NDI frame type constants.
const (
	ndiFrameTypeNone  int32 = 0
	ndiFrameTypeVideo int32 = 1
	ndiFrameTypeError int32 = 4
)

// NDI color format / bandwidth constants.
const (
	ndiColorRGBXRGBA    int32 = 2
	ndiBandwidthLowest  int32 = 0
)

// ---------------------------------------------------------------------------
// High-level helpers
// ---------------------------------------------------------------------------

func ndiFindSources(cameraIP string) ([]ndiSource, func()) {
	settings := ndiFindSettings{showLocalSources: true}
	if cameraIP != "" {
		settings.extraIPs = cStr(cameraIP)
	}
	inst := ndilib_find_create_v2(uintptr(unsafe.Pointer(&settings)))
	if inst == 0 {
		return nil, func() {}
	}

	ndilib_find_wait_for_sources(inst, 5000)

	var count uint32
	ptr := ndilib_find_get_current_sources(inst, uintptr(unsafe.Pointer(&count)))

	sources := make([]ndiSource, count)
	if count > 0 {
		base := *(*unsafe.Pointer)(unsafe.Pointer(&ptr))
		for i := uint32(0); i < count; i++ {
			src := (*ndiSource)(unsafe.Add(base, uintptr(i)*unsafe.Sizeof(ndiSource{})))
			sources[i] = *src
		}
	}
	return sources, func() { ndilib_find_destroy(inst) }
}

func ndiCreateRecv(source *ndiSource) uintptr {
	name := cStr("camctl-preview")
	settings := ndiRecvSettings{
		source:           *source,
		colorFormat:      ndiColorRGBXRGBA,
		bandwidth:        ndiBandwidthLowest,
		allowVideoFields: true,
		name:             name,
	}
	return ndilib_recv_create_v3(uintptr(unsafe.Pointer(&settings)))
}

// ---------------------------------------------------------------------------
// String helpers
// ---------------------------------------------------------------------------

func cStr(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

func goStr(p *byte) string {
	if p == nil {
		return ""
	}
	ptr := unsafe.Pointer(p)
	n := 0
	for {
		if *(*byte)(unsafe.Add(ptr, uintptr(n))) == 0 {
			break
		}
		n++
	}
	return string(unsafe.Slice((*byte)(ptr), n))
}
