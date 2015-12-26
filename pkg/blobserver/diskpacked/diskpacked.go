/*
Copyright 2013 Google Inc.

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
package diskpacked

import (
	"bufio"
	"bytes"
	"errors"
	"expvar"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/local"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/types"
	"camlistore.org/third_party/github.com/camlistore/lock"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"

	"go4.org/strutil"
	"go4.org/syncutil"
)

// TODO(wathiede): replace with glog.V(2) when we decide our logging story.
type debugT bool

var debug = debugT(false)

func (d debugT) Printf(format string, args ...interface{}) {
	if bool(d) {
		log.Printf(format, args...)
	}
}

func (d debugT) Println(args ...interface{}) {
	if bool(d) {
		log.Println(args...)
	}
}

const defaultMaxFileSize = 512 << 20 // 512MB

type storage struct {
	root        string
	index       sorted.KeyValue
	maxFileSize int64

	writeLock io.Closer // Provided by lock.Lock, and guards other processes from accesing the file open for writes.

	*local.Generationer

	mu     sync.Mutex // Guards all I/O state.
	closed bool
	writer *os.File
	fds    []*os.File
	size   int64
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
	if ok, _ := IsDir(dir); ok {
		// TODO: detect existing max size from size of files, if obvious,
		// and set maxSize to that?
	}
	return newStorage(dir, maxSize, nil)
}

// newIndex returns a new sorted.KeyValue, using either the given config, or the default.
func newIndex(root string, indexConf jsonconfig.Obj) (sorted.KeyValue, error) {
	if len(indexConf) > 0 {
		return sorted.NewKeyValue(indexConf)
	}
	return sorted.NewKeyValue(jsonconfig.Obj{
		"type": defaultIndexType,
		"file": filepath.Join(root, defaultIndexFile),
	})
}

// newStorage returns a new storage in path root with the given maxFileSize,
// or defaultMaxFileSize (512MB) if <= 0
func newStorage(root string, maxFileSize int64, indexConf jsonconfig.Obj) (s *storage, err error) {
	fi, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("storage root %q doesn't exist", root)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to stat directory %q: %v", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("storage root %q exists but is not a directory.", root)
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
	root = strings.TrimRight(root, `\/`)
	s = &storage{
		root:         root,
		index:        index,
		maxFileSize:  maxFileSize,
		Generationer: local.NewGenerationer(root),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.openAllPacks(); err != nil {
		return nil, err
	}
	if _, _, err := s.StorageGeneration(); err != nil {
		return nil, fmt.Errorf("Error initialization generation for %q: %v", root, err)
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
	return newStorage(path, int64(maxFileSize), indexConf)
}

func init() {
	blobserver.RegisterStorageConstructor("diskpacked", blobserver.StorageConstructor(newFromConfig))
}

// openForRead will open pack file n for read and keep a handle to it in
// s.fds.  os.IsNotExist returned if n >= the number of pack files in s.root.
// This function is not thread safe, s.mu should be locked by the caller.
func (s *storage) openForRead(n int) error {
	if n > len(s.fds) {
		panic(fmt.Sprintf("openForRead called out of order got %d, expected %d", n, len(s.fds)))
	}

	fn := s.filename(n)
	f, err := os.Open(fn)
	if err != nil {
		return err
	}
	openFdsVar.Add(s.root, 1)
	debug.Printf("diskpacked: opened for read %q", fn)
	s.fds = append(s.fds, f)
	return nil
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

	s.size, err = f.Seek(0, os.SEEK_END)
	if err != nil {
		return err
	}

	s.writer = f
	s.writeLock = l
	return nil
}

// closePack opens any pack file currently open for writing.
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
// This function is not thread safe, s.mu should be locked by the caller.
func (s *storage) nextPack() error {
	debug.Println("diskpacked: nextPack")
	s.size = 0
	if err := s.closePack(); err != nil {
		return err
	}
	n := len(s.fds)
	if err := s.openForWrite(n); err != nil {
		return err
	}
	return s.openForRead(n)
}

// openAllPacks opens read-only each pack file in s.root, populating s.fds.
// The latest pack file will also have a writable handle opened.
// This function is not thread safe, s.mu should be locked by the caller.
func (s *storage) openAllPacks() error {
	debug.Println("diskpacked: openAllPacks")
	n := 0
	for {
		err := s.openForRead(n)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			s.Close()
			return err
		}
		n++
	}

	if n == 0 {
		// If no pack files are found, we create one open for read and write.
		return s.nextPack()
	}

	// If 1 or more pack files are found, open the last one read and write.
	return s.openForWrite(n - 1)
}

func (s *storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var closeErr error
	if !s.closed {
		s.closed = true
		if err := s.index.Close(); err != nil {
			log.Println("diskpacked: closing index:", err)
		}
		for _, f := range s.fds {
			err := f.Close()
			openFdsVar.Add(s.root, -1)
			if err != nil {
				closeErr = err
			}
		}
		if err := s.closePack(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *storage) Fetch(br blob.Ref) (io.ReadCloser, uint32, error) {
	return s.fetch(br, 0, -1)
}

func (s *storage) SubFetch(br blob.Ref, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length < 0 {
		return nil, blob.ErrNegativeSubFetch
	}
	rc, _, err := s.fetch(br, offset, length)
	return rc, err
}

// length of -1 means all
func (s *storage) fetch(br blob.Ref, offset, length int64) (rc io.ReadCloser, size uint32, err error) {
	meta, err := s.meta(br)
	if err != nil {
		return nil, 0, err
	}

	if meta.file >= len(s.fds) {
		return nil, 0, fmt.Errorf("diskpacked: attempt to fetch blob from out of range pack file %d > %d", meta.file, len(s.fds))
	}
	rac := s.fds[meta.file]
	var rs io.ReadSeeker
	if length == -1 {
		// normal Fetch mode
		rs = io.NewSectionReader(rac, meta.offset, int64(meta.size))
	} else {
		if offset > int64(meta.size) {
			return nil, 0, blob.ErrOutOfRangeOffsetSubFetch
		} else if offset+length > int64(meta.size) {
			length = int64(meta.size) - offset
		}
		rs = io.NewSectionReader(rac, meta.offset+offset, length)
	}
	fn := rac.Name()
	// Ensure entry is in map.
	readVar.Add(fn, 0)
	if v, ok := readVar.Get(fn).(*expvar.Int); ok {
		rs = types.NewStatsReadSeeker(v, rs)
	}
	readTotVar.Add(s.root, 0)
	if v, ok := readTotVar.Get(s.root).(*expvar.Int); ok {
		rs = types.NewStatsReadSeeker(v, rs)
	}
	rsc := struct {
		io.ReadSeeker
		io.Closer
	}{
		rs,
		types.NopCloser,
	}
	return rsc, meta.size, nil
}

func (s *storage) filename(file int) string {
	return filepath.Join(s.root, fmt.Sprintf("pack-%05d.blobs", file))
}

var removeGate = syncutil.NewGate(20) // arbitrary

// RemoveBlobs removes the blobs from index and pads data with zero bytes
func (s *storage) RemoveBlobs(blobs []blob.Ref) error {
	batch := s.index.BeginBatch()
	var wg syncutil.Group
	for _, br := range blobs {
		br := br
		removeGate.Start()
		batch.Delete(br.String())
		wg.Go(func() error {
			defer removeGate.Done()
			if err := s.delete(br); err != nil {
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

func (s *storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) (err error) {
	var wg syncutil.Group

	for _, br := range blobs {
		br := br
		statGate.Start()
		wg.Go(func() error {
			defer statGate.Done()

			m, err := s.meta(br)
			if err == nil {
				dest <- m.SizedRef(br)
				return nil
			}
			if err == os.ErrNotExist {
				return nil
			}
			return err
		})
	}
	return wg.Err()
}

func (s *storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	defer close(dest)

	t := s.index.Find(after, "")
	defer func() {
		closeErr := t.Close()
		if err == nil {
			err = closeErr
		}
	}()
	for i := 0; i < limit && t.Next(); {
		key := t.Key()
		if key <= after {
			// EnumerateBlobs' semantics are '>', but sorted.KeyValue.Find is '>='.
			continue
		}
		br, ok := blob.Parse(key)
		if !ok {
			return fmt.Errorf("diskpacked: couldn't parse index key %q", key)
		}
		m, ok := parseBlobMeta(t.Value())
		if !ok {
			return fmt.Errorf("diskpacked: couldn't parse index value %q: %q", key, t.Value())
		}
		select {
		case dest <- m.SizedRef(br):
		case <-ctx.Done():
			return ctx.Err()
		}
		i++
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

// Type readSeekNopCloser is an io.ReadSeeker with a no-op Close method.
type readSeekNopCloser struct {
	io.ReadSeeker
}

func (readSeekNopCloser) Close() error { return nil }

func newReadSeekNopCloser(rs io.ReadSeeker) types.ReadSeekCloser {
	return readSeekNopCloser{rs}
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
	_, err = fd.Seek(offset, os.SEEK_SET)
	if err != nil {
		return err
	}

	const ioBufSize = 256 * 1024

	// We'll use bufio to avoid read system call overhead.
	r := bufio.NewReaderSize(fd, ioBufSize)

	for {
		//  Are we at the EOF of this pack?
		if _, err := r.Peek(1); err != nil {
			if err != io.EOF {
				return err
			}
			// EOF case; continue to the next pack, if any.
			fileNum += 1
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
			if _, err := io.CopyN(ioutil.Discard, r, int64(size)); err != nil {
				return err
			}
			offset += int64(size)
			continue
		}

		// Finally, read and send the blob.

		// TODO: remove this allocation per blob. We can make one instead
		// outside of the loop, guarded by a mutex, and re-use it, only to
		// lock the mutex and clone it if somebody actually calls Open
		// on the *blob.Blob. Otherwise callers just scanning all the blobs
		// to see if they have everything incur lots of garbage if they
		// don't open any blobs.
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		offset += int64(size)
		ref, ok := blob.ParseBytes(digest)
		if !ok {
			return fmt.Errorf("diskpacked: Invalid blobref %q", digest)
		}
		newReader := func() types.ReadSeekCloser {
			return newReadSeekNopCloser(bytes.NewReader(data))
		}
		blob := blob.NewBlob(ref, size, newReader)
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

func (s *storage) ReceiveBlob(br blob.Ref, source io.Reader) (sbr blob.SizedRef, err error) {
	var b bytes.Buffer
	n, err := b.ReadFrom(source)
	if err != nil {
		return
	}

	sbr = blob.SizedRef{Ref: br, Size: uint32(n)}

	// Check if it's a dup. Still accept it if the pack file on disk seems to be corrupt
	// or truncated.
	if m, err := s.meta(br); err == nil {
		fi, err := os.Stat(s.filename(m.file))
		if err == nil && fi.Size() >= m.offset+int64(m.size) {
			return sbr, nil
		}
	}

	err = s.append(sbr, &b)
	return
}

// append writes the provided blob to the current data file.
func (s *storage) append(br blob.SizedRef, r io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("diskpacked: write to closed storage")
	}
	// to be able to undo the append
	origOffset := s.size

	fn := s.writer.Name()
	n, err := fmt.Fprintf(s.writer, "[%v %v]", br.Ref.String(), br.Size)
	s.size += int64(n)
	writeVar.Add(fn, int64(n))
	writeTotVar.Add(s.root, int64(n))
	if err != nil {
		return err
	}

	// TODO(adg): remove this seek and the offset check once confident
	offset, err := s.writer.Seek(0, os.SEEK_CUR)
	if err != nil {
		return err
	}
	if offset != s.size {
		return fmt.Errorf("diskpacked: seek says offset = %d, we think %d",
			offset, s.size)
	}
	offset = s.size // make this a declaration once the above is removed

	n2, err := io.Copy(s.writer, r)
	s.size += n2
	writeVar.Add(fn, int64(n))
	writeTotVar.Add(s.root, int64(n))
	if err != nil {
		return err
	}
	if n2 != int64(br.Size) {
		return fmt.Errorf("diskpacked: written blob size %d didn't match size %d", n, br.Size)
	}
	if err = s.writer.Sync(); err != nil {
		return err
	}

	packIdx := len(s.fds) - 1
	if s.size > s.maxFileSize {
		if err := s.nextPack(); err != nil {
			return err
		}
	}
	err = s.index.Set(br.Ref.String(), blobMeta{packIdx, offset, br.Size}.String())
	if err != nil {
		if _, seekErr := s.writer.Seek(origOffset, os.SEEK_SET); seekErr != nil {
			log.Printf("ERROR seeking back to the original offset: %v", seekErr)
		} else if truncErr := s.writer.Truncate(origOffset); truncErr != nil {
			log.Printf("ERROR truncating file after index error: %v", truncErr)
		} else {
			s.size = origOffset
		}
	}
	return err
}

// meta fetches the metadata for the specified blob from the index.
func (s *storage) meta(br blob.Ref) (m blobMeta, err error) {
	ms, err := s.index.Get(br.String())
	if err != nil {
		if err == sorted.ErrNotFound {
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
