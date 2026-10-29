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
