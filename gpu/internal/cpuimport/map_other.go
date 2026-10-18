// SPDX-License-Identifier: Unlicense OR MIT

//go:build !linux && !darwin

package cpuimport

import (
	"fmt"

	"github.com/yongjohnlee80/gio/op/extimage"
)

// platformTuples: memory frames only on this platform, so far.
func platformTuples(max int) []extimage.Tuple { return nil }

func mapPlatform(d extimage.Desc) ([]plane, func(), error) {
	return nil, nil, fmt.Errorf("cpuimport: cannot map a %v frame on this platform", d.Kind)
}
