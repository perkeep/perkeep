package sourcemap

import (
	"bytes"
	"strings"
	"testing"
)

const testFile = `{"version":3,"file":"min.js","sourceRoot":"/the/root","sources":["one.js","two.js"],"names":["bar","baz","n"],"mappings":"CAAC,IAAI,IAAM,SAAUA,GAClB,OAAOC,IAAID;CCDb,IAAI,IAAM,SAAUE,GAClB,OAAOA"}` + "\n"

func TestReadFrom(t *testing.T) {
	m, err := ReadFrom(strings.NewReader(testFile))
	if err != nil {
		t.Fatal(err)
	}
	if m.File != "min.js" || m.SourceRoot != "/the/root" || len(m.Sources) != 2 || m.Sources[0] != "one.js" || len(m.Names) != 3 || m.Names[0] != "bar" {
		t.Error(m)
	}
	mappings := m.DecodedMappings()
	if len(mappings) != 13 {
		t.Error(m)
	}
	assertMapping := func(got, expected *Mapping) {
		if got.GeneratedLine != expected.GeneratedLine || got.GeneratedColumn != expected.GeneratedColumn || got.OriginalFile != expected.OriginalFile || got.OriginalLine != expected.OriginalLine || got.OriginalColumn != expected.OriginalColumn || got.OriginalName != expected.OriginalName {
			t.Errorf("expected %v, got %v", expected, got)
		}
	}
	assertMapping(mappings[0], &Mapping{1, 1, "one.js", 1, 1, ""})
	assertMapping(mappings[1], &Mapping{1, 5, "one.js", 1, 5, ""})
	assertMapping(mappings[2], &Mapping{1, 9, "one.js", 1, 11, ""})
	assertMapping(mappings[3], &Mapping{1, 18, "one.js", 1, 21, "bar"})
	assertMapping(mappings[4], &Mapping{1, 21, "one.js", 2, 3, ""})
	assertMapping(mappings[5], &Mapping{1, 28, "one.js", 2, 10, "baz"})
	assertMapping(mappings[6], &Mapping{1, 32, "one.js", 2, 14, "bar"})
	assertMapping(mappings[7], &Mapping{2, 1, "two.js", 1, 1, ""})
	assertMapping(mappings[8], &Mapping{2, 5, "two.js", 1, 5, ""})
	assertMapping(mappings[9], &Mapping{2, 9, "two.js", 1, 11, ""})
	assertMapping(mappings[10], &Mapping{2, 18, "two.js", 1, 21, "n"})
	assertMapping(mappings[11], &Mapping{2, 21, "two.js", 2, 3, ""})
	assertMapping(mappings[12], &Mapping{2, 28, "two.js", 2, 10, "n"})
}

func TestWriteTo(t *testing.T) {
	m, err := ReadFrom(strings.NewReader(testFile))
	if err != nil {
		t.Fatal(err)
	}
	m.DecodedMappings()
	m.Swap(3, 4)
	m.Swap(5, 10)
	m.Mappings = ""
	m.Version = 0
	b := bytes.NewBuffer(nil)
	if err := m.WriteTo(b); err != nil {
		t.Fatal(err)
	}
	if b.String() != testFile {
		t.Error(b.String())
	}
}
