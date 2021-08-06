//go:build linux || darwin
// +build linux darwin

/*
Copyright 2012 The Perkeep Authors

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

package fs

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"go4.org/syncutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

const refreshTime = 1 * time.Minute

type rootsDir struct {
	fs *CamliFileSystem
	at time.Time

	mu        sync.Mutex // guards following
	lastQuery time.Time
	m         map[string]blob.Ref // ent name => permanode
	children  map[string]fs.Node  // ent name => child node
}

var (
	_ fs.Node               = (*rootsDir)(nil)
	_ fs.HandleReadDirAller = (*rootsDir)(nil)
	_ fs.NodeRemover        = (*rootsDir)(nil)
	_ fs.NodeRenamer        = (*rootsDir)(nil)
	_ fs.NodeStringLookuper = (*rootsDir)(nil)
	_ fs.NodeMkdirer        = (*rootsDir)(nil)
)

func (n *rootsDir) isRO() bool {
	return !n.at.IsZero()
}

func (n *rootsDir) dirMode() os.FileMode {
	if n.isRO() {
		return 0500
	}
	return 0700
}

func (n *rootsDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | n.dirMode()
	a.Uid = uint32(os.Getuid())
	a.Gid = uint32(os.Getgid())
	return nil
}

func (n *rootsDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.condRefresh(ctx); err != nil {
		return nil, fuse.EIO
	}
	var ents []fuse.Dirent
	for name := range n.m {
		ents = append(ents, fuse.Dirent{Name: name})
	}
	Logger.Printf("rootsDir.ReadDirAll() -> %v", ents)
	return ents, nil
}

func (n *rootsDir) Remove(ctx context.Context, req *fuse.RemoveRequest) error {
	if n.isRO() {
		return fuse.EPERM
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.condRefresh(ctx); err != nil {
		return err
	}
	br := n.m[req.Name]
	if !br.Valid() {
		return fuse.ENOENT
	}

	claim := schema.NewDelAttributeClaim(br, "camliRoot", "")
	_, err := n.fs.client.UploadAndSignBlob(ctx, claim)
	if err != nil {
		Logger.Println("rootsDir.Remove:", err)
		return fuse.EIO
	}

	delete(n.m, req.Name)
	delete(n.children, req.Name)

	return nil
}

func (n *rootsDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	Logger.Printf("rootsDir.Rename %q -> %q", req.OldName, req.NewName)
	if n.isRO() {
		return fuse.EPERM
	}

	n.mu.Lock()
	target, exists := n.m[req.OldName]
	_, collision := n.m[req.NewName]
	n.mu.Unlock()
	if !exists {
		Logger.Printf("*rootsDir.Rename src name %q isn't known", req.OldName)
		return fuse.ENOENT
	}
	if collision {
		Logger.Printf("*rootsDir.Rename dest %q already exists", req.NewName)
		return fuse.EIO
	}

	// Don't allow renames if the root contains content.  Rename
	// is mostly implemented to make GUIs that create directories
	// before asking for the directory name.
	res, err := n.fs.client.Describe(ctx, &search.DescribeRequest{BlobRef: target})
	if err != nil {
		Logger.Println("rootsDir.Rename:", err)
		return fuse.EIO
	}
	db := res.Meta[target.String()]
	if db == nil {
		Logger.Printf("Failed to pull meta for target: %v", target)
		return fuse.EIO
	}

	for k := range db.Permanode.Attr {
		const p = "camliPath:"
		if strings.HasPrefix(k, p) {
			Logger.Printf("Found file in %q: %q, disallowing rename", req.OldName, k[len(p):])
			return fuse.EIO
		}
	}

	claim := schema.NewSetAttributeClaim(target, "camliRoot", req.NewName)
	_, err = n.fs.client.UploadAndSignBlob(ctx, claim)
	if err != nil {
		Logger.Printf("Upload rename link error: %v", err)
		return fuse.EIO
	}

	// Comment transplanted from mutDir.Rename
	// TODO(bradfitz): this locking would be racy, if the kernel
	// doesn't do it properly. (It should) Let's just trust the
	// kernel for now. Later we can verify and remove this
	// comment.
	n.mu.Lock()
	if n.m[req.OldName] != target {
		panic("Race.")
	}
	delete(n.m, req.OldName)
	delete(n.children, req.OldName)
	delete(n.children, req.NewName)
	n.m[req.NewName] = target
	n.mu.Unlock()

	return nil
}

func (n *rootsDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	Logger.Printf("fs.roots: Lookup(%q)", name)
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.condRefresh(ctx); err != nil {
		return nil, err
	}
	br := n.m[name]
	if !br.Valid() {
		return nil, fuse.ENOENT
	}

	nod, ok := n.children[name]
	if ok {
		return nod, nil
	}

	if n.isRO() {
		nod = newRODir(n.fs, br, name, n.at)
	} else {
		nod = &mutDir{
			fs:        n.fs,
			permanode: br,
			name:      name,
			xattrs:    map[string][]byte{},
		}
	}
	n.children[name] = nod

	return nod, nil
}

// requires n.mu is held
func (n *rootsDir) condRefresh(ctx context.Context) error {
	if n.lastQuery.After(time.Now().Add(-refreshTime)) {
		return nil
	}
	Logger.Printf("fs.roots: querying")

	var rootRes, impRes *search.WithAttrResponse
	var grp syncutil.Group
	grp.Go(func() (err error) {
		// TODO(mpl): use a search query instead.
		rootRes, err = n.fs.client.GetPermanodesWithAttr(ctx, &search.WithAttrRequest{N: 100, Attr: "camliRoot", At: n.at})
		return
	})
	grp.Go(func() (err error) {
		impRes, err = n.fs.client.GetPermanodesWithAttr(ctx, &search.WithAttrRequest{N: 100, Attr: "camliImportRoot", At: n.at})
		return
	})
	if err := grp.Err(); err != nil {
		Logger.Printf("fs.roots: error refreshing permanodes: %v", err)
		return fuse.EIO
	}

	n.m = make(map[string]blob.Ref)
	if n.children == nil {
		n.children = make(map[string]fs.Node)
	}

	dr := &search.DescribeRequest{
		Depth: 1,
	}
	for _, wi := range rootRes.WithAttr {
		dr.BlobRefs = append(dr.BlobRefs, wi.Permanode)
	}
	for _, wi := range impRes.WithAttr {
		dr.BlobRefs = append(dr.BlobRefs, wi.Permanode)
	}
	if len(dr.BlobRefs) == 0 {
		return nil
	}

	dres, err := n.fs.client.Describe(ctx, dr)
	if err != nil {
		Logger.Printf("Describe failure: %v", err)
		return fuse.EIO
	}

	// Roots
	currentRoots := map[string]bool{}
	for _, wi := range rootRes.WithAttr {
		pn := wi.Permanode
		db := dres.Meta[pn.String()]
		if db != nil && db.Permanode != nil {
			name := db.Permanode.Attr.Get("camliRoot")
			if name != "" {
				currentRoots[name] = true
				n.m[name] = pn
			}
		}
	}

	// Remove any children objects we have mapped that are no
	// longer relevant.
	for name := range n.children {
		if !currentRoots[name] {
			delete(n.children, name)
		}
	}

	// Importers (mapped as roots for now)
	for _, wi := range impRes.WithAttr {
		pn := wi.Permanode
		db := dres.Meta[pn.String()]
		if db != nil && db.Permanode != nil {
			name := db.Permanode.Attr.Get("camliImportRoot")
			if name != "" {
				name = strings.ReplaceAll(name, ":", "-")
				name = strings.ReplaceAll(name, "/", "-")
				n.m["importer-"+name] = pn
			}
		}
	}

	n.lastQuery = time.Now()
	return nil
}

func (n *rootsDir) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	if n.isRO() {
		return nil, fuse.EPERM
	}

	name := req.Name

	// Create a Permanode for the root.
	pr, err := n.fs.client.UploadNewPermanode(ctx)
	if err != nil {
		Logger.Printf("rootsDir.Create(%q): %v", name, err)
		return nil, fuse.EIO
	}

	var grp syncutil.Group
	// Add a camliRoot attribute to the root permanode.
	grp.Go(func() (err error) {
		claim := schema.NewSetAttributeClaim(pr.BlobRef, "camliRoot", name)
		_, err = n.fs.client.UploadAndSignBlob(ctx, claim)
		return
	})
	// Set the title of the root permanode to the root name.
	grp.Go(func() (err error) {
		claim := schema.NewSetAttributeClaim(pr.BlobRef, "title", name)
		_, err = n.fs.client.UploadAndSignBlob(ctx, claim)
		return
	})
	if err := grp.Err(); err != nil {
		Logger.Printf("rootsDir.Create(%q): %v", name, err)
		return nil, fuse.EIO
	}

	nod := &mutDir{
		fs:        n.fs,
		permanode: pr.BlobRef,
		name:      name,
		xattrs:    map[string][]byte{},
	}
	n.mu.Lock()
	n.m[name] = pr.BlobRef
	n.mu.Unlock()

	return nod, nil
}
