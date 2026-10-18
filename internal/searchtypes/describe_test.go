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

package searchtypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"go4.org/types"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

func TestDescribeRequestClone(t *testing.T) {
	br := blob.RefFromString("root")
	dr := &DescribeRequest{
		BlobRef:        br,
		BlobRefs:       []blob.Ref{blob.RefFromString("other")},
		Depth:          3,
		MaxDirChildren: 17,
		At:             types.Time3339(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		Rules:          []*DescribeRule{{Attrs: []string{"camliContent"}, Rules: []*DescribeRule{{Attrs: []string{"camliMember"}}}}},
	}
	InitDescribeRequest(dr, nil, "owner", nil)
	dr.describedBlob(br)
	dr.addError(br, errors.New("original error"))
	dr.flatRules()
	dr.started = map[blobrefAndDepth]bool{{br, 1}: true}
	dr.blobDesLock = map[blob.Ref]*sync.Mutex{br: new(sync.Mutex)}
	dr.noteResultFromRule(dr.Rules[0], br)

	clone := dr.Clone()
	wantJSON, _ := json.Marshal(dr)
	gotJSON, _ := json.Marshal(clone)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("clone JSON = %s, want %s", gotJSON, wantJSON)
	}
	if clone.sh != dr.sh {
		t.Error("clone did not preserve the describe backend")
	}
	if clone.wg == nil || clone.wg == dr.wg || clone.m == nil || clone.errs == nil {
		t.Fatal("clone did not get independent initialized state")
	}
	if len(clone.m) != 0 || len(clone.errs) != 0 || clone.started != nil || clone.blobDesLock != nil || clone.resFromRule != nil || clone.flatRuleCache != nil {
		t.Error("clone retained original lookup or rule state")
	}
	clone.describedBlob(blob.RefFromString("clone-only"))
	clone.Rules[0].Attrs[0] = "changed"
	if len(dr.m) != 1 || dr.Rules[0].Attrs[0] != "camliContent" {
		t.Error("clone shares mutable state with original")
	}
	if clone.Rules[0].Rules[0].parentRule != nil {
		t.Error("clone retained private parent rule state")
	}
}

func TestDescribeRequestInitTwice(t *testing.T) {
	dr := new(DescribeRequest)
	InitDescribeRequest(dr, nil, "owner", nil)
	original := dr.sh
	defer func() {
		if got := recover(); got != "already initialized" {
			t.Errorf("second initialization panic = %v, want already initialized", got)
		}
		if dr.sh != original {
			t.Error("second initialization replaced original backend")
		}
	}()
	InitDescribeRequest(dr, nil, "other", nil)
}

// describeTestIndex intentionally implements only the small describe interface,
// without importing the index or search implementations.
type describeTestIndex struct {
	metas    map[blob.Ref]camtypes.BlobMeta
	claims   map[blob.Ref][]camtypes.Claim
	files    map[blob.Ref]camtypes.FileInfo
	children map[blob.Ref][]blob.Ref
	metaErr  error
	checkCtx func(context.Context)

	mu       sync.Mutex
	owners   []string
	dirLimit int
}

func (ix *describeTestIndex) GetBlobMeta(ctx context.Context, br blob.Ref) (camtypes.BlobMeta, error) {
	if ix.checkCtx != nil {
		ix.checkCtx(ctx)
	}
	if ix.metaErr != nil {
		return camtypes.BlobMeta{}, ix.metaErr
	}
	if meta, ok := ix.metas[br]; ok {
		return meta, nil
	}
	return camtypes.BlobMeta{}, os.ErrNotExist
}

func (ix *describeTestIndex) AppendClaims(ctx context.Context, dst []camtypes.Claim, br blob.Ref, owner, attr string) ([]camtypes.Claim, error) {
	if ix.checkCtx != nil {
		ix.checkCtx(ctx)
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.owners = append(ix.owners, owner)
	if attr != "" {
		return nil, fmt.Errorf("unexpected attribute filter %q", attr)
	}
	return append(dst, ix.claims[br]...), nil
}

func (ix *describeTestIndex) GetFileInfo(ctx context.Context, br blob.Ref) (camtypes.FileInfo, error) {
	if ix.checkCtx != nil {
		ix.checkCtx(ctx)
	}
	if fi, ok := ix.files[br]; ok {
		return fi, nil
	}
	return camtypes.FileInfo{}, os.ErrNotExist
}

func (*describeTestIndex) GetImageInfo(context.Context, blob.Ref) (camtypes.ImageInfo, error) {
	return camtypes.ImageInfo{}, os.ErrNotExist
}

func (*describeTestIndex) GetMediaTags(context.Context, blob.Ref) (map[string]string, error) {
	return nil, os.ErrNotExist
}

func (*describeTestIndex) GetFileLocation(context.Context, blob.Ref) (camtypes.Location, error) {
	return camtypes.Location{}, os.ErrNotExist
}

func (ix *describeTestIndex) GetDirMembers(_ context.Context, br blob.Ref, dest chan<- blob.Ref, limit int) error {
	defer close(dest)
	ix.mu.Lock()
	ix.dirLimit = limit
	ix.mu.Unlock()
	for i, child := range ix.children[br] {
		if i >= limit {
			break
		}
		dest <- child
	}
	return nil
}

func TestDescribeBackendAndRules(t *testing.T) {
	type contextKey struct{}
	value := new(int)
	ctx := context.WithValue(context.Background(), contextKey{}, value)
	checkCtx := func(got context.Context) {
		if got.Value(contextKey{}) != value {
			t.Error("describe did not forward the caller's context")
		}
	}
	root := blob.RefFromString("root")
	child := blob.RefFromString("child")
	file := blob.RefFromString("file")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ix := &describeTestIndex{
		checkCtx: checkCtx,
		metas: map[blob.Ref]camtypes.BlobMeta{
			root:  {Ref: root, CamliType: "permanode"},
			child: {Ref: child, CamliType: "permanode"},
			file:  {Ref: file, CamliType: "file", Size: 123},
		},
		claims: map[blob.Ref][]camtypes.Claim{
			root: {
				{Type: "set-attribute", Attr: "camliContent", Value: child.String(), Date: at},
				{Type: "set-attribute", Attr: "title", Value: "old title", Date: at},
				{Type: "set-attribute", Attr: "title", Value: "future title", Date: at.Add(time.Second)},
			},
			child: {{Type: "set-attribute", Attr: "camliContent", Value: file.String(), Date: at}},
		},
		files: map[blob.Ref]camtypes.FileInfo{file: {FileName: "file.txt", MIMEType: "text/plain"}},
	}
	var locMu sync.Mutex
	locations := make(map[blob.Ref]time.Time)
	location := camtypes.Location{Latitude: 12, Longitude: 34}
	dr := &DescribeRequest{
		BlobRef: root,
		At:      types.Time3339(at),
		Rules:   []*DescribeRule{{IfResultRoot: true, Attrs: []string{"camliContent"}, Rules: []*DescribeRule{{Attrs: []string{"camliContent"}}}}},
	}
	InitDescribeRequest(dr, ix, "owner-key-id", func(ctx context.Context, br blob.Ref, gotAt time.Time) (camtypes.Location, error) {
		checkCtx(ctx)
		locMu.Lock()
		defer locMu.Unlock()
		locations[br] = gotAt
		return location, nil
	})
	res, err := Describe(ctx, dr)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Meta) != 3 {
		t.Fatalf("described %d blobs, want 3", len(res.Meta))
	}
	rootDesc := res.Meta.Get(root)
	if rootDesc.Request != dr || rootDesc.Title() != "old title" || rootDesc.Permanode.ModTime != at {
		t.Errorf("root description = %+v, want original request and claims at requested time", rootDesc)
	}
	if rootDesc.Location == nil || *rootDesc.Location != location {
		t.Errorf("root location = %v, want %v", rootDesc.Location, location)
	}
	if want := map[blob.Ref]time.Time{root: at, child: at}; !reflect.DeepEqual(locations, want) {
		t.Errorf("location requests = %v, want %v", locations, want)
	}
	if !reflect.DeepEqual(ix.owners, []string{"owner-key-id", "owner-key-id"}) {
		t.Errorf("claim owners = %v", ix.owners)
	}
	path, fi, ok := res.Meta.Get(child).PermanodeFile()
	if !ok || !reflect.DeepEqual(path, []blob.Ref{child, file}) || fi.FileName != "file.txt" {
		t.Errorf("PermanodeFile = %v, %v, %v", path, fi, ok)
	}
	if got := res.Meta.Get(file); got.Size != 123 || got.Title() != "file.txt" {
		t.Errorf("file description = %+v", got)
	}
	delete(res.Meta, root.String())
	if dr.DescribedBlobStr(root.String()) == nil {
		t.Error("response metadata was not a snapshot of request state")
	}
}

func TestDescribeDirectoryLimit(t *testing.T) {
	for _, limit := range []int{-1, 0, 1, 1000, 1001} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			dir := blob.RefFromString("directory")
			file := blob.RefFromString("file")
			ix := &describeTestIndex{
				metas:    map[blob.Ref]camtypes.BlobMeta{dir: {CamliType: "directory"}, file: {CamliType: "file"}},
				files:    map[blob.Ref]camtypes.FileInfo{dir: {FileName: "dir"}, file: {FileName: "file"}},
				children: map[blob.Ref][]blob.Ref{dir: {file}},
			}
			dr := &DescribeRequest{BlobRef: dir, MaxDirChildren: limit}
			InitDescribeRequest(dr, ix, "", nil)
			res, err := Describe(context.Background(), dr)
			if err != nil {
				t.Fatal(err)
			}
			wantLimit := limit
			if limit <= 0 || limit > 1000 {
				wantLimit = 50
			}
			if ix.dirLimit != wantLimit {
				t.Errorf("GetDirMembers limit = %d, want %d", ix.dirLimit, wantLimit)
			}
			if got := res.Meta.Get(dir).DirChildren; !reflect.DeepEqual(got, []blob.Ref{file}) {
				t.Errorf("directory children = %v, want %v", got, []blob.Ref{file})
			}
			if res.Meta.Get(file) == nil {
				t.Error("directory child was not described at default depth")
			}
		})
	}
}

func TestDescribeErrors(t *testing.T) {
	br := blob.RefFromString("missing")
	for _, err := range []error{os.ErrNotExist, errors.New("index failed")} {
		t.Run(err.Error(), func(t *testing.T) {
			dr := &DescribeRequest{BlobRef: br}
			InitDescribeRequest(dr, &describeTestIndex{metaErr: err}, "", nil)
			res, gotErr := Describe(context.Background(), dr)
			if errors.Is(err, os.ErrNotExist) {
				if gotErr != nil || len(res.Meta) != 0 {
					t.Fatalf("missing blob response = %v, %v", res, gotErr)
				}
				return
			}
			if !errors.Is(gotErr, err) || res != nil {
				t.Errorf("Describe = %v, %v, want wrapped index error", res, gotErr)
			}
			_, resultErr := dr.Result()
			var describeErr DescribeError
			if !errors.As(resultErr, &describeErr) || describeErr[br.String()] != err {
				t.Errorf("Result error = %v, want DescribeError containing index error", resultErr)
			}
		})
	}
}
