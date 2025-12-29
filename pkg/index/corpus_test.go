/*
Copyright 2013 The Perkeep Authors

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
	"slices"
	"testing"
	"time"

	"go4.org/types"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/types/camtypes"
)

func newTestCorpusWithPermanode(t *testing.T) (c *index.Corpus, pn blob.Ref, keyID1, keyID2 string) {
	c = index.ExpNewCorpus()
	pn = blob.MustParse("abc-123")
	sig1 := indextest.PubKey.BlobRef()
	keyID2 = "abc-789"
	sig2 := blob.MustParse(keyID2)
	if err := c.Exp_AddKeyID(sig1, indextest.KeyID); err != nil {
		t.Fatal(err)
	}
	if err := c.Exp_AddKeyID(sig2, keyID2); err != nil {
		t.Fatal(err)
	}
	tm := time.Unix(99, 0)
	claim := func(verb, attr, val string, sig blob.Ref) *camtypes.Claim {
		tm = tm.Add(time.Second)
		return &camtypes.Claim{
			Type:   verb + "-attribute",
			Attr:   attr,
			Value:  val,
			Date:   tm,
			Signer: sig,
		}
	}

	c.SetClaims(pn, []*camtypes.Claim{
		claim("set", "foo", "foov", sig1), // time 100

		claim("add", "tag", "a", sig1), // time 101
		claim("add", "tag", "b", sig1), // time 102
		claim("del", "tag", "", sig1),
		claim("add", "tag", "c", sig1),
		claim("add", "tag", "d", sig2),
		claim("add", "tag", "e", sig1),
		claim("del", "tag", "d", sig2),
		claim("add", "tag", "f", sig2),

		claim("add", "DelAll", "a", sig1),
		claim("add", "DelAll", "b", sig1),
		claim("add", "DelAll", "c", sig2),
		claim("del", "DelAll", "", sig1),

		claim("add", "DelOne", "a", sig1),
		claim("add", "DelOne", "b", sig1),
		claim("add", "DelOne", "c", sig1),
		claim("add", "DelOne", "d", sig2),
		claim("add", "DelOne", "e", sig2),
		claim("del", "DelOne", "d", sig2),
		claim("del", "DelOne", "a", sig1),

		claim("add", "SetAfterAdd", "a", sig1),
		claim("add", "SetAfterAdd", "b", sig1),
		claim("add", "SetAfterAdd", "c", sig2),
		claim("set", "SetAfterAdd", "setv", sig1),

		// The claims below help testing
		// slow and fast path equivalence
		// (lookups based on pm.Claims and cached attrs).
		//
		// Permanode attr queries at time.Time{} and
		// time.Unix(200, 0) should yield the same results
		// for the above claims. The difference is that
		// they use the cache at time.Time{} and
		// the pm.Claims directly (bypassing the cache)
		// at time.Unix(200, 0).
		{
			Type:   "set-attribute",
			Attr:   "CacheTest",
			Value:  "foo",
			Date:   time.Unix(201, 0),
			Signer: sig1,
		},
		{
			Type:   "set-attribute",
			Attr:   "CacheTest",
			Value:  "foo",
			Date:   time.Unix(202, 0),
			Signer: sig2,
		},
	})

	return c, pn, indextest.KeyID, keyID2
}

func TestCorpusAppendPermanodeAttrValues(t *testing.T) {
	c, pn, sig1, sig2 := newTestCorpusWithPermanode(t)
	s := func(s ...string) []string { return s }

	sigMissing := "xyz-123"

	tests := []struct {
		attr string
		want []string
		t    time.Time
		sig  string
	}{
		{attr: "not-exist", want: s()},
		{attr: "DelAll", want: s()},
		{attr: "DelOne", want: s("b", "c", "e")},
		{attr: "foo", want: s("foov")},
		{attr: "tag", want: s("c", "e", "f")},
		{attr: "tag", want: s("a", "b"), t: time.Unix(102, 0)},
		{attr: "SetAfterAdd", want: s("setv")},
		// sig1
		{attr: "not-exist", want: s(), sig: sig1},
		{attr: "DelAll", want: s(), sig: sig1},
		{attr: "DelOne", want: s("b", "c"), sig: sig1},
		{attr: "foo", want: s("foov"), sig: sig1},
		{attr: "tag", want: s("c", "e"), sig: sig1},
		{attr: "tag", want: s("a", "b"), t: time.Unix(102, 0), sig: sig1},
		{attr: "SetAfterAdd", want: s("setv"), sig: sig1},
		// sig2
		{attr: "DelAll", want: s("c"), sig: sig2},
		{attr: "DelOne", want: s("e"), sig: sig2},
		{attr: "tag", want: s("d"), t: time.Unix(105, 0), sig: sig2},
		{attr: "SetAfterAdd", want: s("c"), sig: sig2},
		// sigMissing (not present in pn)
		{attr: "tag", want: s(), sig: sigMissing},
	}
	for i, tt := range tests {
		got := c.AppendPermanodeAttrValues(nil, pn, tt.attr, tt.t, tt.sig)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. attr %q = %q; want %q",
				i, tt.attr, got, tt.want)
		}

		if !tt.t.IsZero() {
			// skip equivalence test if specific time was given
			continue
		}
		got = c.AppendPermanodeAttrValues(nil, pn, tt.attr, time.Unix(200, 0), tt.sig)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. attr %q = %q; want %q",
				i, tt.attr, got, tt.want)
		}
	}
}

func TestCorpusPermanodeAttrValue(t *testing.T) {
	c, pn, sig1, sig2 := newTestCorpusWithPermanode(t)

	tests := []struct {
		attr string
		want string
		t    time.Time
		sig  string
	}{
		{attr: "not-exist", want: ""},
		{attr: "DelAll", want: ""},
		{attr: "DelOne", want: "b"},
		{attr: "foo", want: "foov"},
		{attr: "tag", want: "c"},
		{attr: "tag", want: "a", t: time.Unix(102, 0)},
		{attr: "SetAfterAdd", want: "setv"},
		// sig1
		{attr: "not-exist", want: "", sig: sig1},
		{attr: "DelAll", want: "", sig: sig1},
		{attr: "DelOne", want: "b", sig: sig1},
		{attr: "foo", want: "foov", sig: sig1},
		{attr: "tag", want: "c", sig: sig1},
		{attr: "SetAfterAdd", want: "setv", sig: sig1},
		// sig2
		{attr: "DelAll", want: "c", sig: sig2},
		{attr: "DelOne", want: "e", sig: sig2},
		{attr: "foo", want: "", sig: sig2},
		{attr: "tag", want: "f", sig: sig2},
		{attr: "SetAfterAdd", want: "c", sig: sig2},
	}
	for i, tt := range tests {
		got := c.PermanodeAttrValue(pn, tt.attr, tt.t, tt.sig)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. attr %q = %q; want %q",
				i, tt.attr, got, tt.want)
		}

		if !tt.t.IsZero() {
			// skip equivalence test if specific time was given
			continue
		}
		got = c.PermanodeAttrValue(pn, tt.attr, time.Unix(200, 0), tt.sig)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. attr %q = %q; want %q",
				i, tt.attr, got, tt.want)
		}
	}
}

func TestCorpusPermanodeHasAttrValue(t *testing.T) {
	c, pn, _, _ := newTestCorpusWithPermanode(t)

	tests := []struct {
		attr string
		val  string
		want bool
		t    time.Time
	}{
		{attr: "DelAll", val: "a", want: false},
		{attr: "DelOne", val: "b", want: true},
		{attr: "DelOne", val: "a", want: false},
		{attr: "foo", val: "foov", want: true},
		{attr: "tag", val: "c", want: true},
		{attr: "tag", val: "a", want: true, t: time.Unix(102, 0)},
		{attr: "tag", val: "c", want: false, t: time.Unix(102, 0)},
		{attr: "SetAfterAdd", val: "setv", want: true},
		{attr: "SetAfterAdd", val: "a", want: false},
	}
	for _, tt := range tests {
		got := c.PermanodeHasAttrValue(pn, tt.t, tt.attr, tt.val)
		if got != tt.want {
			t.Errorf("attr %q, val %q = %v; want %v",
				tt.attr, tt.val, got, tt.want)
		}

		if !tt.t.IsZero() {
			// skip equivalence test if specific time was given
			continue
		}
		got = c.PermanodeHasAttrValue(pn, time.Unix(200, 0), tt.attr, tt.val)
		if got != tt.want {
			t.Errorf("attr %q, val %q = %v; want %v",
				tt.attr, tt.val, got, tt.want)
		}
	}
}

func TestKVClaimAllocs(t *testing.T) {
	n := testing.AllocsPerRun(20, func() {
		index.ExpKvClaim("claim|sha224-d159f351eca8c09dcf649aae43bbf92d99293242d520a9c0b41070ca|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-39020aa3cc25f74ea6e75793203ef31143dfe3604fb80af3d3816c5c",
			"set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
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
			k:  "claim|sha224-d159f351eca8c09dcf649aae43bbf92d99293242d520a9c0b41070ca|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-39020aa3cc25f74ea6e75793203ef31143dfe3604fb80af3d3816c5c",
			v:  "set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd",
			ok: true,
			want: camtypes.Claim{
				BlobRef:   blob.MustParse("sha224-39020aa3cc25f74ea6e75793203ef31143dfe3604fb80af3d3816c5c"),
				Signer:    blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
				Permanode: blob.MustParse("sha224-d159f351eca8c09dcf649aae43bbf92d99293242d520a9c0b41070ca"),
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
		func(c *index.Corpus, fn func(m camtypes.BlobMeta) bool) error {
			c.EnumeratePermanodesLastModified(fn)
			return nil
		},
	)
}

func TestDeletePermanode_CreateTime(t *testing.T) {
	testDeletePermanodes(t,
		func(c *index.Corpus, fn func(m camtypes.BlobMeta) bool) error {
			c.EnumeratePermanodesCreated(fn, true)
			return nil
		},
	)
}

func testDeletePermanodes(t *testing.T,
	enumFunc func(*index.Corpus, func(m camtypes.BlobMeta) bool) error) {

	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)
	idxd.Fataler = t
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
	var got []camtypes.BlobMeta
	enumFunc(c, func(m camtypes.BlobMeta) bool {
		got = append(got, m)
		return true
	})
	if len(got) != len(want) {
		t.Fatalf("Saw %d permanodes in corpus; want %d", len(got), len(want))
	}
	for _, bm := range got {
		found := slices.Contains(want, bm.Ref)
		if !found {
			t.Fatalf("permanode %v was not found in corpus", bm.Ref)
		}
	}

	// now add a delete claim for permanode baz, and check that we're only left with foo permanode
	delbaz := idxd.Delete(bazpn)
	want = []blob.Ref{foopn}
	got = got[:0]
	enumFunc(c, func(m camtypes.BlobMeta) bool {
		got = append(got, m)
		return true
	})
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
	enumFunc(c, func(m camtypes.BlobMeta) bool {
		got = append(got, m)
		return true
	})
	if len(got) != len(want) {
		t.Fatalf("Saw %d permanodes in corpus; want %d", len(got), len(want))
	}
	for _, bm := range got {
		found := slices.Contains(want, bm.Ref)
		if !found {
			t.Fatalf("permanode %v was not found in corpus", bm.Ref)
		}
	}
}

func TestEnumerateOrder_Modtime(t *testing.T) {
	testEnumerateOrder(t,
		func(c *index.Corpus, fn func(m camtypes.BlobMeta) bool) error {
			c.EnumeratePermanodesLastModified(fn)
			return nil
		},
		modtimeOrder,
	)
}

func TestEnumerateOrder_CreateTime(t *testing.T) {
	testEnumerateOrder(t,
		func(c *index.Corpus, fn func(m camtypes.BlobMeta) bool) error {
			c.EnumeratePermanodesCreated(fn, true)
			return nil
		},
		createOrder,
	)
}

const (
	modtimeOrder = iota
	createOrder
)

func testEnumerateOrder(t *testing.T,
	enumFunc func(*index.Corpus, func(m camtypes.BlobMeta) bool) error,
	order int) {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)
	idxd.Fataler = t

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
	var got []camtypes.BlobMeta
	enumFunc(c, func(m camtypes.BlobMeta) bool {
		got = append(got, m)
		return true
	})
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
		func(c *index.Corpus, fn func(m camtypes.BlobMeta) bool) error {
			c.EnumeratePermanodesLastModified(fn)
			return nil
		},
	)
}

// should be run with -race
func TestCacheSortedPermanodes_CreateTimeRace(t *testing.T) {
	testCacheSortedPermanodesRace(t,
		func(c *index.Corpus, fn func(m camtypes.BlobMeta) bool) error {
			c.EnumeratePermanodesCreated(fn, true)
			return nil
		},
	)
}

// TODO(mpl): see later if we can delete. or if we have to fix it further.
func testCacheSortedPermanodesRace(t *testing.T,
	enumFunc func(*index.Corpus, func(m camtypes.BlobMeta) bool) error) {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)
	idxd.Fataler = t

	c, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	donec := make(chan struct{})
	go func() {
		for i := range 100 {
			nth := fmt.Sprintf("%d", i)
			// No need to lock the index here. It is already done within NewPlannedPermanode,
			// because it calls idxd.Index.ReceiveBlob.
			pn := idxd.NewPlannedPermanode(nth)
			idxd.SetAttribute(pn, "tag", nth)
		}
		donec <- struct{}{}
	}()
	go func() {
		for range 10 {
			idx.RLock()
			enumFunc(c, func(m camtypes.BlobMeta) bool {
				return true
			})
			idx.RUnlock()
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
		c.EnumeratePermanodesCreated(func(m camtypes.BlobMeta) bool { return true }, reverse)
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
