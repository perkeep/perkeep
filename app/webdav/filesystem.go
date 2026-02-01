/*
Copyright 2025 The Perkeep Authors.

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
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/app"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/search"

	"golang.org/x/net/webdav"
)

// perkeepFS implements webdav.FileSystem backed by Perkeep permanodes.
type perkeepFS struct {
	cl     *client.Client
	signer *app.Signer
	root   blob.Ref

	mu    sync.RWMutex
	cache map[string]*cachedNode
}

type cachedNode struct {
	permanode blob.Ref
	content   blob.Ref
	isDir     bool
	size      int64
	modTime   time.Time
	expires   time.Time
}

const cacheDuration = 30 * time.Second

func newPerkeepFS(cl *client.Client, signer *app.Signer, root blob.Ref) *perkeepFS {
	return &perkeepFS{
		cl:     cl,
		signer: signer,
		root:   root,
		cache:  make(map[string]*cachedNode),
	}
}

type rootEntry struct {
	ref     blob.Ref
	isDir   bool
	size    int64
	modTime time.Time
}

// listRootEntries returns all children of the root permanode.
func (pfs *perkeepFS) listRootEntries(ctx context.Context) (map[string]rootEntry, error) {
	rootRes, err := pfs.cl.Describe(ctx, &search.DescribeRequest{
		BlobRef: pfs.root,
		Depth:   1,
	})
	if err != nil {
		return nil, err
	}

	rootDB := rootRes.Meta[pfs.root.String()]
	if rootDB == nil || rootDB.Permanode == nil {
		return nil, fmt.Errorf("root permanode not found")
	}

	childRefs := make([]blob.Ref, 0)
	childNames := make(map[string]string) // ref string -> name

	for k, v := range rootDB.Permanode.Attr {
		if strings.HasPrefix(k, "camliPath:") && len(v) > 0 {
			name := strings.TrimPrefix(k, "camliPath:")
			if childRef, ok := blob.Parse(v[0]); ok {
				childRefs = append(childRefs, childRef)
				childNames[childRef.String()] = name
			}
		}
	}

	if len(childRefs) == 0 {
		return make(map[string]rootEntry), nil
	}

	// Batch describe all children
	childRes, err := pfs.cl.Describe(ctx, &search.DescribeRequest{
		BlobRefs: childRefs,
		Rules: []*search.DescribeRule{
			{Attrs: []string{"camliContent"}},
		},
	})
	if err != nil {
		return nil, err
	}

	// Build result map and populate cache
	entries := make(map[string]rootEntry, len(childRefs))
	pfs.mu.Lock()
	for refStr, name := range childNames {
		childRef, _ := blob.Parse(refStr)
		entry := rootEntry{ref: childRef}
		var contentRef blob.Ref

		childDB := childRes.Meta[refStr]
		if childDB != nil && childDB.Permanode != nil {
			entry.isDir = isDirectory(childDB.Permanode)

			if !entry.isDir {
				if contentRefStr := childDB.Permanode.Attr.Get("camliContent"); contentRefStr != "" {
					contentRef, _ = blob.Parse(contentRefStr)
					if contentDB := childRes.Meta[contentRefStr]; contentDB != nil && contentDB.File != nil {
						entry.size = contentDB.File.Size
						if !contentDB.File.ModTime.IsAnyZero() {
							entry.modTime = contentDB.File.ModTime.Time()
						}
					}
				}
			}

			if entry.modTime.IsZero() && !childDB.Permanode.ModTime.IsZero() {
				entry.modTime = childDB.Permanode.ModTime
			}
		}

		entries[name] = entry

		// Populate cache so Stat() doesn't need individual lookups
		pfs.cache["/"+name] = &cachedNode{
			permanode: childRef,
			content:   contentRef,
			isDir:     entry.isDir,
			size:      entry.size,
			modTime:   entry.modTime,
			expires:   time.Now().Add(cacheDuration),
		}
	}
	pfs.mu.Unlock()

	return entries, nil
}

func (pfs *perkeepFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	name = cleanPath(name)

	parentPath, childName := path.Split(name)
	parentPath = cleanPath(parentPath)

	parentNode, err := pfs.lookup(ctx, parentPath)
	if err != nil {
		return err
	}
	if !parentNode.isDir {
		return os.ErrInvalid
	}

	if _, err = pfs.lookup(ctx, name); err == nil {
		return os.ErrExist
	}

	newPermanode, err := pfs.signer.UploadNewPermanode(ctx)
	if err != nil {
		return fmt.Errorf("failed to create directory permanode: %w", err)
	}

	if err := pfs.signer.SetAttribute(ctx, newPermanode, "camliNodeType", "directory"); err != nil {
		return fmt.Errorf("failed to set directory type: %w", err)
	}

	if err := pfs.signer.SetAttribute(ctx, newPermanode, "title", childName); err != nil {
		return fmt.Errorf("failed to set directory title: %w", err)
	}

	if err := pfs.signer.SetAttribute(ctx, parentNode.permanode, "camliPath:"+childName, newPermanode.String()); err != nil {
		return fmt.Errorf("failed to link directory to parent: %w", err)
	}

	pfs.invalidateCache(parentPath)
	pfs.invalidateCache(name)

	return nil
}

func (pfs *perkeepFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	name = cleanPath(name)

	creating := flag&os.O_CREATE != 0
	truncating := flag&os.O_TRUNC != 0
	writing := flag&(os.O_WRONLY|os.O_RDWR) != 0

	node, err := pfs.lookup(ctx, name)
	if err != nil {
		if !os.IsNotExist(err) || !creating {
			return nil, err
		}
		if writing {
			return pfs.createFile(ctx, name)
		}
		return nil, fs.ErrNotExist
	}

	if node.isDir {
		return &perkeepDir{
			pfs:       pfs,
			path:      name,
			permanode: node.permanode,
		}, nil
	}

	if writing && truncating {
		return pfs.openFileForWrite(ctx, name, node)
	}

	if writing {
		return pfs.openFileForWrite(ctx, name, node)
	}

	return pfs.openFileForRead(ctx, name, node)
}

func (pfs *perkeepFS) RemoveAll(ctx context.Context, name string) error {
	name = cleanPath(name)

	if name == "/" || name == "" {
		return os.ErrPermission
	}

	node, err := pfs.lookup(ctx, name)
	if err != nil {
		return err
	}

	parentPath, childName := path.Split(name)
	parentPath = cleanPath(parentPath)

	parentNode, err := pfs.lookup(ctx, parentPath)
	if err != nil {
		return err
	}

	if err := pfs.signer.DelAttribute(ctx, parentNode.permanode, "camliPath:"+childName, ""); err != nil {
		return fmt.Errorf("failed to unlink from parent: %w", err)
	}

	pfs.invalidateCache(parentPath)
	pfs.invalidateCache(name)
	if node.isDir {
		pfs.invalidateCachePrefix(name + "/")
	}

	return nil
}

func (pfs *perkeepFS) Rename(ctx context.Context, oldName, newName string) error {
	oldName = cleanPath(oldName)
	newName = cleanPath(newName)

	if oldName == "/" || oldName == "" {
		return os.ErrPermission
	}

	node, err := pfs.lookup(ctx, oldName)
	if err != nil {
		return err
	}

	oldParentPath, oldChildName := path.Split(oldName)
	oldParentPath = cleanPath(oldParentPath)

	newParentPath, newChildName := path.Split(newName)
	newParentPath = cleanPath(newParentPath)

	newParentNode, err := pfs.lookup(ctx, newParentPath)
	if err != nil {
		return err
	}

	if err := pfs.signer.SetAttribute(ctx, newParentNode.permanode, "camliPath:"+newChildName, node.permanode.String()); err != nil {
		return fmt.Errorf("failed to create new link: %w", err)
	}

	oldParentNode, err := pfs.lookup(ctx, oldParentPath)
	if err != nil {
		return err
	}

	if err := pfs.signer.DelAttribute(ctx, oldParentNode.permanode, "camliPath:"+oldChildName, ""); err != nil {
		return fmt.Errorf("failed to remove old link: %w", err)
	}

	if node.isDir {
		if err := pfs.signer.SetAttribute(ctx, node.permanode, "title", newChildName); err != nil {
			log.Printf("Warning: failed to update directory title: %v", err)
		}
	}

	pfs.invalidateCache(oldParentPath)
	pfs.invalidateCache(newParentPath)
	pfs.invalidateCache(oldName)
	pfs.invalidateCache(newName)

	return nil
}

func (pfs *perkeepFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	name = cleanPath(name)

	node, err := pfs.lookup(ctx, name)
	if err != nil {
		return nil, err
	}

	return &perkeepFileInfo{
		name:    path.Base(name),
		size:    node.size,
		mode:    nodeMode(node.isDir),
		modTime: node.modTime,
		isDir:   node.isDir,
	}, nil
}

func (pfs *perkeepFS) lookup(ctx context.Context, name string) (*cachedNode, error) {
	name = cleanPath(name)

	pfs.mu.RLock()
	if cached, ok := pfs.cache[name]; ok && time.Now().Before(cached.expires) {
		pfs.mu.RUnlock()
		return cached, nil
	}
	pfs.mu.RUnlock()

	if name == "/" || name == "" {
		node := &cachedNode{
			permanode: pfs.root,
			isDir:     true,
			modTime:   time.Now(),
			expires:   time.Now().Add(cacheDuration),
		}
		pfs.mu.Lock()
		pfs.cache["/"] = node
		pfs.mu.Unlock()
		return node, nil
	}

	parts := strings.Split(strings.Trim(name, "/"), "/")
	currentRef := pfs.root

	for _, part := range parts {
		res, err := pfs.cl.Describe(ctx, &search.DescribeRequest{
			BlobRef: currentRef,
			Depth:   2,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to describe %v: %w", currentRef, err)
		}

		db := res.Meta[currentRef.String()]
		if db == nil || db.Permanode == nil {
			return nil, fs.ErrNotExist
		}

		childRefStr := db.Permanode.Attr.Get("camliPath:" + part)
		if childRefStr == "" {
			return nil, fs.ErrNotExist
		}

		childRef, ok := blob.Parse(childRefStr)
		if !ok {
			return nil, fmt.Errorf("invalid child ref: %s", childRefStr)
		}

		currentRef = childRef
	}

	res, err := pfs.cl.Describe(ctx, &search.DescribeRequest{
		BlobRef: currentRef,
		Depth:   1,
		Rules: []*search.DescribeRule{
			{Attrs: []string{"camliContent"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe %v: %w", currentRef, err)
	}

	db := res.Meta[currentRef.String()]
	if db == nil {
		return nil, fs.ErrNotExist
	}

	node := &cachedNode{
		permanode: currentRef,
		expires:   time.Now().Add(cacheDuration),
	}

	if db.Permanode != nil {
		node.isDir = isDirectory(db.Permanode)
		if !node.isDir {
			if contentRef := db.Permanode.Attr.Get("camliContent"); contentRef != "" {
				node.content, _ = blob.Parse(contentRef)
				if contentDB := res.Meta[contentRef]; contentDB != nil && contentDB.File != nil {
					node.size = contentDB.File.Size
					if !contentDB.File.ModTime.IsAnyZero() {
						node.modTime = contentDB.File.ModTime.Time()
					}
				}
			}
		}
		if node.modTime.IsZero() && !db.Permanode.ModTime.IsZero() {
			node.modTime = db.Permanode.ModTime
		}
	}

	pfs.mu.Lock()
	pfs.cache[name] = node
	pfs.mu.Unlock()

	return node, nil
}

func (pfs *perkeepFS) createFile(ctx context.Context, name string) (webdav.File, error) {
	parentPath, childName := path.Split(name)
	parentPath = cleanPath(parentPath)

	parentNode, err := pfs.lookup(ctx, parentPath)
	if err != nil {
		return nil, err
	}
	if !parentNode.isDir {
		return nil, os.ErrInvalid
	}

	newPermanode, err := pfs.signer.UploadNewPermanode(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create file permanode: %w", err)
	}

	if err := pfs.signer.SetAttribute(ctx, parentNode.permanode, "camliPath:"+childName, newPermanode.String()); err != nil {
		return nil, fmt.Errorf("failed to link file to parent: %w", err)
	}

	pfs.invalidateCache(parentPath)

	return &perkeepFileWriter{
		pfs:       pfs,
		path:      name,
		name:      childName,
		permanode: newPermanode,
	}, nil
}

func (pfs *perkeepFS) openFileForRead(ctx context.Context, name string, node *cachedNode) (webdav.File, error) {
	if !node.content.Valid() {
		return &perkeepFileReader{
			pfs:       pfs,
			path:      name,
			permanode: node.permanode,
			size:      0,
		}, nil
	}

	return &perkeepFileReader{
		pfs:       pfs,
		path:      name,
		permanode: node.permanode,
		content:   node.content,
		size:      node.size,
	}, nil
}

func (pfs *perkeepFS) openFileForWrite(ctx context.Context, name string, node *cachedNode) (webdav.File, error) {
	return &perkeepFileWriter{
		pfs:       pfs,
		path:      name,
		name:      path.Base(name),
		permanode: node.permanode,
	}, nil
}

func (pfs *perkeepFS) invalidateCache(path string) {
	pfs.mu.Lock()
	delete(pfs.cache, path)
	pfs.mu.Unlock()
}

func (pfs *perkeepFS) invalidateCachePrefix(prefix string) {
	pfs.mu.Lock()
	for k := range pfs.cache {
		if strings.HasPrefix(k, prefix) {
			delete(pfs.cache, k)
		}
	}
	pfs.mu.Unlock()
}

func cleanPath(name string) string {
	name = path.Clean("/" + name)
	if name == "" {
		name = "/"
	}
	return name
}

func isDirectory(p *search.DescribedPermanode) bool {
	if p.Attr.Get("camliNodeType") == "directory" {
		return true
	}
	for k := range p.Attr {
		if strings.HasPrefix(k, "camliPath:") {
			return true
		}
	}
	return false
}

func nodeMode(isDir bool) os.FileMode {
	if isDir {
		return os.ModeDir | 0755
	}
	return 0644
}

type perkeepFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (fi *perkeepFileInfo) Name() string       { return fi.name }
func (fi *perkeepFileInfo) Size() int64        { return fi.size }
func (fi *perkeepFileInfo) Mode() os.FileMode  { return fi.mode }
func (fi *perkeepFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *perkeepFileInfo) IsDir() bool        { return fi.isDir }
func (fi *perkeepFileInfo) Sys() any           { return nil }
