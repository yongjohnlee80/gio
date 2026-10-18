// SPDX-License-Identifier: Unlicense OR MIT

package driver

import "github.com/yongjohnlee80/gio/op/extimage"

// ExternalImporter is the driver side of the extimage hardware contract: a device (or the CPU
// driver over any device) that turns frames into textures the stock texture material samples
// as sRGB RGBA. Device does not include it; the renderer finds it by a type assertion, and a
// device without it draws frames only through the CPU driver.
type ExternalImporter interface {
	// ExternalTuples are the combinations this importer proved it draws, each with its path.
	ExternalTuples() []extimage.Tuple
	// ImportExternal makes a texture showing the whole frame d, which matches one of its
	// tuples. The texture does not own the frame's memory.
	ImportExternal(d extimage.Desc, minFilter, magFilter TextureFilter) (Texture, error)
	// UpdateExternal uploads d.Dirty into t, a texture this importer made for d's Source at
	// revision d.Base with d's Size, Format and Color. false: it cannot (a zero-copy texture is
	// the frame's own memory); the caller imports the whole frame instead.
	UpdateExternal(t Texture, d extimage.Desc) bool
	// ReleaseExternal gives back a texture it made, once no submission samples it.
	ReleaseExternal(t Texture)
}
