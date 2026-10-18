// SPDX-License-Identifier: Unlicense OR MIT

//go:build freebsd && !novulkan

package vulkan

// externalSetup: no dma-buf import on this platform.
func (b *Backend) externalSetup(bool) {}
