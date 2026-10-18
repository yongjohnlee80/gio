// SPDX-License-Identifier: Unlicense OR MIT

package extimage

import (
	"image"
	"strings"
	"testing"
)

func rgba(w, h int) Desc {
	return Desc{Kind: KindMemory, Format: FormatRGBA8, Size: image.Pt(w, h), Planes: []Plane{{Data: make([]byte, w*h*4), Pitch: uint32(w * 4)}}}
}

func TestNewFrameRefusesMalformedDescriptions(t *testing.T) {
	cases := map[string]func(*Desc){
		"empty size":        func(d *Desc) { d.Size = image.Point{} },
		"unknown format":    func(d *Desc) { d.Format = 99 },
		"plane count":       func(d *Desc) { d.Planes = nil },
		"short pitch":       func(d *Desc) { d.Planes[0].Pitch = 3 },
		"short data":        func(d *Desc) { d.Planes[0].Data = d.Planes[0].Data[:10] },
		"dirty outside":     func(d *Desc) { d.Dirty = image.Rect(0, 0, 9, 9) },
		"base not before":   func(d *Desc) { d.Source, d.Rev, d.Base = NewSource(), 2, 2 },
		"dmabuf without fd": func(d *Desc) { d.Kind, d.Planes[0].FD = KindDMABuf, -1 },
		"no iosurface":      func(d *Desc) { d.Kind = KindIOSurface },
	}
	for name, spoil := range cases {
		d := rgba(4, 4)
		spoil(&d)
		if _, err := NewFrame(d, func() {}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewFrame(rgba(4, 4), func() {}); err != nil {
		t.Fatalf("a well-formed frame refused: %v", err)
	}
	if _, err := NewFrame(rgba(4, 4), nil); err == nil || !strings.Contains(err.Error(), "release") {
		t.Fatalf("a frame without a release function accepted (%v)", err)
	}
}

// Every holder's Drop counts; the producer's release runs once, on the last.
func TestAFrameIsReleasedExactlyOnceAfterItsLastHolder(t *testing.T) {
	n := 0
	f, err := NewFrame(rgba(2, 2), func() { n++ })
	if err != nil {
		t.Fatal(err)
	}
	f.Retain()
	f.Retain()
	f.Drop()
	f.Drop()
	if n != 0 {
		t.Fatalf("released with a holder left (%d)", n)
	}
	f.Drop()
	if n != 1 {
		t.Fatalf("released %d times after the last Drop; want 1", n)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a Drop past the last did not panic")
		}
	}()
	f.Drop()
}

// NewFrame keeps its own copy of the planes: the caller's slice can change after.
func TestAFrameOwnsItsPlaneList(t *testing.T) {
	d := rgba(2, 2)
	f, _ := NewFrame(d, func() {})
	d.Planes[0].Pitch = 1
	if f.Desc().Planes[0].Pitch != 8 {
		t.Fatal("the frame followed its caller's plane slice")
	}
}

func TestCapsMatchPrefersZeroCopyAndChecksModifierAndSize(t *testing.T) {
	c := Caps{Tuples: []Tuple{
		{Kind: KindDMABuf, Format: FormatNV12, Modifier: 0, Path: PathCPU},
		{Kind: KindDMABuf, Format: FormatNV12, Modifier: 0, Path: PathZeroCopy, MaxSize: image.Pt(1920, 1080)},
		{Kind: KindMemory, Format: FormatRGBA8, Path: PathCPU},
	}}
	d := Desc{Kind: KindDMABuf, Format: FormatNV12, Size: image.Pt(1280, 720)}
	if t0, ok := c.Match(d); !ok || t0.Path != PathZeroCopy {
		t.Fatalf("matched %+v %v; want the zero-copy tuple", t0, ok)
	}
	d.Size = image.Pt(3840, 2160) // past the zero-copy tuple's limit
	if t0, ok := c.Match(d); !ok || t0.Path != PathCPU {
		t.Fatalf("a 4K frame matched %+v %v; want the CPU tuple", t0, ok)
	}
	d.Modifier = 0x0100000000000001 // a tiled modifier no tuple lists
	if _, ok := c.Match(d); ok {
		t.Fatal("a tiled modifier matched")
	}
}
