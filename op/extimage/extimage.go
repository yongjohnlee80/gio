// SPDX-License-Identifier: Unlicense OR MIT

// Package extimage draws pictures that live outside Go's image types: frames a decoder or
// another GPU device produced (a Linux DMA-BUF, a macOS IOSurface) and pixels an application
// edits in place. It is the fork's hardware contract, defined before any driver: a Frame
// describes one picture and its ownership, Caps says what a window can draw and how, and each
// GPU API imports frames behind one driver interface (gpu/internal/driver.ExternalImporter), the
// CPU path being one of those drivers.
//
// Ownership: a Frame is immutable once made and carries one reference count. Every holder
// (an application's stream, a recorded op, each GPU texture-cache entry) takes a reference and
// drops it; the producer's release function runs exactly once, when the last is dropped, outside
// every lock of this package. A producer's file descriptors and surfaces are borrowed, never
// closed or released by the window.
package extimage

import (
	"errors"
	"fmt"
	"image"
	"sync/atomic"

	"github.com/yongjohnlee80/gio/internal/ops"
	"github.com/yongjohnlee80/gio/op"
	"github.com/yongjohnlee80/gio/op/paint"
)

// Kind is where a frame's pixels are.
type Kind uint8

const (
	// KindMemory: Planes' Data, in Go memory.
	KindMemory Kind = iota + 1
	// KindDMABuf: Linux dma-buf file descriptors, Planes' FD, Offset and Pitch, with Modifier.
	KindDMABuf
	// KindIOSurface: a macOS IOSurfaceRef.
	KindIOSurface
)

func (k Kind) String() string {
	switch k {
	case KindMemory:
		return "memory"
	case KindDMABuf:
		return "dmabuf"
	case KindIOSurface:
		return "iosurface"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Format is a frame's pixel layout. The set is open: a new format is a new value plus the tuples
// a driver proves for it, and a frame in a format no driver lists is refused.
type Format uint8

const (
	// FormatRGBA8: one plane, 4 bytes a pixel, R G B A, sRGB.
	FormatRGBA8 Format = iota + 1
	// FormatBGRA8: one plane, 4 bytes a pixel, B G R A, sRGB.
	FormatBGRA8
	// FormatNV12: two planes, Y (1 byte a pixel) and interleaved CbCr (2 bytes per 2×2 pixels),
	// encoded as Desc.Color says.
	FormatNV12
)

func (f Format) String() string {
	switch f {
	case FormatRGBA8:
		return "RGBA8"
	case FormatBGRA8:
		return "BGRA8"
	case FormatNV12:
		return "NV12"
	}
	return fmt.Sprintf("Format(%d)", uint8(f))
}

// planes is how many planes a format has; 0 for an unknown format.
func (f Format) planes() int {
	switch f {
	case FormatRGBA8, FormatBGRA8:
		return 1
	case FormatNV12:
		return 2
	}
	return 0
}

// Matrix is a YUV frame's colour matrix.
type Matrix uint8

const (
	MatrixBT709  Matrix = iota // HD; the zero value
	MatrixBT601                // SD
	MatrixBT2020               // UHD
)

// Range is a YUV frame's quantisation range.
type Range uint8

const (
	RangeLimited Range = iota // 16–235 (Y), 16–240 (CbCr); the zero value
	RangeFull                 // 0–255
)

// Color is a YUV frame's encoding; ignored for RGB formats. The zero value is BT.709, limited.
type Color struct {
	Matrix Matrix
	Range  Range
}

// Plane is one plane of a frame's pixels.
type Plane struct {
	// Data is the plane's bytes, for KindMemory.
	Data []byte
	// FD is a dma-buf file descriptor, for KindDMABuf. It is borrowed: the window never closes
	// it. An importer that needs its own duplicates it.
	FD int
	// Offset is where the plane starts in its memory, in bytes.
	Offset uint32
	// Pitch is the bytes from one row of the plane to the next.
	Pitch uint32
}

// SourceID names a sequence of frames of one image, so successive frames can reuse a texture.
type SourceID uint64

var lastSource atomic.Uint64

// NewSource returns a SourceID no other call in this process returns.
func NewSource() SourceID { return SourceID(lastSource.Add(1)) }

// Desc describes a frame's memory. NewFrame copies it: a Frame never changes.
type Desc struct {
	Kind   Kind
	Format Format
	Color  Color
	Size   image.Point
	// Planes, for KindMemory and KindDMABuf: one per plane of Format.
	Planes []Plane
	// Modifier is the DRM format modifier of a KindDMABuf frame (0 is DRM_FORMAT_MOD_LINEAR).
	Modifier uint64
	// IOSurface is a KindIOSurface frame's IOSurfaceRef, borrowed: an importer that keeps it
	// retains it.
	IOSurface uintptr
	// Source names the image this frame is a revision of; 0 for none.
	Source SourceID
	// Rev is this frame's revision of Source, strictly increasing. Base is the revision Dirty is
	// relative to: Dirty covers every pixel that differs between revision Base and Rev, in frame
	// pixels, empty meaning all of them. An importer whose texture for the Source is at revision
	// Base exactly, with the same Size, Format, Color and device, may upload only Dirty; any
	// other state uploads the whole frame. Only KindMemory frames honour it.
	Rev, Base uint64
	Dirty     image.Rectangle
}

// Frame is one picture to draw: a Desc and its lease. It must be complete when made: no work
// that writes its memory may still be running.
type Frame struct {
	desc    Desc
	refs    atomic.Int32
	release func()
}

// NewFrame validates d and returns a frame holding one reference, the producer's: hand it to a
// stream (or an ImageOp writer that keeps a reference) and do not use it after. release runs
// exactly once, when the last reference is dropped; it must not block.
func NewFrame(d Desc, release func()) (*Frame, error) {
	if release == nil {
		return nil, errors.New("extimage: NewFrame needs a release function")
	}
	if err := d.validate(); err != nil {
		return nil, err
	}
	d.Planes = append([]Plane(nil), d.Planes...)
	f := &Frame{desc: d, release: release}
	f.refs.Store(1)
	return f, nil
}

// Desc is the frame's description. Its Planes are the frame's own copy: do not modify them.
func (f *Frame) Desc() Desc { return f.desc }

// Retain takes a reference for a new holder and returns f.
func (f *Frame) Retain() *Frame {
	if f.refs.Add(1) <= 1 {
		panic("extimage: Retain of a released frame")
	}
	return f
}

// Drop gives up a reference. The last Drop calls the producer's release, on the dropping
// goroutine; call it outside your own locks.
func (f *Frame) Drop() {
	switch n := f.refs.Add(-1); {
	case n == 0:
		f.release()
	case n < 0:
		panic("extimage: Drop of a released frame")
	}
}

func (d Desc) validate() error {
	if d.Size.X <= 0 || d.Size.Y <= 0 {
		return fmt.Errorf("extimage: frame size %v", d.Size)
	}
	n := d.Format.planes()
	if n == 0 {
		return fmt.Errorf("extimage: unknown format %v", d.Format)
	}
	if !d.Dirty.Empty() && !d.Dirty.In(image.Rectangle{Max: d.Size}) {
		return fmt.Errorf("extimage: dirty %v outside a %v frame", d.Dirty, d.Size)
	}
	if d.Source != 0 && d.Base >= d.Rev && d.Rev != 0 {
		return fmt.Errorf("extimage: base revision %d not before revision %d", d.Base, d.Rev)
	}
	switch d.Kind {
	case KindMemory, KindDMABuf:
		if len(d.Planes) != n {
			return fmt.Errorf("extimage: %v has %d planes, the frame %d", d.Format, n, len(d.Planes))
		}
		for i, p := range d.Planes {
			w, h := d.planeSize(i)
			row := uint32(w * d.Format.bytesPerPixel(i))
			if p.Pitch < row {
				return fmt.Errorf("extimage: plane %d pitch %d below its row of %d bytes", i, p.Pitch, row)
			}
			if d.Kind == KindMemory && uint64(p.Offset)+uint64(p.Pitch)*uint64(h-1)+uint64(row) > uint64(len(p.Data)) {
				return fmt.Errorf("extimage: plane %d needs more than its %d bytes", i, len(p.Data))
			}
			if d.Kind == KindDMABuf && p.FD < 0 {
				return fmt.Errorf("extimage: plane %d has no file descriptor", i)
			}
		}
	case KindIOSurface:
		if d.IOSurface == 0 {
			return errors.New("extimage: no IOSurface")
		}
	default:
		return fmt.Errorf("extimage: unknown kind %v", d.Kind)
	}
	return nil
}

// planeSize is plane i's size in its own pixels (samples).
func (d Desc) planeSize(i int) (w, h int) {
	if d.Format == FormatNV12 && i == 1 {
		return (d.Size.X + 1) / 2, (d.Size.Y + 1) / 2
	}
	return d.Size.X, d.Size.Y
}

// bytesPerPixel is plane i's bytes per sample.
func (f Format) bytesPerPixel(i int) int {
	switch f {
	case FormatRGBA8, FormatBGRA8:
		return 4
	case FormatNV12:
		if i == 0 {
			return 1
		}
		return 2
	}
	return 0
}

// Path is how a tuple is drawn.
type Path uint8

const (
	// PathZeroCopy: the device samples the frame's memory itself.
	PathZeroCopy Path = iota + 1
	// PathCPU: the CPU driver maps the frame and uploads it.
	PathCPU
)

// Tuple is one combination a window draws, as proven when its device was set up.
type Tuple struct {
	Kind     Kind
	Format   Format
	Modifier uint64 // KindDMABuf
	Path     Path
	MaxSize  image.Point // zero: no limit beyond the device's texture size
}

// Caps is what one window draws. A frame matching no tuple is refused, never handed to a driver
// that cannot take it.
type Caps struct {
	// Gen changes when the device or the renderer changes; read Caps again then.
	Gen uint64
	// Driver is the device's importer: "vulkan", "metal", or "cpu" when there is none.
	Driver string
	// DeviceID is the GPU's identity (Vulkan's deviceUUID; Metal's registryID, in the first 8
	// bytes), so a producer can render on the same GPU; zero for "cpu".
	DeviceID [16]byte
	Tuples   []Tuple
}

// ErrUnsupported reports a frame no tuple of the window's Caps matches.
var ErrUnsupported = errors.New("extimage: frame not in the window's caps")

// Match is the tuple d is drawn with: the zero-copy one if both paths list it.
func (c Caps) Match(d Desc) (Tuple, bool) {
	var best Tuple
	found := false
	for _, t := range c.Tuples {
		if t.Kind != d.Kind || t.Format != d.Format {
			continue
		}
		if d.Kind == KindDMABuf && t.Modifier != d.Modifier {
			continue
		}
		if (t.MaxSize.X > 0 && d.Size.X > t.MaxSize.X) || (t.MaxSize.Y > 0 && d.Size.Y > t.MaxSize.Y) {
			continue
		}
		if !found || t.Path == PathZeroCopy {
			best, found = t, true
		}
	}
	return best, found
}

// ImageOp draws a Frame as paint.ImageOp draws an image: it sets the brush to the frame, in
// pixel units of its Size, for a following paint.PaintOp.
//
// The op itself holds no reference. Its writer keeps the frame alive until the ops are handed to
// the window and its Frame call returns; the renderer takes its own reference while it imports
// the frame, and its texture cache holds one for as long as it keeps the texture.
type ImageOp struct {
	Filter paint.ImageFilter
	frame  *Frame
	report func(error)
}

// NewImageOp is the brush for f. report, if not nil, is told the outcome of each import: nil,
// or the error that kept the frame from being drawn. It is called on the window's GPU goroutine
// and must not block.
func NewImageOp(f *Frame, report func(error)) ImageOp {
	return ImageOp{frame: f, report: report}
}

// Size is the frame's size in pixels.
func (i ImageOp) Size() image.Point {
	if i.frame == nil {
		return image.Point{}
	}
	return i.frame.desc.Size
}

// Add records the op.
func (i ImageOp) Add(o *op.Ops) {
	if i.frame == nil {
		return
	}
	data := ops.Write2(&o.Internal, ops.TypeExternalImageLen, i.frame, i.report)
	data[0] = byte(ops.TypeExternalImage)
	data[1] = byte(i.Filter)
}

// FrameOf decodes an external image op's refs, for the renderer.
func FrameOf(refs []any) (*Frame, func(error)) {
	f, _ := refs[0].(*Frame)
	r, _ := refs[1].(func(error))
	return f, r
}
