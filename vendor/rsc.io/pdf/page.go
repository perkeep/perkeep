// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"fmt"
	"strings"
)

// A Page represent a single page in a PDF file.
// The methods interpret a Page dictionary stored in V.
type Page struct {
	V Value
}

// Page returns the page for the given page number.
// Page numbers are indexed starting at 1, not 0.
// If the page is not found, Page returns a Page with p.V.IsNull().
func (r *Reader) Page(num int) Page {
	num-- // now 0-indexed
	page := r.Trailer().Key("Root").Key("Pages")
Search:
	for page.Key("Type").Name() == "Pages" {
		count := int(page.Key("Count").Int64())
		if count < num {
			return Page{}
		}
		kids := page.Key("Kids")
		for i := 0; i < kids.Len(); i++ {
			kid := kids.Index(i)
			if kid.Key("Type").Name() == "Pages" {
				c := int(kid.Key("Count").Int64())
				if num < c {
					page = kid
					continue Search
				}
				num -= c
				continue
			}
			if kid.Key("Type").Name() == "Page" {
				if num == 0 {
					return Page{kid}
				}
				num--
			}
		}
	}
	return Page{}
}

// NumPage returns the number of pages in the PDF file.
func (r *Reader) NumPage() int {
	return int(r.Trailer().Key("Root").Key("Pages").Key("Count").Int64())
}

func (p Page) findInherited(key string) Value {
	for v := p.V; !v.IsNull(); v = v.Key("Parent") {
		if r := v.Key(key); !r.IsNull() {
			return r
		}
	}
	return Value{}
}

/*
func (p Page) MediaBox() Value {
	return p.findInherited("MediaBox")
}

func (p Page) CropBox() Value {
	return p.findInherited("CropBox")
}
*/

// Resources returns the resources dictionary associated with the page.
func (p Page) Resources() Value {
	return p.findInherited("Resources")
}

// Fonts returns a list of the fonts associated with the page.
func (p Page) Fonts() []string {
	return p.Resources().Key("Font").Keys()
}

// Font returns the font with the given name associated with the page.
func (p Page) Font(name string) Font {
	return Font{p.Resources().Key("Font").Key(name)}
}

// A Font represent a font in a PDF file.
// The methods interpret a Font dictionary stored in V.
type Font struct {
	V Value
}

// BaseFont returns the font's name (BaseFont property).
func (f Font) BaseFont() string {
	return f.V.Key("BaseFont").Name()
}

// FirstChar returns the code point of the first character in the font.
func (f Font) FirstChar() int {
	return int(f.V.Key("FirstChar").Int64())
}

// LastChar returns the code point of the last character in the font.
func (f Font) LastChar() int {
	return int(f.V.Key("LastChar").Int64())
}

// Widths returns the widths of the glyphs in the font.
// In a well-formed PDF, len(f.Widths()) == f.LastChar()+1 - f.FirstChar().
func (f Font) Widths() []float64 {
	x := f.V.Key("Widths")
	var out []float64
	for i := 0; i < x.Len(); i++ {
		out = append(out, x.Index(i).Float64())
	}
	return out
}

// Width returns the width of the given code point.
func (f Font) Width(code int) float64 {
	first := f.FirstChar()
	last := f.LastChar()
	if code < first || last < code {
		return 0
	}
	return f.V.Key("Widths").Index(code - first).Float64()
}

// Encoder returns the encoding between font code point sequences and UTF-8.
func (f Font) Encoder() TextEncoding {
	enc := f.V.Key("Encoding")
	switch enc.Kind() {
	case Name:
		switch enc.Name() {
		case "WinAnsiEncoding":
			return &byteEncoder{&winAnsiEncoding}
		case "MacRomanEncoding":
			return &byteEncoder{&macRomanEncoding}
		case "Identity-H":
			// TODO: Should be big-endian UCS-2 decoder
			return &nopEncoder{}
		default:
			println("unknown encoding", enc.Name())
			return &nopEncoder{}
		}
	case Dict:
		return &dictEncoder{enc.Key("Differences")}
	case Null:
		// ok, try ToUnicode
	default:
		println("unexpected encoding", enc.String())
		return &nopEncoder{}
	}

	toUnicode := f.V.Key("ToUnicode")
	if toUnicode.Kind() == Dict {
		m := readCmap(toUnicode)
		if m == nil {
			return &nopEncoder{}
		}
		return m
	}

	return &byteEncoder{&pdfDocEncoding}
}

type dictEncoder struct {
	v Value
}

func (e *dictEncoder) Decode(raw string) (text string) {
	r := make([]rune, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		ch := rune(raw[i])
		n := -1
		for j := 0; j < e.v.Len(); j++ {
			x := e.v.Index(j)
			if x.Kind() == Integer {
				n = int(x.Int64())
				continue
			}
			if x.Kind() == Name {
				if int(raw[i]) == n {
					r := nameToRune[x.Name()]
					if r != 0 {
						ch = r
						break
					}
				}
				n++
			}
		}
		r = append(r, ch)
	}
	return string(r)
}

// A TextEncoding represents a mapping between
// font code points and UTF-8 text.
type TextEncoding interface {
	// Decode returns the UTF-8 text corresponding to
	// the sequence of code points in raw.
	Decode(raw string) (text string)
}

type nopEncoder struct {
}

func (e *nopEncoder) Decode(raw string) (text string) {
	return raw
}

type byteEncoder struct {
	table *[256]rune
}

func (e *byteEncoder) Decode(raw string) (text string) {
	r := make([]rune, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		r = append(r, e.table[raw[i]])
	}
	return string(r)
}

type cmap struct {
	space   [4][][2]string
	bfrange []bfrange
}

func (m *cmap) Decode(raw string) (text string) {
	var r []rune
Parse:
	for len(raw) > 0 {
		for n := 1; n <= 4 && n <= len(raw); n++ {
			for _, space := range m.space[n-1] {
				if space[0] <= raw[:n] && raw[:n] <= space[1] {
					text := raw[:n]
					raw = raw[n:]
					for _, bf := range m.bfrange {
						if len(bf.lo) == n && bf.lo <= text && text <= bf.hi {
							if bf.dst.Kind() == String {
								s := bf.dst.RawString()
								if bf.lo != text {
									b := []byte(s)
									b[len(b)-1] += text[len(text)-1] - bf.lo[len(bf.lo)-1]
									s = string(b)
								}
								r = append(r, []rune(utf16Decode(s))...)
								continue Parse
							}
							if bf.dst.Kind() == Array {
								fmt.Printf("array %v\n", bf.dst)
							} else {
								fmt.Printf("unknown dst %v\n", bf.dst)
							}
							r = append(r, noRune)
							continue Parse
						}
					}
					fmt.Printf("no text for %q", text)
					r = append(r, noRune)
					continue Parse
				}
			}
		}
		println("no code space found")
		r = append(r, noRune)
		raw = raw[1:]
	}
	return string(r)
}

type bfrange struct {
	lo  string
	hi  string
	dst Value
}

func readCmap(toUnicode Value) *cmap {
	n := -1
	var m cmap
	ok := true
	Interpret(toUnicode, func(stk *Stack, op string) {
		if !ok {
			return
		}
		switch op {
		case "findresource":
			category := stk.Pop()
			key := stk.Pop()
			fmt.Println("findresource", key, category)
			stk.Push(newDict())
		case "begincmap":
			stk.Push(newDict())
		case "endcmap":
			stk.Pop()
		case "begincodespacerange":
			n = int(stk.Pop().Int64())
		case "endcodespacerange":
			if n < 0 {
				println("missing begincodespacerange")
				ok = false
				return
			}
			for i := 0; i < n; i++ {
				hi, lo := stk.Pop().RawString(), stk.Pop().RawString()
				if len(lo) == 0 || len(lo) != len(hi) {
					println("bad codespace range")
					ok = false
					return
				}
				m.space[len(lo)-1] = append(m.space[len(lo)-1], [2]string{lo, hi})
			}
			n = -1
		case "beginbfrange":
			n = int(stk.Pop().Int64())
		case "endbfrange":
			if n < 0 {
				panic("missing beginbfrange")
			}
			for i := 0; i < n; i++ {
				dst, srcHi, srcLo := stk.Pop(), stk.Pop().RawString(), stk.Pop().RawString()
				m.bfrange = append(m.bfrange, bfrange{srcLo, srcHi, dst})
			}
		case "defineresource":
			category := stk.Pop().Name()
			value := stk.Pop()
			key := stk.Pop().Name()
			fmt.Println("defineresource", key, value, category)
			stk.Push(value)
		default:
			println("interp\t", op)
		}
	})
	if !ok {
		return nil
	}
	return &m
}

type matrix [3][3]float64

var ident = matrix{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

func (x matrix) mul(y matrix) matrix {
	var z matrix
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				z[i][j] += x[i][k] * y[k][j]
			}
		}
	}
	return z
}

// A Text represents a single piece of text drawn on a page.
type Text struct {
	Font     string  // the font used
	FontSize float64 // the font size, in points (1/72 of an inch)
	X        float64 // the X coordinate, in points, increasing left to right
	Y        float64 // the Y coordinate, in points, increasing bottom to top
	W        float64 // the width of the text, in points
	S        string  // the actual UTF-8 text
}

// A Rect represents a rectangle.
type Rect struct {
	Min, Max Point
}

// A Point represents an X, Y pair.
type Point struct {
	X float64
	Y float64
}

// Content describes the basic content on a page: the text and any drawn rectangles.
type Content struct {
	Text []Text
	Rect []Rect
}

type gstate struct {
	Tc    float64
	Tw    float64
	Th    float64
	Tl    float64
	Tf    Font
	Tfs   float64
	Tmode int
	Trise float64
	Tm    matrix
	Tlm   matrix
	Trm   matrix
	CTM   matrix
}

// Content returns the page's content.
func (p Page) Content() Content {
	strm := p.V.Key("Contents")
	var enc TextEncoding = &nopEncoder{}

	var g = gstate{
		Th:  1,
		CTM: ident,
	}

	var text []Text
	showText := func(s string) {
		n := 0
		for _, ch := range enc.Decode(s) {
			Trm := matrix{{g.Tfs * g.Th, 0, 0}, {0, g.Tfs, 0}, {0, g.Trise, 1}}.mul(g.Tm).mul(g.CTM)
			w0 := g.Tf.Width(int(s[n]))
			n++
			if ch != ' ' {
				f := g.Tf.BaseFont()
				if i := strings.Index(f, "+"); i >= 0 {
					f = f[i+1:]
				}
				text = append(text, Text{f, Trm[0][0], Trm[2][0], Trm[2][1], w0 / 1000 * Trm[0][0], string(ch)})
			}
			tx := w0/1000*g.Tfs + g.Tc
			if ch == ' ' {
				tx += g.Tw
			}
			tx *= g.Th
			g.Tm = matrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(g.Tm)
		}
	}

	var rect []Rect
	var gstack []gstate
	Interpret(strm, func(stk *Stack, op string) {
		n := stk.Len()
		args := make([]Value, n)
		for i := n - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		switch op {
		default:
			//fmt.Println(op, args)
			return

		case "cm": // update g.CTM
			if len(args) != 6 {
				panic("bad g.Tm")
			}
			var m matrix
			for i := 0; i < 6; i++ {
				m[i/2][i%2] = args[i].Float64()
			}
			m[2][2] = 1
			g.CTM = m.mul(g.CTM)

		case "gs": // set parameters from graphics state resource
			gs := p.Resources().Key("ExtGState").Key(args[0].Name())
			font := gs.Key("Font")
			if font.Kind() == Array && font.Len() == 2 {
				//fmt.Println("FONT", font)
			}

		case "f": // fill
		case "g": // setgray
		case "l": // lineto
		case "m": // moveto

		case "cs": // set colorspace non-stroking
		case "scn": // set color non-stroking

		case "re": // append rectangle to path
			if len(args) != 4 {
				panic("bad re")
			}
			x, y, w, h := args[0].Float64(), args[1].Float64(), args[2].Float64(), args[3].Float64()
			rect = append(rect, Rect{Point{x, y}, Point{x + w, y + h}})

		case "q": // save graphics state
			gstack = append(gstack, g)

		case "Q": // restore graphics state
			n := len(gstack) - 1
			g = gstack[n]
			gstack = gstack[:n]

		case "BT": // begin text (reset text matrix and line matrix)
			g.Tm = ident
			g.Tlm = g.Tm

		case "ET": // end text

		case "T*": // move to start of next line
			x := matrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm

		case "Tc": // set character spacing
			if len(args) != 1 {
				panic("bad g.Tc")
			}
			g.Tc = args[0].Float64()

		case "TD": // move text position and set leading
			if len(args) != 2 {
				panic("bad Td")
			}
			g.Tl = -args[1].Float64()
			fallthrough
		case "Td": // move text position
			if len(args) != 2 {
				panic("bad Td")
			}
			tx := args[0].Float64()
			ty := args[1].Float64()
			x := matrix{{1, 0, 0}, {0, 1, 0}, {tx, ty, 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm

		case "Tf": // set text font and size
			if len(args) != 2 {
				panic("bad TL")
			}
			f := args[0].Name()
			g.Tf = p.Font(f)
			enc = g.Tf.Encoder()
			if enc == nil {
				println("no cmap for", f)
				enc = &nopEncoder{}
			}
			g.Tfs = args[1].Float64()

		case "\"": // set spacing, move to next line, and show text
			if len(args) != 3 {
				panic("bad \" operator")
			}
			g.Tw = args[0].Float64()
			g.Tc = args[1].Float64()
			args = args[2:]
			fallthrough
		case "'": // move to next line and show text
			if len(args) != 1 {
				panic("bad ' operator")
			}
			x := matrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm
			fallthrough
		case "Tj": // show text
			if len(args) != 1 {
				panic("bad Tj operator")
			}
			showText(args[0].RawString())

		case "TJ": // show text, allowing individual glyph positioning
			v := args[0]
			for i := 0; i < v.Len(); i++ {
				x := v.Index(i)
				if x.Kind() == String {
					showText(x.RawString())
				} else {
					tx := -x.Float64() / 1000 * g.Tfs * g.Th
					g.Tm = matrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(g.Tm)
				}
			}

		case "TL": // set text leading
			if len(args) != 1 {
				panic("bad TL")
			}
			g.Tl = args[0].Float64()

		case "Tm": // set text matrix and line matrix
			if len(args) != 6 {
				panic("bad g.Tm")
			}
			var m matrix
			for i := 0; i < 6; i++ {
				m[i/2][i%2] = args[i].Float64()
			}
			m[2][2] = 1
			g.Tm = m
			g.Tlm = m

		case "Tr": // set text rendering mode
			if len(args) != 1 {
				panic("bad Tr")
			}
			g.Tmode = int(args[0].Int64())

		case "Ts": // set text rise
			if len(args) != 1 {
				panic("bad Ts")
			}
			g.Trise = args[0].Float64()

		case "Tw": // set word spacing
			if len(args) != 1 {
				panic("bad g.Tw")
			}
			g.Tw = args[0].Float64()

		case "Tz": // set horizontal text scaling
			if len(args) != 1 {
				panic("bad Tz")
			}
			g.Th = args[0].Float64() / 100
		}
	})
	return Content{text, rect}
}

// TextVertical implements sort.Interface for sorting
// a slice of Text values in vertical order, top to bottom,
// and then left to right within a line.
type TextVertical []Text

func (x TextVertical) Len() int      { return len(x) }
func (x TextVertical) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x TextVertical) Less(i, j int) bool {
	if x[i].Y != x[j].Y {
		return x[i].Y > x[j].Y
	}
	return x[i].X < x[j].X
}

// TextVertical implements sort.Interface for sorting
// a slice of Text values in horizontal order, left to right,
// and then top to bottom within a column.
type TextHorizontal []Text

func (x TextHorizontal) Len() int      { return len(x) }
func (x TextHorizontal) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x TextHorizontal) Less(i, j int) bool {
	if x[i].X != x[j].X {
		return x[i].X < x[j].X
	}
	return x[i].Y > x[j].Y
}

// An Outline is a tree describing the outline (also known as the table of contents)
// of a document.
type Outline struct {
	Title string    // title for this element
	Child []Outline // child elements
}

// Outline returns the document outline.
// The Outline returned is the root of the outline tree and typically has no Title itself.
// That is, the children of the returned root are the top-level entries in the outline.
func (r *Reader) Outline() Outline {
	return buildOutline(r.Trailer().Key("Root").Key("Outlines"))
}

func buildOutline(entry Value) Outline {
	var x Outline
	x.Title = entry.Key("Title").Text()
	for child := entry.Key("First"); child.Kind() == Dict; child = child.Key("Next") {
		x.Child = append(x.Child, buildOutline(child))
	}
	return x
}
