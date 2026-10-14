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

package search_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

// Unlike the general query test harness, leave storage accessible after
// constructing a scanned corpus: these tests continue receiving claims.
func testMultiClaimSearch(t *testing.T, fn func(*testing.T, *queryTest)) {
	t.Helper()
	for _, itype := range allIndexTypes {
		if *queryType != "" && *queryType != itype.String() {
			continue
		}
		t.Run(itype.String(), func(t *testing.T) {
			testQueryType(t, func(qt *queryTest) {
				if itype == indexCorpusScan {
					qt.newHandler = func() *search.Handler {
						corpus, err := qt.id.Index.KeepInMemory()
						if err != nil {
							t.Fatal(err)
						}
						h := search.NewHandler(qt.id.Index, owner)
						h.SetCorpus(corpus)
						return h
					}
				}
				fn(t, qt)
			}, itype)
		})
	}
}

func multiClaimBuilder(t *testing.T, id *indextest.IndexDeps, defaultPN blob.Ref, claims ...*schema.Builder) *schema.Builder {
	t.Helper()
	m, err := schema.NewMultiClaim(claims...)
	if err != nil {
		t.Fatal(err)
	}
	if defaultPN.Valid() {
		m.SetMultiClaimPermanode(defaultPN)
	}
	// Keep the batch after existing ordinary claims, but before the next
	// mutation made through IndexDeps (which advances its clock a second).
	m.SetClaimDate(id.LastTime().Add(time.Nanosecond))
	return m
}

func multiClaimQuery(attr, value string) *search.SearchQuery {
	return &search.SearchQuery{
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{Attr: attr, Value: value},
		},
		Sort: search.Unsorted,
	}
}

func multiClaimDescribe(h *search.Handler, pns ...blob.Ref) (*search.DescribeResponse, error) {
	return h.Describe(ctxbg, &search.DescribeRequest{BlobRefs: pns, Depth: 1})
}

func multiClaimWantAttrs(t *testing.T, res *search.DescribeResponse, pn blob.Ref, want url.Values) {
	t.Helper()
	db := res.Meta.Get(pn)
	if db == nil || db.Permanode == nil {
		t.Fatalf("Describe(%v) missing permanode: %+v", pn, db)
	}
	if got := db.Permanode.Attr; !reflect.DeepEqual(got, want) {
		t.Errorf("Describe(%v) attributes = %v; want %v", pn, got, want)
	}
}

func TestMultiClaimGetClaims(t *testing.T) {
	testMultiClaimSearch(t, func(t *testing.T, qt *queryTest) {
		id := qt.id
		p1 := id.NewPermanode()
		p2 := id.NewPermanode()
		// Interleaving attributes and permanodes leaves gaps in the array
		// positions of both unfiltered and filtered claim lists.
		children := []*schema.Builder{
			schema.NewSetAttributeClaim(blob.Ref{}, "title", "first"),
			schema.NewSetAttributeClaim(p2, "title", "other"),
		}
		for i := 0; i < 20; i++ {
			children = append(children,
				schema.NewAddAttributeClaim(blob.Ref{}, "tag", fmt.Sprintf("tag-%02d", i)),
				schema.NewAddAttributeClaim(p2, "tag", fmt.Sprintf("other-%02d", i)),
			)
		}
		children = append(children,
			schema.NewDelAttributeClaim(blob.Ref{}, "tag", "tag-00"),
			schema.NewSetAttributeClaim(blob.Ref{}, "title", "last"),
			schema.NewDelAttributeClaim(p2, "title", ""),
		)
		m := multiClaimBuilder(t, id, p1, children...)
		envelope := id.Upload(id.Sign(m))
		date, err := m.Blob().ClaimDate()
		if err != nil {
			t.Fatal(err)
		}

		for _, pn := range []blob.Ref{p1, p2} {
			for _, attr := range []string{"", "tag", "title", "absent"} {
				t.Run(pn.String()+"/"+attr, func(t *testing.T) {
					res, err := qt.Handler().GetClaims(&search.ClaimsRequest{Permanode: pn, AttrFilter: attr})
					if err != nil {
						t.Fatal(err)
					}
					var want []map[string]any
					for _, child := range children {
						cl := parseJSON(mustMultiClaimJSON(t, child))
						target, _ := cl["permaNode"].(string)
						if target == "" {
							target = p1.String()
						}
						if target != pn.String() || attr != "" && cl["attribute"] != attr {
							continue
						}
						item := map[string]any{
							"blobref":   envelope.String(),
							"signer":    id.SignerBlobRef.String(),
							"permanode": pn.String(),
							"date":      date.Format(time.RFC3339Nano),
							"type":      cl["claimType"],
							"attr":      cl["attribute"],
						}
						if value, _ := cl["value"].(string); value != "" {
							item["value"] = value
						}
						want = append(want, item)
					}
					// Compare the actual JSON shape, not ClaimsItem structs:
					// internal child indices must never become API fields.
					raw, err := json.Marshal(res)
					if err != nil {
						t.Fatal(err)
					}
					var got struct {
						Claims []map[string]any `json:"claims"`
					}
					if err := json.Unmarshal(raw, &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got.Claims, want) {
						t.Errorf("GetClaims(%v, %q) JSON = %s; want claims %v", pn, attr, raw, want)
					}
				})
			}
		}
	})
}

func mustMultiClaimJSON(t *testing.T, m *schema.Builder) string {
	t.Helper()
	s, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMultiClaimDescribeQueryDelete(t *testing.T) {
	testMultiClaimSearch(t, func(t *testing.T, qt *queryTest) {
		id := qt.id
		p1, p2 := id.NewPermanode(), id.NewPermanode()
		id.SetAttribute(p1, "title", "original-one")
		id.SetAttribute(p2, "title", "original-two")
		id.AddAttribute(p1, "tag", "original")
		id.AddAttribute(p2, "tag", "original")
		m := multiClaimBuilder(t, id, p1,
			schema.NewAddAttributeClaim(blob.Ref{}, "tag", "discard"),
			schema.NewSetAttributeClaim(p2, "title", "temporary"),
			schema.NewSetAttributeClaim(blob.Ref{}, "tag", "shared"),
			schema.NewAddAttributeClaim(p2, "tag", "shared"),
			schema.NewSetAttributeClaim(blob.Ref{}, "title", "final-one"),
			schema.NewDelAttributeClaim(p2, "tag", "original"),
			schema.NewAddAttributeClaim(blob.Ref{}, "tag", "keep"),
			schema.NewAddAttributeClaim(blob.Ref{}, "tag", "keep"),
			schema.NewDelAttributeClaim(blob.Ref{}, "tag", "keep"),
			schema.NewSetAttributeClaim(p2, "title", "final-two"),
			schema.NewAddAttributeClaim(blob.Ref{}, "gone", "value"),
			schema.NewDelAttributeClaim(blob.Ref{}, "gone", ""),
		)
		envelope := id.Upload(id.Sign(m))
		h := qt.Handler()
		check := func(applied bool) {
			t.Helper()
			res, err := multiClaimDescribe(h, p1, p2)
			if err != nil {
				t.Fatal(err)
			}
			if applied {
				multiClaimWantAttrs(t, res, p1, url.Values{"title": {"final-one"}, "tag": {"shared"}})
				multiClaimWantAttrs(t, res, p2, url.Values{"title": {"final-two"}, "tag": {"shared"}})
				qt.wantRes(multiClaimQuery("tag", "shared"), p1, p2)
				qt.wantRes(multiClaimQuery("title", "final-one"), p1)
				qt.wantRes(multiClaimQuery("title", "final-two"), p2)
				qt.wantRes(multiClaimQuery("tag", "original"))
			} else {
				multiClaimWantAttrs(t, res, p1, url.Values{"title": {"original-one"}, "tag": {"original"}})
				multiClaimWantAttrs(t, res, p2, url.Values{"title": {"original-two"}, "tag": {"original"}})
				qt.wantRes(multiClaimQuery("tag", "shared"))
				qt.wantRes(multiClaimQuery("tag", "original"), p1, p2)
				for _, pn := range []blob.Ref{p1, p2} {
					claims, err := h.GetClaims(&search.ClaimsRequest{Permanode: pn})
					if err != nil {
						t.Fatal(err)
					}
					for _, cl := range claims.Claims {
						if cl.BlobRef == envelope {
							t.Errorf("deleted multi envelope still in GetClaims(%v)", pn)
						}
					}
				}
			}
		}
		check(true)
		deletion := id.Delete(envelope)
		check(false)
		id.Delete(deletion)
		check(true)
	})
}

func TestMultiClaimMissingSigner(t *testing.T) {
	testMultiClaimSearch(t, func(t *testing.T, qt *queryTest) {
		id := qt.id
		p1, p2 := id.NewPermanode(), id.NewPermanode()
		// IndexDeps normally supplies a separate signing-key fetcher.
		// Signing still works, but verification cannot fetch that key
		// until it is uploaded to the ordinary blob source below.
		id.Index.KeyFetcher = id.BlobSource
		// A handler's websocket goroutines run indefinitely. Construct it
		// outside the bubble, which must drain all its own goroutines.
		h := qt.Handler()
		synctest.Test(t, func(t *testing.T) {
			m := multiClaimBuilder(t, id, p1,
				schema.NewSetAttributeClaim(blob.Ref{}, "tag", "shared"),
				schema.NewSetAttributeClaim(p2, "tag", "shared"),
			)
			envelope := id.Upload(id.Sign(m))
			synctest.Wait()
			res, err := multiClaimDescribe(h, p1, p2)
			if err != nil {
				t.Fatal(err)
			}
			for _, pn := range []blob.Ref{p1, p2} {
				multiClaimWantAttrs(t, res, pn, url.Values{})
				claims, err := h.GetClaims(&search.ClaimsRequest{Permanode: pn})
				if err != nil {
					t.Fatal(err)
				}
				if len(claims.Claims) != 0 {
					t.Errorf("GetClaims(%v) exposed children before signer arrived: %+v", pn, claims.Claims)
				}
			}
			qt.wantRes(multiClaimQuery("tag", "shared"))

			id.Upload(indextest.PubKey)
			synctest.Wait()
			res, err = multiClaimDescribe(h, p1, p2)
			if err != nil {
				t.Fatal(err)
			}
			for _, pn := range []blob.Ref{p1, p2} {
				multiClaimWantAttrs(t, res, pn, url.Values{"tag": {"shared"}})
				claims, err := h.GetClaims(&search.ClaimsRequest{Permanode: pn})
				if err != nil {
					t.Fatal(err)
				}
				if len(claims.Claims) != 1 || claims.Claims[0].BlobRef != envelope {
					t.Errorf("GetClaims(%v) after signer arrival = %+v; want one child of %v", pn, claims.Claims, envelope)
				}
			}
			qt.wantRes(multiClaimQuery("tag", "shared"), p1, p2)
		})
	})
}

func TestMultiClaimInvalidEnvelope(t *testing.T) {
	testMultiClaimSearch(t, func(t *testing.T, qt *queryTest) {
		id := qt.id
		p1, p2 := id.NewPermanode(), id.NewPermanode()
		h := qt.Handler()
		type invalidChild struct {
			name  string
			child any
		}
		invalid := []invalidChild{
			{"nested", map[string]any{"claimType": "multi", "claims": []any{}}},
			{"delete", map[string]any{"claimType": "delete", "target": p2.String()}},
			{"share", map[string]any{"claimType": "share", "target": p2.String()}},
			{"bad-target", map[string]any{"claimType": "set-attribute", "permaNode": "bad", "attribute": "tag", "value": "shared"}},
			{"non-object", nil},
		}
		for _, field := range []string{"claimDate", "camliSigner", "camliSig"} {
			invalid = append(invalid, invalidChild{
				field,
				map[string]any{"claimType": "set-attribute", "attribute": "tag", "value": "shared", field: ""},
			})
		}
		for _, tt := range invalid {
			t.Run(tt.name, func(t *testing.T) {
				// Bypass the validating builder to sign a malformed envelope.
				// Valid children before and after the invalid entry must not
				// become visible on either permanode.
				raw, err := json.Marshal(map[string]any{
					"camliVersion": 1,
					"camliType":    "claim",
					"claimType":    "multi",
					"claimDate":    id.LastTime().Format(time.RFC3339Nano),
					"permaNode":    p1.String(),
					"claims": []any{
						map[string]any{"claimType": "set-attribute", "attribute": "tag", "value": "shared"},
						tt.child,
						map[string]any{"claimType": "set-attribute", "permaNode": p2.String(), "attribute": "tag", "value": "shared"},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				unsigned := &test.Blob{Contents: string(raw)}
				b, err := schema.BlobFromReader(unsigned.BlobRef(), strings.NewReader(unsigned.Contents))
				if err != nil {
					t.Fatal(err)
				}
				id.Upload(id.Sign(b.Builder()))
				res, err := multiClaimDescribe(h, p1, p2)
				if err != nil {
					t.Fatal(err)
				}
				for _, pn := range []blob.Ref{p1, p2} {
					multiClaimWantAttrs(t, res, pn, url.Values{})
					claims, err := h.GetClaims(&search.ClaimsRequest{Permanode: pn})
					if err != nil {
						t.Fatal(err)
					}
					if len(claims.Claims) != 0 {
						t.Errorf("invalid multi envelope leaked claims on %v: %+v", pn, claims.Claims)
					}
				}
				qt.wantRes(multiClaimQuery("tag", "shared"))
			})
		}
	})
}

func TestMultiClaimMissingPermanode(t *testing.T) {
	testMultiClaimSearch(t, func(t *testing.T, qt *queryTest) {
		id := qt.id
		p1 := id.NewPermanode()
		missing := id.Sign(schema.NewPlannedPermanode("multi-claim-upload-later"))
		p2 := missing.BlobRef()
		// Ordinary attribute claims do not require the target permanode
		// blob. One missing target must not defer the other child either.
		m := multiClaimBuilder(t, id, p1,
			schema.NewSetAttributeClaim(blob.Ref{}, "tag", "shared"),
			schema.NewSetAttributeClaim(p2, "tag", "shared"),
		)
		envelope := id.Upload(id.Sign(m))
		h := qt.Handler()
		for _, pn := range []blob.Ref{p1, p2} {
			claims, err := h.GetClaims(&search.ClaimsRequest{Permanode: pn})
			if err != nil {
				t.Fatal(err)
			}
			if len(claims.Claims) != 1 || claims.Claims[0].BlobRef != envelope {
				t.Errorf("GetClaims(%v) = %+v; want one child of %v", pn, claims.Claims, envelope)
			}
		}
		res, err := multiClaimDescribe(h, p1)
		if err != nil {
			t.Fatal(err)
		}
		multiClaimWantAttrs(t, res, p1, url.Values{"tag": {"shared"}})
		// Once the missing schema arrives, both permanodes are describable
		// and searchable without reuploading the envelope.
		id.Upload(missing)
		res, err = multiClaimDescribe(h, p1, p2)
		if err != nil {
			t.Fatal(err)
		}
		multiClaimWantAttrs(t, res, p1, url.Values{"tag": {"shared"}})
		multiClaimWantAttrs(t, res, p2, url.Values{"tag": {"shared"}})
		qt.wantRes(multiClaimQuery("tag", "shared"), p1, p2)
	})
}

func TestMultiClaimAtomicSearchDescribe(t *testing.T) {
	testMultiClaimSearch(t, func(t *testing.T, qt *queryTest) {
		id := qt.id
		p1, p2 := id.NewPermanode(), id.NewPermanode()
		id.SetAttribute(p1, "generation", "even")
		id.SetAttribute(p2, "generation", "even")
		h := qt.Handler()
		// Sign before starting readers, so the concurrent writer only has
		// to ingest the envelopes (and does not mutate IndexDeps' clock).
		var batches []*test.Blob
		for i := 1; i <= 24; i++ {
			value := "even"
			if i%2 != 0 {
				value = "odd"
			}
			m := multiClaimBuilder(t, id, p1,
				schema.NewSetAttributeClaim(blob.Ref{}, "generation", value),
				schema.NewSetAttributeClaim(p2, "generation", value),
			)
			m.SetClaimDate(id.LastTime().Add(time.Duration(i) * time.Second))
			batches = append(batches, id.Sign(m))
		}
		describe := func() error {
			res, err := multiClaimDescribe(h, p1, p2)
			if err != nil {
				return err
			}
			var values []string
			for _, pn := range []blob.Ref{p1, p2} {
				db := res.Meta.Get(pn)
				if db == nil || db.Permanode == nil {
					return fmt.Errorf("Describe missing %v", pn)
				}
				values = append(values, db.Permanode.Attr.Get("generation"))
			}
			if values[0] == "" || values[0] != values[1] {
				return fmt.Errorf("Describe saw partial batch: generations %q", values)
			}
			return nil
		}
		query := func() error {
			req := multiClaimQuery("generation", "odd")
			req.Describe = &search.DescribeRequest{}
			res, err := h.Query(ctxbg, req)
			if err != nil {
				return err
			}
			if n := len(res.Blobs); n != 0 && n != 2 {
				return fmt.Errorf("Query saw partial batch: %d matching permanodes", n)
			}
			for _, b := range res.Blobs {
				db := res.Describe.Meta.Get(b.Blob)
				if db == nil || db.Permanode == nil || db.Permanode.Attr.Get("generation") != "odd" {
					return fmt.Errorf("Query and its Describe disagree for %v: %+v", b.Blob, db)
				}
			}
			return nil
		}
		done := make(chan struct{})
		ready := make(chan struct{}, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, read := range []func() error{describe, query} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := read()
				ready <- struct{}{}
				if err != nil {
					errs <- err
					return
				}
				for {
					select {
					case <-done:
						return
					default:
					}
					if err := read(); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		<-ready
		<-ready
		func() {
			defer wg.Wait()
			defer close(done)
			for _, batch := range batches {
				id.Upload(batch)
				runtime.Gosched()
			}
		}()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if err := describe(); err != nil {
			t.Error(err)
		}
		qt.wantRes(multiClaimQuery("generation", "even"), p1, p2)
		qt.wantRes(multiClaimQuery("generation", "odd"))
	})
}
