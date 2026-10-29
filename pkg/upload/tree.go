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

package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
)

// TreeOptions are the options of a Tree.
type TreeOptions struct {
	// Limiter is the Limiter of the Tree's files' resources. If nil, the
	// Tree gets its own, with the default limits.
	Limiter *Limiter

	// File are the options of each of the Tree's files. Their Session
	// and Limiter are the Tree's, and their WholeRefs and ModTime are
	// ignored.
	File Options

	// OpenFiles is how many files the Tree uploads at once. The default
	// is 16.
	OpenFiles int

	// Ignore, if non-nil, reports whether to leave out the file or
	// directory at path, as if it weren't there.
	Ignore func(path string, fi fs.FileInfo) bool

	// Cache, if non-nil, is the cache of files uploaded before, whose
	// file schema blobs the Tree uses without reading them again.
	Cache StatCache
}

// A StatCache remembers the file schema blobs of files uploaded before,
// by their path and FileInfo (in practice, its inode, size and
// modification time), as pk-put's does. A Tree adds a file to it only
// once its file schema blob is durable.
//
// Its methods may be called concurrently.
type StatCache interface {
	FileRef(path string, fi fs.FileInfo) (blob.Ref, bool)
	AddFileRef(path string, fi fs.FileInfo, fileRef blob.Ref)
}

// A Tree is an upload of a directory tree: its files, as by File, and
// its directories, symlinks, and other files, each with the schema blobs
// pk-put has always made for them. Make one with NewTree.
//
// It uploads into one upload session, if the server supports them. It
// uploads a directory's static set and directory schema blobs once its
// children are durable.
type Tree struct {
	c    *client.Client
	fsys fs.FS
	dir  string
	opts TreeOptions
	lim  *Limiter

	mu       sync.Mutex
	progress TreeProgress
	inFlight map[*File]TreeFile // regular files being uploaded
	recent   []TreeFileDone     // the last treeRecentFiles done, oldest first
}

// NewTree returns an upload of the directory tree at dir of fsys to c,
// such as of os.DirFS(root). It uploads symlinks if fsys implements
// fs.ReadLinkFS, and otherwise follows them. opts may be nil.
func NewTree(c *client.Client, fsys fs.FS, dir string, opts *TreeOptions) *Tree {
	t := &Tree{c: c, fsys: fsys, dir: dir}
	if opts != nil {
		t.opts = *opts
	}
	if t.opts.OpenFiles <= 0 {
		t.opts.OpenFiles = 16
	}
	t.lim = t.opts.Limiter
	if t.lim == nil {
		t.lim = NewLimiter(Limits{})
	}
	return t
}

// TreeProgress is the progress of a Tree's upload.
type TreeProgress struct {
	Walked bool // whether the tree's been walked; if not, the totals are so far

	Files, Dirs int   // in the tree, other than directories, and directories
	Bytes       int64 // of the regular files

	FilesDone   int   // uploaded, or found in the Cache
	FilesCached int   // of FilesDone, found in the Cache
	FilesDup    int   // of FilesDone, whose contents the server had
	DirsDone    int   // uploaded
	BytesDone   int64 // of the regular files done

	// BytesUploaded and ChunksUploaded are of the chunks of the files
	// done that the server lacked, and ChunksPresent of those it had.
	BytesUploaded  int64
	ChunksUploaded int
	ChunksPresent  int
}

// A TreeFile is a regular file that a Tree is uploading.
type TreeFile struct {
	Path     string // in the Tree's fs.FS
	Size     int64
	Started  time.Time
	Progress Progress
}

// A TreeFileDone is a regular file that a Tree uploaded.
type TreeFileDone struct {
	Path          string
	Size          int64
	Started       time.Time
	Done          time.Time
	Dup           bool  // the server had its contents
	BytesUploaded int64 // of its chunks the server lacked
	Err           string
}

// treeRecentFiles is how many of the files it uploaded last a Tree keeps,
// for Status.
const treeRecentFiles = 100

// TreeStatus is the detailed state of a Tree's upload, for showing it.
type TreeStatus struct {
	Progress TreeProgress
	InFlight []TreeFile     // sorted by when they started
	Recent   []TreeFileDone // newest first
}

// Status returns the upload's progress, with the files being uploaded
// and those uploaded most recently. It's safe to call at any time,
// including during Run.
func (t *Tree) Status() TreeStatus {
	t.mu.Lock()
	st := TreeStatus{Progress: t.progress}
	files := make(map[*File]TreeFile, len(t.inFlight))
	for f, tf := range t.inFlight {
		files[f] = tf
	}
	for i := len(t.recent) - 1; i >= 0; i-- {
		st.Recent = append(st.Recent, t.recent[i])
	}
	t.mu.Unlock()
	for f, tf := range files {
		tf.Progress = f.Progress()
		st.InFlight = append(st.InFlight, tf)
	}
	slices.SortFunc(st.InFlight, func(a, b TreeFile) int { return a.Started.Compare(b.Started) })
	return st
}

// noteRecentLocked records a file done, for Status. t.mu must be held.
func (t *Tree) noteRecentLocked(d TreeFileDone) {
	if len(t.recent) == treeRecentFiles {
		t.recent = slices.Delete(t.recent, 0, 1)
	}
	t.recent = append(t.recent, d)
}

// Progress returns the upload's progress. It's safe to call at any time,
// including during Run.
func (t *Tree) Progress() TreeProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.progress
}

func (t *Tree) update(fn func(*TreeProgress)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.progress)
}

// TreeResult is the result of a Tree's upload.
type TreeResult struct {
	DirRef blob.Ref // of the tree's root directory schema blob, durable
}

// A treeNode is a file or directory of a Tree.
type treeNode struct {
	path     string
	fi       fs.FileInfo
	parent   *treeNode
	children []*treeNode

	left atomic.Int32 // children not yet done

	// Set when done:
	ref blob.Ref
	// pending are the refs, of this node's blobs and those they refer
	// to, that were uploaded and may not be durable yet.
	pending []blob.Ref
	// cacheFile is whether, once pending is durable, ref is to be added
	// to the StatCache.
	cacheFile bool
}

// Run uploads the tree, returning once it's all durable.
func (t *Tree) Run(ctx context.Context) (*TreeResult, error) {
	fi, err := fs.Stat(t.fsys, t.dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("upload: %s isn't a directory", t.dir)
	}
	root := &treeNode{path: t.dir, fi: fi}
	if err := t.walk(root); err != nil {
		return nil, err
	}
	t.update(func(p *TreeProgress) { p.Walked = true })

	s, err := t.c.NewUploadSession(ctx)
	if err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return nil, err
	}
	if s != nil {
		// Its blobs are durable once Run returns successfully, or the
		// upload failed; either way, there's nothing more to learn from it.
		defer s.Close(context.Background())
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(t.opts.OpenFiles)
	var leaves func(n *treeNode)
	leaves = func(n *treeNode) {
		if n.fi.IsDir() && len(n.children) > 0 {
			for _, c := range n.children {
				leaves(c)
			}
			return
		}
		g.Go(func() error { return t.finish(gctx, s, n) })
	}
	leaves(root)
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if s != nil && len(root.pending) > 0 {
		if err := s.WaitDurable(ctx, root.pending); err != nil {
			return nil, err
		}
	}
	return &TreeResult{DirRef: root.ref}, nil
}

// walk finds the tree under the directory n.
func (t *Tree) walk(n *treeNode) error {
	t.update(func(p *TreeProgress) { p.Dirs++ })
	ents, err := fs.ReadDir(t.fsys, n.path)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		p := path.Join(n.path, ent.Name())
		fi, err := fs.Lstat(t.fsys, p)
		if err != nil {
			return err
		}
		if t.opts.Ignore != nil && t.opts.Ignore(p, fi) {
			continue
		}
		n.children = append(n.children, &treeNode{path: p, fi: fi, parent: n})
	}
	// Files before directories, each by name, as pk-put has always put
	// them in a directory's static set.
	sort.SliceStable(n.children, func(i, j int) bool {
		ci, cj := n.children[i].fi.IsDir(), n.children[j].fi.IsDir()
		if ci != cj {
			return cj
		}
		return n.children[i].fi.Name() < n.children[j].fi.Name()
	})
	n.left.Store(int32(len(n.children)))
	for _, c := range n.children {
		if c.fi.IsDir() {
			if err := t.walk(c); err != nil {
				return err
			}
			continue
		}
		t.update(func(p *TreeProgress) {
			p.Files++
			if c.fi.Mode().IsRegular() {
				p.Bytes += c.fi.Size()
			}
		})
	}
	return nil
}

// finish uploads n, which has no children left to upload, and then its
// parent, if n was its last child left, and so on.
func (t *Tree) finish(ctx context.Context, s *client.UploadSession, n *treeNode) error {
	for ; n != nil; n = n.parent {
		var err error
		if n.fi.IsDir() {
			err = t.uploadDir(ctx, s, n)
		} else {
			err = t.uploadFile(ctx, s, n)
		}
		if err != nil {
			return fmt.Errorf("upload: %s: %w", n.path, err)
		}
		if n.parent != nil && n.parent.left.Add(-1) != 0 {
			return nil // a sibling will finish the parent
		}
	}
	return nil
}

// uploadFile uploads n, which isn't a directory.
func (t *Tree) uploadFile(ctx context.Context, s *client.UploadSession, n *treeNode) error {
	mode := n.fi.Mode()
	if !mode.IsRegular() {
		bb := schema.NewCommonFileMap(n.path, n.fi)
		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := fs.ReadLink(t.fsys, n.path)
			if err != nil {
				return err
			}
			bb.SetSymlinkTarget(target)
		case mode&fs.ModeDevice != 0, mode&fs.ModeSocket != 0:
			bb.SetType(schema.TypeSocket)
		case mode&fs.ModeNamedPipe != 0:
			bb.SetType(schema.TypeFIFO)
		default:
			return fmt.Errorf("unsupported file type %v", mode)
		}
		b := bb.Blob()
		pending, err := t.uploadBlobs(ctx, s, []*schema.Blob{b})
		if err != nil {
			return err
		}
		n.ref, n.pending = b.BlobRef(), pending
		t.update(func(p *TreeProgress) { p.FilesDone++ })
		return nil
	}

	if c := t.opts.Cache; c != nil {
		if ref, ok := c.FileRef(n.path, n.fi); ok {
			n.ref = ref
			t.update(func(p *TreeProgress) {
				p.FilesDone++
				p.FilesCached++
				p.BytesDone += n.fi.Size()
			})
			return nil
		}
	}
	f, err := t.fsys.Open(n.path)
	if err != nil {
		return err
	}
	defer f.Close()
	ra, ok := f.(io.ReaderAt)
	if !ok {
		return errors.New("file doesn't implement io.ReaderAt")
	}
	opts := t.opts.File
	opts.Session, opts.Limiter = s, t.lim
	opts.WholeRefs, opts.ModTime = nil, time.Time{}
	file := NewFile(t.c, n.fi, ra, &opts)
	started := time.Now()
	t.mu.Lock()
	if t.inFlight == nil {
		t.inFlight = make(map[*File]TreeFile)
	}
	t.inFlight[file] = TreeFile{Path: n.path, Size: n.fi.Size(), Started: started}
	t.mu.Unlock()
	res, err := file.Run(ctx)
	pr := file.Progress()
	t.mu.Lock()
	delete(t.inFlight, file)
	done := TreeFileDone{Path: n.path, Size: n.fi.Size(), Started: started, Done: time.Now(), BytesUploaded: pr.BytesUploaded}
	if err != nil {
		done.Err = err.Error()
	} else {
		done.Dup = res.Dup
		t.progress.BytesUploaded += pr.BytesUploaded
		t.progress.ChunksUploaded += pr.ChunksUploaded
		t.progress.ChunksPresent += pr.ChunksPresent
		if res.Dup {
			t.progress.FilesDup++
		}
	}
	t.noteRecentLocked(done)
	t.mu.Unlock()
	if err != nil {
		return err
	}
	n.ref = res.FileRef
	if res.Durable {
		if c := t.opts.Cache; c != nil {
			c.AddFileRef(n.path, n.fi, n.ref)
		}
	} else {
		n.pending, n.cacheFile = []blob.Ref{n.ref}, true
	}
	t.update(func(p *TreeProgress) {
		p.FilesDone++
		p.BytesDone += n.fi.Size()
	})
	return nil
}

// uploadDir uploads the directory n, whose children are done.
func (t *Tree) uploadDir(ctx context.Context, s *client.UploadSession, n *treeNode) error {
	// What the static sets refer to must be durable first.
	var pending []blob.Ref
	for _, c := range n.children {
		pending = append(pending, c.pending...)
	}
	if s != nil && len(pending) > 0 {
		if err := s.WaitDurable(ctx, pending); err != nil {
			return err
		}
	}
	for _, c := range n.children {
		if c.cacheFile && t.opts.Cache != nil {
			t.opts.Cache.AddFileRef(c.path, c.fi, c.ref)
		}
		c.children = nil // done with them
	}

	members := make([]blob.Ref, len(n.children))
	for i, c := range n.children {
		members[i] = c.ref
	}
	ss := schema.NewStaticSet()
	subsets := ss.SetStaticSetMembers(members)
	set := ss.Blob()
	bb := schema.NewCommonFileMap(n.path, n.fi)
	bb.PopulateDirectoryMap(set.BlobRef())
	dir := bb.Blob()
	// The directory schema blob goes with its static sets: the parent
	// waits for them all before referring to it.
	blobs := append(append([]*schema.Blob{set}, subsets...), dir)
	var err error
	if n.pending, err = t.uploadBlobs(ctx, s, blobs); err != nil {
		return err
	}
	n.ref = dir.BlobRef()
	t.update(func(p *TreeProgress) { p.DirsDone++ })
	return nil
}

// uploadBlobs uploads blobs, small schema blobs, into s if non-nil,
// returning the refs of those uploaded into s. It doesn't ask the server
// whether it has them first, which would take as long as uploading
// them; uploads skip those the have cache has.
func (t *Tree) uploadBlobs(ctx context.Context, s *client.UploadSession, blobs []*schema.Blob) ([]blob.Ref, error) {
	var pending []blob.Ref
	for _, b := range blobs {
		h := &client.UploadHandle{BlobRef: b.BlobRef(), Size: uint32(len(b.JSON())), Contents: strings.NewReader(b.JSON())}
		if s == nil {
			if _, err := t.c.Upload(ctx, h); err != nil {
				return nil, err
			}
			continue
		}
		if err := s.Upload(ctx, h); err != nil {
			return nil, err
		}
		pending = append(pending, h.BlobRef)
	}
	return pending, nil
}
