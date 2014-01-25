/*
Copyright 2014 The Camlistore Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package media

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openFile opens fn, a file within the testdata dir, and returns an FD and the file's size.
func openFile(fn string) (*os.File, int64, error) {
	f, err := os.Open(filepath.Join("testdata", fn))
	if err != nil {
		return nil, 0, err
	}
	s, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, s.Size(), nil
}

func TestHasID3v1Tag(t *testing.T) {
	tests := []struct {
		fn     string
		hasTag bool
	}{
		{"xing_header.mp3", false},
		{"id3v1.mp3", true},
	}
	for _, tt := range tests {
		f, s, err := openFile(tt.fn)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		hasTag, err := HasID3v1Tag(io.NewSectionReader(f, 0, s))
		if err != nil {
			t.Fatal(err)
		}
		if hasTag != tt.hasTag {
			t.Errorf("Expected %v for %s but got %v", tt.hasTag, tt.fn, hasTag)
		}
	}
}

func TestGetMPEGAudioDuration(t *testing.T) {
	tests := []struct {
		fn string
		d  time.Duration
	}{
		{"128_cbr.mp3", time.Duration(1088) * time.Millisecond},
		{"xing_header.mp3", time.Duration(1097) * time.Millisecond},
	}
	for _, tt := range tests {
		f, s, err := openFile(tt.fn)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		d, err := GetMPEGAudioDuration(io.NewSectionReader(f, 0, s))
		if err != nil {
			t.Fatal(err)
		}
		if d != tt.d {
			t.Errorf("Expected %d for %s but got %d", tt.d, tt.fn, d)
		}
	}
}
