/*
Copyright 2014 The Camlistore AUTHORS

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
Package blobpacked registers the "blobpacked" blobserver storage type,
storing blobs initially as one physical blob per logical blob, but then
rearranging little physical blobs into large contiguous blobs organized by
how they'll likely be accessed. An index tracks the mapping from logical to
physical blobs.

Example low-level config:

     "/storage/": {
         "handler": "storage-blobpacked",
         "handlerArgs": {
            "smallBlobs": "/small/",
            "largeBlobs": "/large/",
            "metaIndex": {
               "type": "mysql",
                .....
            }
          }
     }

The resulting large blobs are valid zip files. Those blobs may up be up to
16 MB and contain the original contiguous file (or fractions of it), as well
as metadata about how the file is cut up. The zip file will have the
following structure:

    foo.jpg       (or whatever)
    camlistore/sha1-beb1df0b75952c7d277905ad14de71ef7ef90c44.json (some file ref)
    camlistore/sha1-a0ceb10b04403c9cc1d032e07a9071db5e711c9a.json (some bytes ref)
    camlistore/sha1-7b4d9c8529c27d592255c6dfb17188493db96ccc.json (another bytes ref)
    camlistore/camlistore-pack-manifest.json

The camlistore-pack-manifest.json is documented on the exported
Manifest type. It looks like this:

    {
      "wholeRef": "sha1-0e64816d731a56915e8bb4ae4d0ac7485c0b84da",
      "wholeSize": 2962227200, // 2.8GB; so will require ~176-180 16MB chunks
      "wholePartIndex": 17,    // 0-based
      "dataBlobsOrigin": "sha1-355705cf62a56669303d2561f29e0620a676c36e",
      "dataBlobs": [
          {"blob": "sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15", "offset": 0, "size": 273048},
          {"blob": "sha1-e242ed3bffccdf271b7fbaf34ed72d089537b42f", "offset": 273048, "size": 112783},
          {"blob": "sha1-6eadeac2dade6347e87c0d24fd455feffa7069f0", "offset": 385831, ...},
          {"blob": "sha1-beb1df0b75952c7d277905ad14de71ef7ef90c44", "offset": ...},
          {"blob": "sha1-a0ceb10b04403c9cc1d032e07a9071db5e711c9a", "offset": ...},
          {"blob": "sha1-7b4d9c8529c27d592255c6dfb17188493db96ccc", "offset": ...}
      ],
    }

The manifest.json ensures that if the metadata index is lost, all the
data can be reconstructed from the raw zip files.

The 'wholeRef' property specifies which large file that this zip is building
up.  If the file is less than 15.5 MB or so (leaving room for the zip
overhead and manifest size), it will probably all be in one zip and the
first file in the zip will be the whole thing. Otherwise it'll be cut across
multiple zip files, each no larger than 16MB. In that case, each part of the
file will have a different 'wholePartIndex' number, starting at index
0. Each will have the same 'wholeSize'.
*/

package blobpacked

// TODO: BlobStreamer using the zip manifests, for recovery.

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/pools"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/third_party/go/pkg/archive/zip"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"

	"go4.org/strutil"
	"go4.org/syncutil"
)

// TODO: evaluate whether this should even be 0, to keep the schema blobs together at least.
// Files under this size aren't packed.
const packThreshold = 512 << 10

// Overhead for zip files.
// These are only variables so they can be changed by tests, but
// they're effectively constant.
var (
	zipFixedOverhead = 20 /*directory64EndLen*/ +
		56 /*directory64LocLen */ +
		22 /*directoryEndLen*/ +
		512 /* conservative slop space, to get us away from 16 MB zip boundary */
	zipPerEntryOverhead = 30 /*fileHeaderLen*/ +
		24 /*dataDescriptor64Len*/ +
		22 /*directoryEndLen*/ +
		len("camlistore/sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15.dat")*3/2 /*padding for larger blobrefs*/
)

// meta key prefixes
const (
	blobMetaPrefix      = "b:"
	blobMetaPrefixLimit = "b;"

	wholeMetaPrefix = "w:"
)

const (
	zipManifestPath = "camlistore/camlistore-pack-manifest.json"
)

type subFetcherStorage interface {
	blobserver.Storage
	blob.SubFetcher
}

type storage struct {
	small blobserver.Storage
	large subFetcherStorage

	// meta key -> value rows are:
	//
	// For logical blobs packed within a large blog, "b:" prefix:
	//   b:sha1-xxxx -> "<size> <big-blobref> <offset_u32>"
	//
	// For wholerefs: (wholeMetaPrefix)
	//   w:sha1-xxxx(wholeref) -> "<nbytes_total_u64> <nchunks_u32>"
	// Then for each big nchunk of the file:
	//   w:sha1-xxxx:0 -> "<zipchunk-blobref> <offset-in-zipchunk-blobref> <offset-in-whole_u64> <length_u32>"
	//   w:sha1-xxxx:...
	//   w:sha1-xxxx:(nchunks-1)
	//
	// For marking that zips that have blobs (possibly all)
	// deleted from inside them: (deleted zip)
	//   d:sha1-xxxxxx -> <unix-time-of-delete>
	meta sorted.KeyValue

	// If non-zero, the maximum size of a zip blob.
	// It defaults to constants.MaxBlobSize.
	forceMaxZipBlobSize int

	skipDelete bool // don't delete from small after packing

	packGate *syncutil.Gate

	loggerOnce sync.Once
	log        *log.Logger // nil means default
}

var (
	_ blobserver.BlobStreamer    = (*storage)(nil)
	_ blobserver.Generationer    = (*storage)(nil)
	_ blobserver.WholeRefFetcher = (*storage)(nil)
)

func (s *storage) String() string {
	return fmt.Sprintf("\"blobpacked\" storage")
}

func (s *storage) Logf(format string, args ...interface{}) {
	s.logger().Printf(format, args...)
}

func (s *storage) logger() *log.Logger {
	s.loggerOnce.Do(s.initLogger)
	return s.log
}

func (s *storage) initLogger() {
	if s.log == nil {
		s.log = log.New(os.Stderr, "blobpacked: ", log.LstdFlags)
	}
}

func (s *storage) init() {
	s.packGate = syncutil.NewGate(10)
}

func (s *storage) maxZipBlobSize() int {
	if s.forceMaxZipBlobSize > 0 {
		return s.forceMaxZipBlobSize
	}
	return constants.MaxBlobSize
}

func init() {
	blobserver.RegisterStorageConstructor("blobpacked", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		smallPrefix = conf.RequiredString("smallBlobs")
		largePrefix = conf.RequiredString("largeBlobs")
		metaConf    = conf.RequiredObject("metaIndex")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	small, err := ld.GetStorage(smallPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load smallBlobs at %s: %v", smallPrefix, err)
	}
	large, err := ld.GetStorage(largePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load largeBlobs at %s: %v", largePrefix, err)
	}
	largeSubber, ok := large.(subFetcherStorage)
	if !ok {
		return nil, fmt.Errorf("largeBlobs at %q of type %T doesn't support fetching sub-ranges of blobs",
			largePrefix, large)
	}
	meta, err := sorted.NewKeyValue(metaConf)
	if err != nil {
		return nil, fmt.Errorf("failed to setup blobpacked metaIndex: %v", err)
	}
	sto := &storage{
		small: small,
		large: largeSubber,
		meta:  meta,
	}
	sto.init()

	// Check for a weird state: zip files exist, but no metadata about them
	// is recorded. This is probably a corrupt state, and the user likely
	// wants to recover.
	if !sto.anyMeta() && sto.anyZipPacks() {
		log.Printf("Warning: blobpacked storage detects non-zero packed zips, but no metadata. Please re-start in recovery mode.")
		// TODO: add a recovery mode.
		// Old TODO was:
		// fail with a "known corrupt" message and refuse to
		// start unless in recovery mode (perhaps a new environment
		// var? or flag passed down?) using StreamBlobs starting at
		// "l:".  Could even do it automatically if total size is
		// small or fast enough? But that's confusing if it only
		// sometimes finishes recovery. We probably want various
		// server start-up modes anyway: "check", "recover", "garbage
		// collect", "readonly".  So might as well introduce that
		// concept now.

		// TODO: test start-up recovery mode, once it works.
	}
	return sto, nil
}

func WipeMeta(s blobserver.Storage) error {
	bps, ok := s.(*storage)
	if !ok {
		return fmt.Errorf("argument is not blobpacked storage but a %T", s)
	}
	wiper, ok := bps.meta.(sorted.Wiper)
	if !ok {
		return fmt.Errorf("blobpacked meta storage type %T doesn't support sorted.Wiper", bps.meta)
	}
	log.Printf("Wiping blobpacked meta type %T ...", bps.meta)
	if err := wiper.Wipe(); err != nil {
		return fmt.Errorf("error wiping blobpacked meta sorted key/value type %T: %v", bps.meta, err)
	}
	return nil
}

func (s *storage) anyMeta() (v bool) {
	// TODO: we only care about getting 1 row, but the
	// sorted.KeyValue interface doesn't let us give it that
	// hint. Care?
	sorted.Foreach(s.meta, func(_, _ string) error {
		v = true
		return errors.New("stop")
	})
	return
}

func (s *storage) anyZipPacks() (v bool) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	dest := make(chan blob.SizedRef, 1)
	if err := s.large.EnumerateBlobs(ctx, dest, "", 1); err != nil {
		// Not a great interface in general, but only needed
		// by the start-up check for now, where it doesn't
		// really matter.
		return false
	}
	_, ok := <-dest
	return ok
}

func (s *storage) Close() error {
	return nil
}

func (s *storage) StorageGeneration() (initTime time.Time, random string, err error) {
	sgen, sok := s.small.(blobserver.Generationer)
	lgen, lok := s.large.(blobserver.Generationer)
	if !sok || !lok {
		return time.Time{}, "", blobserver.GenerationNotSupportedError("underlying storage engines don't support Generationer")
	}
	st, srand, err := sgen.StorageGeneration()
	if err != nil {
		return
	}
	lt, lrand, err := lgen.StorageGeneration()
	if err != nil {
		return
	}
	hash := sha1.New()
	io.WriteString(hash, srand)
	io.WriteString(hash, lrand)
	maxTime := func(a, b time.Time) time.Time {
		if a.After(b) {
			return a
		}
		return b
	}
	return maxTime(lt, st), fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (s *storage) ResetStorageGeneration() error {
	var retErr error
	for _, st := range []blobserver.Storage{s.small, s.large} {
		if g, ok := st.(blobserver.Generationer); ok {
			if err := g.ResetStorageGeneration(); err != nil {
				retErr = err
			}
		}
	}
	return retErr
}

type meta struct {
	exists   bool
	size     uint32
	largeRef blob.Ref // if invalid, then on small if exists
	largeOff uint32
}

func (m *meta) isPacked() bool { return m.largeRef.Valid() }

// if not found, err == nil.
func (s *storage) getMetaRow(br blob.Ref) (meta, error) {
	v, err := s.meta.Get(blobMetaPrefix + br.String())
	if err == sorted.ErrNotFound {
		return meta{}, nil
	}
	return parseMetaRow([]byte(v))
}

var singleSpace = []byte{' '}

// parses:
// "<size_u32> <big-blobref> <big-offset>"
func parseMetaRow(v []byte) (m meta, err error) {
	row := v
	sp := bytes.IndexByte(v, ' ')
	if sp < 1 || sp == len(v)-1 {
		return meta{}, fmt.Errorf("invalid metarow %q", v)
	}
	m.exists = true
	size, err := strutil.ParseUintBytes(v[:sp], 10, 32)
	if err != nil {
		return meta{}, fmt.Errorf("invalid metarow size %q", v)
	}
	m.size = uint32(size)
	v = v[sp+1:]

	// remains: "<big-blobref> <big-offset>"
	if bytes.Count(v, singleSpace) != 1 {
		return meta{}, fmt.Errorf("invalid metarow %q: wrong number of spaces", row)
	}
	sp = bytes.IndexByte(v, ' ')
	largeRef, ok := blob.ParseBytes(v[:sp])
	if !ok {
		return meta{}, fmt.Errorf("invalid metarow %q: bad blobref %q", row, v[:sp])
	}
	m.largeRef = largeRef
	off, err := strutil.ParseUintBytes(v[sp+1:], 10, 32)
	if err != nil {
		return meta{}, fmt.Errorf("invalid metarow %q: bad offset: %v", row, err)
	}
	m.largeOff = uint32(off)
	return m, nil
}

func parseMetaRowSizeOnly(v []byte) (size uint32, err error) {
	sp := bytes.IndexByte(v, ' ')
	if sp < 1 || sp == len(v)-1 {
		return 0, fmt.Errorf("invalid metarow %q", v)
	}
	size64, err := strutil.ParseUintBytes(v[:sp], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid metarow size %q", v)
	}
	return uint32(size64), nil
}

func (s *storage) ReceiveBlob(br blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
	buf := pools.BytesBuffer()
	defer pools.PutBuffer(buf)

	if _, err := io.Copy(buf, source); err != nil {
		return sb, err
	}
	size := uint32(buf.Len())
	isFile := false
	fileBlob, err := schema.BlobFromReader(br, bytes.NewReader(buf.Bytes()))
	if err == nil && fileBlob.Type() == "file" {
		isFile = true
	}
	meta, err := s.getMetaRow(br)
	if err != nil {
		return sb, err
	}
	if meta.exists {
		sb = blob.SizedRef{Size: size, Ref: br}
	} else {
		sb, err = s.small.ReceiveBlob(br, buf)
		if err != nil {
			return sb, err
		}
	}
	if !isFile || meta.isPacked() || fileBlob.PartsSize() < packThreshold {
		return sb, nil
	}

	// Pack the blob.
	s.packGate.Start()
	defer s.packGate.Done()
	// We ignore the return value from packFile since we can't
	// really recover. At least be happy that we have all the
	// data on 'small' already. packFile will log at least.
	s.packFile(br)
	return sb, nil
}

func (s *storage) Fetch(br blob.Ref) (io.ReadCloser, uint32, error) {
	m, err := s.getMetaRow(br)
	if err != nil {
		return nil, 0, err
	}
	if !m.exists || !m.isPacked() {
		return s.small.Fetch(br)
	}
	rc, err := s.large.SubFetch(m.largeRef, int64(m.largeOff), int64(m.size))
	if err != nil {
		return nil, 0, err
	}
	return rc, m.size, nil
}

const removeLookups = 50 // arbitrary

func (s *storage) RemoveBlobs(blobs []blob.Ref) error {
	// Plan:
	//  -- delete from small (if it's there)
	//  -- if in big, update the meta index to note that it's there, but deleted.
	//  -- fetch big's zip file (constructed from a ReaderAt that is all dummy zeros +
	//     the zip's TOC only, relying on big being a SubFetcher, and keeping info in
	//     the meta about the offset of the TOC+total size of each big's zip)
	//  -- iterate over the zip's blobs (at some point). If all are marked deleted, actually RemoveBlob
	//     on big to delete the full zip and then delete all the meta rows.
	var (
		mu       sync.Mutex
		unpacked []blob.Ref
		packed   []blob.Ref
		large    = map[blob.Ref]bool{} // the large blobs that packed are in
	)
	var grp syncutil.Group
	delGate := syncutil.NewGate(removeLookups)
	for _, br := range blobs {
		br := br
		delGate.Start()
		grp.Go(func() error {
			defer delGate.Done()
			m, err := s.getMetaRow(br)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if m.isPacked() {
				packed = append(packed, br)
				large[m.largeRef] = true
			} else {
				unpacked = append(unpacked, br)
			}
			return nil
		})
	}
	if err := grp.Err(); err != nil {
		return err
	}
	if len(unpacked) > 0 {
		grp.Go(func() error {
			return s.small.RemoveBlobs(unpacked)
		})
	}
	if len(packed) > 0 {
		grp.Go(func() error {
			bm := s.meta.BeginBatch()
			now := time.Now()
			for zipRef := range large {
				bm.Set("d:"+zipRef.String(), fmt.Sprint(now.Unix()))
			}
			for _, br := range packed {
				bm.Delete("b:" + br.String())
			}
			return s.meta.CommitBatch(bm)
		})
	}
	return grp.Err()
}

func (s *storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	if len(blobs) == 0 {
		return nil
	}

	var (
		grp        syncutil.Group
		trySmallMu sync.Mutex
		trySmall   []blob.Ref
	)
	statGate := syncutil.NewGate(50) // arbitrary
	for _, br := range blobs {
		br := br
		statGate.Start()
		grp.Go(func() error {
			defer statGate.Done()
			m, err := s.getMetaRow(br)
			if err != nil {
				return err
			}
			if m.exists {
				dest <- blob.SizedRef{Ref: br, Size: m.size}
			} else {
				trySmallMu.Lock()
				trySmall = append(trySmall, br)
				// Assume append cannot fail or panic
				trySmallMu.Unlock()
			}
			return nil
		})
	}
	if err := grp.Err(); err != nil {
		return err
	}
	if len(trySmall) == 0 {
		return nil
	}
	return s.small.StatBlobs(dest, trySmall)
}

func (s *storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	return blobserver.MergedEnumerate(ctx, dest, []blobserver.BlobEnumerator{
		s.small,
		enumerator{s},
	}, after, limit)
}

// enumerator implements EnumerateBlobs.
type enumerator struct {
	*storage
}

func (s enumerator) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	defer close(dest)
	t := s.meta.Find(blobMetaPrefix+after, blobMetaPrefixLimit)
	defer func() {
		closeErr := t.Close()
		if err == nil {
			err = closeErr
		}
	}()
	n := 0
	afterb := []byte(after)
	for n < limit && t.Next() {
		key := t.KeyBytes()[len(blobMetaPrefix):]
		if n == 0 && bytes.Equal(key, afterb) {
			continue
		}
		n++
		br, ok := blob.ParseBytes(key)
		if !ok {
			return fmt.Errorf("unknown key %q in meta index", t.Key())
		}
		size, err := parseMetaRowSizeOnly(t.ValueBytes())
		if err != nil {
			return err
		}
		dest <- blob.SizedRef{Ref: br, Size: size}
	}
	return nil
}

func (s *storage) packFile(fileRef blob.Ref) (err error) {
	s.Logf("Packing file %s ...", fileRef)
	defer func() {
		if err == nil {
			s.Logf("Packed file %s", fileRef)
		} else {
			s.Logf("Error packing file %s: %v", fileRef, err)
		}
	}()

	fr, err := schema.NewFileReader(s, fileRef)
	if err != nil {
		return err
	}
	return newPacker(s, fileRef, fr).pack()
}

func newPacker(s *storage, fileRef blob.Ref, fr *schema.FileReader) *packer {
	return &packer{
		s:            s,
		fileRef:      fileRef,
		fr:           fr,
		dataSize:     map[blob.Ref]uint32{},
		schemaBlob:   map[blob.Ref]*blob.Blob{},
		schemaParent: map[blob.Ref][]blob.Ref{},
	}
}

// A packer writes a file out
type packer struct {
	s       *storage
	fileRef blob.Ref
	fr      *schema.FileReader

	wholeRef  blob.Ref
	wholeSize int64

	dataRefs []blob.Ref // in order
	dataSize map[blob.Ref]uint32

	schemaRefs   []blob.Ref // in order, but irrelevant
	schemaBlob   map[blob.Ref]*blob.Blob
	schemaParent map[blob.Ref][]blob.Ref // data blob -> its parent/ancestor schema blob(s)

	chunksRemain      []blob.Ref
	zips              []writtenZip
	wholeBytesWritten int64 // sum of zips.dataRefs.size
}

type writtenZip struct {
	blob.SizedRef
	dataRefs []blob.Ref
}

var (
	testHookSawTruncate           func(blob.Ref)
	testHookStopBeforeOverflowing func()
)

func (pk *packer) pack() error {
	if err := pk.scanChunks(); err != nil {
		return err
	}

	// TODO: decide as a fuction of schemaRefs and dataRefs
	// already in s.large whether it makes sense to still compact
	// this from a savings standpoint. For now we just always do.
	// Maybe we'd have knobs in the future. Ideally not.

	// Don't pack a file if we already have its wholeref stored
	// otherwise (perhaps under a different filename). But that
	// means we have to compute its wholeref first. We assume the
	// blob source will cache these lookups so it's not too
	// expensive to do two passes over the input.
	h := blob.NewHash()
	var err error
	pk.wholeSize, err = io.Copy(h, pk.fr)
	if err != nil {
		return err
	}
	pk.wholeRef = blob.RefFromHash(h)
	wholeKey := wholeMetaPrefix + pk.wholeRef.String()
	_, err = pk.s.meta.Get(wholeKey)
	if err == nil {
		// Nil error means there was some knowledge of this wholeref.
		return fmt.Errorf("already have wholeref %v packed; not packing again", pk.wholeRef)
	} else if err != sorted.ErrNotFound {
		return err
	}

	pk.chunksRemain = pk.dataRefs
	var trunc blob.Ref
MakingZips:
	for len(pk.chunksRemain) > 0 {
		if err := pk.writeAZip(trunc); err != nil {
			if needTrunc, ok := err.(needsTruncatedAfterError); ok {
				trunc = needTrunc.Ref
				if fn := testHookSawTruncate; fn != nil {
					fn(trunc)
				}
				continue MakingZips
			}
			return err
		}
		trunc = blob.Ref{}
	}

	// Record the final wholeMetaPrefix record:
	err = pk.s.meta.Set(wholeKey, fmt.Sprintf("%d %d", pk.wholeSize, len(pk.zips)))
	if err != nil {
		return fmt.Errorf("Error setting %s: %v", wholeKey, err)
	}

	return nil
}

func (pk *packer) scanChunks() error {
	schemaSeen := map[blob.Ref]bool{}
	return pk.fr.ForeachChunk(func(schemaPath []blob.Ref, p schema.BytesPart) error {
		if !p.BlobRef.Valid() {
			return errors.New("sparse files are not packed")
		}
		if p.Offset != 0 {
			// TODO: maybe care about this later, if we ever start making
			// these sorts of files.
			return errors.New("file uses complicated schema. not packing.")
		}
		pk.schemaParent[p.BlobRef] = append([]blob.Ref(nil), schemaPath...) // clone it
		pk.dataSize[p.BlobRef] = uint32(p.Size)
		for _, schemaRef := range schemaPath {
			if schemaSeen[schemaRef] {
				continue
			}
			schemaSeen[schemaRef] = true
			pk.schemaRefs = append(pk.schemaRefs, schemaRef)
			if b, err := blob.FromFetcher(pk.s, schemaRef); err != nil {
				return err
			} else {
				pk.schemaBlob[schemaRef] = b
			}
		}
		pk.dataRefs = append(pk.dataRefs, p.BlobRef)
		return nil
	})
}

// needsTruncatedAfterError is returend by writeAZip if it failed in its estimation and the zip file
// was over the 16MB (or whatever) max blob size limit. In this case the caller tries again
type needsTruncatedAfterError struct{ blob.Ref }

func (e needsTruncatedAfterError) Error() string { return "needs truncation after " + e.Ref.String() }

// check should only be used for things which really shouldn't ever happen, but should
// still be checked. If there is interesting logic in the 'else', then don't use this.
func check(err error) {
	if err != nil {
		b := make([]byte, 2<<10)
		b = b[:runtime.Stack(b, false)]
		log.Printf("Unlikely error condition triggered: %v at %s", err, b)
		panic(err)
	}
}

// trunc is a hint about which blob to truncate after. It may be zero.
// If the returned error is of type 'needsTruncatedAfterError', then
// the zip should be attempted to be written again, but truncating the
// data after the listed blob.
func (pk *packer) writeAZip(trunc blob.Ref) (err error) {
	defer func() {
		if e := recover(); e != nil {
			if v, ok := e.(error); ok && err == nil {
				err = v
			} else {
				panic(e)
			}
		}
	}()
	mf := Manifest{
		WholeRef:       pk.wholeRef,
		WholeSize:      pk.wholeSize,
		WholePartIndex: len(pk.zips),
	}
	var zbuf bytes.Buffer
	cw := &countWriter{w: &zbuf}
	zw := zip.NewWriter(cw)

	var approxSize = zipFixedOverhead // can't use zbuf.Len because zw buffers
	var dataRefsWritten []blob.Ref
	var dataBytesWritten int64
	var schemaBlobSeen = map[blob.Ref]bool{}
	var schemaBlobs []blob.Ref // to add after the main file

	baseFileName := pk.fr.FileName()
	if strings.Contains(baseFileName, "/") || strings.Contains(baseFileName, "\\") {
		return fmt.Errorf("File schema blob %v filename had a slash in it: %q", pk.fr.SchemaBlobRef(), baseFileName)
	}
	fh := &zip.FileHeader{
		Name:   baseFileName,
		Method: zip.Store, // uncompressed
	}
	fh.SetModTime(pk.fr.ModTime())
	fh.SetMode(0644)
	fw, err := zw.CreateHeader(fh)
	check(err)
	check(zw.Flush())
	dataStart := cw.n
	approxSize += zipPerEntryOverhead // for the first FileHeader w/ the data

	zipMax := pk.s.maxZipBlobSize()
	chunks := pk.chunksRemain
	chunkWholeHash := blob.NewHash()
	for len(chunks) > 0 {
		dr := chunks[0] // the next chunk to maybe write

		if trunc.Valid() && trunc == dr {
			if approxSize == 0 {
				return errors.New("first blob is too large to pack, once you add the zip overhead")
			}
			break
		}

		schemaBlobsSave := schemaBlobs
		for _, parent := range pk.schemaParent[dr] {
			if !schemaBlobSeen[parent] {
				schemaBlobSeen[parent] = true
				schemaBlobs = append(schemaBlobs, parent)
				approxSize += int(pk.schemaBlob[parent].Size()) + zipPerEntryOverhead
			}
		}

		thisSize := pk.dataSize[dr]
		approxSize += int(thisSize)
		if approxSize+mf.approxSerializedSize() > zipMax {
			if fn := testHookStopBeforeOverflowing; fn != nil {
				fn()
			}
			schemaBlobs = schemaBlobsSave // restore it
			break
		}

		// Copy the data to the zip.
		rc, size, err := pk.s.Fetch(dr)
		check(err)
		if size != thisSize {
			rc.Close()
			return errors.New("unexpected size")
		}
		if n, err := io.Copy(io.MultiWriter(fw, chunkWholeHash), rc); err != nil || n != int64(size) {
			rc.Close()
			return fmt.Errorf("copy to zip = %v, %v; want %v bytes", n, err, size)
		}
		rc.Close()

		dataRefsWritten = append(dataRefsWritten, dr)
		dataBytesWritten += int64(size)
		chunks = chunks[1:]
	}
	mf.DataBlobsOrigin = blob.RefFromHash(chunkWholeHash)

	// zipBlobs is where a schema or data blob is relative to the beginning
	// of the zip file.
	var zipBlobs []BlobAndPos

	var dataOffset int64
	for _, br := range dataRefsWritten {
		size := pk.dataSize[br]
		mf.DataBlobs = append(mf.DataBlobs, BlobAndPos{blob.SizedRef{br, size}, dataOffset})

		zipBlobs = append(zipBlobs, BlobAndPos{blob.SizedRef{br, size}, dataStart + dataOffset})
		dataOffset += int64(size)
	}

	for _, br := range schemaBlobs {
		fw, err := zw.CreateHeader(&zip.FileHeader{
			Name:   "camlistore/" + br.String() + ".json",
			Method: zip.Store, // uncompressed
		})
		check(err)
		check(zw.Flush())
		b := pk.schemaBlob[br]
		zipBlobs = append(zipBlobs, BlobAndPos{blob.SizedRef{br, b.Size()}, cw.n})
		rc := b.Open()
		n, err := io.Copy(fw, rc)
		rc.Close()
		check(err)
		if n != int64(b.Size()) {
			return fmt.Errorf("failed to write all of schema blob %v: %d bytes, not wanted %d", br, n, b.Size())
		}
	}

	// Manifest file
	fw, err = zw.Create(zipManifestPath)
	check(err)
	enc, err := json.MarshalIndent(mf, "", "  ")
	check(err)
	_, err = fw.Write(enc)
	check(err)
	err = zw.Close()
	check(err)

	if zbuf.Len() > zipMax {
		// We guessed wrong. Back up. Find out how many blobs we went over.
		overage := zbuf.Len() - zipMax
		for i := len(dataRefsWritten) - 1; i >= 0; i-- {
			dr := dataRefsWritten[i]
			if overage <= 0 {
				return needsTruncatedAfterError{dr}
			}
			overage -= int(pk.dataSize[dr])
		}
		return errors.New("file is unpackable; first blob is too big to fit")
	}

	zipRef := blob.SHA1FromBytes(zbuf.Bytes())
	zipSB, err := blobserver.ReceiveNoHash(pk.s.large, zipRef, bytes.NewReader(zbuf.Bytes()))
	if err != nil {
		return err
	}

	bm := pk.s.meta.BeginBatch()
	bm.Set(fmt.Sprintf("%s%s:%d", wholeMetaPrefix, pk.wholeRef, len(pk.zips)),
		fmt.Sprintf("%s %d %d %d",
			zipRef,
			dataStart,
			pk.wholeBytesWritten,
			dataBytesWritten))

	pk.wholeBytesWritten += dataBytesWritten
	pk.zips = append(pk.zips, writtenZip{
		SizedRef: zipSB,
		dataRefs: dataRefsWritten,
	})

	for _, zb := range zipBlobs {
		bm.Set(blobMetaPrefix+zb.Ref.String(), fmt.Sprintf("%d %v %d", zb.Size, zipRef, zb.Offset))
	}
	if err := pk.s.meta.CommitBatch(bm); err != nil {
		return err
	}

	// Delete from small
	if !pk.s.skipDelete {
		toDelete := make([]blob.Ref, 0, len(dataRefsWritten)+len(schemaBlobs))
		toDelete = append(toDelete, dataRefsWritten...)
		toDelete = append(toDelete, schemaBlobs...)
		if err := pk.s.small.RemoveBlobs(toDelete); err != nil {
			// Can't really do anything about it and doesn't really matter, so
			// just log for now.
			pk.s.Logf("Error removing blobs from %s: %v", pk.s.small, err)
		}
	}

	// On success, consume the chunks we wrote from pk.chunksRemain.
	pk.chunksRemain = pk.chunksRemain[len(dataRefsWritten):]
	return nil
}

type zipOpenError struct {
	zipRef blob.Ref
	err    error
}

func (ze zipOpenError) Error() string {
	return fmt.Sprintf("Error opening packed zip blob %v: %v", ze.zipRef, ze.err)
}

// foreachZipBlob calls fn for each blob in the zip pack blob
// identified by zipRef.  If fn returns a non-nil error,
// foreachZipBlob stops enumerating with that error.
func (s *storage) foreachZipBlob(zipRef blob.Ref, fn func(BlobAndPos) error) error {
	sb, err := blobserver.StatBlob(s.large, zipRef)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(blob.ReaderAt(s.large, zipRef), int64(sb.Size))
	if err != nil {
		return zipOpenError{zipRef, err}
	}
	var maniFile *zip.File // or nil if not found
	var firstOff int64     // offset of first file (the packed data chunks)
	for i, f := range zr.File {
		if i == 0 {
			firstOff, err = f.DataOffset()
			if err != nil {
				return err
			}
		}
		if f.Name == zipManifestPath {
			maniFile = f
			break
		}
	}
	if maniFile == nil {
		return errors.New("no camlistore manifest file found in zip")
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "camlistore/") || f.Name == zipManifestPath ||
			!strings.HasSuffix(f.Name, ".json") {
			continue
		}
		brStr := strings.TrimSuffix(strings.TrimPrefix(f.Name, "camlistore/"), ".json")
		br, ok := blob.Parse(brStr)
		if ok {
			off, err := f.DataOffset()
			if err != nil {
				return err
			}
			if err := fn(BlobAndPos{
				SizedRef: blob.SizedRef{br, uint32(f.UncompressedSize64)},
				Offset:   off,
			}); err != nil {
				return err
			}
		}
	}
	maniRC, err := maniFile.Open()
	if err != nil {
		return err
	}
	defer maniRC.Close()
	var mf Manifest
	if err := json.NewDecoder(maniRC).Decode(&mf); err != nil {
		return err
	}
	if !mf.WholeRef.Valid() || mf.WholeSize == 0 || !mf.DataBlobsOrigin.Valid() {
		return errors.New("incomplete blobpack manifest JSON")
	}
	for _, bap := range mf.DataBlobs {
		bap.Offset += firstOff
		if err := fn(bap); err != nil {
			return err
		}
	}
	return nil
}

// deleteZipPack deletes the zip pack file br, but only if that zip
// file's parts are deleted already from the meta index.
func (s *storage) deleteZipPack(br blob.Ref) error {
	inUse, err := s.zipPartsInUse(br)
	if err != nil {
		return err
	}
	if len(inUse) > 0 {
		return fmt.Errorf("can't delete zip pack %v: %d parts in use: %v", br, len(inUse), inUse)
	}
	if err := s.large.RemoveBlobs([]blob.Ref{br}); err != nil {
		return err
	}
	return s.meta.Delete("d:" + br.String())
}

func (s *storage) zipPartsInUse(br blob.Ref) ([]blob.Ref, error) {
	var (
		mu    sync.Mutex
		inUse []blob.Ref
	)
	var grp syncutil.Group
	gate := syncutil.NewGate(20) // arbitrary constant
	err := s.foreachZipBlob(br, func(bap BlobAndPos) error {
		gate.Start()
		grp.Go(func() error {
			defer gate.Done()
			mr, err := s.getMetaRow(bap.Ref)
			if err != nil {
				return err
			}
			if mr.isPacked() {
				mu.Lock()
				inUse = append(inUse, mr.largeRef)
				mu.Unlock()
			}
			return nil
		})
		return nil
	})
	if os.IsNotExist(err) {
		// An already-deleted blob from large isn't considered
		// to be in-use.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := grp.Err(); err != nil {
		return nil, err
	}
	return inUse, nil
}

// A BlobAndPos is a blobref, its size, and where it is located within
// a larger group of bytes.
type BlobAndPos struct {
	blob.SizedRef
	Offset int64 `json:"offset"`
}

// Manifest is the JSON description type representing the
// "camlistore/camlistore-pack-manifest.json" file found in a blobpack
// zip file.
type Manifest struct {
	// WholeRef is the blobref of the entire file that this zip is
	// either fully or partially describing.  For files under
	// around 16MB, the WholeRef and DataBlobsOrigin will be
	// the same.
	WholeRef blob.Ref `json:"wholeRef"`

	// WholeSize is the number of bytes in the original file being
	// cut up.
	WholeSize int64 `json:"wholeSize"`

	// WholePartIndex is the chunk number (0-based) of this zip file.
	// If a client has 'n' zip files with the same WholeRef whose
	// WholePartIndexes are contiguous (including 0) and the sum of
	// the DataBlobs equals WholeSize, the client has the entire
	// original file.
	WholePartIndex int `json:"wholePartIndex"`

	// DataBlobsOrigin is the blobref of the contents of the first
	// file in the zip pack file. It is the origin of all the logical data
	// blobs referenced in DataBlobs.
	DataBlobsOrigin blob.Ref `json:"dataBlobsOrigin"`

	// DataBlobs describes all the logical blobs that are
	// concatenated together in the first file in the zip file.
	// The offsets are relative to the beginning of that first
	// file, not the beginning of the zip file itself.
	DataBlobs []BlobAndPos `json:"dataBlobs"`
}

// approxSerializedSize reports how big this Manifest will be
// (approximately), once encoded as JSON. This is used as a hint by
// the packer to decide when to keep trying to add blobs. If this
// number is too low, the packer backs up (at a slight performance
// cost) but is still correct. If this approximation returns too large
// of a number, it just causes multiple zip files to be created when
// the original blobs might've just barely fit.
func (mf *Manifest) approxSerializedSize() int {
	// Empirically (for sha1-* blobrefs) it's 204 bytes fixed
	// encoding overhead (pre-compression), and 119 bytes per
	// encoded DataBlob.
	// And empirically, it compresses down to 30% of its size with flate.
	// So use the sha1 numbers but conseratively assume only 50% compression,
	// to make up for longer sha-3 blobrefs.
	return (204 + len(mf.DataBlobs)*119) / 2
}

type countWriter struct {
	w io.Writer
	n int64
}

func (cw *countWriter) Write(p []byte) (n int, err error) {
	n, err = cw.w.Write(p)
	cw.n += int64(n)
	return
}
