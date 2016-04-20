package charset_test

import (
	"bytes"
	"code.google.com/p/go-charset/charset"
	_ "code.google.com/p/go-charset/data"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

type translateTest struct {
	canRoundTrip bool
	charset      string
	in           string
	out          string
}

// TODO test codepage behaviour at char boundary.

var tests = []translateTest{
	{true, "iso-8859-15", "\xa41 is cheap", "€1 is cheap"},
	{true, "ms-kanji", "\x82\xb1\x82\xea\x82\xcd\x8a\xbf\x8e\x9a\x82\xc5\x82\xb7\x81B", "これは漢字です。"},
	{true, "utf-16le", "S0\x8c0o0\"oW[g0Y0\x020", "これは漢字です。"},
	{true, "utf-16be", "0S0\x8c0oo\"[W0g0Y0\x02", "これは漢字です。"},
	{true, "utf-8", "♔", "♔"},
	{false, "utf-8", "a♔é\x80", "a♔é" + string(utf8.RuneError)},
	{true, "sjis", "", ""},
	{true, "latin1", "\xa35 for Pepp\xe9", "£5 for Peppé"},
}

func TestCharsets(t *testing.T) {
	for i, test := range tests {
		t.Logf("test %d", i)
		test.run(t)
	}
}

func translate(tr charset.Translator, in string) (string, error) {
	var buf bytes.Buffer
	r := charset.NewTranslatingReader(strings.NewReader(in), tr)
	_, err := io.Copy(&buf, r)
	if err != nil {
		return "", err
	}
	return string(buf.Bytes()), nil
}

func (test translateTest) run(t *testing.T) {
	cs := charset.Info(test.charset)
	if cs == nil {
		t.Fatalf("no info found for %q", test.charset)
	}
	fromtr, err := charset.TranslatorFrom(test.charset)
	if err != nil {
		t.Fatalf("error making translator from %q: %v", test.charset, err)
	}
	out, err := translate(fromtr, test.in)
	if err != nil {
		t.Fatalf("error translating from %q: %v", test.charset, err)
	}
	if out != test.out {
		t.Fatalf("error translating from %q: expected %x got %x", test.charset, test.out, out)
	}

	if cs.NoTo || !test.canRoundTrip {
		return
	}

	totr, err := charset.TranslatorTo(test.charset)
	if err != nil {
		t.Fatalf("error making translator to %q: %v", test.charset, err)
	}
	in, err := translate(totr, out)
	if err != nil {
		t.Fatalf("error translating to %q: %v", test.charset, err)
	}
	if in != test.in {
		t.Fatalf("%q round trip conversion failed; expected %x got %x", test.charset, test.in, in)
	}
}

// TODO test big5

var testReaders = []func(io.Reader) io.Reader{
	func(r io.Reader) io.Reader { return r },
	iotest.OneByteReader,
	iotest.HalfReader,
	iotest.DataErrReader,
}

var testWriters = []func(io.Writer) io.Writer{
	func(w io.Writer) io.Writer { return w },
	OneByteWriter,
}

var testTranslators = []func() charset.Translator{
	func() charset.Translator { return new(holdingTranslator) },
	func() charset.Translator { return new(shortTranslator) },
}

var codepageCharsets = []string{"latin1"}

func TestCodepages(t *testing.T) {
	for _, name := range codepageCharsets {
		for _, inr := range testReaders {
			for _, outr := range testReaders {
				testCodepage(t, name, inr, outr)
			}
		}
	}
}

func testCodepage(t *testing.T, name string, inReader, outReader func(io.Reader) io.Reader) {
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	inr := inReader(bytes.NewBuffer(data))
	r, err := charset.NewReader(name, inr)
	if err != nil {
		t.Fatalf("cannot make reader for charset %q: %v", name, err)
	}
	outr := outReader(r)
	r = outr

	var outbuf bytes.Buffer
	w, err := charset.NewWriter(name, &outbuf)
	if err != nil {
		t.Fatalf("cannot make writer  for charset %q: %v", name, err)
	}
	_, err = io.Copy(w, r)
	if err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if len(outbuf.Bytes()) != len(data) {
		t.Fatalf("short result of roundtrip, charset %q, readers %T, %T; expected 256, got %d", name, inr, outr, len(outbuf.Bytes()))
	}
	for i, x := range outbuf.Bytes() {
		if data[i] != x {
			t.Fatalf("charset %q, round trip expected %d, got %d", name, i, data[i])
		}
	}
}

func TestTranslatingReader(t *testing.T) {
	for _, tr := range testTranslators {
		for _, inr := range testReaders {
			for _, outr := range testReaders {
				testTranslatingReader(t, tr(), inr, outr)
			}
		}
	}
}

func testTranslatingReader(t *testing.T, tr charset.Translator, inReader, outReader func(io.Reader) io.Reader) {
	data := make([]byte, 128)
	for i := range data {
		data[i] = byte(i)
	}
	inr := inReader(bytes.NewBuffer(data))
	r := charset.NewTranslatingReader(inr, tr)
	outr := outReader(r)
	r = outr

	var outbuf bytes.Buffer
	_, err := io.Copy(&outbuf, r)
	if err != nil {
		t.Fatalf("translator %T, readers %T, %T, copy failed: %v", tr, inr, outr, err)
	}
	err = checkTranslation(data, outbuf.Bytes())
	if err != nil {
		t.Fatalf("translator %T, readers %T, %T, %v\n", err)
	}
}

func TestTranslatingWriter(t *testing.T) {
	for _, tr := range testTranslators {
		for _, w := range testWriters {
			testTranslatingWriter(t, tr(), w)
		}
	}
}

func testTranslatingWriter(t *testing.T, tr charset.Translator, writer func(io.Writer) io.Writer) {
	var outbuf bytes.Buffer
	trw := charset.NewTranslatingWriter(&outbuf, tr)
	w := writer(trw)

	data := make([]byte, 128)
	for i := range data {
		data[i] = byte(i)
	}
	n, err := w.Write(data)
	if err != nil {
		t.Fatalf("translator %T, writer %T, write error: %v", tr, w, err)
	}
	if n != len(data) {
		t.Fatalf("translator %T, writer %T, short write; expected %d got %d", tr, w, len(data), n)
	}
	trw.Close()
	err = checkTranslation(data, outbuf.Bytes())
	if err != nil {
		t.Fatalf("translator %T, writer %T, %v", tr, w, err)
	}
}

func xlate(x byte) byte {
	return x + 128
}

func checkTranslation(in, out []byte) error {
	if len(in) != len(out) {
		return fmt.Errorf("wrong byte count; expected %d got %d", len(in), len(out))
	}
	for i, x := range out {
		if in[i]+128 != x {
			return fmt.Errorf("bad translation at %d; expected %d, got %d", i, in[i]+128, x)
		}
	}
	return nil
}

// holdingTranslator holds its input until the end.
type holdingTranslator struct {
	scratch []byte
}

func (t *holdingTranslator) Translate(buf []byte, eof bool) (int, []byte, error) {
	t.scratch = append(t.scratch, buf...)
	if !eof {
		return len(buf), nil, nil
	}
	for i, x := range t.scratch {
		t.scratch[i] = xlate(x)
	}
	return len(buf), t.scratch, nil
}

// shortTranslator translates only one byte at a time, even at eof.
type shortTranslator [1]byte

func (t *shortTranslator) Translate(buf []byte, eof bool) (int, []byte, error) {
	if len(buf) == 0 {
		return 0, nil, nil
	}
	t[0] = xlate(buf[0])
	return 1, t[:], nil
}

// OneByteWriter returns a Writer that implements
// each non-empty Write by writing one byte to w.
func OneByteWriter(w io.Writer) io.Writer {
	return &oneByteWriter{w}
}

type oneByteWriter struct {
	w io.Writer
}

func (w *oneByteWriter) Write(buf []byte) (int, error) {
	n := 0
	for len(buf) > 0 {
		nw, err := w.w.Write(buf[0:1])
		n += nw
		if err != nil {
			return n, err
		}
		buf = buf[1:]
	}
	return n, nil
}
