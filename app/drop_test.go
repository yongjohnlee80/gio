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
