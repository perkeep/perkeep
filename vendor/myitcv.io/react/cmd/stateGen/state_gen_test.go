package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTestFiles(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	stderr := bytes.NewBuffer(nil)

	dir := filepath.Join(wd, "_testFiles")

	ok := dogen(stderr, dir, "")

	if !ok {
		t.Fatalf("expected gen to be ok; wasn't:\n\n%v", stderr.String())
	}
}
