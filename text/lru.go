// SPDX-License-Identifier: Unlicense OR MIT

package text

import (
	"image"
	"sync/atomic"
	"unsafe"

	giofont "github.com/yongjohnlee80/gio/font"
	"github.com/yongjohnlee80/gio/io/system"
	"github.com/yongjohnlee80/gio/op"
	"github.com/yongjohnlee80/gio/op/clip"
	"github.com/yongjohnlee80/gio/op/paint"
	"golang.org/x/image/math/fixed"
)

// entry holds a single key-value pair for an LRU cache, and the bytes its value holds.
type entry[K comparable, V any] struct {
	next, prev *entry[K, V]
	key        K
	v          V
	size       int
}

// lru is a generic least-recently-used cache of at most maxSize entries and, when maxBytes is
// set, at most maxBytes bytes of its entries' sizes.
type lru[K comparable, V any] struct {
	m          map[K]*entry[K, V]
	head, tail *entry[K, V]
	maxBytes   int // 0: no byte budget
	bytes      int // the entries' sizes
}

// Get fetches the value associated with the given key, if any.
func (l *lru[K, V]) Get(k K) (V, bool) {
	if lt, ok := l.m[k]; ok {
		l.remove(lt)
		l.insert(lt)
		return lt.v, true
	}
	var v V
	return v, false
}

// Put inserts the given value with the given key, evicting old
// cache entries if necessary.
func (l *lru[K, V]) Put(k K, v V) { l.PutSized(k, v, 0) }

// PutSized is Put of a value holding size bytes. The least recently used entries are evicted
// while the cache holds more than maxSize entries or more than its maxBytes. A value larger than
// maxBytes is not kept, and evicts nothing. A value put again under a key replaces the one there.
func (l *lru[K, V]) PutSized(k K, v V, size int) {
	if l.m == nil {
		l.m = make(map[K]*entry[K, V])
		l.head = new(entry[K, V])
		l.tail = new(entry[K, V])
		l.head.prev = l.tail
		l.tail.next = l.head
	}
	if old, ok := l.m[k]; ok {
		l.remove(old)
		delete(l.m, k)
		l.bytes -= old.size
	}
	if l.maxBytes > 0 && size > l.maxBytes {
		return // larger than the whole budget: keeping it would evict everything else
	}
	val := &entry[K, V]{key: k, v: v, size: size}
	l.m[k] = val
	l.insert(val)
	l.bytes += size
	for len(l.m) > maxSize || (l.maxBytes > 0 && l.bytes > l.maxBytes) {
		oldest := l.tail.next
		l.remove(oldest)
		delete(l.m, oldest.key)
		l.bytes -= oldest.size
	}
}

// remove cuts e out of the lru linked list.
func (l *lru[K, V]) remove(e *entry[K, V]) {
	e.next.prev = e.prev
	e.prev.next = e.next
}

// insert adds e to the lru linked list.
func (l *lru[K, V]) insert(e *entry[K, V]) {
	e.next = l.head
	e.prev = l.head.prev
	e.prev.next = e
	e.next.prev = e
}

type bitmapCache = lru[GlyphID, bitmap]

type bitmap struct {
	img  paint.ImageOp
	size image.Point
}

type layoutCache = lru[layoutKey, document]

type glyphValue[V any] struct {
	v      V
	glyphs []glyphInfo
}

type glyphLRU[V any] struct {
	seed  uint64
	cache lru[uint64, glyphValue[V]]
}

var seed atomic.Uint32

// hashGlyphs computes a hash key based on the ID and X offset of
// every glyph in the slice.
func (c *glyphLRU[V]) hashGlyphs(gs []Glyph) uint64 {
	if c.seed == 0 {
		c.seed = uint64(seed.Add(3900798947))
	}
	if len(gs) == 0 {
		return 0
	}

	h := c.seed
	firstX := gs[0].X
	for _, g := range gs {
		h += uint64(g.X - firstX)
		h *= 6585573582091643
		h += uint64(g.ID)
		h *= 3650802748644053
	}

	return h
}

func (c *glyphLRU[V]) Get(key uint64, gs []Glyph) (V, bool) {
	if v, ok := c.cache.Get(key); ok && gidsEqual(v.glyphs, gs) {
		return v.v, true
	}
	var v V
	return v, false
}

// Put caches v, holding size bytes, for glyphs under key (hashGlyphs). The entry is sized as v's
// bytes and the copy of the glyphs it keeps to compare a lookup with: a long run with little to
// draw (spaces) holds its glyphs and almost no outlines.
func (c *glyphLRU[V]) Put(key uint64, glyphs []Glyph, v V, size int) {
	gids := make([]glyphInfo, len(glyphs))
	firstX := fixed.I(0)
	for i, glyph := range glyphs {
		if i == 0 {
			firstX = glyph.X
		}
		// Cache glyph X offsets relative to the first glyph.
		gids[i] = glyphInfo{ID: glyph.ID, X: glyph.X - firstX}
	}
	val := glyphValue[V]{
		glyphs: gids,
		v:      v,
	}
	c.cache.PutSized(key, val, size+cap(gids)*int(unsafe.Sizeof(glyphInfo{})))
}

type pathCache = glyphLRU[clip.PathSpec]

type bitmapShapeCache = glyphLRU[op.CallOp]

type glyphInfo struct {
	ID GlyphID
	X  fixed.Int26_6
}

type layoutKey struct {
	ppem               fixed.Int26_6
	maxWidth, minWidth int
	maxLines           int
	str                string
	truncator          string
	locale             system.Locale
	font               giofont.Font
	forceTruncate      bool
	wrapPolicy         WrapPolicy
	lineHeight         fixed.Int26_6
	lineHeightScale    float32
}

const maxSize = 1000

// maxPathBytes is the path cache's byte budget. Its entries are the outlines of whole runs of
// glyphs (a line of text is one), each in operations of its own, so a thousand of them can hold
// hundreds of megabytes; the budget keeps the most recently used within it.
const maxPathBytes = 16 << 20

func gidsEqual(a []glyphInfo, glyphs []Glyph) bool {
	if len(a) != len(glyphs) {
		return false
	}
	firstX := fixed.Int26_6(0)
	for i := range a {
		if i == 0 {
			firstX = glyphs[i].X
		}
		// Cache glyph X offsets relative to the first glyph.
		if a[i].ID != glyphs[i].ID || a[i].X != (glyphs[i].X-firstX) {
			return false
		}
	}
	return true
}
