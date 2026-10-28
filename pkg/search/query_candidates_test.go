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
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	. "perkeep.org/pkg/search"
)

// wholeRefsConstraint matches files with any of the contents wholeRefs.
func wholeRefsConstraint(wholeRefs ...blob.Ref) *Constraint {
	var c *Constraint
	for _, wr := range wholeRefs {
		fc := &Constraint{File: &FileConstraint{WholeRef: wr}}
		if c == nil {
			c = fc
		} else {
			c = &Constraint{Logical: &LogicalConstraint{Op: "or", A: c, B: fc}}
		}
	}
	return c
}

// contentConstraint matches permanodes whose camliContent is a file
// with any of the contents wholeRefs.
func contentConstraint(wholeRefs ...blob.Ref) *Constraint {
	return &Constraint{Permanode: &PermanodeConstraint{
		Attr:       "camliContent",
		ValueInSet: wholeRefsConstraint(wholeRefs...),
	}}
}

func TestQueryCandidates(t *testing.T) {
	// Without a corpus, FileConstraint.WholeRef never matches.
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		f1, w1 := id.UploadFile("a.jpg", "hello", time.Unix(100, 0))
		f1b, w1b := id.UploadFile("b.jpg", "hello", time.Unix(200, 0))
		f2, w2 := id.UploadFile("c.jpg", "other", time.Unix(300, 0))
		if f1 == f1b || w1 != w1b {
			t.Fatalf("want two file schemas with the same contents; got %v (%v) and %v (%v)", f1, w1, f1b, w1b)
		}
		missing := blob.RefFromString("not uploaded")

		p1 := id.NewDeterministicPermanodeForTest("1")
		id.SetAttribute(p1, "camliContent", f1.String())
		p2 := id.NewDeterministicPermanodeForTest("2")
		id.SetAttribute(p2, "camliContent", f1b.String())
		p3 := id.NewDeterministicPermanodeForTest("3")
		id.SetAttribute(p3, "camliContent", f2.String())
		// p4 had f1 as its content, and then f2.
		p4 := id.NewDeterministicPermanodeForTest("4")
		id.SetAttribute(p4, "camliContent", f1.String())
		id.SetAttribute(p4, "camliContent", f2.String())
		// p5's claim of f1 as its content was deleted.
		p5 := id.NewDeterministicPermanodeForTest("5")
		id.Delete(id.SetAttribute(p5, "camliContent", f1.String()))
		// p6 refers to f1 in a different attribute.
		p6 := id.NewDeterministicPermanodeForTest("6")
		id.SetAttribute(p6, "foo", f1.String())

		qt.candidateSource = "corpus_candidates"
		tests := []struct {
			name string
			c    *Constraint
			pn   bool // c only matches permanodes, so can be sorted by time
			want []blob.Ref
		}{
			{"file", wholeRefsConstraint(w1), false, []blob.Ref{f1, f1b}},
			{"file_or", wholeRefsConstraint(w1, w2), false, []blob.Ref{f1, f1b, f2}},
			{"file_missing", wholeRefsConstraint(missing), false, nil},
			{"content", contentConstraint(w1), true, []blob.Ref{p1, p2}},
			{"content_or", contentConstraint(missing, w2), true, []blob.Ref{p3, p4}},
			{"content_missing", contentConstraint(missing), true, nil},
			{"content_value", &Constraint{Permanode: &PermanodeConstraint{
				Attr:  "camliContent",
				Value: f1.String(),
			}}, true, []blob.Ref{p1}},
			{"content_and_ref", &Constraint{Logical: &LogicalConstraint{
				Op: "and",
				A:  contentConstraint(w1),
				B:  &Constraint{BlobRefPrefix: p2.String()},
			}}, true, []blob.Ref{p2}},
		}
		for _, tt := range tests {
			sorts := []SortType{UnspecifiedSort, Unsorted}
			if tt.pn {
				sorts = append(sorts, LastModifiedDesc, CreatedDesc)
			}
			for _, sort := range sorts {
				t.Logf("query %s, sort %v", tt.name, sort)
				qt.wantRes(&SearchQuery{Constraint: tt.c, Sort: sort}, tt.want...)
			}
		}
	})
}

// sizeConstraint matches files of exactly size bytes.
func sizeConstraint(size int64) *Constraint {
	return &Constraint{File: &FileConstraint{FileSize: &IntConstraint{Equals: &size}}}
}

func TestQueryFileSizeCandidates(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		f1, _ := id.UploadFile("a.jpg", "hello", time.Unix(100, 0))
		f1b, _ := id.UploadFile("b.jpg", "hello", time.Unix(200, 0))
		f2, _ := id.UploadFile("c.jpg", "world", time.Unix(300, 0))
		f3, _ := id.UploadFile("d.jpg", "hi", time.Unix(400, 0))
		// A directory's indexed size is its number of entries,
		// but it doesn't match a FileConstraint.
		id.UploadDir("dir", []blob.Ref{f1, f1b}, time.Unix(500, 0))

		p1 := id.NewDeterministicPermanodeForTest("1")
		id.SetAttribute(p1, "camliContent", f1.String())
		p3 := id.NewDeterministicPermanodeForTest("3")
		id.SetAttribute(p3, "camliContent", f3.String())

		qt.candidateSource = "corpus_candidates"
		tests := []struct {
			name string
			c    *Constraint
			want []blob.Ref
		}{
			{"size", sizeConstraint(5), []blob.Ref{f1, f1b, f2}},
			{"dir_size", sizeConstraint(2), []blob.Ref{f3}},
			{"size_missing", sizeConstraint(6), nil},
			{"size_and_name", &Constraint{File: &FileConstraint{
				FileSize: sizeConstraint(5).File.FileSize,
				FileName: &StringConstraint{Equals: "b.jpg"},
			}}, []blob.Ref{f1b}},
			{"content_size", &Constraint{Permanode: &PermanodeConstraint{
				Attr:       "camliContent",
				ValueInSet: sizeConstraint(2),
			}}, []blob.Ref{p3}},
		}
		for _, tt := range tests {
			t.Logf("query %s", tt.name)
			qt.wantRes(&SearchQuery{Constraint: tt.c}, tt.want...)
		}

		// Size ranges still scan.
		qt.candidateSource = ""
		var src string
		ExportSetCandidateSourceHook(func(s string) { src = s })
		defer ExportSetCandidateSourceHook(nil)
		qt.wantRes(&SearchQuery{Constraint: &Constraint{File: &FileConstraint{
			FileSize: &IntConstraint{Min: 3, Max: 5},
		}}}, f1, f1b, f2)
		if src == "corpus_candidates" {
			t.Errorf("size range query used candidate source %q", src)
		}
	})
}

// TestQueryCandidatesPaging checks that queries using the
// corpus_candidates source page the same as a full scan of the
// permanodes, including when some have the same time.
func TestQueryCandidatesPaging(t *testing.T) {
	for _, sort := range []SortType{UnspecifiedSort, LastModifiedDesc, CreatedDesc} {
		t.Run(fmt.Sprint(sort), func(t *testing.T) {
			testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
				testQueryCandidatesPaging(qt, sort)
			})
		})
	}
}

func testQueryCandidatesPaging(qt *queryTest, sort SortType) {
	t := qt.t
	id := qt.id
	var whole blob.Ref
	for i := range 7 {
		var f blob.Ref
		f, whole = id.UploadFile(fmt.Sprintf("f%d.jpg", i), "same", time.Unix(int64(100*i), 0))
		pn := id.NewDeterministicPermanodeForTest(fmt.Sprint(i))
		// Each permanode also gets a "mark" attribute, which only a
		// full scan can find, to compare with. Some get the same
		// time as the one before.
		setAttr := id.SetAttribute
		if i%3 == 2 {
			setAttr = id.SetAttribute_NoTimeMove
		}
		setAttr(pn, "camliContent", f.String())
		id.SetAttribute_NoTimeMove(pn, "mark", "x")
	}
	other, _ := id.UploadFile("other.jpg", "different", time.Unix(1000, 0))
	id.SetAttribute(id.NewDeterministicPermanodeForTest("other"), "camliContent", other.String())

	h := qt.Handler()
	var source string
	ExportSetCandidateSourceHook(func(s string) { source = s })
	defer ExportSetCandidateSourceHook(nil)

	// pages returns the pages of results of c, and the source used.
	pages := func(c *Constraint) (pages [][]blob.Ref, conts []string, src string) {
		cont := ""
		for range 10 {
			res, err := h.Query(ctxbg, &SearchQuery{Constraint: c, Limit: 2, Sort: sort, Continue: cont})
			if err != nil {
				t.Fatal(err)
			}
			var page []blob.Ref
			for _, b := range res.Blobs {
				page = append(page, b.Blob)
			}
			pages = append(pages, page)
			conts = append(conts, res.Continue)
			if res.Continue == "" {
				return pages, conts, source
			}
			cont = res.Continue
		}
		t.Fatal("too many pages")
		return
	}
	gotPages, gotConts, gotSrc := pages(contentConstraint(whole))
	wantPages, wantConts, wantSrc := pages(&Constraint{Permanode: &PermanodeConstraint{Attr: "mark", Value: "x"}})
	if gotSrc != "corpus_candidates" {
		t.Errorf("candidate source = %q; want corpus_candidates", gotSrc)
	}
	if wantSrc == "corpus_candidates" {
		t.Errorf("comparison query used candidate source %q", wantSrc)
	}
	if !reflect.DeepEqual(gotPages, wantPages) || !reflect.DeepEqual(gotConts, wantConts) {
		t.Errorf("pages = %v, continue tokens %q\nwant %v, %q", gotPages, gotConts, wantPages, wantConts)
	}
	if n := len(wantPages); n != 4 {
		t.Errorf("got %d pages; want 4", n)
	}
}

// BenchmarkQueryContentByWholeRef compares finding the permanode of a
// file by its contents with a query that must scan all the permanodes.
func BenchmarkQueryContentByWholeRef(b *testing.B) {
	testQueryTypes(b, corpusTypeOnly, func(qt *queryTest) {
		id := qt.id
		const n = 2000
		var whole blob.Ref
		for i := range n {
			f, wr := id.UploadFile(fmt.Sprintf("f%d.jpg", i), fmt.Sprint(i), time.Unix(int64(i), 0))
			pn := id.NewDeterministicPermanodeForTest(fmt.Sprint(i))
			id.SetAttribute(pn, "camliContent", f.String())
			if i == n/2 {
				whole = wr
				id.SetAttribute(pn, "mark", "x")
			}
		}
		h := qt.Handler()
		for _, bc := range []struct {
			name string
			c    *Constraint
		}{
			{"candidates", contentConstraint(whole)},
			{"scan", &Constraint{Permanode: &PermanodeConstraint{Attr: "mark", Value: "x"}}},
		} {
			b.Run(bc.name, func(b *testing.B) {
				for b.Loop() {
					res, err := h.Query(ctxbg, &SearchQuery{Constraint: bc.c})
					if err != nil {
						b.Fatal(err)
					}
					if len(res.Blobs) != 1 {
						b.Fatalf("got %d results; want 1", len(res.Blobs))
					}
				}
			})
		}
	})
}

// BenchmarkQueryFileBySize compares finding a file by its exact size with
// the same query as a size range, which must scan all the files.
func BenchmarkQueryFileBySize(b *testing.B) {
	testQueryTypes(b, corpusTypeOnly, func(qt *queryTest) {
		id := qt.id
		const n = 2000
		for i := range n {
			id.UploadFile(fmt.Sprintf("f%d.jpg", i), strings.Repeat("x", i+1), time.Unix(int64(i), 0))
		}
		const size = n / 2
		h := qt.Handler()
		for _, bc := range []struct {
			name string
			c    *Constraint
		}{
			{"candidates", sizeConstraint(size)},
			{"scan", &Constraint{File: &FileConstraint{FileSize: &IntConstraint{Min: size, Max: size}}}},
		} {
			b.Run(bc.name, func(b *testing.B) {
				for b.Loop() {
					res, err := h.Query(ctxbg, &SearchQuery{Constraint: bc.c})
					if err != nil {
						b.Fatal(err)
					}
					if len(res.Blobs) != 1 {
						b.Fatalf("got %d results; want 1", len(res.Blobs))
					}
				}
			})
		}
	})
}
