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

package index_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// commitCountingKV counts batch commits.
type commitCountingKV struct {
	sorted.KeyValue
	commits int
}

func (kv *commitCountingKV) CommitBatch(b sorted.BatchMutation) error {
	kv.commits++
	return kv.KeyValue.CommitBatch(b)
}

func newCountingIndex(t *testing.T) (*index.Index, *commitCountingKV) {
	t.Helper()
	kv := &commitCountingKV{KeyValue: sorted.NewMemoryKeyValue()}
	ix, err := index.New(kv)
	if err != nil {
		t.Fatal(err)
	}
	return ix, kv
}

// indexRows returns all of ix's rows.
func indexRows(t *testing.T, ix *index.Index) map[string]string {
	t.Helper()
	rows := map[string]string{}
	it := ix.Storage().Find("", "")
	for it.Next() {
		rows[it.Key()] = it.Value()
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// receiveBlobsTestBlobs returns signed blobs that depend on each other: a
// permanode, claims on it, a file and its contents, and a delete claim.
func receiveBlobsTestBlobs(t *testing.T) []*test.Blob {
	signer := indextest.NewIndexDeps(index.NewMemoryIndex())
	signer.Fataler = t
	t.Cleanup(func() {
		if err := signer.Index.Close(); err != nil {
			t.Error(err)
		}
	})
	now := test.ClockOrigin
	claim := func(m *schema.Builder) *test.Blob {
		now = now.Add(time.Second)
		m.SetClaimDate(now)
		return signer.Sign(m)
	}
	pn := signer.Sign(schema.NewPlannedPermanode("receive-blobs"))
	title := claim(schema.NewSetAttributeClaim(pn.BlobRef(), "title", "a title"))
	tag := claim(schema.NewAddAttributeClaim(pn.BlobRef(), "tag", "a tag"))
	contents := &test.Blob{Contents: "some file contents"}
	fm := schema.NewFileMap("file.txt")
	fm.PopulateParts(int64(len(contents.Contents)), []schema.BytesPart{{Size: uint64(len(contents.Contents)), BlobRef: contents.BlobRef()}})
	fjson, err := fm.JSON()
	if err != nil {
		t.Fatal(err)
	}
	file := &test.Blob{Contents: fjson}
	content := claim(schema.NewSetAttributeClaim(pn.BlobRef(), "camliContent", file.BlobRef().String()))
	del := claim(schema.NewDeleteClaim(tag.BlobRef()))
	// Dependents before what they depend on, so receiving them together
	// has to resolve dependencies within the call.
	return []*test.Blob{del, content, title, tag, file, contents, pn}
}

// TestReceiveBlobsMatchesReceiveBlob checks that indexing blobs together,
// including blobs depending on later ones, makes the same rows as
// indexing them one at a time in order of their dependencies.
func TestReceiveBlobsMatchesReceiveBlob(t *testing.T) {
	ctx := context.Background()
	tbs := receiveBlobsTestBlobs(t)

	oneAtATime := indextest.NewIndexDeps(index.NewMemoryIndex())
	oneAtATime.Fataler = t
	for _, tb := range tbs {
		oneAtATime.BlobSource.AddBlob(tb)
	}
	for _, tb := range slices.Backward(tbs) {
		if _, err := oneAtATime.Index.ReceiveBlob(ctx, tb.BlobRef(), tb.Reader()); err != nil {
			t.Fatal(err)
		}
	}
	oneAtATime.Index.Exp_AwaitAsyncIndexing(t)

	ix, kv := newCountingIndex(t)
	together := indextest.NewIndexDeps(ix)
	together.Fataler = t
	var blobs []*blob.Blob
	for _, tb := range tbs {
		together.BlobSource.AddBlob(tb)
		blobs = append(blobs, tb.Blob())
	}
	kv.commits = 0
	if err := blobserver.ReceiveBlobs(ctx, together.Index, blobs); err != nil {
		t.Fatal(err)
	}
	together.Index.Exp_AwaitAsyncIndexing(t)

	want, got := indexRows(t, oneAtATime.Index), indexRows(t, together.Index)
	// The tag's delete claim, received before the tag, takes effect.
	if !slices.ContainsFunc(slices.Collect(maps.Keys(want)), func(k string) bool { return strings.HasPrefix(k, "deleted|") }) {
		t.Fatal("one-at-a-time index has no deleted row; test blobs aren't what they should be")
	}
	if !maps.Equal(got, want) {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("row %q = %q; want %q", k, got[k], v)
			}
		}
		for k, v := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("extra row %q = %q", k, v)
			}
		}
	}
}

// TestReceiveBlobsOneCommit checks that ReceiveBlobs commits independent
// blobs' rows together.
func TestReceiveBlobsOneCommit(t *testing.T) {
	ix, kv := newCountingIndex(t)
	id := indextest.NewIndexDeps(ix)
	id.Fataler = t
	var blobs []*blob.Blob
	for i := range 50 {
		tb := &test.Blob{Contents: fmt.Sprintf("blob %d", i)}
		id.BlobSource.AddBlob(tb)
		blobs = append(blobs, tb.Blob())
	}
	kv.commits = 0
	if err := blobserver.ReceiveBlobs(context.Background(), ix, blobs); err != nil {
		t.Fatal(err)
	}
	if kv.commits != 1 {
		t.Errorf("index commits = %d; want 1", kv.commits)
	}
	for _, b := range blobs {
		if v := id.Get("have:" + b.Ref().String()); v == "" {
			t.Errorf("%v not indexed", b.Ref())
		}
	}
}

// TestReceiveBlobsCorpusRetention exercises multiple preparation-worker waves
// and later uploads while the corpus retains the original blobs' metadata.
func TestReceiveBlobsCorpusRetention(t *testing.T) {
	ctx := context.Background()
	tbs := receiveBlobsTestBlobs(t)
	deleter, tag, file, pn := tbs[0].BlobRef(), tbs[3].BlobRef(), tbs[4].BlobRef(), tbs[6].BlobRef()
	oneAtATime := indextest.NewIndexDeps(index.NewMemoryIndex())
	ix, _ := newCountingIndex(t)
	together := indextest.NewIndexDeps(ix)
	for _, id := range []*indextest.IndexDeps{oneAtATime, together} {
		id.Fataler = t
		t.Cleanup(func() {
			id.Index.Exp_AwaitAsyncIndexing(t)
			if err := id.Index.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	corpus, err := ix.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}

	addFiles := func(blobs []*test.Blob, prefix string) []*test.Blob {
		for i := range 5 {
			raw := &test.Blob{Contents: strings.Repeat(fmt.Sprintf("%s contents %d\n", prefix, i), 256)}
			fm := schema.NewFileMap(fmt.Sprintf("%s-%d.txt", prefix, i))
			if err := fm.PopulateParts(int64(len(raw.Contents)), []schema.BytesPart{{Size: uint64(len(raw.Contents)), BlobRef: raw.BlobRef()}}); err != nil {
				t.Fatal(err)
			}
			blobs = append(blobs, &test.Blob{Contents: fm.Blob().JSON()}, raw)
		}
		return blobs
	}
	otherPN := together.Sign(schema.NewPlannedPermanode("receive-blobs-followup"))
	otherTitle := together.Sign(schema.NewSetAttributeClaim(otherPN.BlobRef(), "title", "a different, longer title").
		SetClaimDate(test.ClockOrigin))
	batches := [][]*test.Blob{
		addFiles(tbs, "initial"),
		addFiles([]*test.Blob{otherTitle, otherPN}, "followup"),
	}
	for batch, tbs := range batches {
		// Each batch has more than eight mixed schema/raw blobs, so workers
		// can reuse sniffers before all prepared mutations are committed.
		var blobs []*blob.Blob
		for _, tb := range tbs {
			oneAtATime.BlobSource.AddBlob(tb)
			together.BlobSource.AddBlob(tb)
			blobs = append(blobs, tb.Blob())
		}
		for _, tb := range slices.Backward(tbs) {
			if _, err := oneAtATime.Index.ReceiveBlob(ctx, tb.BlobRef(), tb.Reader()); err != nil {
				t.Fatal(err)
			}
		}
		oneAtATime.Index.Exp_AwaitAsyncIndexing(t)
		if err := blobserver.ReceiveBlobs(ctx, ix, blobs); err != nil {
			t.Fatal(err)
		}
		ix.Exp_AwaitAsyncIndexing(t)

		want, got := indexRows(t, oneAtATime.Index), indexRows(t, ix)
		for k, v := range want {
			if gotValue, ok := got[k]; !ok || gotValue != v {
				t.Errorf("batch %d: row %q = %q; want %q", batch, k, got[k], v)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("batch %d: extra row %q", batch, k)
			}
		}
		fi, err := corpus.GetFileInfo(ctx, file)
		if err != nil || fi.FileName != "file.txt" {
			t.Errorf("batch %d: original file = %v, %v; want file.txt", batch, fi, err)
		}
		for attr, want := range map[string]string{"title": "a title", "camliContent": file.String()} {
			if got := corpus.PermanodeAttrValue(pn, attr, time.Time{}, indextest.KeyID); got != want {
				t.Errorf("batch %d: original %s = %q; want %q", batch, attr, got, want)
			}
		}
		if got := corpus.AppendPermanodeAttrValues(nil, pn, "tag", time.Time{}, indextest.KeyID); len(got) != 0 {
			t.Errorf("batch %d: deleted tag values = %v", batch, got)
		}
		if !ix.IsDeleted(tag) || !corpus.IsDeleted(tag) || ix.IsDeleted(deleter) || corpus.IsDeleted(deleter) ||
			ix.IsDeleted(pn) || corpus.IsDeleted(pn) {
			t.Errorf("batch %d: original tag/delete/permanode deletion state changed", batch)
		}
	}
}
