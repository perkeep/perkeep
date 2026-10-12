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

package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/types/camtypes"
)

type queryErrorIndex struct {
	index.Interface
	enumerate   func(context.Context, func(camtypes.BlobMeta) bool) error
	fileInfoErr error
}

func (x *queryErrorIndex) EnumerateBlobMeta(ctx context.Context, fn func(camtypes.BlobMeta) bool) error {
	return x.enumerate(ctx, fn)
}

func (x *queryErrorIndex) GetFileInfo(ctx context.Context, br blob.Ref) (camtypes.FileInfo, error) {
	if x.fileInfoErr != nil {
		return camtypes.FileInfo{}, x.fileInfoErr
	}
	return x.Interface.GetFileInfo(ctx, br)
}

func newQueryErrorIndex(t *testing.T) *queryErrorIndex {
	t.Helper()
	idx := index.NewMemoryIndex()
	t.Cleanup(func() {
		if err := idx.Close(); err != nil {
			t.Error(err)
		}
	})
	return &queryErrorIndex{Interface: idx}
}

func TestQueryEnumerationErrors(t *testing.T) {
	enumErr := errors.New("enumeration failed")
	for _, partial := range []bool{false, true} {
		name := "before_results"
		if partial {
			name = "after_partial_results"
		}
		t.Run(name, func(t *testing.T) {
			idx := newQueryErrorIndex(t)
			idx.enumerate = func(ctx context.Context, fn func(camtypes.BlobMeta) bool) error {
				if partial && !fn(camtypes.BlobMeta{Ref: blob.RefFromString("partial result")}) {
					t.Fatal("enumeration stopped before failure")
				}
				return enumErr
			}
			h := &Handler{index: idx}
			res, err := h.Query(context.Background(), &SearchQuery{
				Constraint: &Constraint{Anything: true},
				Sort:       Unsorted,
			})
			if !errors.Is(err, enumErr) || res != nil {
				t.Fatalf("Query = (%v, %v); want (nil, %v)", res, err, enumErr)
			}
		})
	}
}

func TestQueryMatcherErrorPrecedence(t *testing.T) {
	matchErr := errors.New("matcher failed")
	for _, tt := range []struct {
		name    string
		enumErr error
	}{
		{"normal_stop", nil},
		{"enumeration_error", errors.New("enumeration failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			idx := newQueryErrorIndex(t)
			idx.fileInfoErr = matchErr
			idx.enumerate = func(ctx context.Context, fn func(camtypes.BlobMeta) bool) error {
				if fn(camtypes.BlobMeta{Ref: blob.RefFromString("file"), CamliType: "file"}) {
					t.Error("enumeration continued after matcher error")
				}
				return tt.enumErr
			}
			h := &Handler{index: idx}
			res, err := h.Query(context.Background(), &SearchQuery{
				Constraint: &Constraint{File: &FileConstraint{}},
				Sort:       Unsorted,
			})
			if !errors.Is(err, matchErr) || res != nil {
				t.Fatalf("Query = (%v, %v); want (nil, %v)", res, err, matchErr)
			}
		})
	}
}

func TestQueryCallerContext(t *testing.T) {
	for _, when := range []string{"before_results", "after_partial_results", "empty_source", "expired_deadline"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			wantErr := context.Canceled
			if when == "expired_deadline" {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
				wantErr = context.DeadlineExceeded
			} else if when != "after_partial_results" {
				cancel()
			}
			defer cancel()
			idx := newQueryErrorIndex(t)
			idx.enumerate = func(ctx context.Context, fn func(camtypes.BlobMeta) bool) error {
				if when == "empty_source" {
					return nil
				}
				meta := camtypes.BlobMeta{Ref: blob.RefFromString("result")}
				if when == "after_partial_results" {
					if !fn(meta) {
						t.Fatal("enumeration stopped before cancellation")
					}
					cancel()
				}
				if fn(meta) {
					t.Error("enumeration continued after cancellation")
				}
				// Corpus candidate sources can return nil even on cancellation.
				return nil
			}
			h := &Handler{index: idx}
			res, err := h.Query(ctx, &SearchQuery{
				Constraint: &Constraint{Anything: true},
				Sort:       Unsorted,
			})
			if !errors.Is(err, wantErr) || res != nil {
				t.Fatalf("Query = (%v, %v); want (nil, %v)", res, err, wantErr)
			}
		})
	}
}

func TestQueryCallerContextPropagation(t *testing.T) {
	type contextKey struct{}
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.WithValue(context.Background(), contextKey{}, "caller"), deadline)
	defer cancel()
	idx := newQueryErrorIndex(t)
	idx.enumerate = func(ctx context.Context, fn func(camtypes.BlobMeta) bool) error {
		if got := ctx.Value(contextKey{}); got != "caller" {
			t.Errorf("enumeration context value = %v; want caller", got)
		}
		if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
			t.Errorf("enumeration context deadline = (%v, %v); want %v", got, ok, deadline)
		}
		return nil
	}
	h := &Handler{index: idx}
	if _, err := h.Query(ctx, &SearchQuery{Constraint: &Constraint{Anything: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestQueryCanceledCorpus(t *testing.T) {
	idx := index.NewMemoryIndex()
	defer idx.Close()
	corpus, err := idx.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{index: idx, corpus: corpus}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := h.Query(ctx, &SearchQuery{Constraint: &Constraint{CamliType: "file"}})
	if !errors.Is(err, context.Canceled) || res != nil {
		t.Fatalf("Query = (%v, %v); want (nil, %v)", res, err, context.Canceled)
	}
}

func TestQueryLimitStopsNormally(t *testing.T) {
	idx := index.NewMemoryIndex()
	defer idx.Close()
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	first := id.NewPermanode()
	id.SetAttribute(first, "title", "first")
	last := id.NewPermanode()
	id.SetAttribute(last, "title", "last")
	corpus, err := idx.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{index: idx, corpus: corpus}
	res, err := h.Query(context.Background(), &SearchQuery{
		Constraint: &Constraint{CamliType: "permanode"},
		Sort:       CreatedDesc,
		Limit:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blobs) != 1 || res.Blobs[0].Blob != last || res.Continue == "" {
		t.Fatalf("Query = %+v; want last permanode and a continue token", res)
	}
}
