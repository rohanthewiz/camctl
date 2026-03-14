//go:build ndi

package ndi

/*
#cgo darwin LDFLAGS: -L/usr/local/lib -lndi -Wl,-rpath,/usr/local/lib
#cgo linux LDFLAGS: -lndi

#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>

// NDI type definitions matching the NDI SDK v5+ ABI.
// These are declared inline so the NDI SDK headers are not required at build time;
// only the runtime library (libndi) must be installed.

typedef void* NDIlib_find_instance_t;
typedef void* NDIlib_recv_instance_t;

typedef struct {
	const char* p_ndi_name;
	const char* p_url_address;
} NDIlib_source_t;

typedef struct {
	bool        show_local_sources;
	const char* p_groups;
	const char* p_extra_ips;
} NDIlib_find_create_t;

typedef enum {
	NDIlib_recv_bandwidth_metadata_only = -10,
	NDIlib_recv_bandwidth_audio_only    = 10,
	NDIlib_recv_bandwidth_lowest        = 0,
	NDIlib_recv_bandwidth_highest       = 100
} NDIlib_recv_bandwidth_e;

typedef enum {
	NDIlib_recv_color_format_BGRX_BGRA = 0,
	NDIlib_recv_color_format_UXVY_BGRA = 1,
	NDIlib_recv_color_format_RGBX_RGBA = 2,
	NDIlib_recv_color_format_UXVY_RGBA = 3,
	NDIlib_recv_color_format_fastest    = 100,
	NDIlib_recv_color_format_best       = 101
} NDIlib_recv_color_format_e;

typedef struct {
	NDIlib_source_t             source_to_connect_to;
	NDIlib_recv_color_format_e  color_format;
	NDIlib_recv_bandwidth_e     bandwidth;
	bool                        allow_video_fields;
	const char*                 p_ndi_recv_name;
} NDIlib_recv_create_v3_t;

typedef enum {
	NDIlib_FourCC_type_UYVY = 0x59565955,
	NDIlib_FourCC_type_BGRA = 0x41524742,
	NDIlib_FourCC_type_BGRX = 0x58524742,
	NDIlib_FourCC_type_RGBA = 0x41424752,
	NDIlib_FourCC_type_RGBX = 0x58424752
} NDIlib_FourCC_video_type_e;

typedef enum {
	NDIlib_frame_format_type_interleaved = 0,
	NDIlib_frame_format_type_progressive = 1,
	NDIlib_frame_format_type_field_0     = 2,
	NDIlib_frame_format_type_field_1     = 3
} NDIlib_frame_format_type_e;

typedef struct {
	int                              xres, yres;
	NDIlib_FourCC_video_type_e       FourCC;
	float                            frame_rate_N, frame_rate_D;
	float                            picture_aspect_ratio;
	NDIlib_frame_format_type_e       frame_format_type;
	int64_t                          timecode;
	uint8_t*                         p_data;
	int                              line_stride_in_bytes;
	const char*                      p_metadata;
	int64_t                          timestamp;
} NDIlib_video_frame_v2_t;

typedef enum {
	NDIlib_frame_type_none          = 0,
	NDIlib_frame_type_video         = 1,
	NDIlib_frame_type_audio         = 2,
	NDIlib_frame_type_metadata      = 3,
	NDIlib_frame_type_error         = 4,
	NDIlib_frame_type_status_change = 100
} NDIlib_frame_type_e;

extern bool                    NDIlib_initialize(void);
extern void                    NDIlib_destroy(void);
extern NDIlib_find_instance_t  NDIlib_find_create_v2(const NDIlib_find_create_t* p_create_settings);
extern void                    NDIlib_find_destroy(NDIlib_find_instance_t p_instance);
extern bool                    NDIlib_find_wait_for_sources(NDIlib_find_instance_t p_instance, uint32_t timeout_in_ms);
extern const NDIlib_source_t*  NDIlib_find_get_current_sources(NDIlib_find_instance_t p_instance, uint32_t* p_no_sources);
extern NDIlib_recv_instance_t  NDIlib_recv_create_v3(const NDIlib_recv_create_v3_t* p_create_settings);
extern void                    NDIlib_recv_destroy(NDIlib_recv_instance_t p_instance);
extern void                    NDIlib_recv_connect(NDIlib_recv_instance_t p_instance, const NDIlib_source_t* p_src);
extern NDIlib_frame_type_e     NDIlib_recv_capture_v2(NDIlib_recv_instance_t p_instance, NDIlib_video_frame_v2_t* p_video_data, void* p_audio_data, void* p_metadata, uint32_t timeout_in_ms);
extern void                    NDIlib_recv_free_video_v2(NDIlib_recv_instance_t p_instance, const NDIlib_video_frame_v2_t* p_video_data);
*/
import "C"

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"sync"
	"unsafe"
)

type receiver struct {
	mu      sync.RWMutex
	frame   []byte
	running bool
	stopCh  chan struct{}
}

// NewPreviewer returns an NDI-capable previewer (requires libndi at runtime).
func NewPreviewer() Previewer {
	return &receiver{}
}

func (r *receiver) Available() bool { return true }

func (r *receiver) Frame() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.frame) == 0 {
		return nil
	}
	out := make([]byte, len(r.frame))
	copy(out, r.frame)
	return out
}

func (r *receiver) Stop() {
	r.mu.Lock()
	if r.running {
		close(r.stopCh)
		r.running = false
	}
	r.mu.Unlock()
}

func (r *receiver) Start(ip string) error {
	r.Stop()

	if !C.NDIlib_initialize() {
		return fmt.Errorf("failed to initialize NDI SDK")
	}

	// Discover NDI sources, hinting the camera's IP for faster discovery.
	extraIPs := C.CString(ip)
	defer C.free(unsafe.Pointer(extraIPs))

	findCreate := C.NDIlib_find_create_t{
		show_local_sources: C.bool(true),
		p_groups:           nil,
		p_extra_ips:        extraIPs,
	}

	finder := C.NDIlib_find_create_v2(&findCreate)
	if finder == nil {
		return fmt.Errorf("failed to create NDI finder")
	}

	// Wait up to 5 seconds for source discovery.
	C.NDIlib_find_wait_for_sources(finder, 5000)

	var numSources C.uint32_t
	sources := C.NDIlib_find_get_current_sources(finder, &numSources)
	if numSources == 0 {
		C.NDIlib_find_destroy(finder)
		return fmt.Errorf("no NDI sources found at %s", ip)
	}

	// Match source by camera IP; fall back to the first source.
	sourceSlice := unsafe.Slice(sources, int(numSources))
	matchIdx := 0
	for i := range sourceSlice {
		urlAddr := C.GoString(sourceSlice[i].p_url_address)
		if strings.Contains(urlAddr, ip) {
			matchIdx = i
			break
		}
	}

	// Create a low-bandwidth receiver for preview (RGBA format).
	recvName := C.CString("camctl-preview")
	defer C.free(unsafe.Pointer(recvName))

	recvCreate := C.NDIlib_recv_create_v3_t{
		source_to_connect_to: sourceSlice[matchIdx],
		color_format:         C.NDIlib_recv_color_format_RGBX_RGBA,
		bandwidth:            C.NDIlib_recv_bandwidth_lowest,
		allow_video_fields:   C.bool(false),
		p_ndi_recv_name:      recvName,
	}

	recv := C.NDIlib_recv_create_v3(&recvCreate)
	C.NDIlib_find_destroy(finder)

	if recv == nil {
		return fmt.Errorf("failed to create NDI receiver")
	}

	r.mu.Lock()
	r.stopCh = make(chan struct{})
	r.running = true
	r.mu.Unlock()

	go r.captureLoop(recv)
	return nil
}

// captureLoop reads video frames from the NDI receiver and encodes them as JPEG.
func (r *receiver) captureLoop(recv C.NDIlib_recv_instance_t) {
	defer C.NDIlib_recv_destroy(recv)

	for {
		select {
		case <-r.stopCh:
			return
		default:
		}

		var videoFrame C.NDIlib_video_frame_v2_t
		frameType := C.NDIlib_recv_capture_v2(recv, &videoFrame, nil, nil, 500)

		if frameType == C.NDIlib_frame_type_video {
			if jpg := encodeFrame(&videoFrame); jpg != nil {
				r.mu.Lock()
				r.frame = jpg
				r.mu.Unlock()
			}
			C.NDIlib_recv_free_video_v2(recv, &videoFrame)
		}
	}
}

// encodeFrame converts an NDI RGBA video frame to a JPEG byte slice.
func encodeFrame(f *C.NDIlib_video_frame_v2_t) []byte {
	w := int(f.xres)
	h := int(f.yres)
	stride := int(f.line_stride_in_bytes)

	if w == 0 || h == 0 || f.p_data == nil {
		return nil
	}

	data := unsafe.Slice((*byte)(unsafe.Pointer(f.p_data)), stride*h)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		copy(img.Pix[y*img.Stride:y*img.Stride+w*4], data[y*stride:y*stride+w*4])
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
		return nil
	}
	return buf.Bytes()
}
