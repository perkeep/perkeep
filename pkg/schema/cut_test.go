/*
Copyright 2026 The Perkeep Authors

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

package schema

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/test"
)

// goldenFiles are file contents whose file schema refs, written by
// WriteFileMap, mustn't change: a change in where files are cut would
// stop new uploads from sharing chunks with old ones.
var goldenFiles = []struct {
	name     string
	contents func() io.Reader
	fileRef  string
	blobs    int // how many blobs WriteFileMap stores, including the file schema
}{
	{"empty", func() io.Reader { return strings.NewReader("") }, "sha224-ee424489cccad615df2ac6dd3b165f3a0bf4dc089f8d0a26f6b9f462", 1},
	{"small", func() io.Reader { return strings.NewReader("hello") }, "sha224-d2c1cf23d9c450b456864086868c1a8bb4d8a038b2906f24ed6d390b", 2},
	{"first-chunk", func() io.Reader { return &randReader{seed: 1, length: firstChunkSize} }, "sha224-76a7ea7b4a175459727eb62a34f4bbbe330cf7cac2b2aef72ed4ffe3", 2},
	{"first-chunk+1", func() io.Reader { return &randReader{seed: 2, length: firstChunkSize + 1} }, "sha224-2a504df1014dec8e2d62fa9d5c4d65ce732ec7abf142b41d4f0f72d2", 3},
	{"rand-3MB", func() io.Reader { return &randReader{seed: 3, length: 3 << 20} }, "sha224-f492576be03ac0e49ddd0b6c64bfbf9b244ed67ecf25354fd0ee2255", 48},
	{"rand-9MB", func() io.Reader { return &randReader{seed: 4, length: 9<<20 + 12345} }, "sha224-2000fda23f35bce530faadd09d9d813827667967f90559ed466642ee", 162},
	{"zeros-5MB", func() io.Reader { return bytes.NewReader(make([]byte, 5<<20+7)) }, "sha224-f2dddb66f12b1c10e0f299060b665590bebf346402800d8f1e9b598f", 4},
	{"text-2MB", func() io.Reader {
		return strings.NewReader(strings.Repeat("All work and no play makes Jack a dull boy.\n", 2<<20/44))
	}, "sha224-a7ca9529d428734c6a7cfbe0915dd50456a38e724cba1d4ad607746a", 4},
}

// TestWriteFileMapGolden checks that WriteFileMap cuts files as it always
// has.
func TestWriteFileMapGolden(t *testing.T) {
	for _, gf := range goldenFiles {
		t.Run(gf.name, func(t *testing.T) {
			sto := new(test.Fetcher)
			br, err := WriteFileMap(ctxbg, sto, NewFileMap("golden"), gf.contents())
			if err != nil {
				t.Fatal(err)
			}
			if br.String() != gf.fileRef || sto.NumBlobs() != gf.blobs {
				t.Errorf("file schema %v, %d blobs; want %v, %d", br, sto.NumBlobs(), gf.fileRef, gf.blobs)
			}
		})
	}
}

// TestCutFile checks that CutFile's chunks make up the contents, and that
// Populate describes them as WriteFileMap does.
func TestCutFile(t *testing.T) {
	for _, gf := range goldenFiles {
		t.Run(gf.name, func(t *testing.T) {
			want, err := io.ReadAll(gf.contents())
			if err != nil {
				t.Fatal(err)
			}
			var got []byte
			refs := make(map[blob.Ref]bool)
			fc, err := CutFile(bytes.NewReader(want), func(br blob.Ref, data []byte) error {
				if blob.RefFromBytes(data) != br {
					t.Errorf("chunk at %d has ref %v, not of its contents", len(got), br)
				}
				got = append(got, data...)
				refs[br] = true
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) || fc.Size() != int64(len(want)) {
				t.Fatalf("chunks make %d bytes, Size %d; want the %d bytes of the contents", len(got), fc.Size(), len(want))
			}
			file := NewFileMap("golden")
			bytesBlobs, err := fc.Populate(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range bytesBlobs {
				refs[b.BlobRef()] = true
			}
			fileRef := file.Blob().BlobRef()
			refs[fileRef] = true
			if fileRef.String() != gf.fileRef || len(refs) != gf.blobs {
				t.Errorf("file schema %v, %d blobs; want %v, %d", fileRef, len(refs), gf.fileRef, gf.blobs)
			}
		})
	}
}

// TestCutFileChunkError checks that CutFile stops at chunk's error.
func TestCutFileChunkError(t *testing.T) {
	errStop := errors.New("stop")
	calls := 0
	_, err := CutFile(&randReader{seed: 5, length: 3 << 20}, func(blob.Ref, []byte) error {
		calls++
		return errStop
	})
	if !errors.Is(err, errStop) || calls != 1 {
		t.Errorf("CutFile = %v after %d chunks; want %v after 1", err, calls, errStop)
	}
}
