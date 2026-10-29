// SPDX-License-Identifier: Unlicense OR MIT

//go:build (linux || freebsd) && !novulkan

package headless

import (
	"image"
	"image/color"
	"testing"

	"github.com/yongjohnlee80/gio/internal/f32color"
	"github.com/yongjohnlee80/gio/internal/vk"
	"github.com/yongjohnlee80/gio/op"
	"github.com/yongjohnlee80/gio/op/clip"
	"github.com/yongjohnlee80/gio/op/paint"
)

// TestRecordedPathsReuseBuffers draws the same rounded rectangles every frame from freshly
// recorded ops, so each frame misses the path cache and builds every path again, the way a UI that
// re-records its frame does. On Vulkan, a steady frame must reuse the buffers the last frame
// released rather than allocate device memory for each path.
func TestRecordedPathsReuseBuffers(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()

	const paths = 40
	col := color.NRGBA{A: 0xff, R: 0x20, G: 0x80, B: 0xe0}
	var ops op.Ops
	frame := func() {
		ops.Reset()
		for i := range paths {
			x := 10 + (i%8)*30
			y := 10 + (i/8)*30
			r := clip.RRect{Rect: image.Rect(x, y, x+24, y+24), SE: 6, SW: 6, NW: 6, NE: 6}
			paint.FillShape(&ops, col, r.Op(&ops))
		}
		if err := w.Frame(&ops); err != nil {
			t.Fatal(err)
		}
	}

	for range 5 {
		frame()
	}
	before := vk.MemoryAllocations()
	if before == 0 {
		t.Skip("the headless window isn't drawing with Vulkan")
	}
	const frames = 30
	for range frames {
		frame()
	}
	if got := vk.MemoryAllocations() - before; got > 0 {
		t.Errorf("%d steady frames of %d re-recorded paths made %d device-memory allocations, want 0", frames, paths, got)
	}

	// The reused buffers still draw the right shapes.
	img := image.NewRGBA(image.Rectangle{Max: w.Size()})
	if err := w.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	want := f32color.NRGBAToRGBA(col)
	for i := range paths {
		x := 10 + (i%8)*30 + 12
		y := 10 + (i/8)*30 + 12
		if got := img.RGBAAt(x, y); got != want {
			t.Fatalf("path %d: centre pixel (%d,%d) is %v, want %v", i, x, y, got, want)
		}
	}
	if got := img.RGBAAt(10+24+3, 10+12); got == want {
		t.Errorf("the gap between paths is painted: %v", got)
	}
}
