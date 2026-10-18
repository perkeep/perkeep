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

// sessionThreshold is the size above which a File without the caller's
// session opens its own. Files no bigger are cut into one chunk, so a
// session would have nothing to commit together, and would only add
// requests: opening it, flushing it twice, and closing it.
const sessionThreshold = 256 << 10

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
	// opens its own, if the server supports them and the file is over
	// 256 KB (smaller ones are one chunk, with nothing to commit
	// together), and flushes and closes it before Run returns.
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

// ErrChanged is the error of a File whose contents changed while it ran:
// they no longer matched the whole-file refs from its first read of them
// (or given in Options.WholeRefs), or, if they have a Stat method like
// *os.File's, their size, mode or modification time changed. The file
// schema blob is then not uploaded.
var ErrChanged = errors.New("upload: file changed during upload")

// A File is an upload of one regular file's contents and its file schema
// blob. Make one with NewFile or NewFileFromReader.
type File struct {
	c        *client.Client
	name     string
	fi       fs.FileInfo // or nil, from NewFileFromReader
	contents io.ReaderAt // or nil, while reading from stream
	stream   io.Reader   // from NewFileFromReader, if contents is nil
	size     int64       // of the contents, or -1 if not yet known
	opts     Options
	lim      *Limiter

	// stat, if non-nil, is the contents' Stat method, to check the
	// contents haven't changed since before, its result when Run started.
	stat   func() (fs.FileInfo, error)
	before fs.FileInfo

	mu       sync.Mutex
	progress Progress
}

// NewFile returns an upload of the regular file described by fi, with
// contents, to c. opts may be nil.
//
// If contents has a Stat method, like *os.File's, and it reports a
// regular file, the File fails with ErrChanged if the file's size, mode
// or modification time change while it runs.
func NewFile(c *client.Client, fi fs.FileInfo, contents io.ReaderAt, opts *Options) *File {
	f := newFile(c, fi.Name(), opts)
	f.fi, f.contents, f.size = fi, contents, fi.Size()
	if s, ok := contents.(interface{ Stat() (fs.FileInfo, error) }); ok {
		f.stat = s.Stat
	}
	return f
}

// NewFileFromReader returns an upload of a file named name, with contents
// read once from r, for contents that can't be read twice or whose size
// isn't known, such as a file in a form. Its file schema blob records
// only the name (and Options.ModTime, if set). opts may be nil.
//
// It can't ask the server whether it has the contents before reading
// them, unless Options.WholeRefs gives their refs, but it still doesn't
// upload the chunks the server has. Run fails with ErrChanged if the
// contents don't match Options.WholeRefs.
func NewFileFromReader(c *client.Client, name string, r io.Reader, opts *Options) *File {
	f := newFile(c, name, opts)
	f.stream, f.size = r, -1
	return f
}

func newFile(c *client.Client, name string, opts *Options) *File {
	f := &File{c: c, name: name}
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
	FileRef        blob.Ref   // of the file schema blob
	FileSchemaSize uint32     // of the file schema blob
	WholeRefs      []blob.Ref // of the contents, of each hash function computed
	Size           int64      // of the contents

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
	if f.stat != nil {
		fi, err := f.stat()
		if err != nil {
			return nil, err
		}
		if fi.Mode().IsRegular() { // pipes and devices change as they're read
			f.before = fi
		}
	}
	res := &Result{Size: f.size, WholeRefs: f.opts.WholeRefs}
	file := f.newFileMap()

	var err error
	if len(res.WholeRefs) == 0 && f.stream == nil && f.size > dupCheckThreshold {
		res.WholeRefs, err = f.hashWhole(ctx)
		if err != nil {
			return nil, err
		}
	}
	if len(res.WholeRefs) > 0 {
		f.setPhase(PhaseCheckingServer)
		dupRef, err := f.c.SearchExistingFileSchema(ctx, res.WholeRefs...)
		if err == nil && dupRef.Valid() {
			return f.finishDup(ctx, res, file, dupRef)
		}
		// Without a dup, or a working search, the contents get uploaded.
	}

	if f.stream != nil {
		if err := f.readAhead(ctx); err != nil {
			return nil, err
		}
		res.Size = f.size
	}
	sk, err := f.newSink(ctx)
	if err != nil {
		return nil, err
	}
	defer sk.close()
	res.Durable = sk.durableOnReturn()

	// Hash the contents as they're cut too: to check they're what the
	// whole-file refs say, and, for contents from a reader without any,
	// to find their default one.
	var hashes []hash.Hash
	verify := res.WholeRefs
	for _, br := range verify {
		hashes = append(hashes, br.Hash()) // nil for an unsupported hash
	}
	var defaultHash hash.Hash
	if f.fi == nil && len(verify) == 0 { // from NewFileFromReader
		defaultHash = blob.NewHash()
		hashes = append(hashes, defaultHash)
	}
	var writers []io.Writer
	for _, h := range hashes {
		if h != nil {
			writers = append(writers, h)
		}
	}
	r := f.reader(ctx)
	if len(writers) > 0 {
		r = io.TeeReader(r, io.MultiWriter(writers...))
	}

	f.setPhase(PhaseUploading)
	var handles []*client.UploadHandle // of the bytes schema blobs
	var fileHandle *client.UploadHandle
	uploaded, missing, err := f.uploadChunks(ctx, sk, r, func(fc *schema.FileCut) ([]blob.Ref, error) {
		res.Size = fc.Size()
		for i, br := range verify {
			if hashes[i] != nil && !br.HashMatches(hashes[i]) {
				return nil, fmt.Errorf("%w: contents don't match whole-file ref %v", ErrChanged, br)
			}
		}
		if err := f.unchanged(); err != nil {
			return nil, err
		}
		if defaultHash != nil {
			res.WholeRefs = []blob.Ref{blob.RefFromHash(defaultHash)}
		}
		bytesBlobs, err := fc.Populate(file)
		if err != nil {
			return nil, err
		}
		var refs []blob.Ref
		for _, b := range bytesBlobs {
			handles = append(handles, stringHandle(b.BlobRef(), b.JSON()))
			refs = append(refs, b.BlobRef())
		}
		fileJSON := file.Blob().JSON()
		fileHandle = stringHandle(blob.RefFromString(fileJSON), fileJSON)
		return append(refs, fileHandle.BlobRef), nil
	})
	if err != nil {
		return nil, err
	}
	res.FileRef, res.FileSchemaSize = fileHandle.BlobRef, fileHandle.Size

	// Upload the bytes blobs the server lacks, then, once they and the
	// chunks are durable, the file schema blob, if it lacks it.
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
	if err := f.unchanged(); err != nil {
		return nil, err
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

// oneChunk reports whether the file is cut into one chunk, as files of
// up to sessionThreshold bytes are.
func (f *File) oneChunk() bool { return f.size >= 0 && f.size <= sessionThreshold }

// newFileMap returns the builder of the file's schema blob, without its
// parts.
func (f *File) newFileMap() *schema.Builder {
	if f.opts.ContentsOnly {
		return schema.NewFileMap("")
	}
	var file *schema.Builder
	if f.fi == nil {
		file = schema.NewFileMap(f.name)
	} else {
		file = schema.NewCommonFileMap(f.name, f.fi)
		file.SetType(schema.TypeFile)
	}
	if !f.opts.ModTime.IsZero() {
		file.SetModTime(f.opts.ModTime)
	}
	if f.opts.CapCtime {
		file.CapCreationTime()
	}
	return file
}

// readAhead reads the start of a stream: if it ends there, within
// sessionThreshold bytes, the File has its contents, of a known size,
// just as from NewFile.
func (f *File) readAhead(ctx context.Context) error {
	buf := make([]byte, sessionThreshold+1)
	n, err := io.ReadFull(ctxReader{ctx, f.stream}, buf)
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		f.contents, f.size, f.stream = bytes.NewReader(buf[:n]), int64(n), nil
		return nil
	case err != nil:
		return err
	}
	f.stream = io.MultiReader(bytes.NewReader(buf), f.stream)
	return nil
}

// reader returns a reader of the contents, to cut them, that stops once
// ctx is done.
func (f *File) reader(ctx context.Context) io.Reader {
	if f.stream != nil {
		return ctxReader{ctx, f.stream}
	}
	return ctxReader{ctx, io.NewSectionReader(f.contents, 0, f.size)}
}

// unchanged returns an error matching ErrChanged if the contents' Stat
// reports a change since Run started.
func (f *File) unchanged() error {
	if f.before == nil {
		return nil
	}
	after, err := f.stat()
	if err != nil {
		return err
	}
	if after.Size() != f.before.Size() || after.Mode() != f.before.Mode() || !after.ModTime().Equal(f.before.ModTime()) {
		return fmt.Errorf("%w: %s", ErrChanged, f.name)
	}
	return nil
}

// ctxReader is a reader of r that fails once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
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

	r := io.NewSectionReader(f.contents, 0, f.size)
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
//
// It uploads the one blob there is to upload, the file schema blob, into
// the caller's session if there is one, and otherwise plainly, with no
// session of its own to open and flush.
func (f *File) finishDup(ctx context.Context, res *Result, file *schema.Builder, dupRef blob.Ref) (*Result, error) {
	res.Dup = true
	sk := &sink{c: f.c, s: f.opts.Session}
	res.Durable = sk.durableOnReturn()
	dup, err := f.c.FetchSchemaBlob(ctx, dupRef)
	if err != nil {
		return nil, fmt.Errorf("upload: fetching file schema %v with the same contents: %w", dupRef, err)
	}
	if err := file.PopulateParts(dup.PartsSize(), dup.ByteParts()); err != nil {
		return nil, err
	}
	json := file.Blob().JSON()
	h := stringHandle(blob.RefFromString(json), json)
	res.FileRef, res.FileSchemaSize = h.BlobRef, h.Size
	if err := f.unchanged(); err != nil {
		return nil, err
	}
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

// A batch is of chunks to ask the server about, and then upload those it
// lacks. The last batch of a file also has tail: the refs of blobs to
// only ask about.
type batch struct {
	chunks []chunk
	tail   []blob.Ref
}

// uploadChunks cuts the contents into chunks, and uploads those the
// server lacks, returning the refs of the chunks it uploaded. Once the
// contents are cut, it calls tail with the cut, and asks the server about
// the blobs whose refs tail returns along with the last chunks, returning
// which of those the server lacks.
func (f *File) uploadChunks(ctx context.Context, sk *sink, r io.Reader, tail func(*schema.FileCut) ([]blob.Ref, error)) (uploaded []blob.Ref, tailMissing map[blob.Ref]bool, err error) {
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
	batches := make(chan batch, 1)
	toUpload := make(chan chunk)
	var uploadedMu sync.Mutex
	tailMissing = make(map[blob.Ref]bool) // only by the stat goroutine, until g.Wait

	g.Go(func() error {
		defer close(toUpload)
		for b := range batches {
			refs := make([]blob.Ref, 0, len(b.chunks)+len(b.tail))
			for _, ch := range b.chunks {
				refs = append(refs, ch.br)
			}
			refs = append(refs, b.tail...)
			have := make(map[blob.Ref]bool)
			// Asking about the blobs of a file of one chunk takes as long
			// as uploading them, and only spares uploading them when the
			// server has them anyway; uploads skip what the have cache has.
			if !f.oneChunk() {
				err := f.c.StatBlobs(gctx, refs, func(sb blob.SizedRef) error {
					have[sb.Ref] = true
					return nil
				})
				if err != nil {
					return err
				}
			}
			for _, br := range b.tail {
				if !have[br] {
					tailMissing[br] = true
				}
			}
			for _, ch := range b.chunks {
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

	cutErr := f.cut(gctx, batches, r, tail, func(n int64) {
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
	return uploaded, tailMissing, nil
}

// cut cuts the contents into chunks, sending batches of those new to the
// file to batches, the last with the refs tail returns. It acquires the
// bytes of each chunk from the Limiter, calling held with their number,
// for the receiver to release. It holds a hashing core while it cuts.
func (f *File) cut(ctx context.Context, batches chan<- batch, r io.Reader, tail func(*schema.FileCut) ([]blob.Ref, error), held func(n int64)) error {
	if err := f.lim.cores.Acquire(ctx, 1); err != nil {
		return err
	}
	haveCore := true
	defer func() {
		if haveCore {
			f.lim.cores.Release(1)
		}
	}()
	seen := make(map[blob.Ref]bool)
	var next batch
	var batchBytes int
	send := func() error {
		select {
		case batches <- next:
		case <-ctx.Done():
			return ctx.Err()
		}
		next, batchBytes = batch{}, 0
		return nil
	}
	acquire := func(n int64) error {
		if f.lim.bytes.TryAcquire(n) {
			held(n)
			return nil
		}
		// The bytes to wait for may be those of the chunks batched so far,
		// so send them, to be uploaded and released, before waiting.
		if len(next.chunks) > 0 {
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
	fc, err := schema.CutFile(r, func(br blob.Ref, data []byte) error {
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
		next.chunks = append(next.chunks, chunk{br, data})
		batchBytes += len(data)
		if len(next.chunks) >= statBatchChunks || batchBytes >= statBatchBytes {
			return send()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if next.tail, err = tail(fc); err != nil {
		return err
	}
	return send()
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
	if f.oneChunk() {
		return &sink{c: f.c}, nil
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
