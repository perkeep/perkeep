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
	m := NewMapForFileName("schema_test.go")
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