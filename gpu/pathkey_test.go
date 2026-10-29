// SPDX-License-Identifier: Unlicense OR MIT

package gpu

import (
	"image"
	"image/color"
	"testing"

	"github.com/yongjohnlee80/gio/f32"
	"github.com/yongjohnlee80/gio/op"
	"github.com/yongjohnlee80/gio/op/clip"
	"github.com/yongjohnlee80/gio/op/paint"
)

// TestRecordedPathsHitTheCacheByContent re-records the same paths into reset ops each frame, as a
// UI that redraws unchanged content does. The path cache must find them by content, so the cache
// holds one entry per path rather than one per path per frame, and a path whose content changes
// is a new entry.
func TestRecordedPathsHitTheCacheByContent(t *testing.T) {
	const paths = 12
	vp := image.Pt(400, 400)
	var d drawOps
	d.pathCache = newOpCache()
	var o op.Ops
	frame := func(shift int) int {
		o.Reset()
		for i := range paths {
			x := shift + 10 + i*30
			r := clip.RRect{Rect: image.Rect(x, 10, x+24, 34), SE: 6, SW: 6, NW: 6, NE: 6}
			paint.FillShape(&o, color.NRGBA{A: 0xff}, r.Op(&o))
		}
		d.reset(vp)
		d.collect(&o, vp)
		n := len(d.pathCache.index)
		d.pathCache.frame()
		return n
	}

	if got := frame(0); got != paths {
		t.Fatalf("first frame: %d cache entries, want %d", got, paths)
	}
	for f := 2; f <= 5; f++ {
		if got := frame(0); got != paths {
			t.Errorf("frame %d, the same content re-recorded: %d cache entries, want %d (one per path)", f, got, paths)
		}
	}
	// Moved paths are different content: new entries, the old ones still there until the frame ends.
	if got := frame(1); got != 2*paths {
		t.Errorf("paths moved by a pixel: %d cache entries, want %d", got, 2*paths)
	}
}

// TestAPathsFractionalOffsetIsPartOfItsKey draws one path at whole-pixel offsets and at a
// fractional one. The vertices are built with the translation's fractional part (only the whole
// pixels are applied when drawing), so whole-pixel offsets share an entry and a fractional one
// must not reuse it.
func TestAPathsFractionalOffsetIsPartOfItsKey(t *testing.T) {
	vp := image.Pt(400, 400)
	var d drawOps
	d.pathCache = newOpCache()
	var o op.Ops
	frame := func(offsets ...f32.Point) int {
		o.Reset()
		for _, off := range offsets {
			s := op.Affine(f32.AffineId().Offset(off)).Push(&o)
			paint.FillShape(&o, color.NRGBA{A: 0xff}, clip.UniformRRect(image.Rect(20, 20, 90, 75), 13).Op(&o))
			s.Pop()
		}
		d.reset(vp)
		d.collect(&o, vp)
		n := len(d.pathCache.index)
		d.pathCache.frame()
		return n
	}
	if got := frame(f32.Pt(0, 0), f32.Pt(30, 7)); got != 1 {
		t.Errorf("the path at two whole-pixel offsets: %d cache entries, want 1", got)
	}
	if got := frame(f32.Pt(0, 0), f32.Pt(0.5, 0.25)); got != 2 {
		t.Errorf("the path at offsets (0,0) and (0.5,0.25): %d cache entries, want 2", got)
	}
}

// TestTheVertexScratchShrinksAfterABigFrame builds one frame of many new paths, which grows the
// vertex scratch, then draws small frames: within two windows of vertWindow frames (the big frame
// counts in the first) the scratch is given back. A window whose every frame is as big keeps its
// scratch rather than reallocating it.
func TestTheVertexScratchShrinksAfterABigFrame(t *testing.T) {
	vp := image.Pt(4000, 4000)
	var d drawOps
	d.pathCache = newOpCache()
	var o op.Ops
	frame := func(paths, shift int) {
		o.Reset()
		for i := range paths {
			x, y := shift+(i%60)*60, (i/60)*60
			r := clip.RRect{Rect: image.Rect(x, y, x+50, y+50), SE: 9, SW: 9, NW: 9, NE: 9}
			paint.FillShape(&o, color.NRGBA{A: 0xff}, r.Op(&o))
		}
		d.reset(vp)
		d.collect(&o, vp)
		d.pathCache.frame()
	}

	frame(3000, 0)
	big := cap(d.vertCache)
	if big <= 4*vertKeepMin {
		t.Fatalf("a frame of 3000 new paths left %d bytes of scratch; the test needs more than %d to observe a shrink", big, 4*vertKeepMin)
	}
	for range 2*vertWindow + 1 {
		frame(1, 0)
	}
	if got := cap(d.vertCache); got > vertKeepMin {
		t.Errorf("after %d small frames the scratch is still %d bytes (it was %d), want at most %d", 2*vertWindow+1, got, big, vertKeepMin)
	}
	// Nothing the frames left behind still refers to a scratch: a pathOp kept anywhere in the
	// ops' backing arrays would pin the one given back.
	d.reset(vp)
	for i, p := range d.pathOpCache[:cap(d.pathOpCache)] {
		if p.pathVerts != nil || p.parent != nil {
			t.Fatalf("pathOpCache[%d] still holds a frame's vertices or parent after reset", i)
		}
	}
	for i, p := range d.pathOps[:cap(d.pathOps)] {
		if p != nil {
			t.Fatalf("pathOps[%d] still points at a frame's path after reset", i)
		}
	}

	// Every frame big: moving paths miss the cache each frame, so each frame builds them all.
	for f := range vertWindow {
		frame(1000, f%2)
	}
	steady := cap(d.vertCache)
	for f := range vertWindow {
		frame(1000, f%2)
	}
	if got := cap(d.vertCache); got != steady {
		t.Errorf("frames as big as the scratch reallocated it: %d bytes, then %d", steady, got)
	}
}

// TestAFramesOpsLeaveNoPointersPastTheirEnd draws paths and then a full-screen opaque fill, which
// scraps the images drawn before it, and packs the stencils as gpu.frame does: the scrapped
// images' paths have empty clips, so packStencils removes them. Neither shortening leaves a path
// past the slice's end, where it would keep its vertices and the scratch alive.
func TestAFramesOpsLeaveNoPointersPastTheirEnd(t *testing.T) {
	vp := image.Pt(800, 600)
	var d drawOps
	d.pathCache = newOpCache()
	var o op.Ops
	for i := range 200 {
		x, y := (i%20)*40, (i/20)*40
		r := clip.RRect{Rect: image.Rect(x, y, x+30, y+30), SE: 6, SW: 6, NW: 6, NE: 6}
		paint.FillShape(&o, color.NRGBA{R: 0xff, A: 0xff}, r.Op(&o))
	}
	paint.Fill(&o, color.NRGBA{B: 0xff, A: 0xff}) // the whole screen, opaque: a scrap
	d.reset(vp)
	d.collect(&o, vp)
	if !d.clear {
		t.Fatal("the trailing fill didn't scrap the images: the test can't observe the defect")
	}
	for i, img := range d.imageOps[:cap(d.imageOps)] {
		if img.path != nil {
			t.Fatalf("imageOps[%d], scrapped, still points at a path", i)
		}
	}
	for _, img := range d.imageOps {
		expandPathOp(img.path, img.clip)
	}
	if len(d.pathOps) == 0 {
		t.Fatal("no path ops: the test can't observe packStencils' removal")
	}
	var r renderer
	r.packer.maxDims = image.Pt(8192, 8192)
	r.packStencils(&d.pathOps)
	for i, p := range d.pathOps[len(d.pathOps):cap(d.pathOps)] {
		if p != nil {
			t.Fatalf("pathOps[%d], past the end after packStencils, still points at a path", len(d.pathOps)+i)
		}
	}
}
