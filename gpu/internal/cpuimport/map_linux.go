// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux

package cpuimport

import (
	"fmt"
	"image"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/yongjohnlee80/gio/op/extimage"
)

// platformTuples: LINEAR dma-bufs of every format. A tiled modifier's memory is not row-major,
// so the CPU cannot read it; such a frame matches no CPU tuple.
func platformTuples(max int) []extimage.Tuple {
	var out []extimage.Tuple
	for _, f := range formats {
		out = append(out, extimage.Tuple{Kind: extimage.KindDMABuf, Format: f, Modifier: modLinear, Path: extimage.PathCPU, MaxSize: image.Pt(max, max)})
	}
	return out
}

// modLinear is DRM_FORMAT_MOD_LINEAR.
const modLinear = 0

// The dma-buf sync ioctl: DMA_BUF_IOCTL_SYNC is _IOW('b', 0, struct dma_buf_sync { __u64 flags; }).
const (
	dmaBufIoctlSync = 0x40086200
	dmaBufSyncRead  = 1 << 0
	dmaBufSyncStart = 0 << 2
	dmaBufSyncEnd   = 1 << 2
)

func dmaBufSync(fd int, flags uint64) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), dmaBufIoctlSync, uintptr(unsafe.Pointer(&flags)))
	if errno != 0 {
		return errno
	}
	return nil
}

// mapPlatform maps each plane of a LINEAR dma-buf read-only. The producer's fd is borrowed: a
// mapping does not need it to stay open, so it is neither duplicated nor closed. The CPU access
// is bracketed by the sync ioctl, as the kernel asks of a mapped dma-buf.
func mapPlatform(d extimage.Desc) ([]plane, func(), error) {
	if d.Kind != extimage.KindDMABuf || d.Modifier != modLinear {
		return nil, nil, fmt.Errorf("cpuimport: cannot map a %v frame with modifier %#x", d.Kind, d.Modifier)
	}
	type mapping struct {
		fd  int
		mem []byte
	}
	var maps []mapping
	done := func() {
		for _, m := range maps {
			dmaBufSync(m.fd, dmaBufSyncRead|dmaBufSyncEnd)
			unix.Munmap(m.mem)
		}
	}
	out := make([]plane, len(d.Planes))
	for i, p := range d.Planes {
		_, h := planeRows(d, i)
		size := int(p.Offset) + int(p.Pitch)*h
		mem, err := unix.Mmap(p.FD, 0, size, unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			done()
			return nil, nil, fmt.Errorf("cpuimport: map plane %d: %w", i, err)
		}
		if err := dmaBufSync(p.FD, dmaBufSyncRead|dmaBufSyncStart); err != nil {
			unix.Munmap(mem)
			done()
			return nil, nil, fmt.Errorf("cpuimport: sync plane %d: %w", i, err)
		}
		maps = append(maps, mapping{fd: p.FD, mem: mem})
		out[i] = plane{data: mem[p.Offset:], pitch: int(p.Pitch)}
	}
	return out, done, nil
}
