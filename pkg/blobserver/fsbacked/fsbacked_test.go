package fsbacked

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
)

func TestStore(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "fsbacked")
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(tmpdir, 0700)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpdir)

	rootdir := filepath.Join(tmpdir, "root")
	err = os.Mkdir(rootdir, 0700)
	if err != nil {
		t.Fatal(err)
	}

	foofile := filepath.Join(rootdir, "foo")
	err = ioutil.WriteFile(foofile, []byte("foo"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	const foo224 = "sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db"
	fooref, _ := blob.Parse(foo224)

	barfile := filepath.Join(tmpdir, "foo") // n.b. not under rootdir
	err = ioutil.WriteFile(barfile, []byte("bar"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	const bar224 = "sha224-07daf010de7f7f0d8d76a76eb8d1eb40182c8d1e7a3877a6686c9bf0"
	barref, _ := blob.Parse(bar224)

	cases := []struct {
		addfiles       []string
		addrefs        []blob.Ref
		wantnestedrefs []blob.Ref
	}{
		{},
		{
			addfiles: []string{foofile},
			addrefs:  []blob.Ref{fooref},
		},
		{
			addfiles:       []string{barfile},
			addrefs:        []blob.Ref{barref},
			wantnestedrefs: []blob.Ref{barref},
		},
		{
			addfiles:       []string{foofile, barfile},
			addrefs:        []blob.Ref{fooref, barref},
			wantnestedrefs: []blob.Ref{barref},
		},
	}

	for i, c := range cases {
		t.Run(fmt.Sprintf("case_%02d", i+1), func(t *testing.T) {
			f, err := ioutil.TempFile(tmpdir, "db")
			if err != nil {
				t.Fatal(err)
			}
			dbfile := f.Name()
			f.Close()

			ctx := context.Background()
			nested := new(memory.Storage)
			fsb, err := New(ctx, rootdir, dbfile, nested)
			if err != nil {
				t.Fatal(err)
			}

			add := func(filename string, ref blob.Ref) error {
				f, err := os.Open(filename)
				if err != nil {
					return err
				}
				defer f.Close()

				_, err = fsb.ReceiveBlob(ctx, ref, f)
				return err
			}

			for j, filename := range c.addfiles {
				err = add(filename, c.addrefs[j])
				if err != nil {
					t.Fatal(err)
				}
			}

			got := make([]blob.Ref, 0) // `var got []blob.Ref` makes reflect.DeepEqual fail when got is nil
			ch := make(chan blob.SizedRef)

			done := make(chan struct{})

			go func() {
				for sr := range ch {
					got = append(got, sr.Ref)
				}
				close(done)
			}()

			err = fsb.EnumerateBlobs(ctx, ch, "", -1)
			if err != nil {
				t.Fatal(err)
			}

			<-done

			want := make([]blob.Ref, len(c.addrefs))
			copy(want, c.addrefs)
			sort.Slice(want, func(i, j int) bool { return want[i].Less(want[j]) })

			if !reflect.DeepEqual(got, want) {
				t.Errorf("got these top-level refs: %v; want %v", got, want)
			}

			got = got[:0]
			ch = make(chan blob.SizedRef)

			go func() {
				for sr := range ch {
					got = append(got, sr.Ref)
				}
			}()

			err = nested.EnumerateBlobs(ctx, ch, "", -1)
			if err != nil {
				t.Fatal(err)
			}

			want = make([]blob.Ref, len(c.wantnestedrefs))
			copy(want, c.wantnestedrefs)
			sort.Slice(want, func(i, j int) bool { return want[i].Less(want[j]) })

			if !reflect.DeepEqual(got, want) {
				t.Errorf("got these nested refs: %v; want %v", got, want)
			}
		})
	}
}

func TestFindRelPath(t *testing.T) {
	cases := []struct {
		root, path, want string
	}{
		{"a/b", "a/b/c", "c"},
		{"a/b", "a/b/c/d", "c/d"},
		{"a/b", "a/b", ""},
		{"a/b", "a/c", ""},
		{"a/b", "a", ""},
		{"a/b", "c/d", ""},
	}

	for i, c := range cases {
		t.Run(fmt.Sprintf("case_%02d", i+1), func(t *testing.T) {
			s := &Storage{root: c.root}
			got := s.findRelPath(c.path)
			if got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}
