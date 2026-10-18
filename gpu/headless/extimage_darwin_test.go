// SPDX-License-Identifier: Unlicense OR MIT

package headless

import (
	"image"
	"testing"

	"github.com/yongjohnlee80/gio/internal/testsurface"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// An IOSurface frame is drawn: zero-copy on Metal, else through the CPU driver's IOSurface tuple.
func TestAnIOSurfaceFrameIsDrawn(t *testing.T) {
	w, release := newTestWindow(t)
	defer release()
	surf, free := testsurface.NewBGRA(64, 32, 0x40, 0xa0, 0x20, 0xff) // B G R A: RGB (0x20, 0xa0, 0x40)
	if surf == 0 {
		t.Skip("no IOSurface here")
	}
	released := false
	f, err := extimage.NewFrame(extimage.Desc{Kind: extimage.KindIOSurface, Format: extimage.FormatBGRA8, Size: image.Pt(64, 32), IOSurface: surf},
		func() { released = true; free() })
	if err != nil {
		t.Fatal(err)
	}
	var reported []error
	img := drawFrame(t, w, f, func(err error) { reported = append(reported, err) })
	caps := w.ExternalCaps()
	t.Logf("driver %q", caps.Driver)
	if len(reported) != 1 || reported[0] != nil {
		t.Fatalf("reports %v; want one success", reported)
	}
	if got := px(img, 10, 10); !near(got, [4]byte{0x20, 0xa0, 0x40, 0xff}) {
		t.Fatalf("IOSurface pixel %v; want (0x20, 0xa0, 0x40)", got)
	}
	f.Drop()
	if released {
		t.Fatal("released while the renderer holds it")
	}
}
