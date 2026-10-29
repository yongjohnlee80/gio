// SPDX-License-Identifier: Unlicense OR MIT

package headless

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"github.com/yongjohnlee80/gio/f32"
	"github.com/yongjohnlee80/gio/op"
	"github.com/yongjohnlee80/gio/op/clip"
	"github.com/yongjohnlee80/gio/op/paint"
)

// TestARerecordedPathKeepsItsFractionalOffset draws a path at the origin, then the same path
// re-recorded at a fractional offset. It must match a fresh window's draw at that offset, not
// reuse the vertices built for the origin. (Found in review of the content-keyed path cache.)
func TestARerecordedPathKeepsItsFractionalOffset(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()
	fresh, releaseFresh := newTestWindow(t)
	defer releaseFresh()
	var o op.Ops
	draw := func(window *Window, off f32.Point) *image.RGBA {
		o.Reset()
		s := op.Affine(f32.AffineId().Offset(off)).Push(&o)
		paint.FillShape(&o, color.NRGBA{R: 0xff, A: 0xff}, clip.UniformRRect(image.Rect(20, 20, 90, 75), 13).Op(&o))
		s.Pop()
		if err := window.Frame(&o); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: window.Size()})
		if err := window.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		return img
	}
	origin := draw(w, f32.Point{})
	off := f32.Pt(.5, .25)
	want := draw(fresh, off)
	if bytes.Equal(origin.Pix, want.Pix) {
		t.Fatal("the fractional offset changes no pixel, so this test can't observe the defect")
	}
	got := draw(w, off)
	different := 0
	for i := 0; i < len(got.Pix); i += 4 {
		if !bytes.Equal(got.Pix[i:i+4], want.Pix[i:i+4]) {
			different++
		}
	}
	if different != 0 {
		t.Fatalf("the re-recorded path at a fractional offset differs from a fresh draw at %d pixels (equal to the origin's draw: %v)", different, bytes.Equal(got.Pix, origin.Pix))
	}
}
