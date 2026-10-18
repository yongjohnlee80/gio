// SPDX-License-Identifier: Unlicense OR MIT

package metal

/*
#cgo CFLAGS: -Werror -xobjective-c -fobjc-arc
#cgo LDFLAGS: -framework Metal -framework IOSurface -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>
#include <Metal/Metal.h>
#include <IOSurface/IOSurface.h>
#include <TargetConditionals.h>

// gioTextureOverIOSurface is a sampled texture over plane 0 of an IOSurface, sharing its memory.
// The texture keeps the surface alive; the caller's reference stays the caller's.
static CFTypeRef gioTextureOverIOSurface(CFTypeRef devRef, CFTypeRef surfRef, MTLPixelFormat format, NSUInteger width, NSUInteger height) {
	@autoreleasepool {
		id<MTLDevice> dev = (__bridge id<MTLDevice>)devRef;
		MTLTextureDescriptor *desc = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:format
																					   width:width
																					  height:height
																				   mipmapped:NO];
		desc.usage = MTLTextureUsageShaderRead;
#if TARGET_OS_OSX
		desc.storageMode = MTLStorageModeManaged;
#else
		desc.storageMode = MTLStorageModeShared;
#endif
		id<MTLTexture> tex = [dev newTextureWithDescriptor:desc iosurface:(IOSurfaceRef)surfRef plane:0];
		return CFBridgingRetain(tex);
	}
}

static uint64_t gioRegistryID(CFTypeRef devRef) {
	id<MTLDevice> dev = (__bridge id<MTLDevice>)devRef;
	return dev.registryID;
}

static size_t gioSurfaceWidth(CFTypeRef s) { return IOSurfaceGetWidth((IOSurfaceRef)s); }
static size_t gioSurfaceHeight(CFTypeRef s) { return IOSurfaceGetHeight((IOSurfaceRef)s); }
static size_t gioSurfacePlanes(CFTypeRef s) { return IOSurfaceGetPlaneCount((IOSurfaceRef)s); }
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"

	"github.com/yongjohnlee80/gio/gpu/internal/driver"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// The Metal driver of the extimage contract: one-plane IOSurfaces (BGRA8, RGBA8) as sampled
// textures over the surface's own memory, zero-copy. NV12 is not advertised: those frames take
// the CPU driver's IOSurface tuple until the conversion pass is proven on hardware.

var _ driver.ExternalImporter = (*Backend)(nil)

var ioSurfaceFormats = []struct {
	f  extimage.Format
	mf C.MTLPixelFormat
}{
	{extimage.FormatBGRA8, C.MTLPixelFormatBGRA8Unorm_sRGB},
	{extimage.FormatRGBA8, C.MTLPixelFormatRGBA8Unorm_sRGB},
}

func (b *Backend) ExternalTuples() []extimage.Tuple {
	var out []extimage.Tuple
	for _, f := range ioSurfaceFormats {
		out = append(out, extimage.Tuple{Kind: extimage.KindIOSurface, Format: f.f, Path: extimage.PathZeroCopy, MaxSize: image.Pt(16384, 16384)})
	}
	return out
}

func (b *Backend) ExternalName() string { return "metal" }

// DeviceID is the device's registryID in its first 8 bytes.
func (b *Backend) DeviceID() [16]byte {
	var id [16]byte
	binary.LittleEndian.PutUint64(id[:8], uint64(C.gioRegistryID(b.dev)))
	return id
}

// ImportExternal makes a texture over the frame's IOSurface. The texture retains the surface;
// the producer's reference was only borrowed and is never released here.
func (b *Backend) ImportExternal(d extimage.Desc, minFilter, magFilter driver.TextureFilter) (driver.Texture, error) {
	var mf C.MTLPixelFormat
	found := false
	for _, f := range ioSurfaceFormats {
		if f.f == d.Format {
			mf, found = f.mf, true
		}
	}
	if d.Kind != extimage.KindIOSurface || !found || d.IOSurface == 0 {
		return nil, fmt.Errorf("metal: cannot import a %v %v frame", d.Kind, d.Format)
	}
	surf := C.CFTypeRef(d.IOSurface)
	if w, h := int(C.gioSurfaceWidth(surf)), int(C.gioSurfaceHeight(surf)); w != d.Size.X || h != d.Size.Y || C.gioSurfacePlanes(surf) > 1 {
		return nil, fmt.Errorf("metal: the IOSurface is %dx%d with %d planes; the frame says %v", w, h, int(C.gioSurfacePlanes(surf)), d.Size)
	}
	tex := C.gioTextureOverIOSurface(b.dev, surf, mf, C.NSUInteger(d.Size.X), C.NSUInteger(d.Size.Y))
	if tex == 0 {
		return nil, errors.New("metal: [MTLDevice newTextureWithDescriptor:iosurface:plane:] failed")
	}
	s := b.samplerFor(minFilter, magFilter)
	if s == 0 {
		C.CFRelease(tex)
		return nil, errors.New("metal: [MTLDevice newSamplerStateWithDescriptor:] failed")
	}
	return &Texture{backend: b, texture: tex, sampler: s, width: d.Size.X, height: d.Size.Y}, nil
}

// UpdateExternal: the texture is the surface's own memory; a new revision is a new import.
func (b *Backend) UpdateExternal(driver.Texture, extimage.Desc) bool { return false }

// ReleaseExternal releases the texture, and with it the texture's hold on the surface.
func (b *Backend) ReleaseExternal(t driver.Texture) { t.Release() }
