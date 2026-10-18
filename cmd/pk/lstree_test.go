/*
Copyright 2026 The Perkeep Authors.

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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
)

// treeBuilder writes schema blobs for a test directory tree to a
// test.Fetcher.
type treeBuilder struct {
	t  *testing.T
	bs *test.Fetcher
}

func (tb *treeBuilder) put(b *schema.Blob) blob.Ref {
	tb.t.Helper()
	tb.bs.AddBlob(&test.Blob{Contents: b.JSON()})
	return b.BlobRef()
}

func (tb *treeBuilder) file(name, perm string, mtime time.Time, contents string) blob.Ref {
	tb.t.Helper()
	fb := schema.NewFileMap(name)
	fb.SetRawStringField("unixPermission", perm)
	fb.SetModTime(mtime)
	br, err := schema.WriteFileMap(context.Background(), tb.bs, fb, strings.NewReader(contents))
	if err != nil {
		tb.t.Fatal(err)
	}
	return br
}

func (tb *treeBuilder) symlink(name, target string) blob.Ref {
	tb.t.Helper()
	return tb.put(schema.NewFileMap(name).SetSymlinkTarget(target).Blob())
}

func (tb *treeBuilder) dir(name, perm string, mtime time.Time, members ...blob.Ref) blob.Ref {
	tb.t.Helper()
	ss := schema.NewStaticSet()
	for _, sub := range ss.SetStaticSetMembers(members) {
		tb.put(sub)
	}
	ssRef := tb.put(ss.Blob())
	db := schema.NewDirMap(name).PopulateDirectoryMap(ssRef)
	db.SetRawStringField("unixPermission", perm)
	db.SetModTime(mtime)
	return tb.put(db.Blob())
}

// buildTestTree returns a small tree with a nested read-only
// directory, a symlink, an empty file, and a name needing quoting.
func buildTestTree(t *testing.T) (*test.Fetcher, blob.Ref) {
	bs := new(test.Fetcher)
	tb := &treeBuilder{t, bs}
	t0 := time.Date(2007, 5, 22, 18, 45, 0, 0, time.UTC)
	ro := tb.dir("ro", "0555", t0.Add(-time.Hour),
		tb.file("b.txt", "0444", t0.Add(-2*time.Hour), "bee\n"),
	)
	root := tb.dir("home", "0755", t0,
		tb.file("z-empty", "0600", t0, ""),
		tb.file("a.txt", "0644", t0.Add(-24*time.Hour), "hello, world\n"),
		tb.symlink("link", "ro/b.txt"),
		tb.file("tab\tname", "0644", t0, "x"),
		ro,
	)
	return bs, root
}

func listTree(t *testing.T, bs *test.Fetcher, root blob.Ref, tl treeLister) string {
	t.Helper()
	ctx := context.Background()
	b, err := fetchSchemaBlob(ctx, bs, root)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	tl.fetcher = bs
	tl.w = &sb
	tl.loc = time.UTC
	if err := tl.list(ctx, b); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// refRx matches the blobref column of ls-tree's text output.
var refRx = regexp.MustCompile(`  ([a-z0-9]+-[0-9a-f]+)  `)

func TestLsTree(t *testing.T) {
	bs, root := buildTestTree(t)
	got := listTree(t, bs, root, treeLister{})

	// Blobrefs depend on the hash function, so replace them with
	// placeholders after checking they're abbreviated.
	var lines []string
	for line := range strings.Lines(got) {
		m := refRx.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("no blobref in line %q", line)
		}
		if _, digest, _ := strings.Cut(m[1], "-"); len(digest) != abbrevRefDigits {
			t.Errorf("blobref %q in line %q not abbreviated to %d digits", m[1], line, abbrevRefDigits)
		}
		lines = append(lines, strings.Replace(line, m[1], "REF", 1))
	}
	want := strings.Join([]string{
		"drwxr-xr-x            - 2007-05-22 18:45  REF  .\n",
		"-rw-r--r--           13 2007-05-21 18:45  REF  a.txt\n",
		"lrw-r--r--            -                -  REF  link -> ro/b.txt\n",
		"dr-xr-xr-x            - 2007-05-22 17:45  REF  ro\n",
		"-r--r--r--            4 2007-05-22 16:45  REF  ro/b.txt\n",
		"-rw-r--r--            1 2007-05-22 18:45  REF  \"tab\\tname\"\n",
		"-rw-------            0 2007-05-22 18:45  REF  z-empty\n",
	}, "")
	if g := strings.Join(lines, ""); g != want {
		t.Errorf("got:\n%s\nwant:\n%s", g, want)
	}
}

func TestLsTreeFullRefs(t *testing.T) {
	bs, root := buildTestTree(t)
	got := listTree(t, bs, root, treeLister{longRefs: true})
	first, _, _ := strings.Cut(got, "\n")
	if !strings.Contains(first, " "+root.String()+" ") {
		t.Errorf("first line %q doesn't contain full root ref %v", first, root)
	}
}

func TestLsTreeJSON(t *testing.T) {
	bs, root := buildTestTree(t)
	got := listTree(t, bs, root, treeLister{json: true})
	var paths []string
	for line := range strings.Lines(got) {
		var e lsTreeJSONEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		paths = append(paths, e.Path)
		switch e.Path {
		case "ro":
			if e.Type != "directory" || e.Mode != "0555" || e.Size != nil {
				t.Errorf("ro: got %+v", e)
			}
		case "ro/b.txt":
			if e.Type != "file" || e.Mode != "0444" || e.Size == nil || *e.Size != 4 || e.ModTime == nil {
				t.Errorf("ro/b.txt: got %+v", e)
			}
		case "link":
			if e.Type != "symlink" || e.Target != "ro/b.txt" {
				t.Errorf("link: got %+v", e)
			}
		}
	}
	if g, w := strings.Join(paths, ","), ".,a.txt,link,ro,ro/b.txt,tab\tname,z-empty"; g != w {
		t.Errorf("paths = %q; want %q", g, w)
	}
}

// TestLsTreeLargeDir checks that a directory too big for one static-set,
// whose members are spread over merged subsets, lists completely and in
// order.
func TestLsTreeLargeDir(t *testing.T) {
	bs := new(test.Fetcher)
	tb := &treeBuilder{t, bs}
	t0 := time.Date(2007, 5, 22, 0, 0, 0, 0, time.UTC)
	const n = 10005 // more than schema's maxStaticSetMembers
	var members []blob.Ref
	for i := range n {
		members = append(members, tb.symlink(fmt.Sprintf("f%05d", n-1-i), "x"))
	}
	root := tb.dir("big", "0755", t0, members...)
	got := listTree(t, bs, root, treeLister{})
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != n+1 {
		t.Fatalf("got %d lines; want %d", len(lines), n+1)
	}
	for i, line := range lines[1:] {
		if want := fmt.Sprintf(" f%05d -> x", i); !strings.HasSuffix(line, want) {
			t.Fatalf("line %d = %q; want suffix %q", i+1, line, want)
		}
	}
}

type fakeDescriber struct {
	attr url.Values
	at   time.Time // of the last request
}

func (d *fakeDescriber) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	d.at = time.Time(req.At)
	return &search.DescribeResponse{Meta: search.MetaMap{
		req.BlobRef.String(): &search.DescribedBlob{
			BlobRef:   req.BlobRef,
			Permanode: &search.DescribedPermanode{Attr: d.attr},
		},
	}}, nil
}

func TestResolveTreeRoot(t *testing.T) {
	ctx := context.Background()
	bs, root := buildTestTree(t)
	pn := schema.NewPlannedPermanode("ls-tree test").Blob()
	bs.AddBlob(&test.Blob{Contents: pn.JSON()})
	at := time.Date(2007, 5, 20, 0, 0, 0, 0, time.UTC)

	d := &fakeDescriber{attr: url.Values{"camliContent": {root.String()}}}
	b, err := resolveTreeRoot(ctx, bs, d, pn.BlobRef(), at)
	if err != nil {
		t.Fatal(err)
	}
	if b.BlobRef() != root {
		t.Errorf("permanode resolved to %v; want %v", b.BlobRef(), root)
	}
	if !d.at.Equal(at) {
		t.Errorf("describe At = %v; want %v", d.at, at)
	}

	// A directory resolves to itself, without a describe.
	b, err = resolveTreeRoot(ctx, bs, nil, root, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if b.BlobRef() != root {
		t.Errorf("directory resolved to %v; want itself", b.BlobRef())
	}

	d = &fakeDescriber{attr: url.Values{"title": {"no content"}}}
	if _, err := resolveTreeRoot(ctx, bs, d, pn.BlobRef(), time.Time{}); err == nil || !strings.Contains(err.Error(), "no camliContent") {
		t.Errorf("permanode without camliContent: err = %v; want no camliContent error", err)
	}
}
