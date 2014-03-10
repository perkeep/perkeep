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
	"reflect"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
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

func TestDeletePermanode(t *testing.T) {
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
	go func() { errc <- c.EnumeratePermanodesLastModifiedLocked(context.TODO(), ch) }()
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
	go func() { errc <- c.EnumeratePermanodesLastModifiedLocked(context.TODO(), ch) }()
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
	go func() { errc <- c.EnumeratePermanodesLastModifiedLocked(context.TODO(), ch) }()
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
