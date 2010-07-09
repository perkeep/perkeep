package util

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestTee(t *testing.T) {
	sha1 := sha1.New()
	sink := new(bytes.Buffer)
	tee := NewTee(sha1, sink)

	sourceString := "My input text."
	source := strings.NewReader(sourceString)
	written, err := io.Copy(tee, source)

	if written != int64(len(sourceString)) {
		t.Errorf("short write of %d, not %d", written, len(sourceString))
	}

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	sha1hex := fmt.Sprintf("%x", sha1.Sum())
	if sha1hex != "01cb303fa8c30a64123067c5aa6284ba7ec2d31b" {
		t.Error("Bogus sha1 value")
	}

	if sink.String() != sourceString {
		t.Error("Unexpected sink output: %v", sink.String())
	}
}
