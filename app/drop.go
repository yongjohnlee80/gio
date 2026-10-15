// SPDX-License-Identifier: Unlicense OR MIT

package app

import "github.com/yongjohnlee80/gio/f32"

// DropEvent is data dropped onto a desktop window by another application. It is delivered by
// Window.Event, not the in-window transfer router. Type is the offered MIME type (text/uri-list
// for files, or text/plain); Data is a complete, owned copy. Position is in client pixels.
// A drop does not change keyboard focus: the client decides how to use its data.
type DropEvent struct {
	Type     string
	Data     []byte
	Position f32.Point
}

func (DropEvent) ImplementsEvent() {}

// Bound desktop transfer buffering so a source that never closes or floods its pipe cannot
// keep a window's resources indefinitely. Larger offers are refused, not truncated/pasted.
const maxDropBytes = 16 << 20

func dropMIME(offered []string) string {
	for _, want := range []string{"text/uri-list", "text/plain;charset=utf-8", "text/plain;charset=UTF-8", "text/plain"} {
		for _, have := range offered {
			if have == want {
				return have
			}
		}
	}
	return ""
}
