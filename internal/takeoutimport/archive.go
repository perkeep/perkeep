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

package takeoutimport

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

const (
	// nodeTypeTakeoutExport is the camliNodeType of the permanode of an
	// archived Takeout export, whose camliContent is a directory of its
	// zip files' contents.
	nodeTypeTakeoutExport = "takeout.google.com:export"

	// ExportPermanodeKeyPrefix, plus the export's name, is the key of the
	// planned permanode of an archived export.
	ExportPermanodeKeyPrefix = "googleTakeoutExport:"
)

// archive archives a whole Takeout export: every file in its zip files,
// in a directory per zip file, in a directory of the export, the
// camliContent of a permanode of the export.
type archive struct {
	im    *Importer
	exp   *takeout.Export
	batch Batch

	mu       sync.Mutex
	uploaded map[blob.Ref]bool // uploaded already, as identical blobs repeat
}

// Archive archives all of exp's files, of every kind: a directory of each
// zip file's files, in a directory of the export, which is the content of
// a permanode titled "Google Takeout Raw Zips (<export>)". Files the
// server has aren't read again. It returns the export's directory.
func (im *Importer) Archive(ctx context.Context, exp *takeout.Export) (rootDir blob.Ref, err error) {
	a := &archive{im: im, exp: exp}
	return a.run(ctx)
}

func (a *archive) run(ctx context.Context) (rootDir blob.Ref, err error) {
	ents := a.exp.Entries()
	for _, e := range ents {
		if name := e.File.Name; path.Clean(name) != name || path.IsAbs(name) || strings.HasPrefix(name, "../") || name == ".." {
			return blob.Ref{}, fmt.Errorf("unsafe file name %q in %s", name, e.Zip.Name)
		}
	}
	cs := takeout.Contents(ents)
	a.im.logf("Archiving %d files (%d distinct contents) of %d zip files...", len(ents), len(cs), len(a.exp.Zips))

	// The contents that the server doesn't have, such as sidecars and
	// edited copies, without permanodes.
	files := a.im.store(false)
	cpr := a.im.newProgress("archive-check", "Checking which of the export's files the server has", len(cs), 0)
	_, missing, err := takeout.Check(ctx, files, cs, a.im.workers(), func(done int) { cpr.update(done, 0) })
	cpr.finish()
	if err != nil {
		return blob.Ref{}, err
	}
	if len(missing) > 0 {
		a.im.logf("Uploading %d contents the server doesn't have (%s)...", len(missing), FormatBytes(ContentsSize(missing)))
		pr := a.im.newProgress("archive-upload", "Uploading the export's other files", len(missing), ContentsSize(missing))
		defer pr.finish()
		done, doneBytes := 0, int64(0)
		failed, err := takeout.Upload(ctx, files, missing, a.im.workers(), func(c *takeout.Content, err error) {
			done++
			doneBytes += c.Size
			if err != nil {
				a.im.logf("Error uploading %s: %v", c.Entries[0].File.Name, err)
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

	if a.batch, err = a.im.Server.NewBatch(ctx); err != nil {
		return blob.Ref{}, err
	}
	defer a.batch.Close()

	// A file schema blob for each entry, with the parts of a file with
	// its contents.
	a.im.logf("Writing the file schema blobs of %d files...", len(ents))
	fileRefs := make(map[*takeout.Entry]blob.Ref, len(ents))
	var fmu sync.Mutex
	pr := a.im.newProgress("archive-schemas", "Writing the export's file schema blobs", len(cs), 0)
	defer pr.finish()
	done := 0
	err = forEach(ctx, cs, a.im.workers(), func(c *takeout.Content) error {
		size, parts, err := a.contentParts(ctx, c)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Entries[0].File.Name, err)
		}
		for i := range c.Entries {
			e := &c.Entries[i]
			// No modification time: Takeout gives each entry the
			// time the export was made. (See store.Put.)
			fm := schema.NewFileMap(path.Base(e.File.Name))
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
	a.im.logf("Waiting for the schema blobs to be stored...")
	if err := a.batch.Flush(ctx); err != nil {
		return blob.Ref{}, err
	}

	pn, err := a.im.plannedPermanode(ctx, ExportPermanodeKeyPrefix+a.exp.Name)
	if err != nil {
		return blob.Ref{}, err
	}
	res, err := a.im.Server.Describe(ctx, &search.DescribeRequest{BlobRef: pn})
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
	// Its claims are dated by the export, like its other attributes,
	// unless it's archived already, as another directory (by an older
	// pk takeout): a claim of the old one might be dated the same, and
	// then win.
	claimTime := a.exp.Time
	if old := cur[nodeattr.CamliContent]; len(old) > 0 && !slices.Equal(old, []string{rootDir.String()}) {
		claimTime = time.Now()
	}
	if err := a.im.applyChanges(ctx, pn, changes, claimTime); err != nil {
		return blob.Ref{}, err
	}
	a.im.logf("Archived %s as directory %v, the content of permanode %v.", a.exp.Name, rootDir, pn)
	return rootDir, nil
}

// contentParts returns the size and parts of a file on the server with
// contents c.
func (a *archive) contentParts(ctx context.Context, c *takeout.Content) (int64, []schema.BytesPart, error) {
	if c.Size == 0 {
		return 0, nil, nil
	}
	size := c.Size
	res, err := a.im.Server.Query(ctx, &search.SearchQuery{
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
	fb, err := a.im.Server.FetchSchemaBlob(ctx, res.Blobs[0].Blob)
	if err != nil {
		return 0, nil, err
	}
	if int64(fb.PartsSize()) != c.Size {
		return 0, nil, fmt.Errorf("file %v has %d bytes, not %d", fb.BlobRef(), fb.PartsSize(), c.Size)
	}
	return c.Size, fb.ByteParts(), nil
}

// upload uploads b, a small schema blob, into the batch, unless it
// already has.
func (a *archive) upload(ctx context.Context, b *schema.Blob) (blob.Ref, error) {
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
	return b.BlobRef(), a.batch.Put(ctx, b.JSON())
}

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
func (a *archive) uploadDir(ctx context.Context, d *archiveDir) (blob.Ref, error) {
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
