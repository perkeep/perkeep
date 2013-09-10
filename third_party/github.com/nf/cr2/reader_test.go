// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cr2

import (
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"testing"
)

func TestDecode(t *testing.T) {
	f, err := openSampleFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, kind, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "cr2" {
		t.Fatal("unexpected kind:", kind)
	}
	r := m.Bounds()
	if r.Dx() != sampleWidth {
		t.Error("width = %v, want %v", r.Dx(), sampleWidth)
	}
	if r.Dy() != sampleHeight {
		t.Error("height = %v, want %v", r.Dy(), sampleHeight)
	}
}

// Fetch the sample file via HTTP so we don't put a 25mb data file in the repo.

const (
	sampleFile    = "testdata/sample.cr2"
	sampleFileURL = "http://nf.wh3rd.net/img/sample.cr2"
	sampleWidth   = 5184
	sampleHeight  = 3456
)

func openSampleFile(t *testing.T) (io.ReadCloser, error) {
	if f, err := os.Open(sampleFile); err == nil {
		return f, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	t.Logf("Fetching sample file...")
	fi, err := os.Stat("testdata")
	if err == nil && !fi.IsDir() {
		return nil, errors.New("testdata is not a directory")
	}
	if os.IsNotExist(err) {
		err = os.Mkdir("testdata", 0777)
	}
	if err != nil {
		return nil, err
	}
	r, err := http.Get(sampleFileURL)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	f, err := os.Create(sampleFile)
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(f, r.Body); err != nil {
		f.Close()
		os.Remove(sampleFile)
		return nil, err
	}
	if _, err = f.Seek(0, os.SEEK_SET); err != nil {
		f.Close()
		os.Remove(sampleFile)
		return nil, err
	}
	return f, nil
}
