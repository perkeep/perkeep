package fsbacked

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/pkg/errors"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
)

func TestStore(t *testing.T) {
	type upload struct {
		file, refstr, text string
		offset, size       int64
		nested             bool
	}
	uploads := map[string]upload{
		"inside": {
			file:   "testdata/root/insidefile",
			refstr: "sha224-869917e0d3874a284f4f39a43a44ce6ad9047250d98e8ac931e75c7d",
		},
		"inside-5-23": {
			file:   "testdata/root/insidefile",
			refstr: "sha224-4f6797e78e15a898f269de8a69e3ab2bd8302b1add88961350481a5a",
			text:   "the woman that you love",
			offset: 5,
			size:   23,
		},
		"outside": {
			file:   "testdata/outsidefile",
			refstr: "sha224-558c109254f384e05525e2ec5ac78374d5d9b9b31db0ebde68aa23c0",
			nested: true,
		},
		"outside-86-26": {
			file:   "testdata/outsidefile",
			refstr: "sha224-fbc9daf18272013bf007ce97f2cf8eb9b26ea1b8e4e323adce985152",
			text:   "his mouth is ten feet tall",
			offset: 86,
			size:   26,
			nested: true,
		},
	}

	cases := []struct {
		name string
		keys []string
	}{
		{
			name: "empty",
		},
		{
			name: "inside full",
			keys: []string{"inside"},
		},
		{
			name: "outside full",
			keys: []string{"outside"},
		},
		{
			name: "inside partial",
			keys: []string{"inside-5-23"},
		},
		{
			name: "outside partial",
			keys: []string{"outside-86-26"},
		},
		{
			name: "inside full and partial",
			keys: []string{"inside", "inside-5-23"},
		},
		{
			name: "outside full and partial",
			keys: []string{"outside", "outside-86-26"},
		},
		{
			name: "all",
			keys: []string{"inside", "outside", "inside-5-23", "outside-86-26"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := ioutil.TempFile("", "db")
			if err != nil {
				t.Fatal(err)
			}
			dbfile := f.Name()
			f.Close()
			defer os.Remove(dbfile)

			ctx := context.Background()
			nested := new(memory.Storage)
			fsb, err := New(ctx, "testdata/root", dbfile, nested)
			if err != nil {
				t.Fatal(err)
			}

			var (
				// Making empty slices (instead of leaving these vars initialized to nil)
				// because reflect.DeepEqual does not consider nil to be equal to []T{}.
				wantRefs       = make([]blob.Ref, 0)
				wantNestedRefs = make([]blob.Ref, 0)

				texts = make(map[string]string)
			)

			add := func(k string) error {
				u := uploads[k]

				f, err := os.Open(u.file)
				if err != nil {
					return errors.Wrapf(err, "opening %s", u.file)
				}
				defer f.Close()

				var r io.Reader = f

				if u.size > 0 {
					r = NewFileSectionReader(f, u.offset, u.size)
					texts[k] = u.text
				} else {
					text, err := ioutil.ReadAll(f)
					if err != nil {
						return err
					}
					_, err = f.Seek(0, io.SeekStart)
					if err != nil {
						return errors.Wrap(err, "in Seek")
					}
					texts[k] = string(text)
				}

				ref, _ := blob.Parse(u.refstr)
				wantRefs = append(wantRefs, ref)
				if u.nested {
					wantNestedRefs = append(wantNestedRefs, ref)
				}

				_, err = fsb.ReceiveBlob(ctx, ref, r)
				return errors.Wrap(err, "in ReceiveBlob")
			}
			for _, k := range c.keys {
				err = add(k)
				if err != nil {
					t.Fatal(errors.Wrapf(err, "adding %s", k))
				}
			}

			sortrefs := func(slice []blob.Ref) {
				sort.Slice(slice, func(i, j int) bool { return slice[i].Less(slice[j]) })
			}

			sortrefs(wantRefs)
			sortrefs(wantNestedRefs)

			var (
				gotRefs = make([]blob.Ref, 0)
				ch      = make(chan blob.SizedRef)

				gotNestedRefs = make([]blob.Ref, 0)
				nestedCh      = make(chan blob.SizedRef)
			)

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				for sr := range ch {
					gotRefs = append(gotRefs, sr.Ref)
				}
				wg.Done()
			}()
			go func() {
				for sr := range nestedCh {
					gotNestedRefs = append(gotNestedRefs, sr.Ref)
				}
				wg.Done()
			}()

			err = fsb.EnumerateBlobs(ctx, ch, "", -1)
			if err != nil {
				t.Fatal(err)
			}

			err = nested.EnumerateBlobs(ctx, nestedCh, "", -1)
			if err != nil {
				t.Fatal(err)
			}

			wg.Wait()

			sortrefs(gotRefs)
			sortrefs(gotNestedRefs)

			if !reflect.DeepEqual(gotRefs, wantRefs) {
				t.Errorf("got top-level refs %v, want %v", gotRefs, wantRefs)
			}
			if !reflect.DeepEqual(gotNestedRefs, wantNestedRefs) {
				t.Errorf("got nested refs %v, want %v", gotNestedRefs, wantNestedRefs)
			}

			check := func(k string) error {
				u := uploads[k]
				ref, _ := blob.Parse(u.refstr)
				r, _, err := fsb.Fetch(ctx, ref)
				if err != nil {
					return errors.Wrap(err, "in Fetch")
				}
				defer r.Close()
				got, err := ioutil.ReadAll(r)
				if err != nil {
					return errors.Wrap(err, "in ReadAll")
				}
				if string(got) != texts[k] {
					t.Errorf(`got "%s", want "%s"`, string(got), texts[k])
				}
				return nil
			}

			for _, k := range c.keys {
				err = check(k)
				if err != nil {
					t.Fatal(err)
				}
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
