// SPDX-License-Identifier: Unlicense OR MIT

//go:build darwin

package cpuimport

/*
#cgo CFLAGS: -Werror
#cgo LDFLAGS: -framework IOSurface -framework CoreFoundation

#include <IOSurface/IOSurfaceRef.h>

static int gioLock(IOSurfaceRef s) { return IOSurfaceLock(s, kIOSurfaceLockReadOnly, NULL); }
static void gioUnlock(IOSurfaceRef s) { IOSurfaceUnlock(s, kIOSurfaceLockReadOnly, NULL); }
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/yongjohnlee80/gio/op/extimage"
)

// platformTuples: IOSurfaces of every format, mapped by locking them read-only.
func platformTuples(max int) []extimage.Tuple {
	var out []extimage.Tuple
	for _, f := range formats {
		out = append(out, extimage.Tuple{Kind: extimage.KindIOSurface, Format: f, Path: extimage.PathCPU, MaxSize: image.Pt(max, max)})
	}
	return out
}

// mapPlatform locks the frame's IOSurface read-only and reads its planes in place. The
// producer's reference is borrowed: nothing is retained or released here.
func mapPlatform(d extimage.Desc) ([]plane, func(), error) {
	if d.Kind != extimage.KindIOSurface || d.IOSurface == 0 {
		return nil, nil, fmt.Errorf("cpuimport: cannot map a %v frame", d.Kind)
	}
	s := C.IOSurfaceRef(unsafe.Pointer(d.IOSurface))
	if C.gioLock(s) != 0 {
		return nil, nil, fmt.Errorf("cpuimport: IOSurfaceLock failed")
	}
	want := 1
	if d.Format == extimage.FormatNV12 {
		want = 2
	}
	count := int(C.IOSurfaceGetPlaneCount(s))
	if count == 0 && want == 1 {
		// A non-planar surface: one plane, the whole surface.
		base := C.IOSurfaceGetBaseAddress(s)
		pitch := int(C.IOSurfaceGetBytesPerRow(s))
		n := pitch * int(C.IOSurfaceGetHeight(s))
		return []plane{{data: unsafe.Slice((*byte)(base), n), pitch: pitch}}, func() { C.gioUnlock(s) }, nil
	}
	if count != want {
		C.gioUnlock(s)
		return nil, nil, fmt.Errorf("cpuimport: the IOSurface has %d planes; %v needs %d", count, d.Format, want)
	}
	out := make([]plane, want)
	for i := range want {
		base := C.IOSurfaceGetBaseAddressOfPlane(s, C.size_t(i))
		pitch := int(C.IOSurfaceGetBytesPerRowOfPlane(s, C.size_t(i)))
		n := pitch * int(C.IOSurfaceGetHeightOfPlane(s, C.size_t(i)))
		out[i] = plane{data: unsafe.Slice((*byte)(base), n), pitch: pitch}
	}
	return out, func() { C.gioUnlock(s) }, nil
}
