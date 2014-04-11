package iconv_test

import (
	"bytes"
	"camlistore.org/third_party/code.google.com/p/go-charset/charset"
	_ "camlistore.org/third_party/code.google.com/p/go-charset/charset/iconv"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

// TODO(rog) better than this

func TestNames(t *testing.T) {
	charset.Names()
}

type translateTest struct {
	canRoundTrip bool
	charset      string
	in           string
	out          string
}

var tests = []translateTest{
	{true, "iso-8859-15", "\xa41 is cheap", "€1 is cheap"},
	//	{true, "ms-kanji", "\x82\xb1\x82\xea\x82\xcd\x8a\xbf\x8e\x9a\x82\xc5\x82\xb7\x81B", "これは漢字です。"},
	{true, "utf-16le", "S0\x8c0o0\"oW[g0Y0\x020", "これは漢字です。"},
	{true, "utf-16be", "0S0\x8c0oo\"[W0g0Y0\x02", "これは漢字です。"},
	{true, "utf-8", "♔", "♔"},
	{false, "utf-8", "a♔é\x80", "a♔é" + string(utf8.RuneError)},
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
