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

package indextest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

// MultiClaim exercises both persistent replay and the live corpus using the
// same signed envelopes. It is reusable by all index storage implementations.
func MultiClaim(t *testing.T, initIdx func() *index.Index) {
	for _, withCorpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%v", withCorpus), func(t *testing.T) {
			ix := initIdx()
			id := NewIndexDeps(ix)
			id.Fataler = t
			var corpus *index.Corpus
			if withCorpus {
				var err error
				corpus, err = ix.KeepInMemory()
				if err != nil {
					t.Fatal(err)
				}
			}
			pn, pn2 := id.NewPermanode(), id.NewPermanode()
			target := blob.RefFromString("missing path and edge target; never fetched")
			// Attribute claims do not require their permanode to be present either.
			absentPN := blob.RefFromString("missing permanode; never fetched")
			id.SetAttribute(pn, "title", "before")
			id.SetAttribute(pn2, "title", "before2")
			children := []*schema.Builder{
				schema.NewSetAttributeClaim(blob.Ref{}, "title", "first"),
				schema.NewAddAttributeClaim(blob.Ref{}, "tag", "a"),
				schema.NewAddAttributeClaim(blob.Ref{}, "tag", "b"),
				schema.NewDelAttributeClaim(blob.Ref{}, "tag", "a"),
				schema.NewSetAttributeClaim(blob.Ref{}, "tag", "reset"),
				schema.NewAddAttributeClaim(blob.Ref{}, "tag", "c"),
				schema.NewDelAttributeClaim(blob.Ref{}, "tag", ""),
				schema.NewAddAttributeClaim(blob.Ref{}, "tag", "final-a"),
				schema.NewAddAttributeClaim(blob.Ref{}, "tag", "final-b"),
				schema.NewSetAttributeClaim(pn2, "title", "override"),
				schema.NewSetAttributeClaim(blob.Ref{}, "title", "last"),
				schema.NewSetAttributeClaim(pn, "camliRoot", "shared"),
				schema.NewSetAttributeClaim(pn2, "camliRoot", "shared"),
				schema.NewSetAttributeClaim(pn, "camliPath:same", target.String()),
				schema.NewSetAttributeClaim(pn2, "camliPath:same", target.String()),
				schema.NewDelAttributeClaim(pn, "camliPath:same", target.String()),
				schema.NewAddAttributeClaim(pn, "camliPath:same", target.String()),
				schema.NewSetAttributeClaim(pn, "camliPath:other", target.String()),
				schema.NewAddAttributeClaim(pn, "camliMember", target.String()),
				schema.NewAddAttributeClaim(pn2, "camliMember", target.String()),
				schema.NewSetAttributeClaim(absentPN, "title", "present without target"),
				schema.NewSetAttributeClaim(pn, "camliPath:removed", target.String()),
				schema.NewDelAttributeClaim(pn, "camliPath:removed", target.String()),
			}
			multi, err := schema.NewMultiClaim(children...)
			if err != nil {
				t.Fatal(err)
			}
			multi.SetMultiClaimPermanode(pn).SetClaimDate(id.advanceTime())
			when := id.LastTime()
			envelope := id.uploadAndSign(multi)
			// A later ordinary mutation forces historical queries off cached attrs.
			id.SetAttribute(pn, "description", "later")

			checkValues := func(ix *index.Index, c *index.Corpus, p blob.Ref, attr string, at time.Time, want ...string) {
				t.Helper()
				claims, err := ix.AppendClaims(ctxbg, nil, p, KeyID, attr)
				if err != nil {
					t.Fatal(err)
				}
				sort.Sort(camtypes.ClaimsByDate(claims))
				var got []string
				for _, cl := range claims {
					if !at.IsZero() && cl.Date.After(at) {
						continue
					}
					switch cl.Type {
					case string(schema.SetAttributeClaim):
						got = []string{cl.Value}
					case string(schema.AddAttributeClaim):
						got = append(got, cl.Value)
					case string(schema.DelAttributeClaim):
						if cl.Value == "" {
							got = nil
						} else {
							got = slices.DeleteFunc(got, func(v string) bool { return v == cl.Value })
						}
					}
				}
				if !slices.Equal(got, want) {
					t.Errorf("replay %v %s at %v = %q; want %q", p, attr, at, got, want)
				}
				if c == nil {
					return
				}
				for _, signer := range []string{"", KeyID} {
					if got := c.AppendPermanodeAttrValues(nil, p, attr, at, signer); !slices.Equal(got, want) {
						t.Errorf("corpus %v %s at %v signer %q = %q; want %q", p, attr, at, signer, got, want)
					}
					first := ""
					if len(want) > 0 {
						first = want[0]
					}
					if got := c.PermanodeAttrValue(p, attr, at, signer); got != first {
						t.Errorf("corpus single %s = %q; want %q", attr, got, first)
					}
				}
				for _, value := range []string{"a", "reset", "final-a", "last", "before"} {
					if got, w := c.PermanodeHasAttrValue(p, at, attr, value), slices.Contains(want, value); got != w {
						t.Errorf("corpus has %s=%q: %v; want %v", attr, value, got, w)
					}
				}
			}
			checkLive := func(ix *index.Index, c *index.Corpus) {
				checkValues(ix, c, pn, "title", time.Time{}, "last")
				checkValues(ix, c, pn, "title", when, "last")
				checkValues(ix, c, pn, "title", when.Add(-time.Nanosecond), "before")
				checkValues(ix, c, pn, "tag", time.Time{}, "final-a", "final-b")
				checkValues(ix, c, pn, "tag", when, "final-a", "final-b")
				checkValues(ix, c, pn2, "title", time.Time{}, "override")
				checkValues(ix, c, absentPN, "title", time.Time{}, "present without target")
			}
			checkLive(ix, corpus)
			// Returned operations expose only the enclosing reference, retaining their
			// zero-based positions for deterministic internal replay.
			claims, err := ix.AppendClaims(ctxbg, nil, pn, KeyID, "")
			if err != nil {
				t.Fatal(err)
			}
			seen := map[int]bool{}
			for _, cl := range claims {
				if cl.BlobRef == envelope {
					if !cl.Date.Equal(when) || cl.Signer != id.SignerBlobRef {
						t.Errorf("child lost envelope date/signer: %+v", cl)
					}
					if seen[cl.MultiIndex] {
						t.Errorf("duplicate child identity %d", cl.MultiIndex)
					}
					seen[cl.MultiIndex] = true
					data, _ := json.Marshal(cl)
					if bytes.Contains(data, []byte("MultiIndex")) {
						t.Errorf("child ordinal leaked: %s", data)
					}
				}
			}
			if len(seen) != 18 {
				t.Errorf("indexed %d children on default PN; want 18", len(seen))
			}

			// All cross-PN secondary indexes must keep rows that previously collided.
			out := make(chan blob.Ref, 10)
			err = ix.SearchPermanodesWithAttr(ctxbg, out, &camtypes.PermanodeByAttrRequest{Signer: id.SignerBlobRef, Attribute: "camliRoot", Query: "shared", MaxResults: 10})
			if err != nil {
				t.Fatal(err)
			}
			found := map[blob.Ref]bool{}
			for p := range out {
				found[p] = true
			}
			if len(found) != 2 || !found[pn] || !found[pn2] {
				t.Errorf("shared signer-attr-value results = %v", found)
			}
			recent := make(chan camtypes.RecentPermanode, 10)
			if err := ix.GetRecentPermanodes(ctxbg, recent, id.SignerBlobRef, 10, time.Time{}); err != nil {
				t.Fatal(err)
			}
			found = map[blob.Ref]bool{}
			for p := range recent {
				found[p.Permanode] = true
			}
			if !found[pn] || !found[pn2] || !found[absentPN] {
				t.Errorf("recent lost cross-PN child: %v", found)
			}
			paths, err := ix.PathsOfSignerTarget(ctxbg, id.SignerBlobRef, target)
			if err != nil {
				t.Fatal(err)
			}
			if len(paths) != 3 {
				t.Errorf("backward paths = %v; want 3", paths)
			}
			forward, err := ix.PathsLookup(ctxbg, id.SignerBlobRef, pn, "same")
			if err != nil {
				t.Fatal(err)
			}
			if len(forward) != 3 {
				t.Errorf("forward paths = %v; want 3 distinct child rows", forward)
			}
			for _, p := range paths {
				if p.Claim != envelope {
					t.Errorf("path reference = %v; want envelope %v", p.Claim, envelope)
				}
			}
			edges, err := ix.EdgesTo(target, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(edges) != 2 {
				t.Errorf("cross-PN edges = %v; want 2", edges)
			}

			// Delete the entire envelope, including every child on every PN. Deleting
			// that deletion restores all children, including cached/historical attrs.
			deletion := id.Delete(envelope)
			checkValues(ix, corpus, pn, "title", time.Time{}, "before")
			checkValues(ix, corpus, pn, "title", when, "before")
			checkValues(ix, corpus, pn, "tag", time.Time{})
			checkValues(ix, corpus, pn, "tag", when)
			checkValues(ix, corpus, pn2, "title", time.Time{}, "before2")
			checkValues(ix, corpus, absentPN, "title", time.Time{})
			paths, err = ix.PathsOfSignerTarget(ctxbg, id.SignerBlobRef, target)
			if err != nil || len(paths) != 0 {
				t.Errorf("deleted paths = %v, %v", paths, err)
			}
			out = make(chan blob.Ref, 10)
			err = ix.SearchPermanodesWithAttr(ctxbg, out, &camtypes.PermanodeByAttrRequest{Signer: id.SignerBlobRef, Attribute: "camliRoot", Query: "shared", MaxResults: 10})
			if err != nil {
				t.Fatal(err)
			}
			for p := range out {
				t.Errorf("deleted signer attr still found %v", p)
			}
			if p, err := ix.PermanodeOfSignerAttrValue(ctxbg, id.SignerBlobRef, "camliRoot", "shared"); err == nil {
				t.Errorf("deleted unique attr lookup = %v", p)
			}
			// Loading a new corpus must rebuild attrs with deletions already known.
			replay, err := index.New(ix.Storage())
			if err != nil {
				t.Fatal(err)
			}
			reloaded, err := replay.KeepInMemory()
			if err != nil {
				t.Fatal(err)
			}
			checkValues(replay, reloaded, pn, "title", time.Time{}, "before")
			checkValues(replay, reloaded, pn, "tag", when)
			// Continue on the cold index to verify its caches also revive children.
			replay.KeyFetcher = id.PublicKeyFetcher
			replay.InitBlobSource(id.BlobSource)
			id.Index = replay
			id.Delete(deletion)
			checkLive(replay, reloaded)

			// Rebuild a fresh index from the original blob source, not existing rows.
			// This also exercises envelope/deletion arrival in arbitrary blobref order.
			rebuilt := initIdx()
			rebuilt.KeyFetcher = id.PublicKeyFetcher
			rebuilt.InitBlobSource(id.BlobSource)
			if err := rebuilt.Reindex(); err != nil {
				t.Fatal(err)
			}
			checkLive(rebuilt, nil)
			rebuiltCorpus, err := rebuilt.KeepInMemory()
			if err != nil {
				t.Fatal(err)
			}
			checkLive(rebuilt, rebuiltCorpus)
			multiMalformed(t, id, pn, pn2)
			multiSameDateOrder(t, id, reloaded)
		})
	}
}

// Sign raw envelopes so malformed children bypass builder-side validation.
func multiMalformed(t *testing.T, id *IndexDeps, pn, pn2 blob.Ref) {
	t.Helper()
	good := map[string]any{"claimType": "set-attribute", "attribute": "title", "value": "must not apply"}
	invalid := []any{
		nil, "not an object", map[string]any{"claimType": "multi"},
		map[string]any{"claimType": "delete", "target": pn.String()},
		map[string]any{"claimType": "share"},
		map[string]any{"claimType": "set-attribute", "attribute": "title", "value": 42},
		map[string]any{"claimType": "set-attribute", "attribute": "title", "value": "bad", "permaNode": "invalid"},
	}
	for _, field := range []string{"claimDate", "camliSigner", "camliSig"} {
		invalid = append(invalid, map[string]any{"claimType": "set-attribute", "attribute": "title", "value": "bad", field: ""})
	}
	for i, bad := range invalid {
		t.Run(fmt.Sprintf("malformed-%d", i), func(t *testing.T) {
			before, err := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "")
			if err != nil {
				t.Fatal(err)
			}
			signRawMulti(t, id, pn, []any{good, bad, map[string]any{"claimType": "set-attribute", "permaNode": pn2.String(), "attribute": "title", "value": "must not apply either"}})
			after, err := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != len(after) {
				t.Errorf("malformed envelope partially applied: %d claims -> %d", len(before), len(after))
			}
			after, err = id.Index.AppendClaims(ctxbg, nil, pn2, KeyID, "title")
			if err != nil {
				t.Fatal(err)
			}
			for _, cl := range after {
				if cl.Value == "must not apply either" {
					t.Errorf("malformed envelope applied across PN: %+v", cl)
				}
			}
		})
	}
	before, err := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "")
	if err != nil {
		t.Fatal(err)
	}
	signRawMulti(t, id, blob.Ref{}, []any{
		map[string]any{"claimType": "set-attribute", "permaNode": pn.String(), "attribute": "title", "value": "must not apply"},
		good, // No default and no child permanode: invalidates the first child too.
	})
	after, err := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Error("missing child permanode applied earlier mutations")
	}
	// Explicitly empty child PN inherits the parent just like an omitted field.
	ref := signRawMulti(t, id, pn, []any{map[string]any{"claimType": "set-attribute", "permaNode": "", "attribute": "description", "value": "inherited empty"}})
	claims, err := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "description")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cl := range claims {
		if cl.BlobRef == ref {
			found = cl.Value == "inherited empty"
		}
	}
	if !found {
		t.Error("explicit empty child permanode did not inherit parent")
	}
	// Empty arrays are harmless; a missing/incorrect array is malformed.
	for _, children := range []any{[]any{}, nil, "wrong", map[string]any{}} {
		before, _ := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "")
		signRawMulti(t, id, pn, children)
		after, _ := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "")
		if len(before) != len(after) {
			t.Errorf("empty/malformed array changed claim count")
		}
	}
}

func signRawMulti(t *testing.T, id *IndexDeps, pn blob.Ref, children any) blob.Ref {
	t.Helper()
	m := map[string]any{"camliVersion": 1, "camliType": "claim", "claimType": "multi", "camliSigner": id.SignerBlobRef.String(), "claimDate": id.advanceTime().Format(time.RFC3339Nano), "permaNode": pn.String(), "claims": children}
	if !pn.Valid() {
		delete(m, "permaNode")
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sr := &jsonsign.SignRequest{UnsignedJSON: string(data), Fetcher: id.PublicKeyFetcher, EntityFetcher: id.EntityFetcher, SignatureTime: id.now}
	signed, err := sr.Sign(ctxbg)
	if err != nil {
		t.Fatal(err)
	}
	return id.Upload(&test.Blob{Contents: signed})
}

// Receive the greater envelope first so the later-arriving batch belongs before
// existing history. Both whole-envelope and child order must survive live replay.
func multiSameDateOrder(t *testing.T, id *IndexDeps, corpus *index.Corpus) {
	t.Helper()
	pn := id.NewPermanode()
	date := id.advanceTime()
	firstTarget := blob.RefFromString("first path target")
	lastTarget := blob.RefFromString("last path target")
	makeEnvelope := func(prefix string) *test.Blob {
		children := []*schema.Builder{
			schema.NewAddAttributeClaim(pn, "tag", prefix+"0"),
			schema.NewAddAttributeClaim(pn, "tag", prefix+"1"),
			schema.NewSetAttributeClaim(pn, "camliPath:choice", firstTarget.String()),
		}
		for len(children) < 11 {
			children = append(children, schema.NewSetAttributeClaim(pn, "title", prefix))
		}
		children = append(children, schema.NewSetAttributeClaim(pn, "camliPath:choice", lastTarget.String()))
		multi, err := schema.NewMultiClaim(children...)
		if err != nil {
			t.Fatal(err)
		}
		multi.SetClaimDate(date)
		return id.Sign(multi)
	}
	a, b := makeEnvelope("a"), makeEnvelope("b")
	low, high := a, b
	want := []string{"a0", "a1", "b0", "b1"}
	if b.BlobRef().Less(a.BlobRef()) {
		low, high = b, a
		want = []string{"b0", "b1", "a0", "a1"}
	}
	id.Upload(high)
	id.Upload(low)
	claims, err := id.Index.AppendClaims(ctxbg, nil, pn, KeyID, "tag")
	if err != nil {
		t.Fatal(err)
	}
	sort.Sort(camtypes.ClaimsByDate(claims))
	var got []string
	for _, cl := range claims {
		got = append(got, cl.Value)
	}
	if !slices.Equal(got, want) {
		t.Errorf("equal-date envelope replay = %q; want %q", got, want)
	}
	if corpus != nil {
		if got := corpus.AppendPermanodeAttrValues(nil, pn, "tag", date, KeyID); !slices.Equal(got, want) {
			t.Errorf("equal-date live corpus replay = %q; want %q", got, want)
		}
	}
	path, err := id.Index.PathLookup(ctxbg, id.SignerBlobRef, pn, "choice", date)
	if err != nil || path.Target != lastTarget || path.Claim != high.BlobRef() {
		t.Errorf("path replay across equal-date envelopes/child index 11 = %v, %v", path, err)
	}
}
