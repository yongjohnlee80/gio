// SPDX-License-Identifier: Unlicense OR MIT

//go:build ((linux && !android) || freebsd || openbsd) && !nox11

package app

/*
#include <X11/Xlib.h>
#include <X11/Xatom.h>
static long gio_drop_word(XClientMessageEvent *e, int i) { return e->data.l[i]; }
static void gio_drop_message(Display *d, Window dest, Atom type, long a, long b, long c, long e, long f) {
	XEvent ev = {0};
	ev.xclient.type = ClientMessage;
	ev.xclient.display = d;
	ev.xclient.window = dest;
	ev.xclient.message_type = type;
	ev.xclient.format = 32;
	ev.xclient.data.l[0]=a; ev.xclient.data.l[1]=b; ev.xclient.data.l[2]=c; ev.xclient.data.l[3]=e; ev.xclient.data.l[4]=f;
	XSendEvent(d,dest,False,NoEventMask,&ev);
	XFlush(d);
}
*/
import "C"

import (
	"time"
	"unsafe"

	"github.com/yongjohnlee80/gio/f32"
)

type x11Drop struct {
	aware, enter, position, leave, drop, status, finished, selection, typeList, copy, uri, plain, content, incr C.Atom
	source                                                                                                      C.Window
	typ                                                                                                         C.Atom
	version                                                                                                     int
	pos                                                                                                         f32.Point
	waiting                                                                                                     bool
	deadline                                                                                                    time.Time
	timestamp                                                                                                   C.Time
}

func (w *x11Window) initDrop() {
	d := &w.drop
	d.aware = w.atom("XdndAware", false)
	d.enter = w.atom("XdndEnter", false)
	d.position = w.atom("XdndPosition", false)
	d.leave = w.atom("XdndLeave", false)
	d.drop = w.atom("XdndDrop", false)
	d.status = w.atom("XdndStatus", false)
	d.finished = w.atom("XdndFinished", false)
	d.selection = w.atom("XdndSelection", false)
	d.typeList = w.atom("XdndTypeList", false)
	d.copy = w.atom("XdndActionCopy", false)
	d.uri = w.atom("text/uri-list", false)
	d.plain = w.atom("text/plain", false)
	d.content = w.atom("GIO_DESKTOP_DROP", false)
	d.incr = w.atom("INCR", false)
	version := C.long(5)
	C.XChangeProperty(w.x, w.xw, d.aware, C.XA_ATOM, 32, C.PropModeReplace, (*C.uchar)(unsafe.Pointer(&version)), 1)
}

func (w *x11Window) dropMessage(e *C.XClientMessageEvent) bool {
	d := &w.drop
	word := func(i int) C.long { return C.gio_drop_word(e, C.int(i)) }
	source := C.Window(word(0))
	switch e.message_type {
	case d.enter:
		if d.waiting {
			w.finishDrop(false)
		}
		d.source = source
		d.version = int(uint32(word(1)) >> 24)
		d.typ = 0
		if d.version < 3 {
			return true
		}
		d.version = min(d.version, 5)
		var offered []C.Atom
		if word(1)&1 != 0 {
			var typ C.Atom
			var format C.int
			var n, after C.ulong
			var data *C.uchar
			if C.XGetWindowProperty(w.x, source, d.typeList, 0, 1024, C.False, C.XA_ATOM, &typ, &format, &n, &after, &data) == C.Success && data != nil {
				if format == 32 && after == 0 {
					offered = append(offered, unsafe.Slice((*C.Atom)(unsafe.Pointer(data)), int(n))...)
				}
				C.XFree(unsafe.Pointer(data))
			}
		} else {
			for i := 2; i < 5; i++ {
				offered = append(offered, C.Atom(word(i)))
			}
		}
		for _, want := range []C.Atom{d.uri, w.atoms.plaintext, d.plain} {
			for _, have := range offered {
				if want == have {
					d.typ = want
					return true
				}
			}
		}
		return true
	case d.position:
		if source != d.source {
			return true
		}
		packed := uint32(word(2))
		x, y := C.int(int16(packed>>16)), C.int(int16(packed))
		var localX, localY C.int
		var child C.Window
		C.XTranslateCoordinates(w.x, C.XDefaultRootWindow(w.x), w.xw, x, y, &localX, &localY, &child)
		d.pos = f32.Pt(float32(localX), float32(localY))
		var accepted C.long
		var action C.long
		if d.typ != 0 {
			accepted = 1
			action = C.long(d.copy)
		}
		C.gio_drop_message(w.x, source, d.status, C.long(w.xw), accepted|2, 0, 0, action)
		return true
	case d.leave:
		if source == d.source && !d.waiting {
			d.source = 0
			d.typ = 0
		}
		return true
	case d.drop:
		if source != d.source {
			return true
		}
		if d.typ == 0 {
			w.finishDrop(false)
			return true
		}
		d.waiting = true
		d.deadline = time.Now().Add(5 * time.Second)
		d.timestamp = C.Time(word(2))
		C.XDeleteProperty(w.x, w.xw, d.content)
		C.XConvertSelection(w.x, d.selection, d.typ, d.content, w.xw, C.Time(word(2)))
		C.XFlush(w.x)
		return true
	}
	return false
}

func (w *x11Window) receiveDrop(e *C.XSelectionEvent) bool {
	d := &w.drop
	if e.selection != d.selection {
		return false
	}
	if !d.waiting {
		return true
	}
	if e.requestor != w.xw || e.target != d.typ || (d.timestamp != 0 && e.time != d.timestamp) {
		return true
	}
	if e.property != d.content {
		w.finishDrop(false)
		return true
	}
	var typ C.Atom
	var format C.int
	var n, after C.ulong
	var data *C.uchar
	status := C.XGetWindowProperty(w.x, w.xw, d.content, 0, maxDropBytes/4+1, C.True, C.AnyPropertyType, &typ, &format, &n, &after, &data)
	if data != nil {
		defer C.XFree(unsafe.Pointer(data))
	}
	// Large incremental selections are explicitly refused, not partially pasted.
	if status != C.Success || typ == d.incr || typ != d.typ || format != 8 || after != 0 || n > maxDropBytes || data == nil {
		w.finishDrop(false)
		return true
	}
	mime := "text/plain"
	if d.typ == d.uri {
		mime = "text/uri-list"
	}
	w.ProcessEvent(DropEvent{Type: mime, Data: C.GoBytes(unsafe.Pointer(data), C.int(n)), Position: d.pos})
	w.finishDrop(true)
	return true
}

func (w *x11Window) finishDrop(ok bool) {
	d := &w.drop
	if d.source != 0 {
		var accepted, action C.long
		if ok {
			accepted = 1
			action = C.long(d.copy)
		}
		C.gio_drop_message(w.x, d.source, d.finished, C.long(w.xw), accepted, action, 0, 0)
	}
	d.source = 0
	d.typ = 0
	d.waiting = false
}
