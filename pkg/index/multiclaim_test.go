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
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
)

func TestMultiClaim_Memory(t *testing.T) {
	indextest.MultiClaim(t, index.NewMemoryIndex)
}

func TestMultiClaim_LevelDB(t *testing.T) {
	indexTest(t, newLevelDBSorted, indextest.MultiClaim)
}

func TestMultiClaim_Kvfile(t *testing.T) {
	indexTest(t, newKvfileSorted, indextest.MultiClaim)
}

func TestMultiClaimDeleteBeforeEnvelope_Memory(t *testing.T) {
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "true")
	t.Setenv("CAMLI_REINDEX_START", "")
	oldProcs := index.ReindexMaxProcs()
	index.SetReindexMaxProcs(1)
	defer index.SetReindexMaxProcs(oldProcs)
	synctest.Test(t, func(t *testing.T) {
		ix := index.NewMemoryIndex()
		ix.Logger = test.NewLogger(t, "")
		id := indextest.NewIndexDeps(ix)
		id.Fataler = t
		c, err := ix.KeepInMemory()
		if err != nil {
			t.Fatal(err)
		}
		pn, pn2 := id.NewPermanode(), id.NewPermanode()
		multi, err := schema.NewMultiClaim(
			schema.NewSetAttributeClaim(pn, "title", "first"),
			schema.NewSetAttributeClaim(pn, "title", "last"),
			schema.NewSetAttributeClaim(pn2, "title", "other"),
		)
		if err != nil {
			t.Fatal(err)
		}
		multi.SetClaimDate(test.ClockOrigin)
		// Pick an envelope in the upper half of the digest space so that
		// each deletion nonce below has at least even odds of sorting
		// before it. Otherwise an envelope near the bottom of the space
		// makes the search below fail about 1% of the time.
		half := blob.MustParse("sha224-80" + strings.Repeat("0", 54))
		var envelope *test.Blob
		for n := 0; ; n++ {
			envelope = id.Sign(multi.SetRawStringField("nonce", strconv.Itoa(n)))
			if !envelope.BlobRef().Less(half) {
				break
			}
		}
		var deleter *test.Blob
		for n := range 100 {
			deleter = id.Sign(schema.NewDeleteClaim(envelope.BlobRef()).SetClaimDate(test.ClockOrigin).SetRawStringField("nonce", strconv.Itoa(n)))
			if deleter.BlobRef().Less(envelope.BlobRef()) {
				break
			}
		}
		if !deleter.BlobRef().Less(envelope.BlobRef()) {
			t.Fatal("could not construct deletion sorting before envelope")
		}
		// Target not yet in the source or the index: this receive records only
		// metadata and the missing dependency, not the deletion itself.
		id.Upload(deleter)
		id.Upload(envelope)
		synctest.Wait()
		if !ix.IsDeleted(envelope.BlobRef()) || !c.IsDeleted(envelope.BlobRef()) {
			t.Fatal("automatic delete retry did not update both deletion caches")
		}
		for _, pn := range []blob.Ref{pn, pn2} {
			claims, err := ix.AppendClaims(ctxbg, nil, pn, indextest.KeyID, "")
			if err != nil || len(claims) != 0 {
				t.Errorf("deleted children = %v, %v", claims, err)
			}
			if got := c.PermanodeAttrValue(pn, "title", time.Time{}, indextest.KeyID); got != "" {
				t.Errorf("deleted cached title = %q", got)
			}
		}
		// A fresh live corpus must also survive reindex enumeration where the
		// deletion sorts before its envelope (one worker makes that definite).
		rebuilt := index.NewMemoryIndex()
		rebuilt.Logger = test.NewLogger(t, "")
		rebuilt.KeyFetcher = id.PublicKeyFetcher
		rebuilt.InitBlobSource(id.BlobSource)
		rebuiltCorpus, err := rebuilt.KeepInMemory()
		if err != nil {
			t.Fatal(err)
		}
		if err := rebuilt.Reindex(); err != nil {
			t.Fatal(err)
		}
		if !rebuiltCorpus.IsDeleted(envelope.BlobRef()) {
			t.Error("reindex lost pending envelope deletion")
		}
		for _, pn := range []blob.Ref{pn, pn2} {
			if got := rebuiltCorpus.PermanodeAttrValue(pn, "title", time.Time{}, indextest.KeyID); got != "" {
				t.Errorf("reindex retained deleted cached title = %q", got)
			}
		}
		// A forced repeated receive must remain idempotent even after retry.
		id.Upload(deleter)
		id.Upload(envelope)
		id.Delete(deleter.BlobRef())
		synctest.Wait()
		if got := c.PermanodeAttrValue(pn, "title", time.Time{}, indextest.KeyID); got != "last" {
			t.Errorf("restored cached title = %q", got)
		}
		claims, err := ix.AppendClaims(ctxbg, nil, pn, indextest.KeyID, "")
		if err != nil || len(claims) != 2 {
			t.Errorf("restored children = %v, %v; want two", claims, err)
		}
	})
}
