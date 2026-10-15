/*
Copyright 2014 The Perkeep AUTHORS

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
	      {"blob": "sha1-9425cca1dde5d8b6eb70cd087db4e356da92396e", "offset": ...},
	      {"blob": "sha1-7709559a3c8668c57cc0a2f57c418b1cc3598049", "offset": ...},
	      {"blob": "sha1-f62cb5d05cfbf2a7a6c7f8339d0a4bf1dcd0ab6c", "offset": ...}
	  ] // raw data blobs of foo.jpg
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
package blobpacked // import "perkeep.org/pkg/blobserver/blobpacked"

// TODO: BlobStreamer using the zip manifests, for recovery.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/internal/pools"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"

	"go4.org/jsonconfig"
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

	wholeMetaPrefix      = "w:"
	wholeMetaPrefixLimit = "w;"

	zipMetaPrefix      = "z:"
	zipMetaPrefixLimit = "z;"
)

const (
	zipManifestPath = "camlistore/camlistore-pack-manifest.json"
)

// RecoveryMode is the mode in which the blobpacked server starts.
type RecoveryMode int

// Note: not using iota for these, because they're stored in GCE
// instance's metadata values.
const (
	// NoRecovery means blobpacked does not attempt to repair its index on startup.
	// It is the default.
	NoRecovery RecoveryMode = 0
	// FastRecovery populates the blobpacked index, without erasing any existing one.
	FastRecovery RecoveryMode = 1
	// FullRecovery erases the existing blobpacked index, then rebuilds it.
	FullRecovery RecoveryMode = 2
)

var (
	recoveryMu sync.Mutex
	recovery   = NoRecovery
)

// TODO(mpl): make SetRecovery a method of type storage if we ever export it.

// SetRecovery sets the recovery mode for the blobpacked package.
// If set to one of the modes other than NoRecovery, it means that any
// blobpacked storage subsequently initialized will automatically start with
// rebuilding its meta index of zip files, in accordance with the selected mode.
func SetRecovery(mode RecoveryMode) {
	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	recovery = mode
}

type subFetcherStorage interface {
	blobserver.Storage
	blob.SubFetcher
}

// TODO(mpl): all a logf method or something to storage so we get the
// "blobpacked:" prefix automatically to log messages.

type storage struct {
	small blobserver.Storage
	large subFetcherStorage

	// meta key -> value rows are:
	//
	// For logical blobs packed within a large blob, "b:" prefix:
	//   b:sha1-xxxx -> "<size> <big-blobref> <offset_u32>"
	//
	// For wholerefs: (wholeMetaPrefix)
	//   w:sha1-xxxx(wholeref) -> "<nbytes_total_u64> <nchunks_u32>"
	// Then for each big nchunk of the file:
	// The wholeRef and the chunk number as a key to: the blobRef of the zip
	// file, the position of the data within the zip, the position of the data
	// within the uploaded whole file, the length of data in this zip.
	//   w:sha1-xxxx:0 -> "<zipchunk-blobref> <offset-in-zipchunk-blobref> <offset-in-whole_u64> <length_u32>"
	//   w:sha1-xxxx:...
	//   w:sha1-xxxx:(nchunks-1)
	//
	// For zipRefs: (zipMetaPrefix)
	// key: blobref of the zip, prefixed by "z:"
	// value: size of the zip, blobref of the contents of the whole file (which may
	// span multiple zips, ~15.5 MB of data per zip), size of the whole file, position
	// in the whole file of the data (first file) in the zip, size of the data in the
	// zip (== size of the zip's first file).
	//   z:<zip-blobref> -> "<zip_size_u32> <whole_ref_from_zip_manifest> <whole_size_u64>
	//   <zip_data_offset_in_whole_u64> <zip_data_bytes_u32>"
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
	return `"blobpacked" storage`
}

func (s *storage) Logf(format string, args ...any) {
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
		keepGoing   = conf.OptionalBool("keepGoing", false)
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	small, err := ld.GetStorage(smallPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load smallBlobs at %s: %w", smallPrefix, err)
	}
	large, err := ld.GetStorage(largePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load largeBlobs at %s: %w", largePrefix, err)
	}
	largeSubber, ok := large.(subFetcherStorage)
	if !ok {
		return nil, fmt.Errorf("largeBlobs at %q of type %T doesn't support fetching sub-ranges of blobs",
			largePrefix, large)
	}
	meta, err := sorted.NewKeyValueMaybeWipe(metaConf)
	if err != nil {
		return nil, fmt.Errorf("failed to setup blobpacked metaIndex: %w", err)
	}
	sto := &storage{
		small: small,
		large: largeSubber,
		meta:  meta,
	}
	sto.init()

	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	condFatalf := func(pattern string, args ...any) {
		log.Printf(pattern, args...)
		if !keepGoing {
			os.Exit(1)
		}
	}

	var newKv func() (sorted.KeyValue, error)
	switch recovery {
	case FastRecovery:
		newKv = func() (sorted.KeyValue, error) {
			return sorted.NewKeyValue(metaConf)
		}
	case FullRecovery:
		newKv = func() (sorted.KeyValue, error) {
			kv, err := sorted.NewKeyValue(metaConf)
			if err != nil {
				return nil, err
			}
			wiper, ok := kv.(sorted.Wiper)
			if !ok {
				return nil, fmt.Errorf("blobpacked meta index of type %T needs to be wiped, but does not support automatic wiping. It should be removed manually.", kv)
			}
			if err := wiper.Wipe(); err != nil {
				return nil, fmt.Errorf("blobpacked meta index of type %T could not be wiped: %w", kv, err)
			}
			return kv, nil
		}
	}
	if newKv != nil {
		// i.e. we're in one of the recovery modes
		log.Print("Starting recovery of blobpacked index")
		if err := meta.Close(); err != nil {
			return nil, err
		}
		if err := sto.reindex(context.TODO(), newKv); err != nil {
			return nil, err
		}
		if _, err := sto.checkLargeIntegrity(); err != nil {
			condFatalf("blobpacked: reindexed successfully, but error after validation: %v", err)
		}
		return sto, nil
	}

	// Check for a weird state: zip files exist, but no metadata about them
	// is recorded. This is probably a corrupt state, and the user likely
	// wants to recover.
	if !sto.anyMeta() && sto.anyZipPacks() {
		if env.OnGCE() {
			// TODO(mpl): make web UI page/mode that informs about this error.
			condFatalf("Error: blobpacked storage detects non-zero packed zips, but no metadata. Please switch to recovery mode: add the \"camlistore-recovery = %d\" key/value to the Custom metadata of your instance. And restart the instance.", FastRecovery)
		}
		condFatalf("Error: blobpacked storage detects non-zero packed zips, but no metadata. Please re-start in recovery mode with -recovery=%d", FastRecovery)
	}

	// Check that large storage and meta agree in the background, so it
	// doesn't hold up the rest of startup. The server exits if they
	// don't, as it would have if this ran first, just a little later.
	// TODO: report the result to a health (and scrub) subsystem instead,
	// one that can take the server out of service or show the problem.
	go func() {
		mode, err := sto.checkLargeIntegrity()
		if err == nil {
			return
		}
		if mode <= NoRecovery {
			condFatalf("%v", err)
		}
		if env.OnGCE() {
			// TODO(mpl): make web UI page/mode that informs about this error.
			condFatalf("Error: %v. Please switch to recovery mode: add the \"camlistore-recovery = %d\" key/value to the Custom metadata of your instance. And restart the instance.", err, mode)
		}
		condFatalf("Error: %v. Please re-start in recovery mode with -recovery=%d", err, mode)
	}()

	return sto, nil
}

// checkLargeIntegrity verifies that all large blobs in the large storage are
// indexed in meta, and vice-versa, that all rows in meta referring to a large blob
// correspond to an existing large blob in the large storage. If any of the above
// is not true, it returns the recovery mode that should be used to fix the
// problem, as well as the error detailing the problem. It does not perform any
// check about the contents of the large blobs themselves.
func (s *storage) checkLargeIntegrity() (RecoveryMode, error) {
	log.Printf("blobpacked: checking integrity of packed blobs against index...")
	// Collect the large blobs first, rather than walking the z: rows
	// alongside a sorted enumeration, so that large storage can list
	// them in whatever order is fastest (see blobserver.RefLister).
	unindexed := make(map[blob.Ref]bool) // in large but not (yet seen) in meta
	if err := blobserver.ListAllRefs(context.Background(), s.large, func(br blob.Ref) error {
		unindexed[br] = true
		return nil
	}); err != nil {
		return FullRecovery, err
	}
	inLarge := 0
	var extra []blob.Ref // blobs in meta but not in large
	t := s.meta.Find(zipMetaPrefix, zipMetaPrefixLimit)
	defer t.Close()
	for t.Next() {
		metaKey := t.Key()
		zipRef, ok := blob.Parse(strings.TrimPrefix(metaKey, zipMetaPrefix))
		if !ok {
			return FullRecovery, fmt.Errorf("bogus key in z: row: %q", metaKey)
		}
		if !unindexed[zipRef] {
			extra = append(extra, zipRef)
			continue
		}
		delete(unindexed, zipRef)
		if _, err := parseZipMetaRow(t.ValueBytes()); err != nil {
			return FullRecovery, fmt.Errorf("error parsing row from meta: %w", err)
		}
		inLarge++
	}
	if err := t.Close(); err != nil {
		return FullRecovery, fmt.Errorf("error reading or closing index: %w", err)
	}
	missing := slices.Collect(maps.Keys(unindexed)) // blobs in large but not in meta
	if len(missing) > 0 || len(extra) > 0 {
		// This runs while the server is serving, so a zip may have been
		// packed (written to large, then to meta) after large was listed
		// or meta was read. Look at those blobs again, after giving any
		// such packing a moment to finish.
		largeIntegrityRecheckWait()
		missing, extra = s.stillMismatched(missing, extra)
	}
	log.Printf("blobpacked: %d large blobs found in index, %d missing from index", inLarge, len(missing))
	if len(missing) > 0 {
		printSample(missing, "missing")
	}
	if len(extra) > 0 {
		printSample(extra, "extra")
		return FullRecovery, fmt.Errorf("%d large blobs in index but not actually in storage", len(extra))
	}
	if len(missing) > 0 {
		return FastRecovery, fmt.Errorf("%d large blobs missing from index", len(missing))
	}
	return NoRecovery, nil
}

// largeIntegrityRecheckWait is called by checkLargeIntegrity before it
// looks again at blobs that seemed to be in only one of large and meta.
var largeIntegrityRecheckWait = func() { time.Sleep(5 * time.Second) }

// stillMismatched returns which of missing (blobs in large but not in
// meta) are still missing from meta, and which of extra (blobs in meta
// but not in large) are still not in large.
func (s *storage) stillMismatched(missing, extra []blob.Ref) (stillMissing, stillExtra []blob.Ref) {
	for _, br := range missing {
		if _, err := s.meta.Get(zipMetaPrefix + br.String()); err != nil {
			stillMissing = append(stillMissing, br)
		}
	}
	if len(extra) == 0 {
		return stillMissing, nil
	}
	inLarge, err := blobserver.StatBlobs(context.Background(), s.large, extra)
	if err != nil {
		log.Printf("blobpacked: rechecking large blobs: %v", err)
		return stillMissing, extra
	}
	for _, br := range extra {
		if _, ok := inLarge[br]; !ok {
			stillExtra = append(stillExtra, br)
		}
	}
	return stillMissing, stillExtra
}

func printSample(fromSlice []blob.Ref, sliceName string) {
	sort.Slice(fromSlice, func(i, j int) bool { return fromSlice[i].Less(fromSlice[j]) })
	for i, br := range fromSlice {
		if i == 10 {
			break
		}
		log.Printf("  sample %v large blob: %v", sliceName, br)
	}
}

// zipMetaInfo is the info needed to write the wholeMetaPrefix and
// zipMetaPrefix entries when reindexing. For a given file, spread over several
// zips, each zip has a corresponding zipMetaInfo. The wholeMetaPrefix and
// zipMetaPrefix rows pertaining to a file can only be written once all the
// zipMetaInfo have been collected and sorted, because the offset of each zip's
// data is derived from the size of the other pieces that precede it in the file.
type zipMetaInfo struct {
	wholePartIndex int      // index of that zip, 0-based
	zipRef         blob.Ref // ref of the zip file holding packed data blobs + other schema blobs
	zipSize        uint32   // size of the zipped file
	offsetInZip    uint32   // position of the contiguous data blobs, relative to the zip
	dataSize       uint32   // size of the data in the zip
	wholeSize      uint64   // size of the whole file that this zip is a part of
	wholeRef       blob.Ref // ref of the contents of the whole file
}

// rowValue returns the value of the "z:<zipref>" meta key row
// based on the contents of zm and the provided arguments.
func (zm zipMetaInfo) rowValue(offset uint64) string {
	return fmt.Sprintf("%d %v %d %d %d", zm.zipSize, zm.wholeRef, zm.wholeSize, offset, zm.dataSize)
}

// TODO(mpl): add client command to call reindex on an "offline" blobpacked. camtool packblobs -reindex maybe?

// fileName returns the name of the (possibly partial) first file in zipRef
// (i.e. the actual data). It returns a zipOpenError if there was any problem
// reading the zip, and os.ErrNotExist if the zip could not be fetched or if
// there was no file in the zip.
func (s *storage) fileName(ctx context.Context, zipRef blob.Ref) (string, error) {
	_, size, err := s.large.Fetch(ctx, zipRef)
	if err != nil {
		return "", err
	}
	zr, err := zip.NewReader(blob.ReaderAt(ctx, s.large, zipRef), int64(size))
	if err != nil {
		return "", zipOpenError{zipRef, err}
	}
	for _, f := range zr.File {
		return f.Name, nil
	}
	return "", os.ErrNotExist
}

// reindex rebuilds the meta index for packed blobs. It calls newMeta to create
// a new KeyValue on which to write the index, and replaces s.meta with it. There
// is no locking whatsoever so it should not be called when the storage is already
// in use. its signature might change if/when it gets exported.
func (s *storage) reindex(ctx context.Context, newMeta func() (sorted.KeyValue, error)) error {
	meta, err := newMeta()
	if err != nil {
		return fmt.Errorf("failed to create new blobpacked meta index: %w", err)
	}

	zipMetaByWholeRef := make(map[blob.Ref][]zipMetaInfo)

	// first a fast full enumerate, so we can report progress afterwards
	packedTotal := 0
	blobserver.EnumerateAllFrom(ctx, s.large, "", func(sb blob.SizedRef) error {
		packedTotal++
		return nil
	})

	var packedDone, packedSeen int
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	if err := blobserver.EnumerateAllFrom(ctx, s.large, "", func(sb blob.SizedRef) error {
		select {
		case <-t.C:
			log.Printf("blobpacked: %d / %d zip packs seen", packedSeen, packedTotal)
		default:
		}
		zipRef := sb.Ref
		zr, err := zip.NewReader(blob.ReaderAt(ctx, s.large, zipRef), int64(sb.Size))
		if err != nil {
			return zipOpenError{zipRef, err}
		}
		var maniFile *zip.File
		var firstOff int64 // offset of first file (the packed data chunks)
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
			return fmt.Errorf("no perkeep manifest file found in zip %v", zipRef)
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
			return fmt.Errorf("incomplete blobpack manifest JSON in %v", zipRef)
		}

		bm := meta.BeginBatch()
		// In this loop, we write all the blobMetaPrefix entries for the
		// data blobs in this zip, and we also compute the dataBytesWritten, for later.
		var dataBytesWritten int64
		for _, bp := range mf.DataBlobs {
			bm.Set(blobMetaPrefix+bp.SizedRef.Ref.String(), fmt.Sprintf("%d %v %d", bp.SizedRef.Size, zipRef, firstOff+bp.Offset))
			dataBytesWritten += int64(bp.SizedRef.Size)
		}
		if dataBytesWritten > (1<<32 - 1) {
			return fmt.Errorf("total data blobs size in zip %v overflows uint32", zipRef)
		}
		dataSize := uint32(dataBytesWritten)

		// In this loop, we write all the blobMetaPrefix entries for the schema blobs in this zip
		for _, f := range zr.File {
			if !(strings.HasPrefix(f.Name, "camlistore/") && strings.HasSuffix(f.Name, ".json")) ||
				f.Name == zipManifestPath {
				continue
			}
			br, ok := blob.Parse(strings.TrimSuffix(strings.TrimPrefix(f.Name, "camlistore/"), ".json"))
			if !ok {
				return fmt.Errorf("schema file in zip %v does not have blobRef as name: %v", zipRef, f.Name)
			}
			offset, err := f.DataOffset()
			if err != nil {
				return err
			}
			bm.Set(blobMetaPrefix+br.String(), fmt.Sprintf("%d %v %d", f.UncompressedSize64, zipRef, offset))
		}

		if err := meta.CommitBatch(bm); err != nil {
			return err
		}

		// record that info for later, when we got them all, so we can write the wholeMetaPrefix entries.
		zipMetaByWholeRef[mf.WholeRef] = append(zipMetaByWholeRef[mf.WholeRef], zipMetaInfo{
			wholePartIndex: mf.WholePartIndex,
			zipRef:         zipRef,
			zipSize:        sb.Size,
			offsetInZip:    uint32(firstOff),
			dataSize:       dataSize,
			wholeSize:      uint64(mf.WholeSize),
			wholeRef:       mf.WholeRef, // redundant with zipMetaByWholeRef key for now.
		})
		packedSeen++
		return nil
	}); err != nil {
		return err
	}

	// finally, write the wholeMetaPrefix entries
	foundDups := false
	packedFiles := 0
	tt := time.NewTicker(2 * time.Second)
	defer tt.Stop()
	bm := meta.BeginBatch()
	for wholeRef, zipMetas := range zipMetaByWholeRef {
		select {
		case <-tt.C:
			log.Printf("blobpacked: %d files reindexed", packedFiles)
		default:
		}
		sort.Slice(zipMetas, func(i, j int) bool { return zipMetas[i].wholePartIndex < zipMetas[j].wholePartIndex })
		hasDup := hasDups(zipMetas)
		if hasDup {
			foundDups = true
		}
		offsets, wholeBytesWritten := wholeOffsets(zipMetas)
		var minOffset uint64 // where the data of the next part can start
		for _, z := range zipMetas {
			offset, ok := offsets[z.wholePartIndex]
			if !ok {
				// A part before this one is missing, so the sizes of
				// the parts before it don't add up to its offset.
				var err error
				offset, err = s.wholeOffsetFromSchema(ctx, z.zipRef, z.zipSize, minOffset)
				if err != nil {
					return fmt.Errorf("finding offset of zip %v in wholeRef %v: %w", z.zipRef, wholeRef, err)
				}
				offsets[z.wholePartIndex] = offset
			}
			minOffset = offset + uint64(z.dataSize)
			// write the w:row
			bm.Set(fmt.Sprintf("%s%s:%d", wholeMetaPrefix, wholeRef, z.wholePartIndex),
				fmt.Sprintf("%s %d %d %d", z.zipRef, z.offsetInZip, offset, z.dataSize))
			// write the z: row
			bm.Set(fmt.Sprintf("%s%v", zipMetaPrefix, z.zipRef), z.rowValue(offset))
			packedDone++
		}
		if hasDup {
			if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); debug {
				printDuplicates(zipMetas)
			}
		}

		if zipMetas[0].wholeSize != wholeBytesWritten {
			// Any corrupted zip should have been found earlier, so this error means we're
			// missing at least one full zip for the whole file to be complete.
			fileName, err := s.fileName(ctx, zipMetas[0].zipRef)
			if err != nil {
				return fmt.Errorf("could not get filename of file in zip %v: %w", zipMetas[0].zipRef, err)
			}
			log.Printf(
				"blobpacked: file %q (wholeRef %v) is incomplete: sum of all zips (%d bytes) does not match manifest's WholeSize (%d bytes)",
				fileName, wholeRef, wholeBytesWritten, zipMetas[0].wholeSize)
			var allParts []blob.Ref
			for _, z := range zipMetas {
				allParts = append(allParts, z.zipRef)
			}
			log.Printf("blobpacked: known parts of %v: %v", wholeRef, allParts)
			// we skip writing the w: row for the full file, and we don't count the file
			// as complete.
			continue
		}
		bm.Set(fmt.Sprintf("%s%s", wholeMetaPrefix, wholeRef),
			fmt.Sprintf("%d %d", wholeBytesWritten, zipMetas[len(zipMetas)-1].wholePartIndex+1))
		packedFiles++
	}
	if err := meta.CommitBatch(bm); err != nil {
		return err
	}

	log.Printf("blobpacked: %d / %d zip packs successfully reindexed", packedDone, packedTotal)
	if packedFiles < len(zipMetaByWholeRef) {
		log.Printf("blobpacked: %d files reindexed, and %d incomplete file(s) found.", packedFiles, len(zipMetaByWholeRef)-packedFiles)
	} else {
		log.Printf("blobpacked: %d files reindexed.", packedFiles)
	}
	if foundDups {
		if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); !debug {
			log.Print("blobpacked: zip blobs with duplicate contents were found. Re-run with CAMLI_DEBUG=true for more detail.")
		}
	}

	// TODO(mpl): take into account removed blobs. I can't be done for now
	// (2015-01-29) because RemoveBlobs currently only updates the meta index.
	// So if the index was lost, all information about removals was lost too.

	s.meta = meta
	return nil
}

// hasDups reports whether zm contains successive zipRefs which have the same
// wholePartIndex, which we assume means they have the same data contents. It
// panics if that assumption seems wrong, i.e. if the data within assumed
// duplicates is not the same size in all of them. zm must be sorted by
// wholePartIndex.
// See https://github.com/perkeep/perkeep/issues/1079
func hasDups(zm []zipMetaInfo) bool {
	dupFound := false
	for i := 1; i < len(zm); i++ {
		prev, z := zm[i-1], zm[i]
		if z.wholePartIndex != prev.wholePartIndex {
			continue
		}
		// we could return true right now, but we want to go through it all, to make
		// sure our assumption that "same part index -> duplicate" is true, using at least
		// the dataSize to confirm. For a better effort, we should use DataBlobsOrigin.
		if z.dataSize != prev.dataSize {
			panic(fmt.Sprintf("%v and %v looked like duplicates at first, but don't actually have the same dataSize. TODO: add DataBlobsOrigin checking.", prev.zipRef, z.zipRef))
		}
		dupFound = true
	}
	return dupFound
}

// wholeOffsets returns the offset in the whole file of each part in zm that
// is preceded by all the parts before it, keyed by wholePartIndex, and the
// sum of the sizes of those parts. zm must be sorted by wholePartIndex.
// Duplicates are skipped. If a part is missing, the parts after it are not
// in offsets, and prefixSize is less than the size of the whole file.
func wholeOffsets(zm []zipMetaInfo) (offsets map[int]uint64, prefixSize uint64) {
	offsets = make(map[int]uint64)
	next := 0 // the wholePartIndex of the next part we want
	for _, z := range zm {
		if z.wholePartIndex < next {
			continue // a duplicate
		}
		if z.wholePartIndex > next {
			break // part next is missing
		}
		offsets[next] = prefixSize
		prefixSize += uint64(z.dataSize)
		next++
	}
	return offsets, prefixSize
}

// wholeOffsetFromSchema returns the offset in the whole file of the data in
// the zip zipRef, of size zipSize, at or after minOffset. It is used when the
// sizes of the parts before that zip don't add up to the offset because one
// of them is missing.
//
// It walks the file's schema blobs stored in the zip, from the file schema
// blob down to the data chunks, adding up the sizes of the parts before them.
// The zip only has the schema blobs above its own chunks, so the subtrees
// without them are skipped by their size. If a file repeats a run of chunks,
// the zip's run can match in more than one place; those places all hold the
// same bytes, so the first one at or after minOffset is returned, or else
// the first one.
func (s *storage) wholeOffsetFromSchema(ctx context.Context, zipRef blob.Ref, zipSize uint32, minOffset uint64) (uint64, error) {
	zr, err := zip.NewReader(blob.ReaderAt(ctx, s.large, zipRef), int64(zipSize))
	if err != nil {
		return 0, zipOpenError{zipRef, err}
	}
	var mf Manifest
	var fileRef blob.Ref
	schemaBlobs := map[blob.Ref]*schema.Blob{}
	for _, f := range zr.File {
		isManifest := f.Name == zipManifestPath
		if !isManifest && !(strings.HasPrefix(f.Name, "camlistore/") && strings.HasSuffix(f.Name, ".json")) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return 0, err
		}
		if isManifest {
			err = json.NewDecoder(rc).Decode(&mf)
			rc.Close()
			if err != nil {
				return 0, err
			}
			continue
		}
		br, ok := blob.Parse(strings.TrimSuffix(strings.TrimPrefix(f.Name, "camlistore/"), ".json"))
		if !ok {
			rc.Close()
			return 0, fmt.Errorf("schema file in zip does not have blobRef as name: %v", f.Name)
		}
		sb, err := schema.BlobFromReader(br, rc)
		rc.Close()
		if err != nil {
			return 0, fmt.Errorf("schema blob %v: %w", br, err)
		}
		schemaBlobs[br] = sb
		if sb.Type() == schema.TypeFile {
			if fileRef.Valid() {
				return 0, fmt.Errorf("more than one file schema blob: %v and %v", fileRef, br)
			}
			fileRef = br
		}
	}
	if len(mf.DataBlobs) == 0 {
		return 0, errors.New("manifest has no data blobs")
	}
	if !fileRef.Valid() {
		return 0, errors.New("no file schema blob")
	}

	// The data chunks in the zip's subtrees, with their offsets in the
	// whole file.
	type chunk struct {
		blob.SizedRef
		offset uint64
	}
	var chunks []chunk
	var offset uint64
	walking := map[blob.Ref]bool{} // to stop on cycles
	var walk func(br blob.Ref) error
	walk = func(br blob.Ref) error {
		if walking[br] {
			return fmt.Errorf("schema blob %v contains itself", br)
		}
		walking[br] = true
		defer delete(walking, br)
		for _, p := range schemaBlobs[br].ByteParts() {
			if p.Offset != 0 {
				return fmt.Errorf("schema blob %v has a part with an offset", br)
			}
			switch {
			case p.BlobRef.Valid():
				chunks = append(chunks, chunk{blob.SizedRef{Ref: p.BlobRef, Size: uint32(p.Size)}, offset})
				offset += p.Size
			case schemaBlobs[p.BytesRef] != nil:
				if err := walk(p.BytesRef); err != nil {
					return err
				}
			default:
				offset += p.Size
			}
		}
		return nil
	}
	if err := walk(fileRef); err != nil {
		return 0, err
	}

	// matchesAt reports whether the zip's data chunks are at chunks[i:],
	// contiguous in the whole file.
	matchesAt := func(i int) bool {
		if len(chunks)-i < len(mf.DataBlobs) {
			return false
		}
		for j, db := range mf.DataBlobs {
			c := chunks[i+j]
			if c.SizedRef != db.SizedRef || c.offset != chunks[i].offset+uint64(db.Offset) {
				return false
			}
		}
		return true
	}
	first := -1
	for i := range chunks {
		if !matchesAt(i) {
			continue
		}
		if chunks[i].offset >= minOffset {
			return chunks[i].offset, nil
		}
		if first == -1 {
			first = i
		}
	}
	if first == -1 {
		return 0, errors.New("data chunks not found in the zip's schema blobs")
	}
	return chunks[first].offset, nil
}

func printDuplicates(zm []zipMetaInfo) {
	i := 0
	byPartIndex := make(map[int][]zipMetaInfo)
	for _, z := range zm {
		if i == z.wholePartIndex {
			byPartIndex[z.wholePartIndex] = []zipMetaInfo{z}
			i++
			continue
		}
		byPartIndex[z.wholePartIndex] = append(byPartIndex[z.wholePartIndex], z)
	}
	for _, zm := range byPartIndex {
		if len(zm) <= 1 {
			continue
		}
		br := make([]blob.Ref, 0, len(zm))
		for _, z := range zm {
			br = append(br, z.zipRef)
		}
		log.Printf("zip blobs with same data contents: %v", br)
	}
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
	return s.meta.Close()
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
	if errors.Is(err, sorted.ErrNotFound) {
		return meta{}, nil
	}
	if err != nil {
		return meta{}, fmt.Errorf("blobpacked.getMetaRow(%v) = %w", br, err)
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
		return meta{}, fmt.Errorf("invalid metarow %q: bad offset: %w", row, err)
	}
	m.largeOff = uint32(off)
	return m, nil
}

func parseMetaRowSizeOnly(v string) (size uint32, err error) {
	sp := strings.IndexByte(v, ' ')
	if sp < 1 || sp == len(v)-1 {
		return 0, fmt.Errorf("invalid metarow %q", v)
	}
	size64, err := strconv.ParseUint(v[:sp], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid metarow size %q", v)
	}
	return uint32(size64), nil
}

// parses:
// "<zip_size_u32> <whole_ref_from_zip_manifest> <whole_size_u64> <zip_data_offset_in_whole_u64> <zip_data_bytes_u32>"
func parseZipMetaRow(v []byte) (m zipMetaInfo, err error) {
	row := v
	sp := bytes.IndexByte(v, ' ')
	if sp < 1 || sp == len(v)-1 {
		return zipMetaInfo{}, fmt.Errorf("invalid z: meta row %q", row)
	}
	if bytes.Count(v, singleSpace) != 4 {
		return zipMetaInfo{}, fmt.Errorf("wrong number of spaces in z: meta row %q", row)
	}
	zipSize, err := strutil.ParseUintBytes(v[:sp], 10, 32)
	if err != nil {
		return zipMetaInfo{}, fmt.Errorf("invalid zipSize %q in z: meta row: %q", v[:sp], row)
	}
	m.zipSize = uint32(zipSize)

	v = v[sp+1:]
	sp = bytes.IndexByte(v, ' ')
	wholeRef, ok := blob.ParseBytes(v[:sp])
	if !ok {
		return zipMetaInfo{}, fmt.Errorf("invalid wholeRef %q in z: meta row: %q", v[:sp], row)
	}
	m.wholeRef = wholeRef

	v = v[sp+1:]
	sp = bytes.IndexByte(v, ' ')
	wholeSize, err := strutil.ParseUintBytes(v[:sp], 10, 64)
	if err != nil {
		return zipMetaInfo{}, fmt.Errorf("invalid wholeSize %q in z: meta row: %q", v[:sp], row)
	}
	m.wholeSize = uint64(wholeSize)

	v = v[sp+1:]
	sp = bytes.IndexByte(v, ' ')
	if _, err := strutil.ParseUintBytes(v[:sp], 10, 64); err != nil {
		return zipMetaInfo{}, fmt.Errorf("invalid offset %q in z: meta row: %q", v[:sp], row)
	}

	v = v[sp+1:]
	dataSize, err := strutil.ParseUintBytes(v, 10, 32)
	if err != nil {
		return zipMetaInfo{}, fmt.Errorf("invalid dataSize %q in z: meta row: %q", v, row)
	}
	m.dataSize = uint32(dataSize)

	return m, nil
}

func (s *storage) ReceiveBlob(ctx context.Context, br blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
	buf := pools.BytesBuffer()
	defer pools.PutBuffer(buf)

	if _, err := io.Copy(buf, source); err != nil {
		return sb, err
	}
	size := uint32(buf.Len())
	isFile := false
	fileBlob, err := schema.BlobFromReader(br, bytes.NewReader(buf.Bytes()))
	if err == nil && fileBlob.Type() == schema.TypeFile {
		isFile = true
	}
	meta, err := s.getMetaRow(br)
	if err != nil {
		return sb, err
	}
	if meta.exists {
		sb = blob.SizedRef{Size: size, Ref: br}
	} else {
		sb, err = s.small.ReceiveBlob(ctx, br, buf)
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
	s.packFile(ctx, br)
	return sb, nil
}

func (s *storage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	m, err := s.getMetaRow(br)
	if err != nil {
		return nil, 0, err
	}
	if !m.exists || !m.isPacked() {
		return s.small.Fetch(ctx, br)
	}
	rc, err := s.large.SubFetch(ctx, m.largeRef, int64(m.largeOff), int64(m.size))
	if err != nil {
		return nil, 0, err
	}
	return rc, m.size, nil
}

const removeLookups = 50 // arbitrary

func (s *storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
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
			return s.small.RemoveBlobs(ctx, unpacked)
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

var statGate = syncutil.NewGate(50) // arbitrary

func (s *storage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	var (
		trySmallMu sync.Mutex
		trySmall   []blob.Ref
	)

	err := blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(br blob.Ref) (sb blob.SizedRef, err error) {
		m, err := s.getMetaRow(br)
		if err != nil {
			return sb, err
		}
		if m.exists {
			return blob.SizedRef{Ref: br, Size: m.size}, nil
		}
		// Try it in round two against the small blobs:
		trySmallMu.Lock()
		trySmall = append(trySmall, br)
		trySmallMu.Unlock()
		return sb, nil
	})
	if err != nil {
		return err
	}
	if len(trySmall) == 0 {
		return nil
	}
	return s.small.StatBlobs(ctx, trySmall, fn)
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

func (s enumerator) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	if limit <= 0 {
		return nil
	}
	n := 0
	// Close each metadata iterator before sending. Consumers may Fetch from
	// this storage, which needs the same single-slot gate when meta is SQLite.
	for r := range sorted.ScanPaged(s.meta, blobMetaPrefix+after, blobMetaPrefixLimit) {
		row, err := r.Value()
		if err != nil {
			return err
		}
		key := row.Key[len(blobMetaPrefix):]
		if key <= after {
			continue
		}
		br, ok := blob.Parse(key)
		if !ok {
			return fmt.Errorf("unknown key %q in meta index", row.Key)
		}
		size, err := parseMetaRowSizeOnly(row.Value)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case dest <- blob.SizedRef{Ref: br, Size: size}:
		}
		n++
		if n == limit {
			break
		}
	}
	return nil
}

func (s *storage) packFile(ctx context.Context, fileRef blob.Ref) (err error) {
	s.Logf("Packing file %s ...", fileRef)
	defer func() {
		if err == nil {
			s.Logf("Packed file %s", fileRef)
		} else {
			s.Logf("Error packing file %s: %v", fileRef, err)
		}
	}()

	fr, err := schema.NewFileReader(ctx, s, fileRef)
	if err != nil {
		return err
	}
	return newPacker(s, fileRef, fr).pack(ctx)
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
	schemaParent map[blob.Ref][]blob.Ref // data blob -> its parent/ancestor schema blob(s), all the way up to fileRef included

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

func (pk *packer) pack(ctx context.Context) error {
	if err := pk.scanChunks(ctx); err != nil {
		return err
	}

	// TODO: decide as a function of schemaRefs and dataRefs
	// already in s.large whether it makes sense to still compact
	// this from a savings standpoint. For now we just always do.
	// Maybe we'd have knobs in the future. Ideally not.

	// Don't pack a file if we already have its wholeref stored
	// otherwise (perhaps under a different filename). But that
	// means we have to compute its wholeref first. We assume the
	// blob source will cache these lookups so it's not too
	// expensive to do two passes over the input.
	//
	// scanChunks rejects sparse files and offset parts, and the size
	// check below rejects parts that use only a prefix of their chunk, so
	// the file's contents are exactly its data chunks in order. Hash them
	// directly, verifying each chunk's digest along the way: once a file
	// is packed, its small copies get deleted, so a chunk whose contents
	// don't match its blobref must stop the pack before anything is
	// written.
	h := blob.NewHash()
	for _, dr := range pk.dataRefs {
		b, err := blob.FromFetcher(ctx, pk.s, dr)
		if err != nil {
			return err
		}
		if b.Size() != pk.dataSize[dr] {
			return fmt.Errorf("not packing file %v: chunk %v is %d bytes; schema says %d", pk.fileRef, dr, b.Size(), pk.dataSize[dr])
		}
		if err := b.ValidContents(ctx); err != nil {
			return fmt.Errorf("not packing file %v: %w", pk.fileRef, err)
		}
		r, err := b.ReadAll(ctx)
		if err != nil {
			return err
		}
		n, _ := io.Copy(h, r) // bytes.Reader to hash.Hash can't fail
		pk.wholeSize += n
	}
	pk.wholeRef = blob.RefFromHash(h)
	wholeKey := wholeMetaPrefix + pk.wholeRef.String()
	_, err := pk.s.meta.Get(wholeKey)
	if err == nil {
		// Nil error means there was some knowledge of this wholeref.
		return fmt.Errorf("already have wholeref %v packed; not packing again", pk.wholeRef)
	} else if !errors.Is(err, sorted.ErrNotFound) {
		return err
	}

	pk.chunksRemain = pk.dataRefs
	maxChunks := 0 // no limit
MakingZips:
	for len(pk.chunksRemain) > 0 {
		if err := pk.writeAZip(ctx, maxChunks); err != nil {
			var needTruncErr needsTruncatedAfterError
			if errors.As(err, &needTruncErr) {
				maxChunks = needTruncErr.n
				if fn := testHookSawTruncate; fn != nil {
					fn(needTruncErr.Ref)
				}
				continue MakingZips
			}
			return err
		}
		maxChunks = 0
	}

	// Record the final wholeMetaPrefix record:
	err = pk.s.meta.Set(wholeKey, fmt.Sprintf("%d %d", pk.wholeSize, len(pk.zips)))
	if err != nil {
		return fmt.Errorf("Error setting %s: %w", wholeKey, err)
	}

	return nil
}

func (pk *packer) scanChunks(ctx context.Context) error {
	schemaSeen := map[blob.Ref]bool{}
	return pk.fr.ForeachChunk(ctx, func(schemaPath []blob.Ref, p schema.BytesPart) error {
		if !p.BlobRef.Valid() {
			return errors.New("sparse files are not packed")
		}
		if p.Offset != 0 {
			// TODO: maybe care about this later, if we ever start making
			// these sorts of files.
			return errors.New("file uses complicated schema. not packing")
		}
		pk.schemaParent[p.BlobRef] = append([]blob.Ref(nil), schemaPath...) // clone it
		pk.dataSize[p.BlobRef] = uint32(p.Size)
		for _, schemaRef := range schemaPath {
			if schemaSeen[schemaRef] {
				continue
			}
			schemaSeen[schemaRef] = true
			pk.schemaRefs = append(pk.schemaRefs, schemaRef)
			b, err := blob.FromFetcher(ctx, pk.s, schemaRef)
			if err != nil {
				return err
			}
			if err := b.ValidContents(ctx); err != nil {
				return fmt.Errorf("not packing file %v: %w", pk.fileRef, err)
			}
			pk.schemaBlob[schemaRef] = b
		}
		pk.dataRefs = append(pk.dataRefs, p.BlobRef)
		return nil
	})
}

// needsTruncatedAfterError is returned by writeAZip if it failed in its estimation and the zip file
// was over the 16MB (or whatever) max blob size limit. In this case the caller tries again,
// writing only the first n chunks, the last of which is Ref.
type needsTruncatedAfterError struct {
	blob.Ref
	n int
}

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

// maxChunks, if non-zero, is the most data chunks to write to the zip.
// If the returned error is of type 'needsTruncatedAfterError', then
// the zip should be attempted to be written again, but truncating the
// data after the listed blob.
func (pk *packer) writeAZip(ctx context.Context, maxChunks int) (err error) {
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
		Name:     baseFileName,
		Method:   zip.Store, // uncompressed
		Modified: pk.fr.ModTime(),
	}
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
		rc, size, err := pk.s.Fetch(ctx, dr)
		check(err)
		if size != thisSize {
			rc.Close()
			return errors.New("unexpected size")
		}
		// pack verified this chunk already, but this is a separate read,
		// and these are the bytes that end up in the zip.
		chunkHash := dr.Hash()
		if n, err := io.Copy(io.MultiWriter(fw, chunkWholeHash, chunkHash), rc); err != nil || n != int64(size) {
			rc.Close()
			return fmt.Errorf("copy to zip = %v, %w; want %v bytes", n, err, size)
		}
		rc.Close()
		if !dr.HashMatches(chunkHash) {
			return fmt.Errorf("not packing file %v: blob contents don't match digest for ref %v", pk.fileRef, dr)
		}

		dataRefsWritten = append(dataRefsWritten, dr)
		dataBytesWritten += int64(size)
		chunks = chunks[1:]
		if len(dataRefsWritten) == maxChunks {
			break
		}
	}
	mf.DataBlobsOrigin = blob.RefFromHash(chunkWholeHash)

	// zipBlobs is where a schema or data blob is relative to the beginning
	// of the zip file.
	var zipBlobs []BlobAndPos

	var dataOffset int64
	for _, br := range dataRefsWritten {
		size := pk.dataSize[br]
		mf.DataBlobs = append(mf.DataBlobs, BlobAndPos{blob.SizedRef{Ref: br, Size: size}, dataOffset})

		zipBlobs = append(zipBlobs, BlobAndPos{blob.SizedRef{Ref: br, Size: size}, dataStart + dataOffset})
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
		zipBlobs = append(zipBlobs, BlobAndPos{blob.SizedRef{Ref: br, Size: b.Size()}, cw.n})
		r, err := b.ReadAll(ctx)
		if err != nil {
			return err
		}
		n, err := io.Copy(fw, r)

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
				return needsTruncatedAfterError{dr, i + 1}
			}
			overage -= int(pk.dataSize[dr])
		}
		return errors.New("file is unpackable; first blob is too big to fit")
	}

	zipRef := blob.RefFromBytes(zbuf.Bytes())
	zipSB, err := blobserver.ReceiveNoHash(ctx, pk.s.large, zipRef, bytes.NewReader(zbuf.Bytes()))
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
	bm.Set(fmt.Sprintf("%s%v", zipMetaPrefix, zipRef),
		fmt.Sprintf("%d %v %d %d %d",
			zipSB.Size,
			pk.wholeRef,
			pk.wholeSize,
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
		if err := pk.s.small.RemoveBlobs(ctx, toDelete); err != nil {
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
func (s *storage) foreachZipBlob(ctx context.Context, zipRef blob.Ref, fn func(BlobAndPos) error) error {
	sb, err := blobserver.StatBlob(ctx, s.large, zipRef)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(blob.ReaderAt(ctx, s.large, zipRef), int64(sb.Size))
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
	// apply fn to all the schema blobs
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
				SizedRef: blob.SizedRef{Ref: br, Size: uint32(f.UncompressedSize64)},
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
	// apply fn to all the data blobs
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
func (s *storage) deleteZipPack(ctx context.Context, br blob.Ref) error {
	inUse, err := s.zipPartsInUse(ctx, br)
	if err != nil {
		return err
	}
	if len(inUse) > 0 {
		return fmt.Errorf("can't delete zip pack %v: %d parts in use: %v", br, len(inUse), inUse)
	}
	if err := s.large.RemoveBlobs(ctx, []blob.Ref{br}); err != nil {
		return err
	}
	return s.meta.Delete("d:" + br.String())
}

func (s *storage) zipPartsInUse(ctx context.Context, br blob.Ref) ([]blob.Ref, error) {
	var (
		mu    sync.Mutex
		inUse []blob.Ref
	)
	var grp syncutil.Group
	gate := syncutil.NewGate(20) // arbitrary constant
	err := s.foreachZipBlob(ctx, br, func(bap BlobAndPos) error {
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
	// file in the zip pack file. This first file is the actual data,
	// or a part of it, that the rest of this zip is describing or
	// referencing.
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
