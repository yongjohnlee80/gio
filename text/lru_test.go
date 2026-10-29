// SPDX-License-Identifier: Unlicense OR MIT

package text

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/yongjohnlee80/gio/font/opentype"
	"github.com/yongjohnlee80/gio/op/clip"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"
)

func TestLayoutLRU(t *testing.T) {
	c := new(layoutCache)
	put := func(i int) {
		c.Put(layoutKey{str: strconv.Itoa(i)}, document{})
	}
	get := func(i int) bool {
		_, ok := c.Get(layoutKey{str: strconv.Itoa(i)})
		return ok
	}
	testLRU(t, put, get)
}

func TestPathLRU(t *testing.T) {
	c := new(pathCache)
	shaped := []Glyph{{ID: 1}}
	put := func(i int) {
		c.Put(uint64(i), shaped, clip.PathSpec{}, 0)
	}
	get := func(i int) bool {
		_, ok := c.Get(uint64(i), shaped)
		return ok
	}
	testLRU(t, put, get)
}

func testLRU(t *testing.T, put func(i int), get func(i int) bool) {
	for i := range maxSize {
		put(i)
	}
	for i := range maxSize {
		if !get(i) {
			t.Fatalf("key %d was evicted", i)
		}
	}
	put(maxSize)
	for i := 1; i < maxSize+1; i++ {
		if !get(i) {
			t.Fatalf("key %d was evicted", i)
		}
	}
	if i := 0; get(i) {
		t.Fatalf("key %d was not evicted", i)
	}
}

// A byte budget evicts the least recently used entries while the sizes are over it, keeps an
// entry larger than the whole budget out without evicting anything for it, and a value put again
// under its key replaces the old one and its size.
func TestLRUByteBudget(t *testing.T) {
	c := &lru[int, int]{maxBytes: 100}
	for i := range 10 {
		c.PutSized(i, i, 30)
	}
	if c.bytes > 100 || len(c.m) != 3 {
		t.Fatalf("after ten 30-byte entries: %d bytes in %d entries; want 90 in 3", c.bytes, len(c.m))
	}
	for i := range 10 {
		if _, ok := c.Get(i); ok != (i >= 7) {
			t.Errorf("entry %d kept %v; want the three most recent", i, ok)
		}
	}
	c.PutSized(100, 100, 200)
	if _, ok := c.Get(100); ok || len(c.m) != 3 || c.bytes != 90 {
		t.Errorf("an entry over the budget: kept %v, %d entries, %d bytes; want it out and the rest kept", ok, len(c.m), c.bytes)
	}
	c.PutSized(9, 9, 10)
	if len(c.m) != 3 || c.bytes != 70 {
		t.Errorf("a key put again: %d entries, %d bytes; want 3 and 70", len(c.m), c.bytes)
	}
	n := 0
	for e := c.tail.next; e != c.head; e = e.next {
		n++
	}
	if n != len(c.m) {
		t.Errorf("the list holds %d entries, the map %d", n, len(c.m))
	}
}

// A Shaper's path cache stays within maxPathBytes however many long lines it shapes, though the
// lines' outlines together hold far more, and the line shaped last is still a hit.
func TestShapedPathsStayWithinTheBudget(t *testing.T) {
	face, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	s := NewShaper(NoSystemFonts(), WithCollection([]FontFace{{Face: face}}))
	words := strings.Fields("lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua")
	shaped := 0
	var last []Glyph
	for i := 0; shaped <= 2*maxPathBytes && i < 20000; i++ {
		var b strings.Builder
		fmt.Fprintf(&b, "%d ", i)
		for j := range 24 {
			b.WriteString(words[(i*7+j)%len(words)])
			b.WriteByte(' ')
		}
		s.LayoutString(Parameters{PxPerEm: fixed.I(16), MaxWidth: 1 << 20, Locale: english}, b.String())
		var gs []Glyph
		for g, ok := s.NextGlyph(); ok; g, ok = s.NextGlyph() {
			gs = append(gs, g)
		}
		s.Shape(gs)
		if newest := s.pathCache.cache.head.prev; newest != s.pathCache.cache.tail {
			shaped += newest.size
		}
		last = gs
	}
	if shaped <= maxPathBytes {
		t.Fatalf("the lines' outlines hold %d bytes in all, not over the budget: the cell shows nothing", shaped)
	}
	if got := s.pathCache.cache.bytes; got > maxPathBytes || got == 0 {
		t.Errorf("the path cache holds %d bytes; want at most %d, and some", got, maxPathBytes)
	}
	if _, ok := s.pathCache.Get(s.pathCache.hashGlyphs(last), last); !ok {
		t.Error("the line shaped last is not cached")
	}
}

// A run's glyphs count against the budget too: runs of spaces, with almost no outlines but long
// glyph sequences the cache keeps to compare lookups with, still leave the cache within it.
func TestLongBlankRunsStayWithinTheBudget(t *testing.T) {
	face, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	s := NewShaper(NoSystemFonts(), WithCollection([]FontFace{{Face: face}}))
	s.LayoutString(Parameters{PxPerEm: fixed.I(16), MaxWidth: 1 << 20, Locale: english}, " ")
	space, ok := s.NextGlyph()
	if !ok {
		t.Fatal("no glyph for a space")
	}
	const runs, n = 20, 100_000
	for r := range runs {
		gs := make([]Glyph, n+r) // a distinct length per run: a distinct key
		for i := range gs {
			gs[i] = space
			gs[i].X = fixed.Int26_6(i) * space.Advance
		}
		s.Shape(gs)
	}
	keys := 0
	for e := s.pathCache.cache.tail.next; e != s.pathCache.cache.head; e = e.next {
		keys += cap(e.v.glyphs) * int(unsafe.Sizeof(glyphInfo{}))
	}
	if keys > maxPathBytes || s.pathCache.cache.bytes > maxPathBytes {
		t.Errorf("the cache keeps %d bytes of glyphs (%d accounted); want at most %d", keys, s.pathCache.cache.bytes, maxPathBytes)
	}
	if len(s.pathCache.cache.m) == 0 || len(s.pathCache.cache.m) >= runs {
		t.Errorf("%d of %d runs cached; want the most recent within the budget", len(s.pathCache.cache.m), runs)
	}
}
