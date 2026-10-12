/*
Copyright 2025 The Perkeep Authors

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

package index

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

type kv struct{ K, V string }

func TestDupSkipper(t *testing.T) {
	var s dupSkipper
	for _, tc := range []struct {
		value string
		dup   bool
	}{
		{"", false}, {"a", false}, {"b", false},
		{"a", true}, {"", true}, {"b", true}, {"a", true},
	} {
		if got := s.Dup(tc.value); got != tc.dup {
			t.Errorf("Dup(%q) = %v; want %v", tc.value, got, tc.dup)
		}
	}
}

var testKvClaims = map[kv]camtypes.Claim{
	{
		K: "claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-60c6ad71a30be964f8d8a6f148a053af238de0d9c300bea7fe93fdf0",
		V: "set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-60c6ad71a30be964f8d8a6f148a053af238de0d9c300bea7fe93fdf0"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:37.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo1",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},

	{
		K: "claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:38.000123456Z|sha224-9ca71b6ffbb46cda3d5cc4c1ef68ab3860cc2638febd1254cd4c5891",
		V: "set-attribute|tag|foo2|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-9ca71b6ffbb46cda3d5cc4c1ef68ab3860cc2638febd1254cd4c5891"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:38.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo2",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	{
		K: "claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:39.000123456Z|sha224-79aca6e3cafa70ec76603a62acacf98722d561490664af378cdadef6",
		V: "set-attribute|camliRoot|rootval|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-79aca6e3cafa70ec76603a62acacf98722d561490664af378cdadef6"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:39.000123456Z"),
		Type:    "set-attribute", Attr: "camliRoot", Value: "rootval",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	{
		K: "claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:42.000123456Z|sha224-755a6cb33d2dd321c9d84be933273dee704f7b49ad0de31a778c10bf",
		V: "add-attribute|camliMember|sha224-ebc479bcb179797980b20e7bc7946b0c74cec7c0aec253632f009d3a|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-755a6cb33d2dd321c9d84be933273dee704f7b49ad0de31a778c10bf"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:42.000123456Z"),
		Type:    "add-attribute", Attr: "camliMember", Value: "sha224-ebc479bcb179797980b20e7bc7946b0c74cec7c0aec253632f009d3a",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	{
		K: "claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:43.000123456Z|sha224-00331f51dd359449e5ad95d42bbe3d770d5ed22e0894bb46a0bb2afb",
		V: "del-attribute|title|pony|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-00331f51dd359449e5ad95d42bbe3d770d5ed22e0894bb46a0bb2afb"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:43.000123456Z"),
		Type:    "del-attribute", Attr: "title", Value: "pony",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	{
		K: "claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c",
		V: "set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:37.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo1",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	{
		K: "claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:38.000123456Z|sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622",
		V: "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:38.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	{
		K: "claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:39.000123456Z|sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495",
		V: "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:39.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	{
		K: "claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c",
		V: "set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:37.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo1",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	{
		K: "claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:38.000123456Z|sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622",
		V: "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:38.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	{
		K: "claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:39.000123456Z|sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495",
		V: "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
	}: {
		BlobRef: blob.MustParse("sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    mustTimeParse("2011-11-28T01:32:39.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
}

func TestKvClaim(t *testing.T) {
	var cp Corpus
	for kv, claim := range testKvClaims {
		kv, claim := kv, claim
		t.Run(kv.String(), func(t *testing.T) {
			c, ok := kvClaim(kv.K, kv.V, blob.Parse)
			if !ok {
				t.Errorf("got %t, wanted %t", ok, true)
			}
			if c != claim {
				t.Errorf("got %+v, wanted %+v", c, claim)
			}

			c2, ok := cp.kvClaimBytes([]byte(kv.K), []byte(kv.V))
			if !ok {
				t.Errorf("got %t, wanted %t", ok, true)
			}
			if c2 != claim {
				t.Errorf("got %+v, wanted %+v", c2, claim)
			}
		})
	}
}

func BenchmarkKvClaim(b *testing.B) {
	type testCase struct {
		k, v  string
		claim camtypes.Claim
	}
	bb := make([]testCase, 0, len(testKvClaims))
	for kv, claim := range testKvClaims {
		bb = append(bb, testCase{k: kv.K, v: kv.V, claim: claim})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		for _, tC := range bb {
			c, ok := kvClaim(tC.k, tC.v, blob.Parse)
			if !ok {
				b.Errorf("got %t, wanted %t", ok, true)
			}
			if c != tC.claim {
				b.Errorf("got %+v, wanted %+v", c, tC.claim)
			}
		}
	}
}

func BenchmarkKvClaimBytes(b *testing.B) {
	type testCase struct {
		k, v  []byte
		claim camtypes.Claim
	}
	bb := make([]testCase, 0, len(testKvClaims))
	for kv, claim := range testKvClaims {
		bb = append(bb, testCase{k: []byte(kv.K), v: []byte(kv.V), claim: claim})
	}
	b.ResetTimer()
	b.ReportAllocs()
	var cp Corpus
	for b.Loop() {
		for _, tC := range bb {
			c, ok := cp.kvClaimBytes(tC.k, tC.v)
			if !ok {
				b.Errorf("got %t, wanted %t", ok, true)
			}
			if c != tC.claim {
				b.Errorf("got %+v, wanted %+v", c, tC.claim)
			}
		}
	}
}

func mustTimeParse(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func (kv kv) String() string { return kv.K + "," + kv.V }

type reindexEnumerationErrorSource struct {
	*test.Fetcher
	enumerate func(chan<- blob.SizedRef) error
	onFetch   func(blob.Ref)
}

func (s *reindexEnumerationErrorSource) EnumerateBlobs(_ context.Context, dest chan<- blob.SizedRef, _ string, _ int) error {
	defer close(dest)
	return s.enumerate(dest)
}

func (s *reindexEnumerationErrorSource) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	s.onFetch(br)
	return s.Fetcher.Fetch(ctx, br)
}

func TestReindexWaitsForWorkersOnEnumerationError(t *testing.T) {
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	synctest.Test(t, func(t *testing.T) {
		// Use the memory backend directly so Reindex can use its Wiper.
		s := sorted.NewMemoryKeyValue()
		ix, err := New(s)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := ix.Close(); err != nil {
				t.Error(err)
			}
		})
		primary := &test.Blob{Contents: "raw enumeration-error dependency"}
		ready := &test.Blob{Contents: `{"camliVersion":1,"camliType":"static-set","members":[]}`}
		primaryStarted, releasePrimary := make(chan struct{}), make(chan struct{})
		readyStarted, releaseReady := make(chan struct{}), make(chan struct{})
		enumErr := errors.New("enumeration failed after one blob")
		src := &reindexEnumerationErrorSource{
			Fetcher: new(test.Fetcher),
			enumerate: func(dest chan<- blob.SizedRef) error {
				dest <- primary.SizedRef()
				// Ensure EnumerateAll has delivered the ref to a worker
				// before reporting its error.
				<-primaryStarted
				return enumErr
			},
			onFetch: func(br blob.Ref) {
				switch br {
				case primary.BlobRef():
					close(primaryStarted)
					<-releasePrimary
				case ready.BlobRef():
					close(readyStarted)
					<-releaseReady
				default:
					t.Errorf("unexpected Fetch(%v)", br)
				}
			},
		}
		src.AddBlob(primary)
		src.AddBlob(ready)
		ix.InitBlobSource(src)
		// Simulate a blob waiting for primary. Its raw completion must queue
		// ready work, which must also finish before Reindex returns.
		ix.Lock()
		err = ix.noteNeededLocked(ready.BlobRef(), primary.BlobRef())
		ix.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		var reindexErr error
		returned := false
		go func() {
			reindexErr = ix.Reindex()
			returned = true
		}()
		synctest.Wait()
		if returned {
			t.Error("Reindex returned while a primary worker was blocked in Fetch")
		}
		close(releasePrimary)
		synctest.Wait()
		select {
		case <-readyStarted:
		default:
			t.Error("primary completion did not start the ready worker")
		}
		if returned {
			t.Error("Reindex returned while the ready worker was blocked in Fetch")
		}
		close(releaseReady)
		synctest.Wait()
		if !returned || !errors.Is(reindexErr, enumErr) {
			t.Errorf("Reindex returned = %v, error = %v; want true, %v", returned, reindexErr, enumErr)
		}
		for _, b := range []*test.Blob{primary, ready} {
			value, err := s.Get("have:" + b.BlobRef().String())
			if err != nil || !strings.HasSuffix(value, "|indexed") {
				t.Errorf("have:%v = %q, %v; want indexed", b.BlobRef(), value, err)
			}
		}
		ix.RLock()
		defer ix.RUnlock()
		if ix.inFlight.Len() != 0 || ix.readyWorkerDone != nil || len(ix.needs) != 0 {
			t.Errorf("undrained indexing state: inFlight=%d, readyWorkerDone=%v, needs=%d",
				ix.inFlight.Len(), ix.readyWorkerDone, len(ix.needs))
		}
	})
}

type readyReindexSource struct {
	*test.Fetcher
	onFetch func(blob.Ref)
}

func (s *readyReindexSource) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	s.onFetch(br)
	return s.Fetcher.Fetch(ctx, br)
}

func TestReadyReindexWorkerRequeuesDuringFetch(t *testing.T) {
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	synctest.Test(t, func(t *testing.T) {
		ix, err := New(sorted.NewMemoryKeyValue())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := ix.Close(); err != nil {
				t.Error(err)
			}
		})
		a, b := &test.Blob{Contents: "ready a"}, &test.Blob{Contents: "ready b"}
		started, release := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		var fetched []blob.Ref
		src := &readyReindexSource{
			Fetcher: new(test.Fetcher),
			onFetch: func(br blob.Ref) {
				fetched = append(fetched, br)
				if len(fetched) == 1 {
					close(started)
					<-release
				}
			},
		}
		src.AddBlob(a)
		src.AddBlob(b)
		ix.InitBlobSource(src)
		ix.Lock()
		ix.enqueueReadyLocked(a.BlobRef())
		ix.enqueueReadyLocked(b.BlobRef())
		done := ix.readyWorkerDone
		ix.Unlock()
		<-started

		ix.Lock()
		// A has been popped, so it must be queued again; B and the second
		// enqueue of A are still pending and must be coalesced.
		ix.enqueueReadyLocked(a.BlobRef())
		ix.enqueueReadyLocked(b.BlobRef())
		ix.enqueueReadyLocked(a.BlobRef())
		if ix.readyWorkerDone != done {
			t.Error("requeue started a second worker")
		}
		ix.Unlock()
		idle := false
		go func() {
			ix.awaitReadyIdle()
			idle = true
		}()
		synctest.Wait()
		if idle {
			t.Error("awaitReadyIdle returned while Fetch was blocked")
		}
		close(release)
		synctest.Wait()
		if want := []blob.Ref{a.BlobRef(), b.BlobRef(), a.BlobRef()}; !slices.Equal(fetched, want) {
			t.Errorf("Fetch order = %v; want %v", fetched, want)
		}
		if !idle {
			t.Error("awaitReadyIdle did not return after the queue drained")
		}
		select {
		case <-done:
		default:
			t.Error("worker did not close its completion channel")
		}
		ix.Lock()
		if ix.readyWorkerDone != nil || ix.readyReindex.Len() != 0 {
			t.Error("worker did not reset drained queue state")
		}
		ix.enqueueReadyLocked(b.BlobRef())
		if ix.readyWorkerDone == nil || ix.readyWorkerDone == done {
			t.Error("enqueue after drain did not start a new worker")
		}
		ix.Unlock()
		synctest.Wait()
		if want := []blob.Ref{a.BlobRef(), b.BlobRef(), a.BlobRef(), b.BlobRef()}; !slices.Equal(fetched, want) {
			t.Errorf("Fetch order after restart = %v; want %v", fetched, want)
		}
		for _, b := range []*test.Blob{a, b} {
			value, err := ix.s.Get("have:" + b.BlobRef().String())
			if err != nil || !strings.HasSuffix(value, "|indexed") {
				t.Errorf("have:%v = %q, %v; want indexed", b.BlobRef(), value, err)
			}
		}
	})
}

func TestReadyReindexWorkerRecoversFromFetchError(t *testing.T) {
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	synctest.Test(t, func(t *testing.T) {
		ix, err := New(sorted.NewMemoryKeyValue())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := ix.Close(); err != nil {
				t.Error(err)
			}
		})
		b := &test.Blob{Contents: "ready fetch-error retry"}
		ref := b.BlobRef()
		fetches := 0
		src := &readyReindexSource{
			Fetcher: &test.Fetcher{FetchErr: func() error {
				fetches++
				if fetches == 1 {
					return errors.New("temporary fetch failure")
				}
				return nil
			}},
			onFetch: func(br blob.Ref) {
				if br != ref {
					t.Errorf("Fetch(%v); want %v", br, ref)
				}
			},
		}
		src.AddBlob(b)
		ix.InitBlobSource(src)
		var previousDone chan struct{}
		for attempt := range 2 {
			ix.Lock()
			ix.enqueueReadyLocked(ref)
			done := ix.readyWorkerDone
			ix.Unlock()
			if done == nil || done == previousDone {
				t.Fatal("enqueue did not start a new worker")
			}
			ix.awaitReadyIdle()
			select {
			case <-done:
			default:
				t.Error("idle worker did not close its completion channel")
			}
			ix.RLock()
			wantFailed := attempt == 0
			if got := ix.readyFailed.Contains(ref); got != wantFailed {
				t.Errorf("attempt %d: readyFailed.Contains(%v) = %v; want %v", attempt, ref, got, wantFailed)
			}
			if got := ix.readyFailed.Len(); got != 1-attempt {
				t.Errorf("attempt %d: readyFailed.Len() = %d; want %d", attempt, got, 1-attempt)
			}
			if ix.readyWorkerDone != nil || ix.readyReindex.Len() != 0 || ix.inFlight.Len() != 0 {
				t.Error("worker did not reset drained indexing state")
			}
			ix.RUnlock()
			previousDone = done
		}
		if fetches != 2 {
			t.Errorf("fetches = %d; want 2", fetches)
		}
		value, err := ix.s.Get("have:" + ref.String())
		if err != nil || !strings.HasSuffix(value, "|indexed") {
			t.Errorf("have:%v = %q, %v; want indexed after retry", ref, value, err)
		}
	})
}
