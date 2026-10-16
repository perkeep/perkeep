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

package blobserver_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

// sortedRefs returns refs to blobs "a", "b", ..., sorted.
func sortedRefs(n int) []blob.Ref {
	var refs []blob.Ref
	for i := range n {
		refs = append(refs, blob.RefFromString(string(rune('a'+i))))
	}
	slices.SortFunc(refs, func(a, b blob.Ref) int {
		if a.Less(b) {
			return -1
		}
		if b.Less(a) {
			return 1
		}
		return 0
	})
	return refs
}

func TestBlobsErrorAdd(t *testing.T) {
	refs := sortedRefs(3)
	errA, errB := errors.New("a"), errors.New("b")
	var be blobserver.BlobsError
	if be.Len() != 0 || be.OrNil() != nil {
		t.Fatalf("zero BlobsError: Len = %d, OrNil = %v", be.Len(), be.OrNil())
	}
	be.Add(refs[2], errA)
	be.Add(refs[2], errB) // the first error wins
	be.Add(refs[0], errB)
	be.Add(refs[1], nil) // ignored
	if be.Len() != 2 {
		t.Fatalf("Len = %d; want 2", be.Len())
	}
	for i, want := range []error{errB, nil, errA} {
		if got := be.Err(refs[i]); got != want {
			t.Errorf("Err(refs[%d]) = %v; want %v", i, got, want)
		}
	}
	var gotRefs []blob.Ref
	var gotErrs []error
	for br, err := range be.All() {
		gotRefs = append(gotRefs, br)
		gotErrs = append(gotErrs, err)
	}
	if want := []blob.Ref{refs[0], refs[2]}; !slices.Equal(gotRefs, want) {
		t.Errorf("All refs = %v; want %v", gotRefs, want)
	}
	if want := []error{errB, errA}; !slices.Equal(gotErrs, want) {
		t.Errorf("All errors = %v; want %v", gotErrs, want)
	}
	if got := be.Unwrap(); !slices.Equal(got, gotErrs) {
		t.Errorf("Unwrap = %v; want %v", got, gotErrs)
	}
	if be.OrNil() != &be {
		t.Errorf("OrNil = %v; want the BlobsError", be.OrNil())
	}

	var nilBE *blobserver.BlobsError
	if nilBE.Len() != 0 || nilBE.Err(refs[0]) != nil || nilBE.OrNil() != nil {
		t.Error("nil *BlobsError isn't empty")
	}
}

func TestBlobsErrorIs(t *testing.T) {
	refs := sortedRefs(2)
	be := &blobserver.BlobsError{Op: "receive"}
	be.Add(refs[0], fmt.Errorf("checking: %w", blobserver.ErrCorruptBlob))
	be.Add(refs[1], errors.New("other"))
	err := fmt.Errorf("wrapped: %w", be)
	if !errors.Is(err, blobserver.ErrCorruptBlob) {
		t.Error("errors.Is(err, ErrCorruptBlob) = false; want true, as one blob was corrupt")
	}
	if got, ok := errors.AsType[*blobserver.BlobsError](err); !ok || got != be {
		t.Errorf("errors.AsType = %v, %v", got, ok)
	}
}

func TestBlobsErrorMessage(t *testing.T) {
	refs := sortedRefs(6)
	tests := []struct {
		name string
		errs map[int]string // index in refs to message
		op   string
		want string
	}{
		{
			name: "one",
			op:   "receive",
			errs: map[int]string{1: "disk full"},
			want: fmt.Sprintf("receive %v: disk full", refs[1]),
		},
		{
			name: "grouped",
			op:   "receive",
			errs: map[int]string{0: "disk full", 1: "corrupt", 2: "disk full"},
			want: fmt.Sprintf("receive: 3 blobs failed: disk full (2 blobs, including %v); %v: corrupt", refs[0], refs[1]),
		},
		{
			name: "many",
			errs: map[int]string{0: "e0", 1: "e1", 2: "e2", 3: "e3", 4: "e4", 5: "e1"},
			want: fmt.Sprintf("blobs operation: 6 blobs failed: %v: e0; e1 (2 blobs, including %v); %v: e2; and 2 more distinct errors", refs[0], refs[1], refs[2]),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be := &blobserver.BlobsError{Op: tt.op}
			for i, msg := range tt.errs {
				be.Add(refs[i], errors.New(msg))
			}
			if got := be.Error(); got != tt.want {
				t.Errorf("Error() =\n\t%s\nwant\n\t%s", got, tt.want)
			}
		})
	}
}

func TestBlobsErrorAddResult(t *testing.T) {
	refs := sortedRefs(4)
	errChild, errAll := errors.New("child"), errors.New("all")
	child := &blobserver.BlobsError{}
	child.Add(refs[1], errChild)

	var be blobserver.BlobsError
	be.AddResult(refs[:2], nil)
	if be.Len() != 0 {
		t.Fatalf("after nil result, Len = %d", be.Len())
	}
	be.AddResult(refs[:2], fmt.Errorf("shard 1: %w", child))
	be.AddResult(refs[2:], errAll)
	for i, want := range []error{nil, errChild, errAll, errAll} {
		if got := be.Err(refs[i]); got != want {
			t.Errorf("Err(refs[%d]) = %v; want %v", i, got, want)
		}
	}
}

func TestBlobErr(t *testing.T) {
	refs := sortedRefs(2)
	errOne, errAll := errors.New("one"), errors.New("all")
	be := &blobserver.BlobsError{}
	be.Add(refs[0], errOne)
	tests := []struct {
		err  error
		br   blob.Ref
		want error
	}{
		{nil, refs[0], nil},
		{be, refs[0], errOne},
		{be, refs[1], nil},
		{fmt.Errorf("wrapped: %w", be), refs[0], errOne},
		{errAll, refs[1], errAll},
	}
	for i, tt := range tests {
		if got := blobserver.BlobErr(tt.err, tt.br); got != tt.want {
			t.Errorf("%d. BlobErr(%v, %v) = %v; want %v", i, tt.err, tt.br, got, tt.want)
		}
	}
}
