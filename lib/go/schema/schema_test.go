package schema

import (
	"strings"
	"testing"
)

type isUtf8Test struct {
	s string
	e bool
}

func TestIsUtf8(t *testing.T) {
	tests := []isUtf8Test{
		{"foo", true},
		{"Straße", true},
		{string([]uint8{65, 234, 234, 192, 23, 123}), false},
		{string([]uint8{65, 97}), true},
	}
	for idx, test := range tests {
		if isValidUtf8(test.s) != test.e {
			t.Errorf("expected isutf8==%d for test index %d", test.e, idx)
		}
	}
}

const kExpectedHeader = `{"camliVersion"`

func TestJson(t *testing.T) {
	m := newMapForFileName("schema_test.go")
	json, err := MapToCamliJson(m)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json: [%s]\n", json)
	// TODO: test it parses back

	if !strings.HasPrefix(json, kExpectedHeader) {
		t.Errorf("JSON does't start with expected header.")
	}
	
}

type rfc3339NanoTest struct {
	nanos int64
	e     string
}

func TestRfc3339FromNanos(t *testing.T) {
	tests := []rfc3339NanoTest{
		{0, "1970-01-01T00:00:00Z"},
		{1, "1970-01-01T00:00:00.000000001Z"},
		{10, "1970-01-01T00:00:00.00000001Z"},
		{1000, "1970-01-01T00:00:00.000001Z"},
	}
	for idx, test := range tests {
		got := rfc3339FromNanos(test.nanos)
		if got != test.e {
			t.Errorf("On test %d got %q; expected %q", idx, got, test.e)
		}
	}
}

func TestRegularFile(t *testing.T) {
	m, err := NewFileMap("schema_test.go", nil)
	if err != nil {
                t.Fatalf("Unexpected error: %v", err)
        }
	json, err := MapToCamliJson(m)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	t.Logf("Got json for regular file: [%s]\n", json)
	// TODO: test it parses back
	
	
}