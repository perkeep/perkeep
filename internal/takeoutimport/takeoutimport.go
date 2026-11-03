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
	"time"

	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
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
	pr := im.newProgress(len(cs), 0)
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
	lib, err := takeout.NewLibrary(ctx, exp, cs, im.workers())
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
	pr := im.newProgress(len(missing), ContentsSize(missing))
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
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// progress logs progress through a number of items, at most every few
// seconds. If they have sizes, its estimate of the time left is by
// bytes, as their sizes vary a lot.
type progress struct {
	logf       func(format string, args ...any)
	total      int
	totalBytes int64 // or zero, to estimate by items
	start      time.Time
	last       time.Time
}

func (im *Importer) newProgress(total int, totalBytes int64) *progress {
	now := time.Now()
	return &progress{logf: im.logf, total: total, totalBytes: totalBytes, start: now, last: now}
}

// update records that done items, of doneBytes bytes, are done.
func (p *progress) update(done int, doneBytes int64) {
	now := time.Now()
	if now.Sub(p.last) < 5*time.Second || done == p.total {
		return
	}
	p.last = now
	elapsed := now.Sub(p.start)
	if p.totalBytes == 0 || doneBytes == 0 {
		eta := time.Duration(float64(elapsed) / float64(done) * float64(p.total-done))
		p.logf("%d of %d done; about %v left", done, p.total, eta.Round(time.Second))
		return
	}
	rate := float64(doneBytes) / elapsed.Seconds()
	eta := time.Duration(float64(p.totalBytes-doneBytes) / rate * float64(time.Second))
	p.logf("%d of %d done, %s of %s (%s/s); about %v left", done, p.total,
		FormatBytes(doneBytes), FormatBytes(p.totalBytes), FormatBytes(int64(rate)), eta.Round(time.Second))
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
	lib, err := takeout.NewLibrary(ctx, exp, cs, s.im.workers())
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
