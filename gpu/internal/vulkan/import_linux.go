// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux && !novulkan

package vulkan

import (
	"fmt"
	"image"

	"golang.org/x/sys/unix"

	"github.com/yongjohnlee80/gio/gpu/internal/driver"
	"github.com/yongjohnlee80/gio/internal/vk"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// The Vulkan driver of the extimage contract: one-plane dma-bufs (RGBA8, BGRA8) imported as
// sampled images, zero-copy. A tuple is advertised only for a modifier the device reports as an
// importable sampled image of that format. NV12 is not advertised: its planes and a tiled
// modifier's memory planes differ, and LINEAR NV12 is unproven on hardware; those frames take
// the CPU driver's LINEAR tuple, or are refused.

var _ driver.ExternalImporter = (*Backend)(nil)

// dmaBufFormats are the formats imported zero-copy, with their sRGB Vulkan formats.
var dmaBufFormats = []struct {
	f  extimage.Format
	vk vk.Format
}{
	{extimage.FormatRGBA8, vk.FORMAT_R8G8B8A8_SRGB},
	{extimage.FormatBGRA8, vk.FORMAT_B8G8R8A8_SRGB},
}

// externalSetup proves the tuples once, when the device was made with the dma-buf extensions.
func (b *Backend) externalSetup(dmabuf bool) {
	if !dmabuf {
		return
	}
	b.deviceID = vk.DeviceUUID(b.physDev)
	for _, f := range dmaBufFormats {
		for _, m := range vk.FormatModifiers(b.physDev, f.vk) {
			if m.Planes != 1 || !m.Importer {
				continue
			}
			b.extTuples = append(b.extTuples, extimage.Tuple{Kind: extimage.KindDMABuf, Format: f.f, Modifier: m.Mod,
				Path: extimage.PathZeroCopy, MaxSize: image.Pt(m.MaxW, m.MaxH)})
		}
	}
}

func (b *Backend) ExternalTuples() []extimage.Tuple { return b.extTuples }
func (b *Backend) ExternalName() string             { return "vulkan" }
func (b *Backend) DeviceID() [16]byte               { return b.deviceID }

// ImportExternal imports a one-plane dma-buf as a sampled image, and records the barrier that
// acquires it from its foreign owner. The producer's descriptor is borrowed: the import uses a
// fresh duplicate, which a successful import consumes and only a failed one closes.
func (b *Backend) ImportExternal(d extimage.Desc, minFilter, magFilter driver.TextureFilter) (driver.Texture, error) {
	var vkfmt vk.Format
	for _, f := range dmaBufFormats {
		if f.f == d.Format {
			vkfmt = f.vk
		}
	}
	if d.Kind != extimage.KindDMABuf || vkfmt == 0 || len(d.Planes) != 1 {
		return nil, fmt.Errorf("vulkan: cannot import a %v %v frame", d.Kind, d.Format)
	}
	p := d.Planes[0]
	dup, err := unix.FcntlInt(uintptr(p.FD), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("vulkan: dup dma-buf: %w", err)
	}
	img, mem, consumed, err := vk.ImportDMABuf(b.physDev, b.dev, vkfmt, d.Size.X, d.Size.Y, d.Modifier, uint64(p.Offset), uint64(p.Pitch), dup)
	if err != nil {
		if !consumed {
			unix.Close(dup)
		}
		return nil, mapErr(err)
	}
	filter := func(f driver.TextureFilter) vk.Filter {
		if f == driver.FilterNearest {
			return vk.FILTER_NEAREST
		}
		return vk.FILTER_LINEAR
	}
	sampler, err := vk.CreateSampler(b.dev, filter(minFilter), filter(magFilter), vk.SAMPLER_MIPMAP_MODE_NEAREST)
	if err != nil {
		vk.DestroyImage(b.dev, img)
		vk.FreeMemory(b.dev, mem)
		return nil, mapErr(err)
	}
	view, err := vk.CreateImageView(b.dev, img, vkfmt)
	if err != nil {
		vk.DestroySampler(b.dev, sampler)
		vk.DestroyImage(b.dev, img)
		vk.FreeMemory(b.dev, mem)
		return nil, mapErr(err)
	}
	t := &Texture{backend: b, img: img, mem: mem, view: view, sampler: sampler, format: vkfmt, mipmaps: 1,
		passLayout: vk.IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL, width: d.Size.X, height: d.Size.Y}
	acquire := vk.BuildAcquireBarrier(img, b.queueFam)
	vk.CmdPipelineBarrier(b.ensureCmdBuf(), vk.PIPELINE_STAGE_TOP_OF_PIPE_BIT, vk.PIPELINE_STAGE_FRAGMENT_SHADER_BIT, 0, nil, nil, []vk.ImageMemoryBarrier{acquire})
	t.layout = vk.IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL
	t.scope.stage = vk.PIPELINE_STAGE_FRAGMENT_SHADER_BIT
	t.scope.access = vk.ACCESS_SHADER_READ_BIT
	return t, nil
}

// UpdateExternal: a zero-copy texture is the frame's own memory; a new revision is a new import.
func (b *Backend) UpdateExternal(driver.Texture, extimage.Desc) bool { return false }

// ReleaseExternal destroys the imported image and frees its memory, deferred until the device is
// done with it (Texture.Release). Freeing the memory ends the implementation's hold on the
// dma-buf; the producer's own descriptor was never ours.
func (b *Backend) ReleaseExternal(t driver.Texture) { t.Release() }
