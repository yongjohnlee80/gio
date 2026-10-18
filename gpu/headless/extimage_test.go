// SPDX-License-Identifier: Unlicense OR MIT

package headless

import (
	"errors"
	"image"
	"testing"

	"github.com/yongjohnlee80/gio/op"
	"github.com/yongjohnlee80/gio/op/extimage"
	"github.com/yongjohnlee80/gio/op/paint"
)

// solidRGBA is a w×h memory frame of one colour.
func solidRGBA(w, h int, c [4]byte) extimage.Desc {
	data := make([]byte, w*h*4)
	for i := 0; i < len(data); i += 4 {
		copy(data[i:], c[:])
	}
	return extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Size: image.Pt(w, h), Planes: []extimage.Plane{{Data: data, Pitch: uint32(w * 4)}}}
}

func drawFrame(t *testing.T, w *Window, f *extimage.Frame, report func(error)) *image.RGBA {
	t.Helper()
	var ops op.Ops
	extimage.NewImageOp(f, report).Add(&ops)
	paint.PaintOp{}.Add(&ops)
	if err := w.Frame(&ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: w.Size()})
	if err := w.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	return img
}

func near(a, b [4]byte) bool {
	for i := range a {
		if d := int(a[i]) - int(b[i]); d > 3 || d < -3 {
			return false
		}
	}
	return true
}

func px(img *image.RGBA, x, y int) [4]byte {
	c := img.RGBAAt(x, y)
	return [4]byte{c.R, c.G, c.B, c.A}
}

func TestAMemoryFrameIsDrawn(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()
	var reported []error
	f, _ := extimage.NewFrame(solidRGBA(64, 32, [4]byte{0x20, 0xa0, 0x40, 0xff}), func() {})
	img := drawFrame(t, w, f, func(err error) { reported = append(reported, err) })
	if got := px(img, 10, 10); !near(got, [4]byte{0x20, 0xa0, 0x40, 0xff}) {
		t.Fatalf("frame pixel %v", got)
	}
	if got := px(img, 100, 10); near(got, [4]byte{0x20, 0xa0, 0x40, 0xff}) {
		t.Fatal("the frame drew past its own size")
	}
	if len(reported) != 1 || reported[0] != nil {
		t.Fatalf("reports %v; want one success", reported)
	}
	if c := w.ExternalCaps(); len(c.Tuples) == 0 {
		t.Fatal("no tuples")
	}
}

func TestAnNV12FrameIsConvertedOnTheWayIn(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()
	// BT.709 limited red: Y 63, Cb 102, Cr 240.
	y := make([]byte, 16*16)
	for i := range y {
		y[i] = 63
	}
	uv := make([]byte, 8*8*2)
	for i := 0; i < len(uv); i += 2 {
		uv[i], uv[i+1] = 102, 240
	}
	d := extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatNV12, Size: image.Pt(16, 16), Planes: []extimage.Plane{{Data: y, Pitch: 16}, {Data: uv, Pitch: 16}}}
	f, _ := extimage.NewFrame(d, func() {})
	if got := px(drawFrame(t, w, f, nil), 4, 4); !near(got, [4]byte{255, 0, 0, 255}) {
		t.Fatalf("NV12 red drew %v", got)
	}
}

// A new revision of a Source whose Base is the drawn revision updates the same texture where it
// is dirty; the pixels match what the new revision holds.
func TestADirtyRevisionUpdatesItsSource(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()
	src := extimage.NewSource()
	d1 := solidRGBA(32, 32, [4]byte{0, 0, 0xff, 0xff})
	d1.Source, d1.Rev = src, 1
	f1, _ := extimage.NewFrame(d1, func() {})
	drawFrame(t, w, f1, nil)
	d2 := solidRGBA(32, 32, [4]byte{0, 0, 0xff, 0xff})
	for y := 8; y < 12; y++ {
		for x := 8; x < 12; x++ {
			copy(d2.Planes[0].Data[(y*32+x)*4:], []byte{0xff, 0xff, 0, 0xff})
		}
	}
	d2.Source, d2.Rev, d2.Base, d2.Dirty = src, 2, 1, image.Rect(8, 8, 12, 12)
	f2, _ := extimage.NewFrame(d2, func() {})
	img := drawFrame(t, w, f2, nil)
	if got := px(img, 9, 9); !near(got, [4]byte{0xff, 0xff, 0, 0xff}) {
		t.Fatalf("dirty pixel %v; want the new revision's yellow", got)
	}
	if got := px(img, 20, 20); !near(got, [4]byte{0, 0, 0xff, 0xff}) {
		t.Fatalf("clean pixel %v; want blue kept", got)
	}
}

// A frame no tuple matches is reported and draws nothing.
func TestAnUnsupportedFrameIsReportedAndNotDrawn(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()
	d := extimage.Desc{Kind: extimage.KindDMABuf, Format: extimage.FormatRGBA8, Size: image.Pt(8, 8), Modifier: 0x0100000000000001,
		Planes: []extimage.Plane{{FD: 0, Pitch: 32}}}
	f, err := extimage.NewFrame(d, func() {})
	if err != nil {
		t.Fatal(err)
	}
	var reported error
	img := drawFrame(t, w, f, func(err error) { reported = err })
	if !errors.Is(reported, extimage.ErrUnsupported) {
		t.Fatalf("reported %v; want ErrUnsupported", reported)
	}
	if got := px(img, 2, 2); got[3] != 0 && near(got, [4]byte{0, 0, 0, 0xff}) {
		t.Fatalf("an unsupported frame drew %v", got)
	}
}

// The renderer's references go: a frame drawn once, then not drawn for two frames, is released.
func TestAFrameIsReleasedAfterItsTextureIsEvicted(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()
	n := 0
	f, _ := extimage.NewFrame(solidRGBA(8, 8, [4]byte{1, 2, 3, 0xff}), func() { n++ })
	drawFrame(t, w, f, nil)
	f.Drop() // the producer's own reference: the renderer's keep it alive
	if n != 0 {
		t.Fatal("released while the renderer holds it")
	}
	for range 3 {
		if err := w.Frame(new(op.Ops)); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("released %d times after eviction; want 1", n)
	}
}
