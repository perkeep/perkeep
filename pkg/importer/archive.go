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

package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
)

// ArchiveImporter is an optional capability of an Importer. Inspection must be
// read-only and derive identity from archive contents, not its filename alone.
// RunArchive must be replay-safe: the same input can be explicitly run again.
type ArchiveImporter interface {
	InspectArchive(context.Context, *Archive) (camtypes.ArchiveIdentity, error)
	RunArchive(*RunContext, *Archive) error
}

const (
	// AcctAttrEmail is the owner's email address used for archive account matching.
	AcctAttrEmail = "email"
	// AcctAttrArchiveInput retains the latest input file-schema reference. Earlier
	// inputs and their outcomes remain in archiveImport:<file-ref> attributes.
	AcctAttrArchiveInput = "archiveImportInput"
	// These attributes retain a first-observed, unverified filename hint and
	// its provenance. Neither is an authoritative provider user ID.
	AcctAttrArchiveFilenameUserID       = "archiveFilenameUserID"
	AcctAttrArchiveFilenameUserIDSource = "archiveFilenameUserIDSource"
	archiveHistoryPrefix                = "archiveImport:"
	maxArchiveSize                      = uint64(32 << 30)
	maxArchiveDirectory                 = 32 << 20
	maxArchiveEntries                   = 100000
	maxArchiveEntry                     = int64(4 << 30)
	maxArchiveRead                      = int64(64 << 30)
	maxArchiveWarnings                  = 100
)

// Archive is a random-access ZIP backed by schema.FileReader, not a downloaded
// copy. Providers must use Open/OpenLimit (not Zip.File[i].Open) for bounded
// decompression. Limits are 32 GiB logical ZIP size, 100,000 entries, 32 MiB
// central-directory metadata, 4 GiB per entry, and 64 GiB actually read across
// all opens (including reopens).
// Inspect and Run receive separate Archive instances and budgets.
type Archive struct {
	Zip      *zip.Reader
	FileRef  blob.Ref
	FileName string
	ctx      context.Context
	file     *schema.FileReader
	entries  map[string]*zip.File
	mu       sync.Mutex
	read     int64
}

// Open opens a named entry with the shared default decompression limit.
func (a *Archive) Open(name string) (io.ReadCloser, error) { return a.OpenLimit(name, maxArchiveEntry) }

// OpenLimit additionally restricts this entry's declared and actual size. It
// does not allocate limit bytes. Callers must read to EOF to verify ZIP checksums.
func (a *Archive) OpenLimit(name string, limit int64) (io.ReadCloser, error) {
	if err := a.ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxArchiveEntry {
		return nil, errors.New("invalid archive entry limit")
	}
	f := a.entries[name]
	if f == nil {
		return nil, fmt.Errorf("archive entry %q: %w", name, fs.ErrNotExist)
	}
	if f.FileInfo().IsDir() {
		return nil, fmt.Errorf("archive entry %q is a directory", name)
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("archive entry %q exceeds %d bytes", name, limit)
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	return &archiveEntryReader{ReadCloser: r, archive: a, left: limit}, nil
}

type archiveEntryReader struct {
	io.ReadCloser
	archive *Archive
	left    int64
	failed  bool
}

func (r *archiveEntryReader) Read(p []byte) (int, error) {
	if err := r.archive.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.failed {
		return 0, errors.New("archive read limit exceeded")
	}
	if int64(len(p)) > r.left+1 {
		p = p[:r.left+1]
	}
	n, err := r.ReadCloser.Read(p)
	r.archive.mu.Lock()
	r.archive.read += int64(n)
	total := r.archive.read
	r.archive.mu.Unlock()
	if int64(n) > r.left || total > maxArchiveRead {
		r.failed = true
		return 0, errors.New("archive read limit exceeded")
	}
	r.left -= int64(n)
	return n, err
}

func openArchive(ctx context.Context, fetcher blob.Fetcher, ref blob.Ref) (*Archive, error) {
	if !ref.Valid() {
		return nil, errors.New("archive requires a valid file-schema reference")
	}
	if fetcher == nil {
		return nil, errors.New("archive blob source is unavailable")
	}
	// FileReader.ReadAt currently passes context.TODO to its fetcher.
	// Bind fetches to this inspection/run so cancellation still reaches I/O.
	fetcher = archiveFetcher{ctx, fetcher}
	r, _, err := fetcher.Fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	b, err := schema.BlobFromReader(ref, io.LimitReader(r, 1<<20))
	r.Close()
	if err != nil {
		return nil, fmt.Errorf("archive file schema: %w", err)
	}
	if b.Type() != schema.TypeFile {
		return nil, errors.New("archive reference must identify a file schema")
	}
	var size uint64
	for _, part := range b.ByteParts() {
		if part.Size > maxArchiveSize-size {
			return nil, errors.New("archive file exceeds 32 GiB size limit")
		}
		size += part.Size
		if part.Offset > uint64(1<<63-1)-part.Size {
			return nil, errors.New("archive file part offset overflow")
		}
	}
	if size == 0 {
		return nil, errors.New("archive file is empty")
	}
	// Reuse the bounded, already parsed file schema rather than fetching and
	// decoding the schema a second time without a size bound.
	fr, err := b.NewFileReader(fetcher)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Archive, error) { fr.Close(); return nil, err }
	if err := checkZIPDirectory(fr, fr.Size()); err != nil {
		return fail(err)
	}
	zr, err := zip.NewReader(fr, fr.Size())
	if err != nil {
		return fail(err)
	}
	if len(zr.File) > maxArchiveEntries {
		return fail(errors.New("too many archive entries"))
	}
	a := &Archive{Zip: zr, FileRef: ref, FileName: fr.FileName(), ctx: ctx, file: fr, entries: make(map[string]*zip.File, len(zr.File))}
	var declared uint64
	for _, f := range zr.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00:") || strings.ContainsFunc(name, unicode.IsControl) {
			return fail(fmt.Errorf("unsafe archive path %q", f.Name))
		}
		if _, ok := a.entries[f.Name]; ok {
			return fail(fmt.Errorf("duplicate archive path %q", f.Name))
		}
		if mode := f.Mode(); !mode.IsRegular() && !mode.IsDir() {
			return fail(fmt.Errorf("non-regular archive entry %q", f.Name))
		}
		if f.UncompressedSize64 > uint64(maxArchiveEntry) {
			return fail(fmt.Errorf("archive entry %q is too large", f.Name))
		}
		declared += f.UncompressedSize64
		if declared > uint64(maxArchiveRead) {
			return fail(errors.New("archive uncompressed size exceeds limit"))
		}
		a.entries[f.Name] = f
	}
	return a, nil
}

type archiveFetcher struct {
	ctx context.Context
	blob.Fetcher
}

func (f archiveFetcher) Fetch(_ context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	return f.Fetcher.Fetch(f.ctx, ref)
}

// Bound the directory before archive/zip allocates its entry metadata. ZIP64 is
// supported, but multi-disk ZIPs and self-extracting offset correction are not.
func checkZIPDirectory(r io.ReaderAt, size int64) error {
	if size < 22 {
		return errors.New("invalid ZIP archive")
	}
	n := min(size, int64(22+65535))
	tail := make([]byte, n)
	if _, err := r.ReadAt(tail, size-n); err != nil {
		return err
	}
	for i := len(tail) - 22; i >= 0; i-- {
		if !bytes.Equal(tail[i:i+4], []byte{'P', 'K', 5, 6}) {
			continue
		}
		e := tail[i:]
		if i+22+int(binary.LittleEndian.Uint16(e[20:])) != len(tail) {
			// archive/zip selects the last signature, even if it would leave
			// trailing bytes. Do not validate an earlier record while it
			// subsequently opens a different, unchecked directory.
			return errors.New("ZIP trailing data or invalid end-of-directory comment")
		}
		if binary.LittleEndian.Uint16(e[4:]) != 0 || binary.LittleEndian.Uint16(e[6:]) != 0 {
			return errors.New("multi-disk ZIP is unsupported")
		}
		entries := uint64(binary.LittleEndian.Uint16(e[10:]))
		countDisk := uint64(binary.LittleEndian.Uint16(e[8:]))
		dirSize := uint64(binary.LittleEndian.Uint32(e[12:]))
		offset := uint64(binary.LittleEndian.Uint32(e[16:]))
		end := uint64(size - n + int64(i))
		if entries == 65535 || dirSize == 0xffffffff || offset == 0xffffffff {
			if end < 20 {
				return errors.New("invalid ZIP64 locator")
			}
			loc := make([]byte, 20)
			if _, err := r.ReadAt(loc, int64(end)-20); err != nil {
				return err
			}
			if !bytes.Equal(loc[:4], []byte{'P', 'K', 6, 7}) || binary.LittleEndian.Uint32(loc[4:]) != 0 || binary.LittleEndian.Uint32(loc[16:]) != 1 {
				return errors.New("invalid ZIP64 locator")
			}
			pos := binary.LittleEndian.Uint64(loc[8:])
			if pos > end-20 || end-20-pos < 56 {
				return errors.New("invalid ZIP64 directory")
			}
			z := make([]byte, 56)
			if _, err := r.ReadAt(z, int64(pos)); err != nil {
				return err
			}
			if !bytes.Equal(z[:4], []byte{'P', 'K', 6, 6}) || binary.LittleEndian.Uint64(z[4:]) < 44 || binary.LittleEndian.Uint32(z[16:]) != 0 || binary.LittleEndian.Uint32(z[20:]) != 0 {
				return errors.New("invalid ZIP64 directory")
			}
			countDisk = binary.LittleEndian.Uint64(z[24:])
			entries = binary.LittleEndian.Uint64(z[32:])
			dirSize = binary.LittleEndian.Uint64(z[40:])
			offset = binary.LittleEndian.Uint64(z[48:])
			end = pos
		}
		if countDisk != entries || entries > maxArchiveEntries || dirSize > maxArchiveDirectory || offset > end || dirSize > end-offset {
			return errors.New("ZIP directory exceeds limits or is invalid")
		}
		// Require exact positioning so zip.NewReader cannot apply an unchecked
		// self-extracting prefix correction to the bounded directory location.
		if offset+dirSize != end {
			return errors.New("unsupported ZIP directory layout")
		}
		return checkZIPRecords(r, int64(offset), int64(dirSize), entries)
	}
	return errors.New("ZIP end-of-directory record not found")
}

func checkZIPRecords(r io.ReaderAt, offset, size int64, entries uint64) error {
	sr := io.NewSectionReader(r, offset, size)
	var header [46]byte
	for range entries {
		if _, err := io.ReadFull(sr, header[:]); err != nil {
			return fmt.Errorf("invalid ZIP directory entry: %w", err)
		}
		if !bytes.Equal(header[:4], []byte{'P', 'K', 1, 2}) {
			return errors.New("invalid ZIP directory entry")
		}
		length := int64(binary.LittleEndian.Uint16(header[28:])) +
			int64(binary.LittleEndian.Uint16(header[30:])) +
			int64(binary.LittleEndian.Uint16(header[32:]))
		pos, err := sr.Seek(length, io.SeekCurrent)
		if err != nil || pos > size {
			return errors.New("invalid ZIP directory entry length")
		}
	}
	if pos, _ := sr.Seek(0, io.SeekCurrent); pos != size {
		return errors.New("ZIP directory entry count mismatch")
	}
	return nil
}

func normalizeArchiveIdentity(id camtypes.ArchiveIdentity) (camtypes.ArchiveIdentity, error) {
	fields := []*string{&id.UserName, &id.Email, &id.Name, &id.FilenameUserID}
	for _, p := range fields {
		*p = strings.TrimSpace(*p)
		if len(*p) > 4096 || strings.ContainsFunc(*p, unicode.IsControl) {
			return id, errors.New("invalid archive owner identity")
		}
	}
	if id.UserName == "" && id.Email == "" {
		return id, errors.New("archive must identify its owner by username or email")
	}
	return id, nil
}

var errArchiveConflict = errors.New("ambiguous archive account identity")
var errArchiveActive = errors.New("account already has an active import with a different input")

func matchArchiveAccount(accounts []*importerAcct, id camtypes.ArchiveIdentity) (*importerAcct, error) {
	var matches []*importerAcct
	for _, ia := range accounts {
		if ia.acct.Attr(attrNodeType) != nodeTypeImporterAccount {
			continue
		}
		if (id.UserName != "" && strings.TrimSpace(ia.acct.Attr(AcctAttrUserName)) == id.UserName) || (id.Email != "" && strings.EqualFold(strings.TrimSpace(ia.acct.Attr(AcctAttrEmail)), id.Email)) {
			matches = append(matches, ia)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if id.FilenameUserID != "" {
		var chosen *importerAcct
		for _, ia := range matches {
			storedID := ia.acct.Attr(AcctAttrUserID)
			if storedID == "" {
				storedID = ia.acct.Attr(AcctAttrArchiveFilenameUserID)
			}
			if storedID == id.FilenameUserID {
				if chosen != nil {
					return nil, errArchiveConflict
				}
				chosen = ia
			}
		}
		if chosen != nil {
			return chosen, nil
		}
	}
	return nil, errArchiveConflict
}

// ImportArchive inspects an uploaded ZIP and optionally resolves/creates its
// account and starts managed asynchronous importing. Requests do not own runs:
// disconnecting after acceptance does not cancel the import. Same-input active
// starts are idempotent; replacing an active account's input is rejected.
func (h *Host) ImportArchive(ctx context.Context, name string, req camtypes.ArchiveImportRequest) (*camtypes.ArchiveImportResult, error) {
	h.didInit.Wait()
	if !h.beginWork() {
		return nil, errors.New("importer host is closed")
	}
	defer h.work.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	defer cancel()
	im := h.imp[name]
	if im == nil {
		return nil, errors.New("unknown importer")
	}
	ai, ok := im.impl.(ArchiveImporter)
	if !ok {
		return nil, errors.New("importer does not support archives")
	}
	if req.Action != "inspect" && req.Action != "start" {
		return nil, errors.New("archive action must be inspect or start")
	}
	a, err := openArchive(ctx, h.blobSource, req.FileRef)
	if err != nil {
		return nil, err
	}
	defer a.file.Close()
	id, err := ai.InspectArchive(ctx, a)
	if err != nil {
		return nil, err
	}
	id, err = normalizeArchiveIdentity(id)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	im.archiveMu.Lock()
	defer im.archiveMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accounts, err := im.Accounts()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ia, err := matchArchiveAccount(accounts, id)
	if err != nil {
		return nil, err
	}
	result := &camtypes.ArchiveImportResult{Identity: id}
	var identityAttrs []string
	for _, p := range [][2]string{{AcctAttrUserName, id.UserName}, {AcctAttrEmail, id.Email}, {AcctAttrName, id.Name}} {
		if p[1] != "" {
			identityAttrs = append(identityAttrs, p[0], p[1])
		}
	}
	if ia == nil {
		if req.Action == "inspect" {
			return result, nil
		}
		if err := h.health.ReadOnlyError(); err != nil {
			return nil, err
		}
		if id.FilenameUserID != "" {
			identityAttrs = append(identityAttrs, AcctAttrArchiveFilenameUserID, id.FilenameUserID, AcctAttrArchiveFilenameUserIDSource, req.FileRef.String())
		}
		ia, err = im.newAccountWithAttrs(identityAttrs...)
		if err != nil {
			return nil, err
		}
		result.Created = true
	}
	result.AccountRef = ia.acct.PermanodeRef()
	ia.mu.Lock()
	defer ia.mu.Unlock()
	if req.Action == "start" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ia.current != nil {
			rc := ia.current
			rc.mu.Lock()
			sameInput := rc.archiveJob != nil && rc.archiveJob.FileRef == req.FileRef
			rc.mu.Unlock()
			if !sameInput {
				return nil, errArchiveActive
			}
		} else {
			// Never overwrite an earlier filename hint or a conflicting
			// authoritative user ID with a later, possibly renamed filename.
			if id.FilenameUserID != "" && ia.acct.Attr(AcctAttrArchiveFilenameUserID) == "" &&
				(ia.acct.Attr(AcctAttrUserID) == "" || ia.acct.Attr(AcctAttrUserID) == id.FilenameUserID) {
				identityAttrs = append(identityAttrs, AcctAttrArchiveFilenameUserID, id.FilenameUserID, AcctAttrArchiveFilenameUserIDSource, req.FileRef.String())
			}
			if err := ia.startLocked(req.FileRef, identityAttrs...); err != nil {
				return nil, err
			}
		}
	}
	status, err := ia.archiveStatusLocked(req.FileRef)
	if err != nil {
		return nil, err
	}
	result.Status = status
	return result, nil
}

// ArchiveStatus returns the latest input's durable status. A persisted running
// record without an active managed run is reported as interrupted, so callers
// can restart the same file reference after a process restart.
func (h *Host) ArchiveStatus(ctx context.Context, name string, account blob.Ref) (*camtypes.ArchiveImportStatus, error) {
	h.didInit.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !h.beginWork() {
		return nil, errors.New("importer host is closed")
	}
	defer h.work.Done()
	im := h.imp[name]
	if im == nil {
		return nil, errors.New("unknown importer")
	}
	if _, ok := im.impl.(ArchiveImporter); !ok {
		return nil, errors.New("importer does not support archives")
	}
	ia, err := im.account(account)
	if err != nil {
		return nil, err
	}
	ia.mu.Lock()
	defer ia.mu.Unlock()
	ref, ok := blob.Parse(ia.acct.Attr(AcctAttrArchiveInput))
	if !ok {
		return nil, errors.New("account has no archive input")
	}
	return ia.archiveStatusLocked(ref)
}

func (ia *importerAcct) archiveStatusLocked(ref blob.Ref) (*camtypes.ArchiveImportStatus, error) {
	if ia.current != nil {
		rc := ia.current
		rc.mu.Lock()
		var status *camtypes.ArchiveImportStatus
		if rc.archiveJob != nil && rc.archiveJob.FileRef == ref {
			copy := *rc.archiveJob
			copy.Progress.Warnings = append([]string(nil), copy.Progress.Warnings...)
			status = &copy
		}
		rc.mu.Unlock()
		if status != nil {
			return status, nil
		}
	}
	if ia.lastArchive != nil && ia.lastArchive.FileRef == ref {
		status := *ia.lastArchive
		status.Progress.Warnings = append([]string(nil), status.Progress.Warnings...)
		return &status, nil
	}
	raw := ia.acct.Attr(archiveHistoryPrefix + ref.String())
	if raw == "" {
		return nil, nil
	}
	var status camtypes.ArchiveImportStatus
	if len(raw) > 1<<20 {
		return nil, errors.New("invalid archive history size")
	}
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return nil, fmt.Errorf("invalid archive history: %w", err)
	}
	if status.FileRef != ref || status.AccountRef != ia.acct.PermanodeRef() {
		return nil, errors.New("invalid archive history references")
	}
	switch status.State {
	case "running":
		status.State = "interrupted"
		status.Error = "previous importer process stopped before recording completion"
	case "complete", "error", "interrupted":
	default:
		return nil, errors.New("invalid archive history state")
	}
	status.Progress = boundedArchiveProgress(status.Progress)
	return &status, nil
}

// prepareArchiveLocked runs before setting current, with ia.mu held. Persisting
// input plus running status in one claim makes a failed/disconnected start
// retryable; neither mutable request state nor a scratch cache is authoritative.
func (ia *importerAcct) prepareArchiveLocked(ref blob.Ref, attrs ...string) (*camtypes.ArchiveImportStatus, error) {
	if _, ok := ia.im.impl.(ArchiveImporter); !ok {
		return nil, nil
	}
	if !ref.Valid() {
		input := ia.acct.Attr(AcctAttrArchiveInput)
		if input == "" {
			return nil, nil
		}
		var ok bool
		ref, ok = blob.Parse(input)
		if !ok {
			return nil, errors.New("invalid stored archive input")
		}
	}
	job := &camtypes.ArchiveImportStatus{AccountRef: ia.acct.PermanodeRef(), FileRef: ref, State: "running", Started: time.Now().UTC()}
	raw, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	attrs = append(attrs, AcctAttrArchiveInput, ref.String(), archiveHistoryPrefix+ref.String(), string(raw))
	if err := ia.acct.SetAttrs(attrs...); err != nil {
		return nil, err
	}
	return job, nil
}

func boundedArchiveProgress(p camtypes.ArchiveImportProgress) camtypes.ArchiveImportProgress {
	p.ItemsTotal = max(0, p.ItemsTotal)
	p.ItemsDone = max(0, p.ItemsDone)
	p.ItemsUnchanged = max(0, p.ItemsUnchanged)
	p.WarningCount = max(p.WarningCount, len(p.Warnings))
	p.Warnings = append([]string(nil), p.Warnings[:min(len(p.Warnings), maxArchiveWarnings)]...)
	for i, w := range p.Warnings {
		if len(w) > 1024 {
			p.Warnings[i] = w[:1024]
		}
	}
	return p
}

// SetArchiveProgress publishes a bounded durable progress snapshot. Providers
// should batch calls, rather than upload one claim per item. The final snapshot
// is also stored with completion/error by the managed run lifecycle.
func (rc *RunContext) SetArchiveProgress(p camtypes.ArchiveImportProgress) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.archiveJob == nil {
		return errors.New("not an archive run")
	}
	status := *rc.archiveJob
	status.Progress = boundedArchiveProgress(p)
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if err := rc.AccountNode().SetAttr(archiveHistoryPrefix+status.FileRef.String(), string(raw)); err != nil {
		return err
	}
	*rc.archiveJob = status
	return nil
}

func (rc *RunContext) runManaged() error {
	rc.mu.Lock()
	var ref blob.Ref
	if rc.archiveJob != nil {
		ref = rc.archiveJob.FileRef
	}
	rc.mu.Unlock()
	if !ref.Valid() {
		return rc.ia.im.impl.Run(rc)
	}
	a, err := openArchive(rc.Context(), rc.Host.blobSource, ref)
	if err != nil {
		return err
	}
	defer a.file.Close()
	return rc.ia.im.impl.(ArchiveImporter).RunArchive(rc, a)
}

func (rc *RunContext) finishArchive(err error) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.archiveJob == nil {
		return err
	}
	status := *rc.archiveJob
	status.Finished = time.Now().UTC()
	status.State = "complete"
	status.Error = ""
	if err != nil {
		status.State = "error"
		status.Error = err.Error()
		if len(status.Error) > 4096 {
			status.Error = status.Error[:4096]
		}
	}
	raw, marshalErr := json.Marshal(status)
	if marshalErr == nil {
		marshalErr = rc.AccountNode().SetAttr(archiveHistoryPrefix+status.FileRef.String(), string(raw))
	}
	if marshalErr != nil {
		err = errors.Join(err, fmt.Errorf("recording archive outcome: %w", marshalErr))
		status.State = "error"
		status.Error = err.Error()
		if len(status.Error) > 4096 {
			status.Error = status.Error[:4096]
		}
	}
	*rc.archiveJob = status
	return err
}

func archiveJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func archiveError(w http.ResponseWriter, code int, err error) {
	archiveJSON(w, code, map[string]string{"error": err.Error()})
}

// JSON-only POST plus strict Origin/fetch metadata checks prevent ambient-auth
// cross-site form submissions. Authentication remains the enclosing handler's
// responsibility, exactly as for other importer endpoints.
func archiveJSONRequest(r *http.Request, baseURL string) error {
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return errors.New("cross-origin archive request rejected")
	}
	if origins := r.Header.Values("Origin"); len(origins) != 0 {
		if len(origins) != 1 {
			return errors.New("invalid Origin")
		}
		u, err := url.Parse(origins[0])
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		host := r.Host
		// baseURL is trusted server configuration, unlike Forwarded headers.
		// Reverse proxies may terminate TLS before reaching this handler.
		if public, parseErr := url.Parse(baseURL); parseErr == nil && public.Host != "" && (public.Scheme == "http" || public.Scheme == "https") {
			scheme, host = public.Scheme, public.Host
		}
		if err != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("cross-origin archive request rejected")
		}
	}
	return nil
}

func (h *Host) serveArchive(w http.ResponseWriter, r *http.Request, im *importer) {
	if _, ok := im.impl.(ArchiveImporter); !ok {
		archiveError(w, http.StatusNotFound, errors.New("importer does not support archives"))
		return
	}
	switch r.Method {
	case "GET":
		ref, ok := blob.Parse(r.URL.Query().Get("account"))
		if !ok {
			archiveError(w, 400, errors.New("invalid account reference"))
			return
		}
		status, err := h.ArchiveStatus(r.Context(), im.name, ref)
		if err != nil {
			archiveError(w, 400, err)
			return
		}
		if status == nil {
			archiveError(w, 404, errors.New("archive status not found"))
			return
		}
		archiveJSON(w, 200, status)
	case "POST":
		if err := archiveJSONRequest(r, h.baseURL); err != nil {
			archiveError(w, 403, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req camtypes.ArchiveImportRequest
		if err := dec.Decode(&req); err != nil {
			archiveError(w, 400, fmt.Errorf("invalid archive request: %w", err))
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			archiveError(w, 400, errors.New("archive request must contain exactly one JSON object"))
			return
		}
		result, err := h.ImportArchive(r.Context(), im.name, req)
		if err != nil {
			code := 400
			if errors.Is(err, errArchiveConflict) || errors.Is(err, errArchiveActive) {
				code = 409
			}
			archiveError(w, code, err)
			return
		}
		archiveJSON(w, 200, result)
	default:
		w.Header().Set("Allow", "GET, POST")
		archiveError(w, 405, errors.New("method not allowed"))
	}
}
