// SPDX-License-Identifier: Unlicense OR MIT

// Package cpuimport is the CPU driver of the extimage contract: over any driver.Device, it maps
// a frame's memory, converts it to sRGB RGBA and uploads it. Every device draws frames this way
// when it has no importer of its own, or for a tuple its importer does not list.
package cpuimport

import (
	"errors"
	"image"
	"math"

	"github.com/yongjohnlee80/gio/gpu/internal/driver"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// Importer is the CPU driver. It is used on the renderer's goroutine only.
type Importer struct {
	dev  driver.Device
	pool map[poolKey][]driver.Texture // textures given back, by size and filters
	made map[driver.Texture]poolKey   // what each texture this importer made is
	buf  []byte                       // conversion scratch, used within one call
}

type poolKey struct {
	size     image.Point
	min, mag driver.TextureFilter
}

// poolDepth is how many idle textures of one size and filtering are kept for reuse.
const poolDepth = 4

// New is the CPU driver over dev.
func New(dev driver.Device) *Importer {
	return &Importer{dev: dev, pool: map[poolKey][]driver.Texture{}, made: map[driver.Texture]poolKey{}}
}

var formats = []extimage.Format{extimage.FormatRGBA8, extimage.FormatBGRA8, extimage.FormatNV12}

// ExternalTuples are memory frames of every format, and the platform's own kinds it can map
// (platformTuples): a CPU path, never zero-copy.
func (im *Importer) ExternalTuples() []extimage.Tuple {
	max := im.dev.Caps().MaxTextureSize
	var out []extimage.Tuple
	for _, f := range formats {
		out = append(out, extimage.Tuple{Kind: extimage.KindMemory, Format: f, Path: extimage.PathCPU, MaxSize: image.Pt(max, max)})
	}
	return append(out, platformTuples(max)...)
}

// ImportExternal uploads the whole of d into a texture of its size: a pooled one if one is idle.
func (im *Importer) ImportExternal(d extimage.Desc, minFilter, magFilter driver.TextureFilter) (driver.Texture, error) {
	key := poolKey{size: d.Size, min: minFilter, mag: magFilter}
	var tex driver.Texture
	if idle := im.pool[key]; len(idle) > 0 {
		tex, im.pool[key] = idle[len(idle)-1], idle[:len(idle)-1]
	} else {
		t, err := im.dev.NewTexture(driver.TextureFormatSRGBA, d.Size.X, d.Size.Y, minFilter, magFilter, driver.BufferBindingTexture)
		if err != nil {
			return nil, err
		}
		tex = t
		im.made[tex] = key
	}
	if err := im.upload(tex, d, image.Rectangle{Max: d.Size}); err != nil {
		im.ReleaseExternal(tex)
		return nil, err
	}
	return tex, nil
}

// UpdateExternal uploads only d.Dirty into t.
func (im *Importer) UpdateExternal(t driver.Texture, d extimage.Desc) bool {
	if _, ok := im.made[t]; !ok || d.Dirty.Empty() {
		return false
	}
	return im.upload(t, d, d.Dirty) == nil
}

// ReleaseExternal pools t for reuse, or frees it when its pool is full. The renderer calls it once
// no submission samples t.
func (im *Importer) ReleaseExternal(t driver.Texture) {
	key, ok := im.made[t]
	if !ok {
		t.Release()
		return
	}
	if len(im.pool[key]) < poolDepth {
		im.pool[key] = append(im.pool[key], t)
		return
	}
	delete(im.made, t)
	t.Release()
}

// upload converts region r of d to RGBA and uploads it at r.Min.
func (im *Importer) upload(t driver.Texture, d extimage.Desc, r image.Rectangle) error {
	r = r.Intersect(image.Rectangle{Max: d.Size})
	if r.Empty() {
		return nil
	}
	planes, done, err := mapPlanes(d)
	if err != nil {
		return err
	}
	defer done()
	n := r.Dx() * r.Dy() * 4
	if cap(im.buf) < n {
		im.buf = make([]byte, n)
	}
	out := im.buf[:n]
	if err := convert(out, d, planes, r); err != nil {
		return err
	}
	t.Upload(r.Min, r.Size(), out, r.Dx()*4)
	return nil
}

// plane is one plane's bytes, from its offset, and its pitch.
type plane struct {
	data  []byte
	pitch int
}

// mapPlanes is d's planes in memory, and the call that unmaps them.
func mapPlanes(d extimage.Desc) ([]plane, func(), error) {
	switch d.Kind {
	case extimage.KindMemory:
		out := make([]plane, len(d.Planes))
		for i, p := range d.Planes {
			out[i] = plane{data: p.Data[p.Offset:], pitch: int(p.Pitch)}
		}
		return out, func() {}, nil
	}
	return mapPlatform(d)
}

var errFormat = errors.New("cpuimport: format not supported")

// convert writes region r of d as sRGB RGBA rows into out (r.Dx()*4 bytes a row).
func convert(out []byte, d extimage.Desc, planes []plane, r image.Rectangle) error {
	w := r.Dx()
	switch d.Format {
	case extimage.FormatRGBA8, extimage.FormatBGRA8:
		src := planes[0]
		for y := r.Min.Y; y < r.Max.Y; y++ {
			row := src.data[y*src.pitch+r.Min.X*4 : y*src.pitch+r.Max.X*4]
			dst := out[(y-r.Min.Y)*w*4 : (y-r.Min.Y+1)*w*4]
			copy(dst, row)
			if d.Format == extimage.FormatBGRA8 {
				for i := 0; i < len(dst); i += 4 {
					dst[i], dst[i+2] = dst[i+2], dst[i]
				}
			}
		}
		return nil
	case extimage.FormatNV12:
		m := coefficients(d.Color)
		yp, cp := planes[0], planes[1]
		for y := r.Min.Y; y < r.Max.Y; y++ {
			dst := out[(y-r.Min.Y)*w*4:]
			for x := r.Min.X; x < r.Max.X; x++ {
				Y := yp.data[y*yp.pitch+x]
				ci := (y/2)*cp.pitch + (x/2)*2
				R, G, B := m.rgb(Y, cp.data[ci], cp.data[ci+1])
				i := (x - r.Min.X) * 4
				dst[i], dst[i+1], dst[i+2], dst[i+3] = R, G, B, 0xff
			}
		}
		return nil
	}
	return errFormat
}

// yuv is one YUV encoding's conversion to gamma-encoded RGB.
type yuv struct {
	kr, kb             float32
	yOff, yScale       float32
	cScale             float32
	rCr, gCb, gCr, bCb float32
}

func coefficients(c extimage.Color) yuv {
	var m yuv
	switch c.Matrix {
	case extimage.MatrixBT601:
		m.kr, m.kb = 0.299, 0.114
	case extimage.MatrixBT2020:
		m.kr, m.kb = 0.2627, 0.0593
	default:
		m.kr, m.kb = 0.2126, 0.0722
	}
	if c.Range == extimage.RangeFull {
		m.yOff, m.yScale, m.cScale = 0, 1.0/255, 1.0/255
	} else {
		m.yOff, m.yScale, m.cScale = 16, 1.0/219, 1.0/224
	}
	kg := 1 - m.kr - m.kb
	m.rCr = 2 * (1 - m.kr)
	m.bCb = 2 * (1 - m.kb)
	m.gCb = -2 * (1 - m.kb) * m.kb / kg
	m.gCr = -2 * (1 - m.kr) * m.kr / kg
	return m
}

// rgb converts one sample to 8-bit gamma-encoded RGB.
func (m yuv) rgb(y, cb, cr uint8) (r, g, b uint8) {
	Y := (float32(y) - m.yOff) * m.yScale
	Cb := (float32(cb) - 128) * m.cScale
	Cr := (float32(cr) - 128) * m.cScale
	return to8(Y + m.rCr*Cr), to8(Y + m.gCb*Cb + m.gCr*Cr), to8(Y + m.bCb*Cb)
}

func to8(v float32) uint8 {
	return uint8(math.Round(float64(min(max(v, 0), 1) * 255)))
}
