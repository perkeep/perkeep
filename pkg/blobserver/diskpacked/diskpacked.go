/*
Copyright 2013 The Perkeep Authors

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

/*
Package diskpacked registers the "diskpacked" blobserver storage type,
storing blobs packed together into monolithic data files
with an index listing the sizes and offsets of the little blobs
within the large files.

Example low-level config:

	"/storage/": {
	    "handler": "storage-diskpacked",
	    "handlerArgs": {
	       "path": "/var/camlistore/blobs"
	     }
	},
*/
package diskpacked // import "perkeep.org/pkg/blobserver/diskpacked"

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/local"
	"perkeep.org/pkg/sorted"

	"go4.org/jsonconfig"
	"go4.org/lock"
	"go4.org/readerutil"
	"go4.org/strutil"
	"go4.org/syncutil"
)

// TODO(wathiede): replace with glog.V(2) when we decide our logging story.
type debugT bool

var debug = debugT(false)

func (d debugT) Printf(format string, args ...any) {
	if bool(d) {
		log.Printf(format, args...)
	}
}

func (d debugT) Println(args ...any) {
	if bool(d) {
		log.Println(args...)
	}
}

const defaultMaxFileSize = 512 << 20 // 512MB

type storage struct {
	index sorted.KeyValue

	writeLock io.Closer // Provided by lock.Lock, and guards other processes from accessing the file open for writes.

	*local.Generationer

	root        string
	maxFileSize int64

	mu        sync.Mutex // Guards all I/O state.
	writer    *os.File
	fdCache   *fdCache
	fileCount int
	size      int64
	closed    bool
}

func (s *storage) String() string {
	return fmt.Sprintf("\"diskpacked\" blob packs at %s", s.root)
}

var (
	readVar     = expvar.NewMap("diskpacked-read-bytes")
	readTotVar  = expvar.NewMap("diskpacked-total-read-bytes")
	openFdsVar  = expvar.NewMap("diskpacked-open-fds")
	writeVar    = expvar.NewMap("diskpacked-write-bytes")
	writeTotVar = expvar.NewMap("diskpacked-total-write-bytes")
)

const defaultIndexType = sorted.DefaultKVFileType
const defaultIndexFile = "index." + defaultIndexType

// IsDir reports whether dir is a diskpacked directory.
func IsDir(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, defaultIndexFile))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// New returns a diskpacked storage implementation, adding blobs to
// the provided directory. It doesn't delete any existing blob pack
// files.
func New(dir string) (blobserver.Storage, error) {
	var maxSize int64
	if dh, err := os.Open(dir); err == nil {
		defer dh.Close()
		var nBlobFiles, atMax int
		if fis, err := dh.Readdir(-1); err == nil {
			// Detect existing max size from size of files, if obvious, and set maxSize to that
			for _, fi := range fis {
				if nm := fi.Name(); strings.HasPrefix(nm, "pack-") && strings.HasSuffix(nm, ".blobs") {
					nBlobFiles++
					if s := fi.Size(); s > maxSize {
						maxSize, atMax = fi.Size(), 0
					} else if s == maxSize {
						atMax++
					}
				}
			}
		}
		// Believe the deduced size only if at least 2 files has that maximum size,
		// and all files (except one) has the same.
		if !(atMax > 1 && nBlobFiles == atMax+1) {
			maxSize = 0
		}
	}
	return newStorage(dir, maxSize, 0, nil)
}

// newIndex returns a new sorted.KeyValue, using either the given config, or the default.
func newIndex(root string, indexConf jsonconfig.Obj) (sorted.KeyValue, error) {
	if len(indexConf) > 0 {
		return sorted.NewKeyValueMaybeWipe(indexConf)
	}
	return sorted.NewKeyValueMaybeWipe(jsonconfig.Obj{
		"type": defaultIndexType,
		"file": filepath.Join(root, defaultIndexFile),
	})
}

// newStorage returns a new storage in path root with the given maxFileSize,
// or defaultMaxFileSize (512MB) if <= 0.
//
// fdCacheLimit limits the maximum number of opened (cached) files,
// with 80% of ulimit -n being the default (fdCacheLimit <= 0).
func newStorage(root string, maxFileSize int64, fdCacheLimit int, indexConf jsonconfig.Obj) (s *storage, err error) {
	fi, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("storage root %q doesn't exist", root)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat directory %q: %w", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("storage root %q exists but is not a directory", root)
	}
	index, err := newIndex(root, indexConf)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			index.Close()
		}
	}()
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileSize
	}
	// Be consistent with trailing slashes.  Makes expvar stats for total
	// reads/writes consistent across diskpacked targets, regardless of what
	// people put in their low level config.
	if fdCacheLimit <= 0 {
		ul, _ := osutil.MaxFD()
		if ul > 0 {
			fdCacheLimit = int(ul * 80 / 100)
		}
	}
	if fdCacheLimit == 0 {
		fdCacheLimit = 1024
	}
	root = strings.TrimRight(root, `\/`)
	s = &storage{
		root:         root,
		index:        index,
		maxFileSize:  maxFileSize,
		fdCache:      newFDCache(root, fdCacheLimit),
		Generationer: local.NewGenerationer(root),
	}

	if err := s.openAllPacks(); err != nil {
		s.Close()
		return nil, err
	}
	// Make the names of the index and pack files that opening them may
	// have created durable.
	if err := osutil.SyncDir(root); err != nil {
		s.Close()
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, _, err := s.StorageGeneration(); err != nil {
		return nil, fmt.Errorf("error initialization generation for %q: %w", root, err)
	}
	return s, nil
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	var (
		path        = config.RequiredString("path")
		maxFileSize = config.OptionalInt("maxFileSize", 0)
		indexConf   = config.OptionalObject("metaIndex")
	)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return newStorage(path, int64(maxFileSize), 0, indexConf)
}

func init() {
	blobserver.RegisterStorageConstructor("diskpacked", blobserver.StorageConstructor(newFromConfig))
}

// openForWrite will create or open pack file n for writes, create a lock
// visible external to the process and seek to the end of the file ready for
// appending new data.
// This function is not thread safe, s.mu should be locked by the caller.
func (s *storage) openForWrite(n int) error {
	fn := s.filename(n)
	l, err := lock.Lock(fn + ".lock")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(fn, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		l.Close()
		return err
	}
	openFdsVar.Add(s.root, 1)
	debug.Printf("diskpacked: opened for write %q", fn)

	s.size, err = f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		l.Close()
		return err
	}

	s.writer = f
	s.writeLock = l
	return nil
}

// closePack closes any pack file currently open for writing.
func (s *storage) closePack() error {
	var err error
	if s.writer != nil {
		err = s.writer.Close()
		openFdsVar.Add(s.root, -1)
		s.writer = nil
	}
	if s.writeLock != nil {
		lerr := s.writeLock.Close()
		if err == nil {
			err = lerr
		}
		s.writeLock = nil
	}
	return err
}

// nextPack will close the current writer and release its lock if open,
// open the next pack file in sequence for writing, grab its lock, set it
// to the currently active writer, and open another copy for read-only use.
// The new file's name is durable before any blob is written to it. If
// nextPack fails, s.writer may be nil, and append tries again.
// This function is not thread safe, s.mu should be locked by the caller.
func (s *storage) nextPack() error {
	debug.Println("diskpacked: nextPack")
	s.size = 0
	if err := s.closePack(); err != nil {
		return err
	}
	n := s.fileCount
	if err := s.openForWrite(n); err != nil {
		return err
	}
	if err := osutil.SyncDir(s.root); err != nil {
		s.closePack()
		return err
	}
	s.fileCount = n + 1
	return s.fdCache.open(context.Background(), s.filename(n))
}

// openAllPacks counts the pack files in s.root.
// The latest pack file will have a writable handle opened.
// Other pack files are opened for read lazily, on demand.
// This function is not thread safe, s.mu should be locked by the caller.
func (s *storage) openAllPacks() error {
	debug.Println("diskpacked: openAllPacks")
	n := 0
	for {
		_, err := os.Stat(s.filename(n))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return err
		}
		n++
		s.fileCount++
	}

	if n == 0 {
		// If no pack files are found, we create one open for read and write.
		return s.nextPack()
	}

	// If 1 or more pack files are found, open the last one read and write.
	return s.openForWrite(n - 1)
}

// Close index and all opened fds, with locking.
func (s *storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	var closeErr error
	s.closed = true
	if err := s.index.Close(); err != nil {
		log.Println("diskpacked: closing index:", err)
	}

	// Don't wait for readers still holding pack files;
	// their files close when the readers do.
	closeErr = s.fdCache.Shutdown()
	if err := s.closePack(); err != nil && closeErr == nil {
		closeErr = err
	}
	return closeErr
}

func (s *storage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	return s.fetch(ctx, br, 0, -1)
}

func (s *storage) SubFetch(ctx context.Context, br blob.Ref, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length < 0 {
		return nil, blob.ErrNegativeSubFetch
	}
	rc, _, err := s.fetch(ctx, br, offset, length)
	return rc, err
}

// length of -1 means all
func (s *storage) fetch(ctx context.Context, br blob.Ref, offset, length int64) (rc io.ReadCloser, size uint32, err error) {
	meta, err := s.meta(br)
	if err != nil {
		return nil, 0, err
	}

	s.mu.Lock()
	fileCount := s.fileCount
	s.mu.Unlock()
	if meta.file >= fileCount || meta.file < 0 {
		return nil, 0, fmt.Errorf("diskpacked: attempt to fetch blob from out of range pack file %d not in [0, %d]", meta.file, fileCount)
	}
	var sr *sectionReader
	fn := s.filename(meta.file)
	if length == -1 {
		// normal Fetch mode
		sr, err = s.fdCache.OpenSection(ctx, fn,
			meta.offset, int64(meta.size))
	} else {
		if offset > int64(meta.size) {
			return nil, 0, blob.ErrOutOfRangeOffsetSubFetch
		} else if offset+length > int64(meta.size) {
			length = int64(meta.size) - offset
		}
		sr, err = s.fdCache.OpenSection(ctx, fn,
			meta.offset+offset, length)
	}
	if err != nil {
		return nil, 0, err
	}
	// Ensure entry is in map.
	readVar.Add(fn, 0)
	var rs io.ReadSeeker = sr
	if v, ok := readVar.Get(fn).(*expvar.Int); ok {
		rs = readerutil.NewStatsReadSeeker(v, rs)
	}
	readTotVar.Add(s.root, 0)
	if v, ok := readTotVar.Get(s.root).(*expvar.Int); ok {
		rs = readerutil.NewStatsReadSeeker(v, rs)
	}
	rsc := struct {
		io.ReadSeeker
		io.Closer
	}{
		rs,
		sr,
	}
	return rsc, meta.size, nil
}

func (s *storage) filename(file int) string {
	return filepath.Join(s.root, fmt.Sprintf("pack-%05d.blobs", file))
}

var removeGate = syncutil.NewGate(20) // arbitrary

// RemoveBlobs removes the blobs from index and pads data with zero bytes
func (s *storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	batch := s.index.BeginBatch()
	var wg syncutil.Group
	for _, br := range blobs {
		removeGate.Start()
		batch.Delete(br.String())
		wg.Go(func() error {
			defer removeGate.Done()
			if err := s.delete(br); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		})
	}
	err1 := wg.Err()
	err2 := s.index.CommitBatch(batch)
	if err1 != nil {
		return err1
	}
	return err2
}

var statGate = syncutil.NewGate(20) // arbitrary

func (s *storage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(br blob.Ref) (sb blob.SizedRef, err error) {
		m, err := s.meta(br)
		if err == nil {
			return m.SizedRef(br), nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return sb, nil
		}
		return sb, err
	})
}

func (s *storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	if limit <= 0 {
		return nil
	}
	for r := range sorted.ScanPaged(s.index, after, "") {
		row, err := r.Value()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.Key <= after {
			// EnumerateBlobs' semantics are '>', but sorted.KeyValue.Find is '>='.
			continue
		}
		br, ok := blob.Parse(row.Key)
		if !ok {
			return fmt.Errorf("diskpacked: couldn't parse index key %q", row.Key)
		}
		m, ok := parseBlobMeta(row.Value)
		if !ok {
			return fmt.Errorf("diskpacked: couldn't parse index value %q: %q", row.Key, row.Value)
		}
		select {
		case dest <- m.SizedRef(br):
		case <-ctx.Done():
			return ctx.Err()
		}
		limit--
		if limit == 0 {
			return nil
		}
	}
	return nil
}

// The continuation token will be in the form: "<pack#> <offset>"
func parseContToken(token string) (pack int, offset int64, err error) {
	// Special case
	if token == "" {
		pack = 0
		offset = 0
		return
	}
	_, err = fmt.Sscan(token, &pack, &offset)

	return
}

// readHeader parses "[sha1-fooooo 1234]" from r and returns the
// number of bytes read (including the starting '[' and ending ']'),
// the blobref bytes (not necessarily valid) and the number as a
// uint32.
// The consumed count returned is only valid if err == nil.
// The returned digest slice is only valid until the next read from br.
func readHeader(br *bufio.Reader) (consumed int, digest []byte, size uint32, err error) {
	line, err := br.ReadSlice(']')
	if err != nil {
		return
	}
	const minSize = len("[b-c 0]")
	sp := bytes.IndexByte(line, ' ')
	size64, err := strutil.ParseUintBytes(line[sp+1:len(line)-1], 10, 32)
	if len(line) < minSize || line[0] != '[' || line[len(line)-1] != ']' || sp < 0 || err != nil {
		return 0, nil, 0, errors.New("diskpacked: invalid header reader")
	}
	return len(line), line[1:sp], uint32(size64), nil
}

// The header of deleted blobs has a digest in which the hash type is
// set to all 'x', the hash value is all '0', and has the correct size.
var deletedBlobRef = regexp.MustCompile(`^x+-0+$`)

var _ blobserver.BlobStreamer = (*storage)(nil)

// StreamBlobs Implements the blobserver.StreamBlobs interface.
func (s *storage) StreamBlobs(ctx context.Context, dest chan<- blobserver.BlobAndToken, contToken string) error {
	defer close(dest)

	fileNum, offset, err := parseContToken(contToken)
	if err != nil {
		return errors.New("diskpacked: invalid continuation token")
	}
	debug.Printf("Continuing blob streaming from pack %s, offset %d",
		s.filename(fileNum), offset)

	fd, err := os.Open(s.filename(fileNum))
	if err != nil {
		return err
	}
	// fd will change over time; Close whichever is current when we exit.
	defer func() {
		if fd != nil { // may be nil on os.Open error below
			fd.Close()
		}
	}()

	// ContToken always refers to the exact next place we will read from.
	// Note that seeking past the end is legal on Unix and for io.Seeker,
	// but that will just result in a mostly harmless EOF.
	//
	// TODO: probably be stricter here and don't allow seek past
	// the end, since we know the size of closed files and the
	// size of the file diskpacked currently still writing.
	_, err = fd.Seek(offset, io.SeekStart)
	if err != nil {
		return err
	}

	const ioBufSize = 256 * 1024

	// We'll use bufio to avoid read system call overhead.
	r := bufio.NewReaderSize(fd, ioBufSize)

	for {
		//  Are we at the EOF of this pack?
		if _, err := r.Peek(1); err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			// EOF case; continue to the next pack, if any.
			fileNum++
			offset = 0
			fd.Close() // Close the previous pack
			fd, err = os.Open(s.filename(fileNum))
			if os.IsNotExist(err) {
				// We reached the end.
				return nil
			} else if err != nil {
				return err
			}
			r.Reset(fd)
			continue
		}

		thisOffset := offset // of current blob's header
		consumed, digest, size, err := readHeader(r)
		if err != nil {
			return err
		}

		offset += int64(consumed)
		if deletedBlobRef.Match(digest) {
			// Skip over deletion padding
			if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
				return err
			}
			offset += int64(size)
			continue
		}

		ref, ok := blob.ParseBytes(digest)
		if !ok {
			return fmt.Errorf("diskpacked: Invalid blobref %q", digest)
		}

		// Finally, read and send the blob.

		// TODO: remove this allocation per blob. We can make one instead
		// outside of the loop, guarded by a mutex, and re-use it, only to
		// lock the mutex and clone it if somebody actually calls ReadFull
		// on the *blob.Blob. Otherwise callers just scanning all the blobs
		// to see if they have everything incur lots of garbage if they
		// don't open any blobs.
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		offset += int64(size)
		blob := blob.NewBlob(ref, size, func(context.Context) ([]byte, error) {
			return data, nil
		})
		select {
		case dest <- blobserver.BlobAndToken{
			Blob:  blob,
			Token: fmt.Sprintf("%d %d", fileNum, thisOffset),
		}:
			// Nothing.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *storage) ReceiveBlob(ctx context.Context, br blob.Ref, source io.Reader) (sbr blob.SizedRef, err error) {
	var b bytes.Buffer
	n, err := b.ReadFrom(source)
	if err != nil {
		return
	}

	sbr = blob.SizedRef{Ref: br, Size: uint32(n)}
	if s.stored(sbr.Ref) {
		return sbr, nil
	}
	be := &blobserver.BlobsError{Op: "receive"}
	s.appendBlobs([]pendingBlob{{sbr, bytes.NewReader(b.Bytes())}}, be)
	return sbr, be.Err(br)
}

// ReceiveBlobs implements blobserver.BlobsReceiver. It appends the blobs
// to the current pack file, then syncs it and indexes them, so they share
// one sync and one index commit (per pack file, if they fill one).
func (s *storage) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	be := &blobserver.BlobsError{Op: "receive"}
	pending := make([]pendingBlob, 0, len(blobs))
	for _, b := range blobs {
		if s.stored(b.Ref()) {
			continue
		}
		r, err := b.ReadAll(ctx)
		if err != nil {
			be.Add(b.Ref(), err)
			continue
		}
		pending = append(pending, pendingBlob{b.SizedRef(), r})
	}
	s.appendBlobs(pending, be)
	return be.OrNil()
}

// stored reports whether br is already stored. A blob whose pack file
// on disk seems to be corrupt or truncated isn't, so it's stored again.
func (s *storage) stored(br blob.Ref) bool {
	m, err := s.meta(br)
	if err != nil {
		return false
	}
	fi, err := os.Stat(s.filename(m.file))
	return err == nil && fi.Size() >= m.offset+int64(m.size)
}

// A pendingBlob is a blob to append.
type pendingBlob struct {
	sb blob.SizedRef
	r  io.Reader
}

// appendBlobs appends blobs to the pack files, recording failures in be.
func (s *storage) appendBlobs(blobs []pendingBlob, be *blobserver.BlobsError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(blobs) > 0 {
		blobs = blobs[s.appendGroupLocked(blobs, be):]
	}
}

// appendGroupLocked appends blobs to the current pack file, stopping after
// one fills it, syncs the file, and then indexes the written blobs in one
// commit. If the pack file is full, it starts the next one. It returns how
// many blobs it handled, recording their failures in be. s.mu must be
// held.
func (s *storage) appendGroupLocked(blobs []pendingBlob, be *blobserver.BlobsError) (handled int) {
	failRest := func(err error) {
		for _, pb := range blobs[handled:] {
			be.Add(pb.sb.Ref, err)
		}
		handled = len(blobs)
	}
	if s.closed {
		failRest(errors.New("diskpacked: write to closed storage"))
		return
	}
	if s.writer == nil {
		// Starting a new pack file failed after a previous append.
		if err := s.nextPack(); err != nil {
			failRest(err)
			return
		}
	}

	groupStart := s.size
	packIdx := s.fileCount - 1
	batch := s.index.BeginBatch()
	var written []blob.Ref
	for handled < len(blobs) && s.size <= s.maxFileSize {
		pb := blobs[handled]
		offset, err := s.writeBlobLocked(pb)
		if err != nil {
			// Probably the disk is full or failing; don't try the rest.
			failRest(err)
			break
		}
		handled++
		batch.Set(pb.sb.Ref.String(), blobMeta{packIdx, offset, pb.sb.Size}.String())
		written = append(written, pb.sb.Ref)
	}
	if len(written) > 0 {
		// The blobs aren't visible until they're indexed, after the sync.
		err := s.writer.Sync()
		if err == nil {
			err = s.index.CommitBatch(batch)
		}
		if err != nil {
			s.truncateLocked(groupStart)
			for _, br := range written {
				be.Add(br, err)
			}
		}
	}

	// The written blobs are durable and indexed, so they're received even
	// if starting the next pack file fails; the next append retries.
	if s.size > s.maxFileSize {
		if err := s.nextPack(); err != nil {
			log.Printf("diskpacked: starting a new pack file in %s: %v", s.root, err)
		}
	}
	return handled
}

// writeBlobLocked writes pb to the current pack file, without syncing it,
// and returns the offset of its data. If it fails, it removes whatever it
// wrote. s.mu must be held.
func (s *storage) writeBlobLocked(pb pendingBlob) (offset int64, err error) {
	origOffset := s.size
	defer func() {
		if err != nil {
			s.truncateLocked(origOffset)
		}
	}()

	fn := s.writer.Name()
	n, err := fmt.Fprintf(s.writer, "[%v %v]", pb.sb.Ref.String(), pb.sb.Size)
	s.size += int64(n)
	writeVar.Add(fn, int64(n))
	writeTotVar.Add(s.root, int64(n))
	if err != nil {
		return 0, err
	}

	// TODO(adg): remove this seek and the offset check once confident
	offset, err = s.writer.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if offset != s.size {
		return 0, fmt.Errorf("diskpacked: seek says offset = %d, we think %d",
			offset, s.size)
	}

	n2, err := io.Copy(s.writer, pb.r)
	s.size += n2
	writeVar.Add(fn, n2)
	writeTotVar.Add(s.root, n2)
	if err != nil {
		return 0, err
	}
	if n2 != int64(pb.sb.Size) {
		return 0, fmt.Errorf("diskpacked: written blob size %d didn't match size %d", n2, pb.sb.Size)
	}
	return offset, nil
}

// truncateLocked truncates the current pack file to size, after a failed
// append. s.mu must be held.
func (s *storage) truncateLocked(size int64) {
	if _, err := s.writer.Seek(size, io.SeekStart); err != nil {
		log.Printf("ERROR seeking back to the original offset: %v", err)
	} else if err := s.writer.Truncate(size); err != nil {
		log.Printf("ERROR truncating file after failed append: %v", err)
	} else {
		s.size = size
	}
}

// meta fetches the metadata for the specified blob from the index.
func (s *storage) meta(br blob.Ref) (m blobMeta, err error) {
	ms, err := s.index.Get(br.String())
	if err != nil {
		if errors.Is(err, sorted.ErrNotFound) {
			err = os.ErrNotExist
		}
		return
	}
	m, ok := parseBlobMeta(ms)
	if !ok {
		err = fmt.Errorf("diskpacked: bad blob metadata: %q", ms)
	}
	return
}

// blobMeta is the blob metadata stored in the index.
type blobMeta struct {
	file   int
	offset int64
	size   uint32
}

func parseBlobMeta(s string) (m blobMeta, ok bool) {
	n, err := fmt.Sscan(s, &m.file, &m.offset, &m.size)
	return m, n == 3 && err == nil
}

func (m blobMeta) String() string {
	return fmt.Sprintf("%v %v %v", m.file, m.offset, m.size)
}

func (m blobMeta) SizedRef(br blob.Ref) blob.SizedRef {
	return blob.SizedRef{Ref: br, Size: m.size}
}
