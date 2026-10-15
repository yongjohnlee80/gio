// SPDX-License-Identifier: Unlicense OR MIT

//go:build gio_xdnd_test && ((linux && !android) || freebsd || openbsd) && !nox11

package app

import (
	"testing"
	"time"
)

func TestXDNDSourceExchange(t *testing.T) {
	// Keep one client connected across cells so Xvfb does not reset its server
	// between the last close and the next XOpenDisplay.
	guard, err := newXDNDHarness()
	if err != nil {
		t.Fatal(err)
	}
	defer guard.close()
	for _, mode := range []string{"uri-inline", "uri-type-list", "unsupported", "incr", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			h, err := newXDNDHarness()
			if err != nil {
				t.Fatal(err)
			}
			defer h.close()
			h.enter(mode != "unsupported", mode == "uri-type-list")
			status := h.next()
			wantFlags := int64(3)
			wantAction := h.copyAction()
			if mode == "unsupported" {
				wantFlags = 2
				wantAction = 0
			}
			if status.kind != "status" || status.words[1] != wantFlags || status.words[4] != wantAction {
				t.Fatalf("status %+v, want flags=%d action=%d", status, wantFlags, wantAction)
			}
			h.drop()
			if mode != "unsupported" {
				if request := h.next(); request.kind != "request" {
					t.Fatalf("finished before requesting/reading selection: %+v", request)
				}
				if len(h.drops()) != 0 {
					t.Fatal("drop delivered before selection was answered")
				}
				if mode == "timeout" {
					if elapsed := h.expire(); elapsed > 500*time.Millisecond {
						t.Fatalf("deadline took %v", elapsed)
					}
				} else {
					h.reply("file:///tmp/screen%20shot.png\r\n", mode == "incr")
				}
			}
			finished := h.next()
			wantAccepted := int64(0)
			valid := mode == "uri-inline" || mode == "uri-type-list"
			if valid {
				wantAccepted = 1
			}
			if finished.kind != "finished" || finished.words[1] != wantAccepted {
				t.Fatalf("finished %+v, want accepted %d", finished, wantAccepted)
			}
			if valid {
				drops := h.drops()
				if len(drops) != 1 || drops[0].Type != "text/uri-list" || string(drops[0].Data) != "file:///tmp/screen%20shot.png\r\n" || drops[0].Position.X != 11 || drops[0].Position.Y != 13 {
					t.Fatalf("drops %+v", drops)
				}
				if finished.words[2] != h.copyAction() {
					t.Fatalf("finished action %d", finished.words[2])
				}
			} else if len(h.drops()) != 0 {
				t.Fatalf("refused drop delivered: %+v", h.drops())
			}
			if mode == "timeout" {
				if h.lateReplyLeavesProperty() {
					t.Fatal("late SelectionNotify retained GIO_DESKTOP_DROP")
				}
				if len(h.drops()) != 0 {
					t.Fatal("late selection became a paste after timeout")
				}
			}
		})
	}
}
