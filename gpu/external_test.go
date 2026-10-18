// SPDX-License-Identifier: Unlicense OR MIT

package gpu

import (
	"image"
	"testing"

	"github.com/yongjohnlee80/gio/gpu/internal/driver"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// countingImporter is an importer that records what it was asked to do.
type countingImporter struct {
	tuples           []extimage.Tuple
	imports, updates int
	released         int
}

type stubTexture struct{ driver.Texture }

func (c *countingImporter) ExternalTuples() []extimage.Tuple { return c.tuples }
func (c *countingImporter) ImportExternal(extimage.Desc, driver.TextureFilter, driver.TextureFilter) (driver.Texture, error) {
	c.imports++
	return &stubTexture{}, nil
}
func (c *countingImporter) UpdateExternal(driver.Texture, extimage.Desc) bool {
	c.updates++
	return true
}
func (c *countingImporter) ReleaseExternal(driver.Texture) { c.released++ }

func memDesc(src extimage.SourceID, rev, base uint64, dirty image.Rectangle) extimage.Desc {
	return extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Size: image.Pt(4, 4),
		Planes: []extimage.Plane{{Data: make([]byte, 64), Pitch: 16}}, Source: src, Rev: rev, Base: base, Dirty: dirty}
}

func testRenderer(dev, cpu *countingImporter) *renderer {
	r := &renderer{cpu: cpu}
	if dev != nil {
		r.importer, r.importerName = dev, "fake"
	}
	r.caps = r.externalCaps()
	return r
}

// A frame whose tuple is zero-copy is imported only by the device's importer; the CPU driver is
// never called for it.
func TestAZeroCopyFrameNeverReachesTheCPUDriver(t *testing.T) {
	dev := &countingImporter{tuples: []extimage.Tuple{{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Path: extimage.PathZeroCopy}}}
	cpu := &countingImporter{tuples: []extimage.Tuple{{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Path: extimage.PathCPU}}}
	r := testRenderer(dev, cpu)
	f, _ := extimage.NewFrame(memDesc(0, 0, 0, image.Rectangle{}), func() {})
	if r.externalTex(newTextureCache(), imageOpData{ext: f}) == nil {
		t.Fatal("not drawn")
	}
	if dev.imports != 1 || cpu.imports != 0 {
		t.Fatalf("device imported %d, CPU %d; want 1 and 0", dev.imports, cpu.imports)
	}
	// Positive control: without the device's tuple, the CPU driver takes it.
	r = testRenderer(nil, cpu)
	f2, _ := extimage.NewFrame(memDesc(0, 0, 0, image.Rectangle{}), func() {})
	r.externalTex(newTextureCache(), imageOpData{ext: f2})
	if cpu.imports != 1 {
		t.Fatalf("CPU imported %d without a device importer; want 1", cpu.imports)
	}
}

// A drawn, B superseded (never drawn), C pushed with Base B: the texture shows A, not B, so C
// is imported whole, never sub-uploaded onto A (which would lose B's change). C with Base A
// updates in place.
func TestADirtyRevisionUpdatesOnlyFromTheRevisionItsTextureShows(t *testing.T) {
	cpu := &countingImporter{tuples: []extimage.Tuple{{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Path: extimage.PathCPU}}}
	r := testRenderer(nil, cpu)
	cache := newTextureCache()
	src := extimage.NewSource()
	a, _ := extimage.NewFrame(memDesc(src, 1, 0, image.Rectangle{}), func() {})
	r.externalTex(cache, imageOpData{ext: a})
	c, _ := extimage.NewFrame(memDesc(src, 3, 2, image.Rect(0, 0, 1, 1)), func() {})
	r.externalTex(cache, imageOpData{ext: c})
	if cpu.updates != 0 || cpu.imports != 2 {
		t.Fatalf("C on base 2 over A (rev 1): %d updates, %d imports; want 0 and 2", cpu.updates, cpu.imports)
	}
	d, _ := extimage.NewFrame(memDesc(src, 4, 3, image.Rect(1, 1, 2, 2)), func() {})
	r.externalTex(cache, imageOpData{ext: d})
	if cpu.updates != 1 || cpu.imports != 2 {
		t.Fatalf("D on base 3 over C (rev 3): %d updates, %d imports; want 1 and 2", cpu.updates, cpu.imports)
	}
}

// A replaced texture is released at the end of the next frame, not when it is replaced: the
// frame that replaced it may still sample it.
func TestAReplacedTextureIsReleasedAFrameLater(t *testing.T) {
	cpu := &countingImporter{tuples: []extimage.Tuple{{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Path: extimage.PathCPU}}}
	r := testRenderer(nil, cpu)
	cache := newTextureCache()
	src := extimage.NewSource()
	a, _ := extimage.NewFrame(memDesc(src, 1, 0, image.Rectangle{}), func() {})
	r.externalTex(cache, imageOpData{ext: a})
	b, _ := extimage.NewFrame(memDesc(src, 5, 4, image.Rectangle{}), func() {}) // a whole replacement
	r.externalTex(cache, imageOpData{ext: b})
	if cpu.released != 0 {
		t.Fatal("released at replacement")
	}
	cache.frame()
	if cpu.released != 0 {
		t.Fatal("released at the end of the replacing frame")
	}
	r.externalTex(cache, imageOpData{ext: b})
	cache.frame()
	if cpu.released != 1 {
		t.Fatalf("released %d at the end of the next frame; want 1", cpu.released)
	}
}
