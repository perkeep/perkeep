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

// Package upload uploads files to a Perkeep server: their contents, cut
// into chunks, and their file schema blobs.
//
// A File uploads one regular file. It first checks whether the server
// already has the file's contents, by their whole-file refs; if not, it
// cuts the contents into chunks, asks the server which of those it has
// (so an interrupted upload resumes cheaply), and uploads the rest into
// an upload session, uploading the file schema blob only once they're
// durable.
//
// Files given the same Limiter share its budget of CPU and memory.
package upload // import "perkeep.org/pkg/upload"

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/schema"
)

// dupCheckThreshold is the size above which a File checks whether the
// server already has its contents before cutting them. For smaller files,
// cutting and asking about the chunks costs about as much.
const dupCheckThreshold = 256 << 10

// A File's chunks are asked about in batches of up to statBatchChunks
// chunks or about statBatchBytes bytes, and uploaded by uploadWorkers
// goroutines.
const (
	statBatchChunks = 256
	statBatchBytes  = 16 << 20
	uploadWorkers   = 16
)

// Limits are the limits of a Limiter. Zero fields get the defaults given.
type Limits struct {
	// HashCores is how many goroutines can hash or cut file contents at
	// once. The default is runtime.GOMAXPROCS(0).
	HashCores int

	// BytesInFlight is how many bytes of chunks can be read and not yet
	// uploaded (or found on the server) at once. The default is 64 MB.
	BytesInFlight int64
}

// A Limiter bounds the resources the Files it's given use, together.
type Limiter struct {
	limits Limits
	cores  *semaphore.Weighted
	bytes  *semaphore.Weighted
}

// NewLimiter returns a Limiter with the given limits.
func NewLimiter(limits Limits) *Limiter {
	if limits.HashCores <= 0 {
		limits.HashCores = runtime.GOMAXPROCS(0)
	}
	if limits.BytesInFlight <= 0 {
		limits.BytesInFlight = 64 << 20
	}
	// Room for the biggest blob, so it can be read at all.
	limits.BytesInFlight = max(limits.BytesInFlight, constants.MaxBlobSize)
	return &Limiter{
		limits: limits,
		cores:  semaphore.NewWeighted(int64(limits.HashCores)),
		bytes:  semaphore.NewWeighted(limits.BytesInFlight),
	}
}

// Options are the options of a File.
type Options struct {
	// Session is the upload session to upload into. If nil, the File
	// opens its own, if the server supports them, and flushes and closes
	// it before Run returns.
	Session *client.UploadSession

	// Limiter is the Limiter of the File's resources. If nil, the File
	// gets its own, with the default limits.
	Limiter *Limiter

	// ContentsOnly makes the file schema blob describe only the
	// contents: no name, mode or times.
	ContentsOnly bool

	// ModTime, if non-zero, is the modification time to record, instead
	// of the FileInfo's.
	ModTime time.Time

	// CapCtime records a creation time no later than the modification
	// time. See schema.Builder.CapCreationTime.
	CapCtime bool

	// WholeRefs are the contents' known whole-file refs. If given, the
	// File doesn't hash the contents to find them.
	WholeRefs []blob.Ref
}

// A File is an upload of one regular file's contents and its file schema
// blob. Make one with NewFile.
type File struct {
	c        *client.Client
	fi       fs.FileInfo
	contents io.ReaderAt
	opts     Options
	lim      *Limiter

	mu       sync.Mutex
	progress Progress
}

// NewFile returns an upload of the regular file described by fi, with
// contents, to c. opts may be nil.
func NewFile(c *client.Client, fi fs.FileInfo, contents io.ReaderAt, opts *Options) *File {
	f := &File{c: c, fi: fi, contents: contents}
	if opts != nil {
		f.opts = *opts
	}
	f.lim = f.opts.Limiter
	if f.lim == nil {
		f.lim = NewLimiter(Limits{})
	}
	return f
}

// A Phase is a step of a File's upload.
type Phase int

const (
	PhaseStarting       Phase = iota
	PhaseHashing              // hashing the contents, to find them on the server
	PhaseCheckingServer       // asking whether the server has the contents
	PhaseUploading            // cutting the contents, and uploading chunks
	PhaseFlushing             // waiting for the server to make blobs durable
	PhaseDone
)

func (p Phase) String() string {
	switch p {
	case PhaseStarting:
		return "starting"
	case PhaseHashing:
		return "hashing"
	case PhaseCheckingServer:
		return "checking server"
	case PhaseUploading:
		return "uploading"
	case PhaseFlushing:
		return "flushing"
	case PhaseDone:
		return "done"
	}
	return fmt.Sprintf("Phase(%d)", int(p))
}

// Progress is the progress of a File's upload.
type Progress struct {
	Phase Phase

	BytesHashed   int64 // of the contents, hashed whole
	BytesCut      int64 // of the contents, cut into chunks
	BytesUploaded int64 // of chunks uploaded

	Chunks         int // cut so far, including repeats within the file
	ChunksPresent  int // found on the server
	ChunksUploaded int
}

// Progress returns the upload's progress. It's safe to call at any time,
// including during Run.
func (f *File) Progress() Progress {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progress
}

func (f *File) update(fn func(*Progress)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.progress)
}

func (f *File) setPhase(p Phase) { f.update(func(pr *Progress) { pr.Phase = p }) }

// Result is the result of a File's upload.
type Result struct {
	FileRef   blob.Ref   // of the file schema blob
	WholeRefs []blob.Ref // of the contents, of each hash function computed
	Size      int64      // of the contents

	// Dup is whether the server already had the contents, so that only
	// the file schema blob was uploaded, if it wasn't there either.
	Dup bool

	// Durable is whether the file schema blob is durable. It's false
	// only when it was uploaded into the caller's Options.Session, whose
	// Flush says when it is.
	Durable bool
}

// Run uploads the file. It returns once the file schema blob is
// uploaded: with the File's own session, or none, it's then durable;
// with Options.Session, the caller's Flush says when it is. The chunks
// it refers to are durable either way.
func (f *File) Run(ctx context.Context) (*Result, error) {
	defer f.setPhase(PhaseDone)
	sk, err := f.newSink(ctx)
	if err != nil {
		return nil, err
	}
	defer sk.close()

	res := &Result{Size: f.fi.Size(), WholeRefs: f.opts.WholeRefs, Durable: sk.durableOnReturn()}
	file := f.newFileMap()

	if len(res.WholeRefs) == 0 && res.Size > dupCheckThreshold {
		res.WholeRefs, err = f.hashWhole(ctx)
		if err != nil {
			return nil, err
		}
	}
	if len(res.WholeRefs) > 0 {
		f.setPhase(PhaseCheckingServer)
		dupRef, err := f.c.SearchExistingFileSchema(ctx, res.WholeRefs...)
		if err == nil && dupRef.Valid() {
			return f.finishDup(ctx, sk, res, file, dupRef)
		}
		// Without a dup, or a working search, the contents get uploaded.
	}

	f.setPhase(PhaseUploading)
	fc, uploaded, err := f.uploadChunks(ctx, sk)
	if err != nil {
		return nil, err
	}
	bytesBlobs, err := fc.Populate(file)
	if err != nil {
		return nil, err
	}
	var handles []*client.UploadHandle
	for _, b := range bytesBlobs {
		handles = append(handles, stringHandle(b.BlobRef(), b.JSON()))
	}
	fileJSON := file.Blob().JSON()
	fileHandle := stringHandle(blob.RefFromString(fileJSON), fileJSON)
	res.FileRef = fileHandle.BlobRef

	// Upload the bytes blobs the server lacks, then, once they and the
	// chunks are durable, the file schema blob, if it lacks it.
	missing, err := f.missing(ctx, append(handles, fileHandle))
	if err != nil {
		return nil, err
	}
	for _, h := range handles {
		if missing[h.BlobRef] {
			if err := sk.upload(ctx, h); err != nil {
				return nil, err
			}
			uploaded = append(uploaded, h.BlobRef)
		}
	}
	f.setPhase(PhaseFlushing)
	if err := sk.waitDurable(ctx, uploaded); err != nil {
		return nil, err
	}
	if !missing[fileHandle.BlobRef] {
		res.Durable = true
		return res, nil
	}
	f.setPhase(PhaseUploading)
	if err := sk.upload(ctx, fileHandle); err != nil {
		return nil, err
	}
	if err := sk.finish(ctx, fileHandle.BlobRef); err != nil {
		return nil, err
	}
	return res, nil
}

// newFileMap returns the builder of the file's schema blob, without its
// parts.
func (f *File) newFileMap() *schema.Builder {
	if f.opts.ContentsOnly {
		return schema.NewFileMap("")
	}
	file := schema.NewCommonFileMap(f.fi.Name(), f.fi)
	file.SetType(schema.TypeFile)
	if !f.opts.ModTime.IsZero() {
		file.SetModTime(f.opts.ModTime)
	}
	if f.opts.CapCtime {
		file.CapCreationTime()
	}
	return file
}

// hashWhole returns the contents' whole-file refs, of each hash function
// the server can find files by, or none if it can't find files by any.
func (f *File) hashWhole(ctx context.Context) ([]blob.Ref, error) {
	newHashes, err := f.c.WholeRefHashes()
	if err != nil {
		// Without discovery (as with a client of local storage), there's
		// no searching for the contents either.
		return nil, nil
	}
	f.setPhase(PhaseHashing)
	hashes := make([]hash.Hash, len(newHashes))
	for i, newHash := range newHashes {
		hashes[i] = newHash()
	}
	cores := int64(min(len(hashes), f.lim.limits.HashCores))
	if err := f.lim.cores.Acquire(ctx, cores); err != nil {
		return nil, err
	}
	defer f.lim.cores.Release(cores)

	r := io.NewSectionReader(f.contents, 0, f.fi.Size())
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			if len(hashes) == 1 {
				hashes[0].Write(buf[:n])
			} else {
				var wg sync.WaitGroup
				for _, h := range hashes {
					wg.Go(func() { h.Write(buf[:n]) })
				}
				wg.Wait()
			}
			f.update(func(p *Progress) { p.BytesHashed += int64(n) })
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	refs := make([]blob.Ref, len(hashes))
	for i, h := range hashes {
		refs[i] = blob.RefFromHash(h)
	}
	return refs, nil
}

// finishDup finishes the upload of a file whose contents the server has,
// in the file of schema blob dupRef.
func (f *File) finishDup(ctx context.Context, sk *sink, res *Result, file *schema.Builder, dupRef blob.Ref) (*Result, error) {
	res.Dup = true
	dup, err := f.c.FetchSchemaBlob(ctx, dupRef)
	if err != nil {
		return nil, fmt.Errorf("upload: fetching file schema %v with the same contents: %w", dupRef, err)
	}
	if err := file.PopulateParts(dup.PartsSize(), dup.ByteParts()); err != nil {
		return nil, err
	}
	json := file.Blob().JSON()
	h := stringHandle(blob.RefFromString(json), json)
	res.FileRef = h.BlobRef
	if h.BlobRef == dupRef {
		// The same file: same name, times, and so on.
		res.Durable = true
		return res, nil
	}
	f.setPhase(PhaseUploading)
	if err := sk.upload(ctx, h); err != nil {
		return nil, err
	}
	if err := sk.finish(ctx, h.BlobRef); err != nil {
		return nil, err
	}
	return res, nil
}

// A chunk is a chunk of a file's contents.
type chunk struct {
	br   blob.Ref
	data []byte
}

// uploadChunks cuts the contents into chunks, and uploads those the
// server lacks, returning the cut and the refs of the chunks it uploaded.
func (f *File) uploadChunks(ctx context.Context, sk *sink) (*schema.FileCut, []blob.Ref, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// held counts the bytes of chunks this upload holds of the Limiter's
	// budget, which it gives back, even when it stops early.
	var held int64
	var heldMu sync.Mutex
	release := func(n int64) {
		heldMu.Lock()
		defer heldMu.Unlock()
		held -= n
		f.lim.bytes.Release(n)
	}
	defer func() {
		heldMu.Lock()
		defer heldMu.Unlock()
		if held > 0 {
			f.lim.bytes.Release(held)
		}
	}()

	g, gctx := errgroup.WithContext(ctx)
	batches := make(chan []chunk, 1)
	toUpload := make(chan chunk)
	var uploadedMu sync.Mutex
	var uploaded []blob.Ref

	g.Go(func() error {
		defer close(toUpload)
		for batch := range batches {
			refs := make([]blob.Ref, len(batch))
			for i, ch := range batch {
				refs[i] = ch.br
			}
			have := make(map[blob.Ref]bool)
			err := f.c.StatBlobs(gctx, refs, func(sb blob.SizedRef) error {
				have[sb.Ref] = true
				return nil
			})
			if err != nil {
				return err
			}
			for _, ch := range batch {
				if have[ch.br] {
					release(int64(len(ch.data)))
					f.update(func(p *Progress) { p.ChunksPresent++ })
					continue
				}
				select {
				case toUpload <- ch:
				case <-gctx.Done():
					return gctx.Err()
				}
			}
		}
		return nil
	})
	for range uploadWorkers {
		g.Go(func() error {
			for ch := range toUpload {
				err := sk.upload(gctx, &client.UploadHandle{
					BlobRef:  ch.br,
					Size:     uint32(len(ch.data)),
					Contents: bytes.NewReader(ch.data),
				})
				release(int64(len(ch.data)))
				if err != nil {
					return err
				}
				uploadedMu.Lock()
				uploaded = append(uploaded, ch.br)
				uploadedMu.Unlock()
				f.update(func(p *Progress) {
					p.ChunksUploaded++
					p.BytesUploaded += int64(len(ch.data))
				})
			}
			return nil
		})
	}

	fc, cutErr := f.cut(gctx, batches, func(n int64) {
		heldMu.Lock()
		defer heldMu.Unlock()
		held += n
	})
	close(batches)
	if cutErr != nil {
		cancel()
	}
	if err := g.Wait(); err != nil && (cutErr == nil || errors.Is(cutErr, context.Canceled)) {
		return nil, nil, err
	}
	if cutErr != nil {
		return nil, nil, cutErr
	}
	return fc, uploaded, nil
}

// cut cuts the contents into chunks, sending batches of those new to the
// file to batches. It acquires the bytes of each chunk from the Limiter,
// calling held with their number, for the receiver to release. It holds a
// hashing core while it cuts.
func (f *File) cut(ctx context.Context, batches chan<- []chunk, held func(n int64)) (*schema.FileCut, error) {
	if err := f.lim.cores.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	haveCore := true
	defer func() {
		if haveCore {
			f.lim.cores.Release(1)
		}
	}()
	seen := make(map[blob.Ref]bool)
	var batch []chunk
	var batchBytes int
	send := func() error {
		select {
		case batches <- batch:
		case <-ctx.Done():
			return ctx.Err()
		}
		batch, batchBytes = nil, 0
		return nil
	}
	acquire := func(n int64) error {
		if f.lim.bytes.TryAcquire(n) {
			held(n)
			return nil
		}
		// The bytes to wait for may be those of the chunks batched so far,
		// so send them, to be uploaded and released, before waiting.
		if len(batch) > 0 {
			if err := send(); err != nil {
				return err
			}
		}
		// Waiting may also mean waiting for other Files' uploads, so give
		// up the core meanwhile.
		f.lim.cores.Release(1)
		haveCore = false
		if err := f.lim.bytes.Acquire(ctx, n); err != nil {
			return err
		}
		held(n)
		if err := f.lim.cores.Acquire(ctx, 1); err != nil {
			return err
		}
		haveCore = true
		return nil
	}
	fc, err := schema.CutFile(io.NewSectionReader(f.contents, 0, f.fi.Size()), func(br blob.Ref, data []byte) error {
		f.update(func(p *Progress) {
			p.Chunks++
			p.BytesCut += int64(len(data))
		})
		if seen[br] {
			return nil
		}
		seen[br] = true
		if err := acquire(int64(len(data))); err != nil {
			return err
		}
		batch = append(batch, chunk{br, data})
		batchBytes += len(data)
		if len(batch) >= statBatchChunks || batchBytes >= statBatchBytes {
			return send()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(batch) > 0 {
		if err := send(); err != nil {
			return nil, err
		}
	}
	return fc, nil
}

// missing returns which of the blobs of handles the server lacks.
func (f *File) missing(ctx context.Context, handles []*client.UploadHandle) (map[blob.Ref]bool, error) {
	missing := make(map[blob.Ref]bool, len(handles))
	refs := make([]blob.Ref, len(handles))
	for i, h := range handles {
		refs[i] = h.BlobRef
		missing[h.BlobRef] = true
	}
	err := f.c.StatBlobs(ctx, refs, func(sb blob.SizedRef) error {
		delete(missing, sb.Ref)
		return nil
	})
	return missing, err
}

func stringHandle(br blob.Ref, s string) *client.UploadHandle {
	return &client.UploadHandle{BlobRef: br, Size: uint32(len(s)), Contents: strings.NewReader(s)}
}

// A sink is where a File uploads blobs: an upload session, or, if the
// server has none, plain uploads, each durable once done.
type sink struct {
	c   *client.Client
	s   *client.UploadSession // or nil, for plain uploads
	own bool                  // whether s is the File's own
}

func (f *File) newSink(ctx context.Context) (*sink, error) {
	if f.opts.Session != nil {
		return &sink{c: f.c, s: f.opts.Session}, nil
	}
	s, err := f.c.NewUploadSession(ctx)
	if errors.Is(err, errors.ErrUnsupported) {
		return &sink{c: f.c}, nil
	}
	if err != nil {
		return nil, err
	}
	return &sink{c: f.c, s: s, own: true}, nil
}

// durableOnReturn reports whether what Run uploads is durable when it
// returns.
func (sk *sink) durableOnReturn() bool { return sk.s == nil || sk.own }

func (sk *sink) upload(ctx context.Context, h *client.UploadHandle) error {
	if sk.s != nil {
		return sk.s.Upload(ctx, h)
	}
	_, err := sk.c.Upload(ctx, h)
	return err
}

// waitDurable waits until the blobs refs, uploaded with upload, are
// durable.
func (sk *sink) waitDurable(ctx context.Context, refs []blob.Ref) error {
	if sk.s == nil || len(refs) == 0 {
		return nil
	}
	return sk.s.WaitDurable(ctx, refs)
}

// finish finishes uploading the file schema blob fileRef: with the
// File's own session, waiting until it's durable.
func (sk *sink) finish(ctx context.Context, fileRef blob.Ref) error {
	if !sk.own {
		return nil
	}
	return sk.s.WaitDurable(ctx, []blob.Ref{fileRef})
}

// close closes the File's own session.
func (sk *sink) close() {
	if sk.own {
		// Its blobs are durable, or the upload failed; either way,
		// there's nothing more to learn from it.
		sk.s.Close(context.Background())
	}
}
