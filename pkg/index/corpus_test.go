/*
Copyright 2013 The Camlistore Authors

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
	"fmt"
	"reflect"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
	"golang.org/x/net/context"
)

func TestCorpusAppendPermanodeAttrValues(t *testing.T) {
	c := index.ExpNewCorpus()
	pn := blob.MustParse("abc-123")
	tm := time.Unix(99, 0)
	claim := func(verb, attr, val string) *camtypes.Claim {
		tm = tm.Add(time.Second)
		return &camtypes.Claim{
			Type:  verb + "-attribute",
			Attr:  attr,
			Value: val,
			Date:  tm,
		}
	}
	s := func(s ...string) []string { return s }

	c.SetClaims(pn, &index.PermanodeMeta{
		Claims: []*camtypes.Claim{
			claim("set", "foo", "foov"), // time 100

			claim("add", "tag", "a"), // time 101
			claim("add", "tag", "b"), // time 102
			claim("del", "tag", ""),
			claim("add", "tag", "c"),
			claim("add", "tag", "d"),
			claim("add", "tag", "e"),
			claim("del", "tag", "d"),

			claim("add", "DelAll", "a"),
			claim("add", "DelAll", "b"),
			claim("add", "DelAll", "c"),
			claim("del", "DelAll", ""),

			claim("add", "DelOne", "a"),
			claim("add", "DelOne", "b"),
			claim("add", "DelOne", "c"),
			claim("add", "DelOne", "d"),
			claim("del", "DelOne", "d"),
			claim("del", "DelOne", "a"),

			claim("add", "SetAfterAdd", "a"),
			claim("add", "SetAfterAdd", "b"),
			claim("set", "SetAfterAdd", "setv"),
		},
	})

	tests := []struct {
		attr string
		want []string
		t    time.Time
	}{
		{attr: "not-exist", want: s()},
		{attr: "DelAll", want: s()},
		{attr: "DelOne", want: s("b", "c")},
		{attr: "foo", want: s("foov")},
		{attr: "tag", want: s("c", "e")},
		{attr: "tag", want: s("a", "b"), t: time.Unix(102, 0)},
		{attr: "SetAfterAdd", want: s("setv")},
	}
	for i, tt := range tests {
		got := c.AppendPermanodeAttrValues(nil, pn, tt.attr, tt.t, blob.Ref{})
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. attr %q = %q; want %q",
				i, tt.attr, got, tt.want)
		}
	}

}

func TestKVClaimAllocs(t *testing.T) {
	n := testing.AllocsPerRun(20, func() {
		index.ExpKvClaim("claim|sha1-b380b3080f9c71faa5c1d82bbd4d583a473bc77d|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha1-b3d93daee62e40d36237ff444022f42d7d0e43f2",
			"set-attribute|tag|foo1|sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007",
			blob.Parse)
	})
	t.Logf("%v allocations", n)
}

func TestKVClaim(t *testing.T) {
	tests := []struct {
		k, v string
		ok   bool
		want camtypes.Claim
	}{
		{
			k:  "claim|sha1-b380b3080f9c71faa5c1d82bbd4d583a473bc77d|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha1-b3d93daee62e40d36237ff444022f42d7d0e43f2",
			v:  "set-attribute|tag|foo1|sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007",
			ok: true,
			want: camtypes.Claim{
				BlobRef:   blob.MustParse("sha1-b3d93daee62e40d36237ff444022f42d7d0e43f2"),
				Signer:    blob.MustParse("sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007"),
				Permanode: blob.MustParse("sha1-b380b3080f9c71faa5c1d82bbd4d583a473bc77d"),
				Type:      "set-attribute",
				Attr:      "tag",
				Value:     "foo1",
				Date:      time.Time(types.ParseTime3339OrZero("2011-11-28T01:32:37.000123456Z")),
			},
		},
	}
	for _, tt := range tests {
		got, ok := index.ExpKvClaim(tt.k, tt.v, blob.Parse)
		if ok != tt.ok {
			t.Errorf("kvClaim(%q, %q) = ok %v; want %v", tt.k, tt.v, ok, tt.ok)
			continue
		}
		if got != tt.want {
			t.Errorf("kvClaim(%q, %q) = %+v; want %+v", tt.k, tt.v, got, tt.want)
			continue
		}
	}
}

func TestDeletePermanode_Modtime(t *testing.T) {
	testDeletePermanodes(t,
		func(c *index.Corpus, ctx context.Context, ch chan<- camtypes.BlobMeta) error {
			return c.EnumeratePermanodesLastModifiedLocked(ctx, ch)
		},
	)
}

func TestDeletePermanode_CreateTime(t *testing.T) {
	testDeletePermanodes(t,
		func(c *index.Corpus, ctx context.Context, ch chan<- camtypes.BlobMeta) error {
			return c.EnumeratePermanodesCreatedLocked(ctx, ch, true)
		},
	)
}

func testDeletePermanodes(t *testing.T,
	enumFunc func(*index.Corpus, context.Context, chan<- camtypes.BlobMeta) error) {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)

	foopn := idxd.NewPlannedPermanode("foo")
	idxd.SetAttribute(foopn, "tag", "foo")
	barpn := idxd.NewPlannedPermanode("bar")
	idxd.SetAttribute(barpn, "tag", "bar")
	bazpn := idxd.NewPlannedPermanode("baz")
	idxd.SetAttribute(bazpn, "tag", "baz")
	idxd.Delete(barpn)
	c, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}

	// check that we initially only find permanodes foo and baz,
	// because bar is already marked as deleted.
	want := []blob.Ref{foopn, bazpn}
	ch := make(chan camtypes.BlobMeta, 10)
	var got []camtypes.BlobMeta
	errc := make(chan error, 1)
	c.RLock()
	go func() { errc <- enumFunc(c, context.TODO(), ch) }()
	for blobMeta := range ch {
		got = append(got, blobMeta)
	}
	err = <-errc
	c.RUnlock()
	if err != nil {
		t.Fatalf("Could not enumerate permanodes: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Saw %d permanodes in corpus; want %d", len(got), len(want))
	}
	for _, bm := range got {
		found := false
		for _, perm := range want {
			if bm.Ref == perm {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("permanode %v was not found in corpus", bm.Ref)
		}
	}

	// now add a delete claim for permanode baz, and check that we're only left with foo permanode
	delbaz := idxd.Delete(bazpn)
	want = []blob.Ref{foopn}
	got = got[:0]
	ch = make(chan camtypes.BlobMeta, 10)
	c.RLock()
	go func() { errc <- enumFunc(c, context.TODO(), ch) }()
	for blobMeta := range ch {
		got = append(got, blobMeta)
	}
	err = <-errc
	c.RUnlock()
	if err != nil {
		t.Fatalf("Could not enumerate permanodes: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Saw %d permanodes in corpus; want %d", len(got), len(want))
	}
	if got[0].Ref != foopn {
		t.Fatalf("Wrong permanode found in corpus. Wanted %v, got %v", foopn, got[0].Ref)
	}

	// baz undeletion. delete delbaz.
	idxd.Delete(delbaz)
	want = []blob.Ref{foopn, bazpn}
	got = got[:0]
	ch = make(chan camtypes.BlobMeta, 10)
	c.RLock()
	go func() { errc <- enumFunc(c, context.TODO(), ch) }()
	for blobMeta := range ch {
		got = append(got, blobMeta)
	}
	err = <-errc
	c.RUnlock()
	if err != nil {
		t.Fatalf("Could not enumerate permanodes: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Saw %d permanodes in corpus; want %d", len(got), len(want))
	}
	for _, bm := range got {
		found := false
		for _, perm := range want {
			if bm.Ref == perm {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("permanode %v was not found in corpus", bm.Ref)
		}
	}
}

func TestEnumerateOrder_Modtime(t *testing.T) {
	testEnumerateOrder(t,
		func(c *index.Corpus, ctx context.Context, ch chan<- camtypes.BlobMeta) error {
			return c.EnumeratePermanodesLastModifiedLocked(ctx, ch)
		},
		modtimeOrder,
	)
}

func TestEnumerateOrder_CreateTime(t *testing.T) {
	testEnumerateOrder(t,
		func(c *index.Corpus, ctx context.Context, ch chan<- camtypes.BlobMeta) error {
			return c.EnumeratePermanodesCreatedLocked(ctx, ch, true)
		},
		createOrder,
	)
}

const (
	modtimeOrder = iota
	createOrder
)

func testEnumerateOrder(t *testing.T,
	enumFunc func(*index.Corpus, context.Context, chan<- camtypes.BlobMeta) error,
	order int) {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)

	// permanode with no contents
	foopn := idxd.NewPlannedPermanode("foo")
	idxd.SetAttribute(foopn, "tag", "foo")
	// permanode with file contents
	// we set the time of the contents 1 second older than the modtime of foopn
	fooModTime := idxd.LastTime()
	fileTime := fooModTime.Add(-1 * time.Second)
	fileRef, _ := idxd.UploadFile("foo.html", "<html>I am an html file.</html>", fileTime)
	barpn := idxd.NewPlannedPermanode("bar")
	idxd.SetAttribute(barpn, "camliContent", fileRef.String())

	c, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}

	// check that we get a different order whether with enumerate according to
	// contents time, or to permanode modtime.
	var want []blob.Ref
	if order == modtimeOrder {
		// modtime.
		want = []blob.Ref{barpn, foopn}
	} else {
		// creation time.
		want = []blob.Ref{foopn, barpn}
	}
	ch := make(chan camtypes.BlobMeta, 10)
	var got []camtypes.BlobMeta
	errc := make(chan error, 1)
	c.RLock()
	go func() { errc <- enumFunc(c, context.TODO(), ch) }()
	for blobMeta := range ch {
		got = append(got, blobMeta)
	}
	err = <-errc
	c.RUnlock()
	if err != nil {
		t.Fatalf("Could not enumerate permanodes: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Saw %d permanodes in corpus; want %d", len(got), len(want))
	}
	for k, v := range got {
		if v.Ref != want[k] {
			t.Fatalf("Wrong result from enumeration. Got %v, wanted %v.", v.Ref, want[k])
		}
	}
}

// should be run with -race
func TestCacheSortedPermanodes_ModtimeRace(t *testing.T) {
	testCacheSortedPermanodesRace(t,
		func(c *index.Corpus, ctx context.Context, ch chan<- camtypes.BlobMeta) error {
			return c.EnumeratePermanodesLastModifiedLocked(ctx, ch)
		},
	)
}

// should be run with -race
func TestCacheSortedPermanodes_CreateTimeRace(t *testing.T) {
	testCacheSortedPermanodesRace(t,
		func(c *index.Corpus, ctx context.Context, ch chan<- camtypes.BlobMeta) error {
			return c.EnumeratePermanodesCreatedLocked(ctx, ch, true)
		},
	)
}

func testCacheSortedPermanodesRace(t *testing.T,
	enumFunc func(*index.Corpus, context.Context, chan<- camtypes.BlobMeta) error) {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)
	idxd.Fataler = t
	c, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	donec := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			nth := fmt.Sprintf("%d", i)
			pn := idxd.NewPlannedPermanode(nth)
			idxd.SetAttribute(pn, "tag", nth)
		}
		donec <- struct{}{}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			ch := make(chan camtypes.BlobMeta, 10)
			errc := make(chan error, 1)
			c.RLock()
			go func() { errc <- enumFunc(c, context.TODO(), ch) }()
			for _ = range ch {
			}
			err := <-errc
			c.RUnlock()
			if err != nil {
				t.Fatalf("Could not enumerate permanodes: %v", err)
			}
		}
		donec <- struct{}{}
	}()
	<-donec
	<-donec
}

func TestLazySortedPermanodes(t *testing.T) {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)
	idxd.Fataler = t
	c, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}

	lsp := c.Exp_LSPByTime(false)
	if len(lsp) != 0 {
		t.Fatal("LazySortedPermanodes cache should be empty on startup")
	}

	pn := idxd.NewPlannedPermanode("one")
	idxd.SetAttribute(pn, "tag", "one")

	enum := func(reverse bool) {
		ch := make(chan camtypes.BlobMeta, 10)
		errc := make(chan error, 1)
		c.RLock()
		go func() { errc <- c.EnumeratePermanodesCreatedLocked(context.TODO(), ch, reverse) }()
		for _ = range ch {
		}
		err := <-errc
		c.RUnlock()
		if err != nil {
			t.Fatalf("Could not enumerate permanodes: %v", err)
		}
	}
	enum(false)
	lsp = c.Exp_LSPByTime(false)
	if len(lsp) != 1 {
		t.Fatalf("LazySortedPermanodes after 1st enum: got %v items, wanted 1", len(lsp))
	}
	lsp = c.Exp_LSPByTime(true)
	if len(lsp) != 0 {
		t.Fatalf("LazySortedPermanodes reversed after 1st enum: got %v items, wanted 0", len(lsp))
	}

	enum(true)
	lsp = c.Exp_LSPByTime(false)
	if len(lsp) != 1 {
		t.Fatalf("LazySortedPermanodes after 2nd enum: got %v items, wanted 1", len(lsp))
	}
	lsp = c.Exp_LSPByTime(true)
	if len(lsp) != 1 {
		t.Fatalf("LazySortedPermanodes reversed after 2nd enum: got %v items, wanted 1", len(lsp))
	}

	pn = idxd.NewPlannedPermanode("two")
	idxd.SetAttribute(pn, "tag", "two")

	enum(true)
	lsp = c.Exp_LSPByTime(false)
	if len(lsp) != 0 {
		t.Fatalf("LazySortedPermanodes after 2nd permanode: got %v items, wanted 0 because of cache invalidation", len(lsp))
	}
	lsp = c.Exp_LSPByTime(true)
	if len(lsp) != 2 {
		t.Fatalf("LazySortedPermanodes reversed after 2nd permanode: got %v items, wanted 2", len(lsp))
	}

	pn = idxd.NewPlannedPermanode("three")
	idxd.SetAttribute(pn, "tag", "three")

	enum(false)
	lsp = c.Exp_LSPByTime(true)
	if len(lsp) != 0 {
		t.Fatalf("LazySortedPermanodes reversed after 3rd permanode: got %v items, wanted 0 because of cache invalidation", len(lsp))
	}
	lsp = c.Exp_LSPByTime(false)
	if len(lsp) != 3 {
		t.Fatalf("LazySortedPermanodes after 3rd permanode: got %v items, wanted 3", len(lsp))
	}

	enum(true)
	lsp = c.Exp_LSPByTime(false)
	if len(lsp) != 3 {
		t.Fatalf("LazySortedPermanodes after 5th enum: got %v items, wanted 3", len(lsp))
	}
	lsp = c.Exp_LSPByTime(true)
	if len(lsp) != 3 {
		t.Fatalf("LazySortedPermanodes reversed after 5th enum: got %v items, wanted 3", len(lsp))
	}
}
