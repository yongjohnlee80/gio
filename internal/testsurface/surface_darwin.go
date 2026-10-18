// SPDX-License-Identifier: Unlicense OR MIT

// Package testsurface makes IOSurfaces for tests of the extimage drivers. It is not for
// production use.
package testsurface

/*
#cgo CFLAGS: -Werror
#cgo LDFLAGS: -framework IOSurface -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>
#include <IOSurface/IOSurfaceRef.h>

static void setInt(CFMutableDictionaryRef d, CFStringRef k, int v) {
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberIntType, &v);
	CFDictionarySetValue(d, k, n);
	CFRelease(n);
}

// newBGRA makes a w×h BGRA surface filled with one colour.
static IOSurfaceRef newBGRA(int w, int h, unsigned char b, unsigned char g, unsigned char r, unsigned char a) {
	CFMutableDictionaryRef p = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	setInt(p, kIOSurfaceWidth, w);
	setInt(p, kIOSurfaceHeight, h);
	setInt(p, kIOSurfaceBytesPerElement, 4);
	setInt(p, kIOSurfacePixelFormat, 'BGRA');
	IOSurfaceRef s = IOSurfaceCreate(p);
	CFRelease(p);
	if (s == NULL) {
		return NULL;
	}
	IOSurfaceLock(s, 0, NULL);
	unsigned char *base = IOSurfaceGetBaseAddress(s);
	size_t pitch = IOSurfaceGetBytesPerRow(s);
	for (int y = 0; y < h; y++) {
		for (int x = 0; x < w; x++) {
			unsigned char *px = base + y*pitch + x*4;
			px[0] = b; px[1] = g; px[2] = r; px[3] = a;
		}
	}
	IOSurfaceUnlock(s, 0, NULL);
	return s;
}
*/
import "C"

import "unsafe"

// NewBGRA is a w×h BGRA IOSurface of one colour, as an IOSurfaceRef, and the call that releases it;
// 0 if the system cannot make one.
func NewBGRA(w, h int, b, g, r, a byte) (uintptr, func()) {
	s := C.newBGRA(C.int(w), C.int(h), C.uchar(b), C.uchar(g), C.uchar(r), C.uchar(a))
	if s == 0 {
		return 0, func() {}
	}
	return uintptr(unsafe.Pointer(s)), func() { C.CFRelease(C.CFTypeRef(s)) }
}
