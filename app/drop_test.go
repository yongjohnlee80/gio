// SPDX-License-Identifier: Unlicense OR MIT

package app

import "testing"

func TestDesktopDropFollowsAnAlreadyQueuedInputFrame(t *testing.T) {
	w := &Window{coalesced: eventSummary{frame: new(frameEvent)}}
	w.processEvent(DropEvent{Type: "text/plain", Data: []byte("drop")})
	e, ok := w.nextEvent()
	if _, frame := e.(FrameEvent); !ok || !frame {
		t.Fatalf("queued input frame overtaken by %T", e)
	}
}

func TestDesktopDropMIMEPreference(t *testing.T) {
	for _, c := range []struct {
		offers []string
		want   string
	}{
		{[]string{"text/plain", "text/uri-list"}, "text/uri-list"},
		{[]string{"image/png", "text/plain;charset=utf-8"}, "text/plain;charset=utf-8"},
		{[]string{"image/png"}, ""},
		{nil, ""},
	} {
		if got := dropMIME(c.offers); got != c.want {
			t.Errorf("%v: got %q, want %q", c.offers, got, c.want)
		}
	}
}

func TestDesktopDropsAreQueuedInOrderWithOwnedData(t *testing.T) {
	w := new(Window)
	data := []byte("file:///tmp/first.png\r\n")
	w.processEvent(DropEvent{Type: "text/uri-list", Data: data})
	data[0] = 'X'
	w.processEvent(DropEvent{Type: "text/plain", Data: []byte("second")})
	for _, want := range []string{"file:///tmp/first.png\r\n", "second"} {
		e, ok := w.nextEvent()
		if !ok {
			t.Fatal("drop lost")
		}
		d, ok := e.(DropEvent)
		if !ok || string(d.Data) != want {
			t.Fatalf("drop %T %v, want %q", e, e, want)
		}
	}
	if _, ok := w.nextEvent(); ok {
		t.Fatal("drop delivered twice")
	}
}

// A Wayland drop reads a copy and finishes it; reads a move or ask without finishing, so the source
// keeps its files (Hyprland keeps the move it chose before set_actions); refuses no action; and
// reads, unfinished, an offer older than actions.
func TestWaylandDropOutcome(t *testing.T) {
	for _, c := range []struct {
		version, action uint32
		read, finish    bool
	}{
		{3, 1, true, true},   // copy
		{3, 2, true, false},  // move: never finish
		{3, 4, true, false},  // ask
		{3, 3, true, false},  // copy|move as one value: not a copy, so unfinished
		{3, 0, false, false}, // none
		{2, 0, true, false},  // before actions
	} {
		read, finish := dropOutcome(c.version, c.action)
		if read != c.read || finish != c.finish {
			t.Errorf("v%d action %d: read %v finish %v, want %v %v", c.version, c.action, read, finish, c.read, c.finish)
		}
	}
}
