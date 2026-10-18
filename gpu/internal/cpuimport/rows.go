// SPDX-License-Identifier: Unlicense OR MIT

package cpuimport

import "github.com/yongjohnlee80/gio/op/extimage"

// planeRows is plane i's size in samples: NV12's chroma plane is half the frame each way.
func planeRows(d extimage.Desc, i int) (w, h int) {
	if d.Format == extimage.FormatNV12 && i == 1 {
		return (d.Size.X + 1) / 2, (d.Size.Y + 1) / 2
	}
	return d.Size.X, d.Size.Y
}
