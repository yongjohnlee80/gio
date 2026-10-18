// SPDX-License-Identifier: Unlicense OR MIT

package cpuimport

import (
	"image"
	"testing"

	"github.com/yongjohnlee80/gio/gpu/internal/driver"
	"github.com/yongjohnlee80/gio/op/extimage"
)

// fakeDevice makes fakeTextures and nothing else.
type fakeDevice struct {
	driver.Device
	made int
}

func (d *fakeDevice) Caps() driver.Caps { return driver.Caps{MaxTextureSize: 4096} }
func (d *fakeDevice) NewTexture(f driver.TextureFormat, w, h int, min, mag driver.TextureFilter, b driver.BufferBinding) (driver.Texture, error) {
	d.made++
	return &fakeTexture{pix: make([]byte, w*h*4), w: w}, nil
}

type upload struct{ off, size image.Point }

type fakeTexture struct {
	driver.Texture
	pix      []byte
	w        int
	uploads  []upload
	released bool
}

func (t *fakeTexture) Upload(off, size image.Point, pixels []byte, stride int) {
	t.uploads = append(t.uploads, upload{off, size})
	for y := 0; y < size.Y; y++ {
		copy(t.pix[((off.Y+y)*t.w+off.X)*4:], pixels[y*stride:y*stride+size.X*4])
	}
}
func (t *fakeTexture) Release() { t.released = true }

func (t *fakeTexture) at(x, y int) [4]byte {
	i := (y*t.w + x) * 4
	return [4]byte{t.pix[i], t.pix[i+1], t.pix[i+2], t.pix[i+3]}
}

func nv12(y, cb, cr uint8, c extimage.Color) extimage.Desc {
	return extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatNV12, Color: c, Size: image.Pt(2, 2), Planes: []extimage.Plane{
		{Data: []byte{y, y, y, y}, Pitch: 2},
		{Data: []byte{cb, cr}, Pitch: 2},
	}}
}

// Reference colours for each matrix and range: white, black and a primary that tells the
// matrices apart.
func TestNV12ConvertsWithItsMatrixAndRange(t *testing.T) {
	cases := []struct {
		name      string
		c         extimage.Color
		y, cb, cr uint8
		want      [4]byte
	}{
		{"709 limited white", extimage.Color{}, 235, 128, 128, [4]byte{255, 255, 255, 255}},
		{"709 limited black", extimage.Color{}, 16, 128, 128, [4]byte{0, 0, 0, 255}},
		{"709 full white", extimage.Color{Range: extimage.RangeFull}, 255, 128, 128, [4]byte{255, 255, 255, 255}},
		{"709 limited red", extimage.Color{}, 63, 102, 240, [4]byte{255, 0, 0, 255}},
		{"601 limited red", extimage.Color{Matrix: extimage.MatrixBT601}, 81, 90, 240, [4]byte{255, 0, 0, 255}},
		{"2020 limited red", extimage.Color{Matrix: extimage.MatrixBT2020}, 74, 97, 240, [4]byte{255, 0, 0, 255}},
	}
	for _, c := range cases {
		im := New(&fakeDevice{})
		tex, err := im.ImportExternal(nv12(c.y, c.cb, c.cr, c.c), driver.FilterNearest, driver.FilterNearest)
		if err != nil {
			t.Fatal(err)
		}
		got := tex.(*fakeTexture).at(1, 1)
		for i := range got {
			if d := int(got[i]) - int(c.want[i]); d > 2 || d < -2 {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestBGRAIsSwizzledToRGBA(t *testing.T) {
	d := extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatBGRA8, Size: image.Pt(1, 1), Planes: []extimage.Plane{{Data: []byte{1, 2, 3, 4}, Pitch: 4}}}
	tex, _ := New(&fakeDevice{}).ImportExternal(d, driver.FilterNearest, driver.FilterNearest)
	if got := tex.(*fakeTexture).at(0, 0); got != [4]byte{3, 2, 1, 4} {
		t.Fatalf("BGRA 1,2,3,4 uploaded as %v; want 3,2,1,4", got)
	}
}

// An update uploads only its dirty rectangle, from the frame's own pixels.
func TestUpdateUploadsOnlyTheDirtyRectangle(t *testing.T) {
	im := New(&fakeDevice{})
	d := extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Size: image.Pt(8, 8), Planes: []extimage.Plane{{Data: make([]byte, 8*8*4), Pitch: 32}}}
	tex, _ := im.ImportExternal(d, driver.FilterNearest, driver.FilterNearest)
	ft := tex.(*fakeTexture)
	d.Planes[0].Data[(5*8+6)*4] = 0x7f
	d.Dirty = image.Rect(6, 5, 7, 6)
	if !im.UpdateExternal(tex, d) {
		t.Fatal("update refused")
	}
	if last := ft.uploads[len(ft.uploads)-1]; last != (upload{image.Pt(6, 5), image.Pt(1, 1)}) {
		t.Fatalf("update uploaded %+v; want only the 1×1 dirty rectangle at (6,5)", last)
	}
	if ft.at(6, 5)[0] != 0x7f {
		t.Fatal("the dirty pixel did not reach the texture")
	}
	if im.UpdateExternal(&fakeTexture{}, d) {
		t.Fatal("updated a texture this importer did not make")
	}
}

// A released texture is reused for the next frame of its size, not reallocated; past the pool's
// depth it is freed.
func TestReleasedTexturesArePooledThenFreed(t *testing.T) {
	dev := &fakeDevice{}
	im := New(dev)
	d := extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatRGBA8, Size: image.Pt(4, 4), Planes: []extimage.Plane{{Data: make([]byte, 64), Pitch: 16}}}
	a, _ := im.ImportExternal(d, driver.FilterNearest, driver.FilterNearest)
	im.ReleaseExternal(a)
	b, _ := im.ImportExternal(d, driver.FilterNearest, driver.FilterNearest)
	if a != b || dev.made != 1 {
		t.Fatalf("a released texture was not reused (made %d)", dev.made)
	}
	var texs []driver.Texture
	for range poolDepth + 2 {
		x, _ := im.ImportExternal(d, driver.FilterNearest, driver.FilterNearest)
		texs = append(texs, x)
	}
	for _, x := range texs {
		im.ReleaseExternal(x)
	}
	freed := 0
	for _, x := range texs {
		if x.(*fakeTexture).released {
			freed++
		}
	}
	if freed != 2 {
		t.Fatalf("%d textures freed past a pool of %d; want 2", freed, poolDepth)
	}
}
