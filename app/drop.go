// SPDX-License-Identifier: Unlicense OR MIT

package app

import "github.com/yongjohnlee80/gio/f32"

// DropEvent is data dropped onto a desktop window by another application. It is delivered by
// Window.Event, not the in-window transfer router. Type is the offered MIME type (text/uri-list
// or text/plain); Data is a complete, owned copy. URI lists can contain non-file URIs and
// comment lines: clients must filter/decode them. Position is in client pixels.
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

// The wl_data_device_manager.dnd_action values dropOutcome reads (stable since version 3).
const (
	wlActionNone = 0
	wlActionCopy = 1
)

// dropOutcome is what a Wayland drop of an offer at version, with the last action the compositor
// selected, does: whether its data is read, and whether a successful read ends with
// wl_data_offer.finish.
//
// Only a copy is finished. A compositor can keep a move it chose before the client's set_actions
// (Hyprland 0.56 sends action move ahead of enter and never revises it), and finishing a move tells
// the source to delete its files. So a move or ask is read and the offer destroyed unfinished: the
// source sees the drop cancelled and keeps its files, and the client still has the data. An offer
// before version 3 has no actions or finish. No action at all is refused.
func dropOutcome(version, action uint32) (read, finish bool) {
	switch {
	case version < 3:
		return true, false
	case action == wlActionCopy:
		return true, true
	case action != wlActionNone:
		return true, false
	}
	return false, false
}
