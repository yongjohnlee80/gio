// SPDX-License-Identifier: Unlicense OR MIT

package gpu

import (
	"image"
	"sync/atomic"

	"github.com/yongjohnlee80/gio/gpu/internal/cpuimport"
	"github.com/yongjohnlee80/gio/gpu/internal/driver"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// External frames (op/extimage) through the renderer.
//
// A frame's tuple, matched in the window's caps, picks its importer: the device's own for a
// zero-copy tuple, the CPU driver for a CPU one. A frame matching none is reported and draws
// nothing. Texture-cache entries hold a reference to the frame they show; a frame with a Source
// is cached by (Source, filter), so the next revision can reuse the texture and upload only what
// changed, when its Base is the revision the texture shows.

// gens numbers renderers, so a new device is a new Caps.Gen.
var gens atomic.Uint64

// extTexture is a texture-cache entry showing an external frame.
type extTexture struct {
	tex      driver.Texture
	imp      driver.ExternalImporter
	frame    *extimage.Frame // the frame shown: a reference, dropped when replaced or evicted
	rev      uint64
	size     image.Point
	format   extimage.Format
	color    extimage.Color
	zeroCopy bool
}

func (t *extTexture) release() {
	t.imp.ReleaseExternal(t.tex)
	t.frame.Drop()
}

// sourceKey keys a Source's cache entry; a frame without a Source is keyed by itself.
type sourceKey struct{ id extimage.SourceID }

// externalCaps is the window's caps: the device importer's zero-copy tuples, then the CPU
// driver's, with the zero-copy one winning where both list a tuple (extimage.Caps.Match).
func (r *renderer) externalCaps() extimage.Caps {
	c := extimage.Caps{Gen: r.gen, Driver: "cpu"}
	if r.importer != nil {
		c.Driver = r.importerName
		c.DeviceID = r.deviceID
		c.Tuples = append(c.Tuples, r.importer.ExternalTuples()...)
	}
	c.Tuples = append(c.Tuples, r.cpu.ExternalTuples()...)
	return c
}

// externalTex is the texture for an external image op's frame, importing or updating it as its
// cache entry needs. nil: the frame is not drawn (reported).
func (r *renderer) externalTex(cache *textureCache, data imageOpData) driver.Texture {
	f := data.ext
	d := f.Desc()
	report := func(err error) {
		if data.report != nil {
			data.report(err)
		}
	}
	t, ok := r.caps.Match(d)
	if !ok {
		report(extimage.ErrUnsupported)
		return nil
	}
	imp := driver.ExternalImporter(r.cpu)
	if t.Path == extimage.PathZeroCopy {
		imp = r.importer
	}
	var key textureCacheKey
	if d.Source != 0 {
		key = textureCacheKey{filter: data.filter, handle: sourceKey{d.Source}}
	} else {
		key = textureCacheKey{filter: data.filter, handle: f}
	}
	minFilter, magFilter := filtersOf(data.filter)
	res, exists := cache.get(key)
	if exists {
		e := res.(*extTexture)
		if e.frame == f {
			return e.tex
		}
		same := e.imp == imp && e.size == d.Size && e.format == d.Format && e.color == d.Color
		if same && !d.Dirty.Empty() && e.rev == d.Base && imp.UpdateExternal(e.tex, d) {
			e.frame.Drop()
			e.frame, e.rev = f.Retain(), d.Rev
			report(nil)
			return e.tex
		}
	}
	tex, err := imp.ImportExternal(d, minFilter, magFilter)
	if err != nil {
		report(err)
		return nil
	}
	e := &extTexture{tex: tex, imp: imp, frame: f.Retain(), rev: d.Rev, size: d.Size, format: d.Format, color: d.Color, zeroCopy: t.Path == extimage.PathZeroCopy}
	if exists {
		cache.replace(key, e) // the old entry's texture and frame are released by replace
	} else {
		cache.put(key, e)
	}
	report(nil)
	return tex
}

// filtersOf is the texture filters of an image op's filter byte.
func filtersOf(filter byte) (min, mag driver.TextureFilter) {
	switch filter {
	case filterNearest:
		return driver.FilterNearest, driver.FilterNearest
	}
	return driver.FilterLinearMipmapLinear, driver.FilterLinear
}

// newImporters finds the device's own importer, if it has one, and makes the CPU driver.
func (r *renderer) newImporters() {
	r.gen = gens.Add(1)
	r.cpu = cpuimport.New(r.ctx)
	if imp, ok := r.ctx.(driver.ExternalImporter); ok && len(imp.ExternalTuples()) > 0 {
		r.importer = imp
		if n, ok := r.ctx.(interface{ ExternalName() string }); ok {
			r.importerName = n.ExternalName()
		}
		if id, ok := r.ctx.(interface{ DeviceID() [16]byte }); ok {
			r.deviceID = id.DeviceID()
		}
	}
	r.caps = r.externalCaps()
}
