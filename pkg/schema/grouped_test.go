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
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/test"
)

// groupDest is storage recording its ReceiveBlobs calls, and failing them
// with err, if set.
type groupDest struct {
	*test.Fetcher
	mu    sync.Mutex
	calls [][]blob.Ref
	err   error
}

func (d *groupDest) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var refs []blob.Ref
	for _, b := range blobs {
		refs = append(refs, b.Ref())
	}
	d.calls = append(d.calls, refs)
	if d.err != nil {
		return d.err
	}
	return blobserver.ReceiveBlobsUnchecked(ctx, d.Fetcher, blobs)
}

// TestWriteFileMapGrouped checks that WriteFileMapGrouped writes what
// WriteFileMap does, in groups, with the file schema blob alone and last,
// and doesn't write blobs again.
func TestWriteFileMapGrouped(t *testing.T) {
	for _, gf := range goldenFiles {
		t.Run(gf.name, func(t *testing.T) {
			d := &groupDest{Fetcher: new(test.Fetcher)}
			br, err := WriteFileMapGrouped(ctxbg, d, NewFileMap("golden"), gf.contents())
			if err != nil {
				t.Fatal(err)
			}
			if br.String() != gf.fileRef || d.NumBlobs() != gf.blobs {
				t.Errorf("file schema %v, %d blobs; want %v, %d", br, d.NumBlobs(), gf.fileRef, gf.blobs)
			}
			if last := d.calls[len(d.calls)-1]; !slices.Equal(last, []blob.Ref{br}) {
				t.Errorf("last ReceiveBlobs call of %v; want just the file schema blob %v", last, br)
			}
			// Chunks, bytes blobs, and the file schema: at most two calls,
			// for files of at most groupBlobs-1 chunks and bytes blobs.
			if gf.blobs <= groupBlobs && len(d.calls) > 2 {
				t.Errorf("%d ReceiveBlobs calls for %d blobs; want at most 2", len(d.calls), gf.blobs)
			}
			for _, call := range d.calls {
				if len(call) > groupBlobs {
					t.Errorf("ReceiveBlobs call of %d blobs; want at most %d", len(call), groupBlobs)
				}
			}

			d.calls = nil
			if _, err := WriteFileMapGrouped(ctxbg, d, NewFileMap("golden"), gf.contents()); err != nil {
				t.Fatal(err)
			}
			if len(d.calls) != 0 {
				t.Errorf("writing the same file again made %d ReceiveBlobs calls; want none", len(d.calls))
			}
		})
	}
}

// TestWriteFileMapGroupedManyGroups checks a file of more blobs than fit
// in one group.
func TestWriteFileMapGroupedManyGroups(t *testing.T) {
	d := &groupDest{Fetcher: new(test.Fetcher)}
	r := &randReader{seed: 6, length: 40 << 20} // about 600 chunks
	var want bytes.Buffer
	br, err := WriteFileMapGrouped(ctxbg, d, NewFileMap("big"), io.TeeReader(r, &want))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.calls) < 4 {
		t.Errorf("%d ReceiveBlobs calls; want 3 or more groups and the file schema blob", len(d.calls))
	}
	fr, err := NewFileReader(ctxbg, d, br)
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	got, err := io.ReadAll(fr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Errorf("file reads back as %d bytes; want the %d written", len(got), want.Len())
	}
}

// TestWriteFileMapGroupedError checks that a failed group stops the write
// before the file schema blob.
func TestWriteFileMapGroupedError(t *testing.T) {
	errFail := errors.New("receive failed")
	d := &groupDest{Fetcher: new(test.Fetcher), err: errFail}
	_, err := WriteFileMapGrouped(ctxbg, d, NewFileMap("big"), &randReader{seed: 7, length: 40 << 20})
	if !errors.Is(err, errFail) {
		t.Errorf("WriteFileMapGrouped = %v; want %v", err, errFail)
	}
	if len(d.calls) != 1 {
		t.Errorf("%d ReceiveBlobs calls; want 1, the failed one", len(d.calls))
	}
}
