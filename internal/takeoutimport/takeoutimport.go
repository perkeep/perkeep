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

// Package takeoutimport imports the Google Photos photos and albums of
// Google Takeout exports, and archives whole exports, into a Perkeep
// server, for both pk takeout and perkeepd.
package takeoutimport

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/progress"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

// Server is how an [Importer] reaches the Perkeep server it imports into:
// over HTTP, with a [Client], for pk takeout, or directly, for perkeepd.
type Server interface {
	Query(context.Context, *search.SearchQuery) (*search.SearchResult, error)
	Describe(context.Context, *search.DescribeRequest) (*search.DescribeResponse, error)
	GetClaims(context.Context, *search.ClaimsRequest) (*search.ClaimsResponse, error)
	FetchSchemaBlob(context.Context, blob.Ref) (*schema.Blob, error)

	// PutBlob uploads the blob with the given contents, a small one,
	// such as a signed claim, returning its ref.
	PutBlob(ctx context.Context, contents string) (blob.Ref, error)

	// PutFile uploads the contents r of a file named name, with no
	// modification time, returning the ref of its file schema blob and
	// the whole-file ref of its contents.
	PutFile(ctx context.Context, name string, r io.Reader) (file, whole blob.Ref, err error)

	// NewBatch returns a [Batch] for uploading many small blobs.
	NewBatch(context.Context) (Batch, error)
}

// A Batch uploads many small blobs, such as an archive's schema blobs.
// It may not have stored them until Flush returns.
type Batch interface {
	// Put uploads the blob with the given contents, whose ref is
	// [blob.RefFromString] of them.
	Put(ctx context.Context, contents string) error

	// Flush waits for the blobs Put so far to be stored.
	Flush(context.Context) error

	Close() error
}

// An Importer imports Takeout exports into a [Server].
type Importer struct {
	Server Server
	Signer *schema.Signer // to sign permanodes and claims

	Workers int  // how many files to check or upload at once
	Verbose bool // log each uploaded file and changed album

	// Logf, if non-nil, logs progress, instead of log.Printf.
	Logf func(format string, args ...any)

	// Progress, if non-nil, gets a progress bar of each step of an
	// import, such as uploading, rather than the step's progress being
	// logged every few seconds.
	Progress *progress.Set
}

func (im *Importer) logf(format string, args ...any) {
	if im.Logf != nil {
		im.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (im *Importer) workers() int { return max(1, im.Workers) }

// CheckServer checks that the server indexes files' digests. A server
// that doesn't would ignore the Digest of a query, and match any file of
// the same size.
func (im *Importer) CheckServer(ctx context.Context) error {
	_, err := im.Server.Query(ctx, &search.SearchQuery{
		Constraint: &search.Constraint{File: &search.FileConstraint{Digest: "crc32-invalid"}},
		Limit:      1,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid digest") {
		return fmt.Errorf("the server doesn't support searching files by digest; it needs upgrading (to index schema version 11 or later). Checking returned error: %v", err)
	}
	return nil
}

// Check reports which of cs the server has, as takeout.Check does.
func (im *Importer) Check(ctx context.Context, cs []*takeout.Content) (have, missing []*takeout.Content, err error) {
	pr := im.newProgress("check", "Checking which files the server has", len(cs), 0)
	defer pr.finish()
	return takeout.Check(ctx, im.store(false), cs, im.workers(), func(done int) { pr.update(done, 0) })
}

// Options are the options of Import.
type Options struct {
	// Permanodes is whether to make permanodes of the uploaded files,
	// with their Google Photos metadata, sync that of the photos the
	// server has, keep the albums, and archive the export.
	Permanodes bool

	// All is whether cs are all of the export's files, not just its
	// photos and videos, in which case Import only uploads them.
	All bool
}

// Import imports the photos cs of exp, of which the server doesn't have
// missing: it uploads them, with permanodes with their metadata,
// archives exp, and then syncs the metadata of the photos the server
// had, and the albums. Without opts.Permanodes, or with opts.All, it only
// uploads them.
func (im *Importer) Import(ctx context.Context, exp *takeout.Export, cs, missing []*takeout.Content, opts Options) (*MetaStats, error) {
	if !opts.Permanodes || opts.All {
		// Without permanodes, there's nothing to set metadata on,
		// and the files of -all aren't all photos.
		return nil, im.upload(ctx, im.store(false), missing)
	}
	st := im.store(true)
	if len(missing) > 0 {
		// Read the photos' metadata first, so each permanode gets it
		// with its contents, rather than after all the uploads.
		im.logf("Reading the metadata of %d photos to upload...", len(missing))
		if err := st.readMeta(ctx, exp, missing); err != nil {
			return nil, err
		}
	}
	if err := im.upload(ctx, st, missing); err != nil {
		return nil, err
	}
	if err := im.archiveNew(ctx, exp); err != nil {
		return nil, err
	}
	return im.SyncMeta(ctx, exp, cs)
}

// archiveNew archives exp, as Archive does, unless it's archived
// already: its permanode has contents. Importing reads the export's
// photos and sidecars anyway, so keeping all of it costs little more,
// and puts its sidecars on the server too.
func (im *Importer) archiveNew(ctx context.Context, exp *takeout.Export) error {
	pn, err := im.plannedPermanodeRef(ctx, ExportPermanodeKeyPrefix+exp.Name)
	if err != nil {
		return err
	}
	res, err := im.Server.Describe(ctx, &search.DescribeRequest{BlobRef: pn})
	if err != nil {
		return err
	}
	if db := res.Meta.Get(pn); db != nil && db.Permanode != nil && db.Permanode.Attr.Get(nodeattr.CamliContent) != "" {
		im.logf("%s is archived already, as permanode %v.", exp.Name, pn)
		return nil
	}
	_, err = im.Archive(ctx, exp)
	return err
}

// SyncMeta applies the metadata of the photos cs of exp, and its albums,
// to their permanodes.
func (im *Importer) SyncMeta(ctx context.Context, exp *takeout.Export, cs []*takeout.Content) (*MetaStats, error) {
	im.logf("Reading the metadata of %d photos...", len(cs))
	lib, err := im.newLibrary(ctx, exp, cs, "meta-read", "Reading the photos' metadata")
	if err != nil {
		return nil, err
	}
	ms := &metaSync{im: im, exp: exp, lib: lib}
	err = ms.run(ctx)
	return &ms.stats, err
}

// upload uploads the missing contents to st.
func (im *Importer) upload(ctx context.Context, st *store, missing []*takeout.Content) error {
	if len(missing) == 0 {
		return nil
	}
	im.logf("Uploading %d files (%s)...", len(missing), FormatBytes(ContentsSize(missing)))
	pr := im.newProgress("upload", "Uploading", len(missing), ContentsSize(missing))
	defer pr.finish()
	done, doneBytes := 0, int64(0)
	failed, err := takeout.Upload(ctx, st, missing, im.workers(), func(mc *takeout.Content, err error) {
		done++
		doneBytes += mc.Size
		name := mc.Entries[0].File.Name
		if err != nil {
			im.logf("Error uploading %s: %v", name, err)
		} else if im.Verbose {
			im.logf("Uploaded %s (%s)", name, FormatBytes(mc.Size))
		}
		pr.update(done, doneBytes)
	})
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d files failed to upload", failed, len(missing))
	}
	im.logf("Uploaded %d files.", len(missing))
	return nil
}

// ContentsSize returns the total size of cs.
func ContentsSize(cs []*takeout.Content) int64 {
	var n int64
	for _, c := range cs {
		n += c.Size
	}
	return n
}

// FormatBytes formats n bytes in binary units, such as "1.5 GiB".
func FormatBytes(n int64) string { return progress.FormatBytes(n) }

// step tracks the progress of a step of an import through a number of
// items, as a bar of im.Progress, or if im has no Progress, by logging it
// at most every few seconds. If the items have sizes, the bar counts
// bytes, as their sizes vary a lot.
type step struct {
	im    *Importer
	bar   *progress.Bar
	total int
	bytes bool
	last  time.Time
	done  atomic.Int64 // items done
}

// newProgress returns the step named name, with the title title, of
// total items, of totalBytes bytes, or zero if they have no sizes.
func (im *Importer) newProgress(name, title string, total int, totalBytes int64) *step {
	set := im.Progress
	if set == nil {
		set = new(progress.Set)
	}
	p := &step{im: im, bar: set.Bar(name, title), total: total, bytes: totalBytes > 0, last: time.Now()}
	if p.bytes {
		p.bar.SetUnit(progress.Bytes)
		p.bar.SetTotal(totalBytes)
		p.bar.SetDescription(fmt.Sprintf("0 of %d files", total))
	} else {
		p.bar.SetTotal(int64(total))
	}
	return p
}

// update records that done items, of doneBytes bytes, are done.
func (p *step) update(done int, doneBytes int64) {
	p.done.Store(int64(done))
	if p.bytes {
		p.bar.Set(doneBytes)
		p.bar.SetDescription(fmt.Sprintf("%d of %d files", done, p.total))
	} else {
		p.bar.Set(int64(done))
	}
	now := time.Now()
	if p.im.Progress != nil || now.Sub(p.last) < 5*time.Second || done == p.total {
		return
	}
	p.last = now
	detail := p.bar.Status().Detail()
	if p.bytes {
		detail = fmt.Sprintf("%d of %d files, %s", done, p.total, detail)
	}
	p.im.logf("%s: %s", p.bar.Status().Title, detail)
}

// finish marks the step finished.
// finish marks the step's bar finished, if all of its items were done, or
// else stopped short, as when the step failed or its run was stopped.
func (p *step) finish() {
	if p.done.Load() >= int64(p.total) {
		p.bar.Finish()
	} else {
		p.bar.Stop()
	}
}

// newLibrary returns takeout.NewLibrary's Library of the photos cs of
// exp, as a step named name, with the title title.
func (im *Importer) newLibrary(ctx context.Context, exp *takeout.Export, cs []*takeout.Content, name, title string) (*takeout.Library, error) {
	var pr *step
	defer func() {
		if pr != nil {
			pr.finish()
		}
	}()
	return takeout.NewLibrary(ctx, exp, cs, im.workers(), func(done, total int) {
		if pr == nil {
			pr = im.newProgress(name, title, total, 0)
		}
		pr.update(done, 0)
	})
}

// store is a takeout.Store of the Importer's server.
type store struct {
	im         *Importer
	permanodes bool

	// meta, if non-nil, is the Google Photos metadata of the photos to
	// upload, by the names of their entries, which Put sets on their
	// permanodes in the claims of their camliContent. Its claims are
	// dated as metaSync's are: by a photo's modification time in Google
	// Photos, or else metaTime, the export's.
	meta     map[string]*takeout.PhotoMetadata
	metaTime time.Time
	exp      string // the export of meta
}

func (im *Importer) store(permanodes bool) *store {
	return &store{im: im, permanodes: permanodes}
}

// readMeta reads the Google Photos metadata of the photos cs of exp, for
// Put to set on their permanodes.
func (s *store) readMeta(ctx context.Context, exp *takeout.Export, cs []*takeout.Content) error {
	lib, err := s.im.newLibrary(ctx, exp, cs, "upload-meta", "Reading the metadata of the photos to upload")
	if err != nil {
		return err
	}
	s.meta, s.metaTime, s.exp = make(map[string]*takeout.PhotoMetadata), exp.Time, exp.Name
	for _, p := range lib.Photos {
		for _, e := range p.Content.Entries {
			s.meta[e.File.Name] = p.Meta
		}
	}
	return nil
}

func (s *store) Has(ctx context.Context, size int64, crc32 uint32) (bool, error) {
	res, err := s.im.Server.Query(ctx, &search.SearchQuery{
		Constraint: &search.Constraint{File: &search.FileConstraint{
			FileSize: &search.IntConstraint{Equals: &size},
			Digest:   fmt.Sprintf("crc32-%08x", crc32),
		}},
		Limit: 1,
	})
	if err != nil {
		return false, err
	}
	return len(res.Blobs) > 0, nil
}

// Put uploads the contents r of e. It records no modification time:
// Takeout's zip files give each entry the time the export was made,
// which says nothing of the photo. Its date is in its sidecar, which
// Put sets on its permanode, with s.meta, or syncing metadata does.
func (s *store) Put(ctx context.Context, e takeout.Entry, r io.Reader) error {
	file, whole, err := s.im.Server.PutFile(ctx, path.Base(e.File.Name), r)
	if err != nil {
		return err
	}
	if !s.permanodes {
		return nil
	}
	var changes []attrChange
	claimTime := time.Now()
	if s.meta != nil {
		claimTime = s.metaTime
		if m := s.meta[e.File.Name]; m != nil {
			changes = photoAttrChanges(url.Values{}, m)
			changes = append(changes, attrChange{"set", attrTakeoutExport, s.exp})
			if !m.Modified.IsZero() {
				claimTime = m.Modified
			}
		}
	}
	// Last, so that a permanode never has the contents without the
	// metadata, even if there's too much metadata for one claim.
	changes = append(changes, attrChange{"set", nodeattr.CamliContent, file.String()})
	pn, err := s.im.plannedPermanode(ctx, whole.String())
	if err != nil {
		return err
	}
	return s.im.applyChanges(ctx, pn, changes, claimTime)
}

// forEach calls fn on each of items, up to workers at once, stopping at
// the first error, which it returns.
func forEach[T any](ctx context.Context, items []T, workers int, fn func(T) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	work := make(chan T)
	var wg sync.WaitGroup
	for range max(1, min(workers, len(items))) {
		wg.Go(func() {
			for it := range work {
				if err := fn(it); err != nil {
					cancel(err)
				}
			}
		})
	}
feed:
	for _, it := range items {
		select {
		case work <- it:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	return context.Cause(ctx)
}
