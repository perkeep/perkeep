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

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

const (
	// nodeTypeTakeoutExport is the camliNodeType of the permanode of an
	// archived Takeout export, whose camliContent is a directory of its
	// zip files' contents.
	nodeTypeTakeoutExport = "takeout.google.com:export"

	// exportPermanodeKeyPrefix, plus the export's name, is the key of the
	// planned permanode of an archived export.
	exportPermanodeKeyPrefix = "googleTakeoutExport:"
)

// takeoutArchive archives a whole Takeout export: every file in its zip
// files, in a directory per zip file, in a directory of the export, the
// camliContent of a permanode of the export.
type takeoutArchive struct {
	st      *takeoutStore
	exp     *takeout.Export
	workers int
	verbose bool

	sess       *client.UploadSession // or nil, if the server has none
	flushEvery int                   // if non-zero, instead of archiveFlushEvery

	mu       sync.Mutex
	uploaded map[blob.Ref]bool // uploaded already, as identical blobs repeat
	pending  []blob.Ref        // uploaded into sess, to wait for
}

func (a *takeoutArchive) run(ctx context.Context) (rootDir blob.Ref, err error) {
	ents := a.exp.Entries()
	for _, e := range ents {
		if name := e.File.Name; path.Clean(name) != name || path.IsAbs(name) || strings.HasPrefix(name, "../") || name == ".." {
			return blob.Ref{}, fmt.Errorf("unsafe file name %q in %s", name, e.Zip.Name)
		}
	}
	cs := takeout.Contents(ents)
	log.Printf("Archiving %d files (%d distinct contents) of %d zip files...", len(ents), len(cs), len(a.exp.Zips))

	// The contents that the server doesn't have, such as sidecars and
	// edited copies, without permanodes.
	files := &takeoutStore{c: a.st.c, lim: a.st.lim}
	_, missing, err := takeout.Check(ctx, files, cs, a.workers, nil)
	if err != nil {
		return blob.Ref{}, err
	}
	if len(missing) > 0 {
		log.Printf("Uploading %d contents the server doesn't have (%s)...", len(missing), takeoutBytes(contentsSize(missing)))
		pr := newTakeoutProgress(len(missing), contentsSize(missing))
		done, doneBytes := 0, int64(0)
		failed, err := takeout.Upload(ctx, files, missing, a.workers, func(c *takeout.Content, err error) {
			done++
			doneBytes += c.Size
			if err != nil {
				log.Printf("Error uploading %s: %v", c.Entries[0].File.Name, err)
			}
			pr.update(done, doneBytes)
		})
		if err != nil {
			return blob.Ref{}, err
		}
		if failed > 0 {
			return blob.Ref{}, fmt.Errorf("%d of %d contents failed to upload; run archive again to retry", failed, len(missing))
		}
	}

	if a.sess, err = a.st.c.NewUploadSession(ctx); err != nil {
		if !errors.Is(err, errors.ErrUnsupported) {
			return blob.Ref{}, err
		}
		a.sess = nil
	}
	if a.sess != nil {
		defer a.sess.Close(context.WithoutCancel(ctx))
	}

	// A file schema blob for each entry, with the parts of a file with
	// its contents.
	log.Printf("Writing the file schema blobs of %d files...", len(ents))
	fileRefs := make(map[*takeout.Entry]blob.Ref, len(ents))
	var fmu sync.Mutex
	pr := newTakeoutProgress(len(cs), 0)
	done := 0
	err = takeoutForEach(ctx, cs, a.workers, func(c *takeout.Content) error {
		size, parts, err := a.contentParts(ctx, c)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Entries[0].File.Name, err)
		}
		for i := range c.Entries {
			e := &c.Entries[i]
			fm := schema.NewFileMap(path.Base(e.File.Name))
			if !e.File.Modified.IsZero() {
				fm.SetModTime(e.File.Modified)
			}
			if err := fm.PopulateParts(size, parts); err != nil {
				return err
			}
			br, err := a.upload(ctx, fm.Blob())
			if err != nil {
				return err
			}
			fmu.Lock()
			fileRefs[e] = br
			fmu.Unlock()
		}
		fmu.Lock()
		defer fmu.Unlock()
		done++
		pr.update(done, 0)
		return nil
	})
	if err != nil {
		return blob.Ref{}, err
	}

	// The directories: one per zip file, mirroring its paths, in one of
	// the export.
	zipRoots := make(map[*takeout.Zip]*archiveDir)
	for _, z := range a.exp.Zips {
		zipRoots[z] = newArchiveDir(z.Name)
	}
	for e, br := range fileRefs {
		zipRoots[e.Zip].add(e.File.Name, br)
	}
	var zipDirs []blob.Ref
	for _, z := range a.exp.Zips {
		br, err := a.uploadDir(ctx, zipRoots[z])
		if err != nil {
			return blob.Ref{}, err
		}
		zipDirs = append(zipDirs, br)
	}
	exportDir := newArchiveDir(a.exp.Name)
	for i, z := range a.exp.Zips {
		exportDir.files[z.Name] = zipDirs[i]
	}
	rootDir, err = a.uploadDir(ctx, exportDir)
	if err != nil {
		return blob.Ref{}, err
	}
	if a.sess != nil {
		log.Printf("Waiting for %d schema blobs to be durable...", len(a.pending))
		if err := a.sess.WaitDurable(ctx, a.pending); err != nil {
			return blob.Ref{}, err
		}
	}

	pn, err := a.st.plannedPermanode(ctx, exportPermanodeKeyPrefix+a.exp.Name, false)
	if err != nil {
		return blob.Ref{}, err
	}
	res, err := a.st.c.Describe(ctx, &search.DescribeRequest{BlobRef: pn})
	if err != nil {
		return blob.Ref{}, err
	}
	cur := make(map[string][]string)
	if db := res.Meta.Get(pn); db != nil && db.Permanode != nil {
		cur = db.Permanode.Attr
	}
	// It's not a set, so leave any members as they are.
	changes := setChanges(cur, []attrChange{
		{"set", nodeattr.CamliContent, rootDir.String()},
		{"set", nodeattr.Type, nodeTypeTakeoutExport},
		{"set", nodeattr.Title, fmt.Sprintf("Google Takeout Raw Zips (%s)", a.exp.Name)},
		{"set", nodeattr.DateCreated, takeoutTime(a.exp.Time)},
		{"set", attrTakeoutExport, a.exp.Name},
	}, cur[nodeattr.CamliMember])
	if err := a.st.applyChanges(ctx, pn, changes, a.exp.Time, false); err != nil {
		return blob.Ref{}, err
	}
	log.Printf("Archived %s as directory %v, the content of permanode %v.", a.exp.Name, rootDir, pn)
	return rootDir, nil
}

// contentParts returns the size and parts of a file on the server with
// contents c.
func (a *takeoutArchive) contentParts(ctx context.Context, c *takeout.Content) (int64, []schema.BytesPart, error) {
	if c.Size == 0 {
		return 0, nil, nil
	}
	size := c.Size
	res, err := a.st.c.Query(ctx, &search.SearchQuery{
		Constraint: &search.Constraint{File: &search.FileConstraint{
			FileSize: &search.IntConstraint{Equals: &size},
			Digest:   c.Digest(),
		}},
		Limit: 1,
	})
	if err != nil {
		return 0, nil, err
	}
	if len(res.Blobs) == 0 {
		return 0, nil, errors.New("no file with its contents on the server")
	}
	fb, err := a.st.c.FetchSchemaBlob(ctx, res.Blobs[0].Blob)
	if err != nil {
		return 0, nil, err
	}
	if int64(fb.PartsSize()) != c.Size {
		return 0, nil, fmt.Errorf("file %v has %d bytes, not %d", fb.BlobRef(), fb.PartsSize(), c.Size)
	}
	return c.Size, fb.ByteParts(), nil
}

// upload uploads b, a small schema blob, into the session if any, unless
// it already has.
func (a *takeoutArchive) upload(ctx context.Context, b *schema.Blob) (blob.Ref, error) {
	a.mu.Lock()
	if a.uploaded == nil {
		a.uploaded = make(map[blob.Ref]bool)
	}
	dup := a.uploaded[b.BlobRef()]
	a.uploaded[b.BlobRef()] = true
	a.mu.Unlock()
	if dup {
		return b.BlobRef(), nil
	}
	h := client.NewUploadHandleFromString(b.JSON())
	if a.sess == nil {
		_, err := a.st.c.Upload(ctx, h)
		return b.BlobRef(), err
	}
	if err := a.sess.Upload(ctx, h); err != nil {
		return blob.Ref{}, err
	}
	// The server holds only so many blobs in a session until they're
	// acknowledged, which the session does as it flushes, so wait for
	// them every so often.
	a.mu.Lock()
	a.pending = append(a.pending, b.BlobRef())
	var batch []blob.Ref
	if len(a.pending) >= cmp.Or(a.flushEvery, archiveFlushEvery) {
		batch, a.pending = a.pending, nil
	}
	a.mu.Unlock()
	if batch != nil {
		if err := a.sess.WaitDurable(ctx, batch); err != nil {
			return blob.Ref{}, err
		}
	}
	return b.BlobRef(), nil
}

// archiveFlushEvery is how many blobs takeoutArchive uploads into its
// session between waiting for them to be durable: well under the
// server's limit of blobs not acknowledged (100,000 by default).
const archiveFlushEvery = 10_000

// archiveDir is a directory to archive.
type archiveDir struct {
	name  string
	files map[string]blob.Ref // by name: files, and directories once uploaded
	dirs  map[string]*archiveDir
}

func newArchiveDir(name string) *archiveDir {
	return &archiveDir{name: name, files: make(map[string]blob.Ref), dirs: make(map[string]*archiveDir)}
}

// add adds the file fileRef at name, a slash-separated path under d.
func (d *archiveDir) add(name string, fileRef blob.Ref) {
	dir, rest, ok := strings.Cut(name, "/")
	if !ok {
		d.files[name] = fileRef
		return
	}
	sub, ok := d.dirs[dir]
	if !ok {
		sub = newArchiveDir(dir)
		d.dirs[dir] = sub
	}
	sub.add(rest, fileRef)
}

// uploadDir uploads the directory schema blobs of d and its
// subdirectories, returning d's.
func (a *takeoutArchive) uploadDir(ctx context.Context, d *archiveDir) (blob.Ref, error) {
	members := maps.Clone(d.files)
	for name, sub := range d.dirs {
		br, err := a.uploadDir(ctx, sub)
		if err != nil {
			return blob.Ref{}, err
		}
		members[name] = br
	}
	var refs []blob.Ref
	for _, name := range slices.Sorted(maps.Keys(members)) {
		refs = append(refs, members[name])
	}
	ss := schema.NewStaticSet()
	subsets := ss.SetStaticSetMembers(refs)
	set := ss.Blob()
	dir := schema.NewDirMap(d.name).PopulateDirectoryMap(set.BlobRef()).Blob()
	for _, b := range append(append(subsets, set), dir) {
		if _, err := a.upload(ctx, b); err != nil {
			return blob.Ref{}, err
		}
	}
	return dir.BlobRef(), nil
}
