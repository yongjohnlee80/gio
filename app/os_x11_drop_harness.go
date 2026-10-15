// SPDX-License-Identifier: Unlicense OR MIT

//go:build gio_xdnd_test && ((linux && !android) || freebsd || openbsd) && !nox11

package app

// This cgo harness is opt-in: Go cannot import C from a _test.go file. Its two
// independent Xlib connections exercise the real backend through the X server.

/*
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <stdlib.h>
static void xdnd_test_message(Display *d, Window to, Atom type, long a, long b, long c, long e, long f) {
 XEvent v = {0}; v.xclient.type=ClientMessage; v.xclient.window=to;
 v.xclient.message_type=type; v.xclient.format=32;
 v.xclient.data.l[0]=a; v.xclient.data.l[1]=b; v.xclient.data.l[2]=c;
 v.xclient.data.l[3]=e; v.xclient.data.l[4]=f;
 XSendEvent(d,to,False,NoEventMask,&v); XSync(d,False);
}
static long xdnd_test_word(XEvent *v, int i) {return v->xclient.data.l[i];}
static void xdnd_test_reply(Display *d, XSelectionRequestEvent *r, char *s, int n, int incr) {
 if (incr) {
  long size=n;
  XChangeProperty(d,r->requestor,r->property,XInternAtom(d,"INCR",False),32,PropModeReplace,(unsigned char*)&size,1);
 } else {
  XChangeProperty(d,r->requestor,r->property,r->target,8,PropModeReplace,(unsigned char*)s,n);
 }
 XEvent v = {0}; v.xselection.type=SelectionNotify; v.xselection.requestor=r->requestor;
 v.xselection.selection=r->selection; v.xselection.target=r->target;
 v.xselection.property=r->property; v.xselection.time=r->time;
 XSendEvent(d,r->requestor,False,NoEventMask,&v); XSync(d,False);
}
*/
import "C"

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type xdndHarness struct {
	target  *x11Window
	source  *C.Display
	window  C.Window
	request C.XSelectionRequestEvent
}

type xdndMessage struct {
	kind  string
	words [5]int64
}

func newXDNDHarness() (*xdndHarness, error) {
	target, source := C.XOpenDisplay(nil), C.XOpenDisplay(nil)
	if target == nil || source == nil {
		if target != nil {
			C.XCloseDisplay(target)
		}
		if source != nil {
			C.XCloseDisplay(source)
		}
		return nil, errors.New("XDND tests require DISPLAY (run under xvfb-run)")
	}
	h := &xdndHarness{source: source}
	w := &x11Window{x: target, w: &callbacks{w: new(Window)}}
	h.target = w
	w.xw = C.XCreateSimpleWindow(target, C.XDefaultRootWindow(target), 23, 37, 100, 100, 0, 0, 0)
	h.window = C.XCreateSimpleWindow(source, C.XDefaultRootWindow(source), 0, 0, 1, 1, 0, 0, 0)
	fds := make([]int, 2)
	if err := unix.Pipe2(fds, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		h.close()
		return nil, err
	}
	w.notify.read, w.notify.write = fds[0], fds[1]
	w.handler = x11EventHandler{w: w, xev: new(C.XEvent)}
	w.atoms.plaintext = w.atom("text/plain;charset=utf-8", false)
	w.initDrop()
	C.XSync(target, C.False)
	return h, nil
}

func (h *xdndHarness) close() {
	if h.target != nil {
		if h.target.notify.read != 0 {
			unix.Close(h.target.notify.read)
			unix.Close(h.target.notify.write)
		}
		C.XDestroyWindow(h.target.x, h.target.xw)
		C.XCloseDisplay(h.target.x)
	}
	if h.source != nil {
		if h.window != 0 {
			C.XDestroyWindow(h.source, h.window)
		}
		C.XCloseDisplay(h.source)
	}
}

func (h *xdndHarness) enter(supported, typeList bool) {
	w := h.target
	typ := w.drop.uri
	if !supported {
		typ = w.atom("image/png", false)
	}
	flags := C.long(5 << 24)
	if typeList {
		flags |= 1
		C.XChangeProperty(h.source, h.window, w.drop.typeList, C.XA_ATOM, 32, C.PropModeReplace, (*C.uchar)(unsafe.Pointer(&typ)), 1)
	}
	C.XSetSelectionOwner(h.source, w.drop.selection, h.window, C.CurrentTime)
	C.xdnd_test_message(h.source, w.xw, w.drop.enter, C.long(h.window), flags, C.long(typ), 0, 0)
	// Window origin (23,37), root (34,50) -> client (11,13).
	C.xdnd_test_message(h.source, w.xw, w.drop.position, C.long(h.window), 0, C.long(34<<16|50), 0, C.long(w.drop.copy))
}

func (h *xdndHarness) drop() {
	w := h.target
	C.xdnd_test_message(h.source, w.xw, w.drop.drop, C.long(h.window), 0, 0, 0, 0)
}

func (h *xdndHarness) reply(payload string, incr bool) {
	s := C.CString(payload)
	defer C.free(unsafe.Pointer(s))
	flag := C.int(0)
	if incr {
		flag = 1
	}
	C.xdnd_test_reply(h.source, &h.request, s, C.int(len(payload)), flag)
}

func (h *xdndHarness) next() xdndMessage {
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		C.XSync(h.target.x, C.False)
		h.target.handler.handleEvents()
		if C.XPending(h.source) == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		var ev C.XEvent
		C.XNextEvent(h.source, &ev)
		switch (*C.XAnyEvent)(unsafe.Pointer(&ev))._type {
		case C.SelectionRequest:
			h.request = *(*C.XSelectionRequestEvent)(unsafe.Pointer(&ev))
			return xdndMessage{kind: "request"}
		case C.ClientMessage:
			msg := (*C.XClientMessageEvent)(unsafe.Pointer(&ev))
			out := xdndMessage{}
			if msg.message_type == h.target.drop.status {
				out.kind = "status"
			}
			if msg.message_type == h.target.drop.finished {
				out.kind = "finished"
			}
			for i := range out.words {
				out.words[i] = int64(C.xdnd_test_word(&ev, C.int(i)))
			}
			return out
		}
	}
	return xdndMessage{}
}

func (h *xdndHarness) copyAction() int64  { return int64(h.target.drop.copy) }
func (h *xdndHarness) drops() []DropEvent { return h.target.w.w.coalesced.drops }

func (h *xdndHarness) expire() time.Duration {
	h.target.drop.deadline = time.Now().Add(40 * time.Millisecond)
	// A broken poll timeout must fail the assertion rather than hang the test.
	done := make(chan struct{})
	watchdog := time.AfterFunc(time.Second, func() { h.target.invalidate(); close(done) })
	defer func() {
		if !watchdog.Stop() {
			<-done
		}
	}()
	start := time.Now()
	// A dispatch can return for an unrelated server notification. The native
	// Event loop dispatches again while no application event is ready.
	for h.target.drop.waiting && time.Since(start) < 500*time.Millisecond {
		h.target.dispatch() // real event-loop poll/deadline checks
	}
	return time.Since(start)
}
