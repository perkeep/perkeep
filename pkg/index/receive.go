/*
Copyright 2011 The Perkeep Authors

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

package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hjfreyer/taglib-go/taglib"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
	_ "go4.org/media/heif"
	"go4.org/readerutil"
	"go4.org/types"
	"golang.org/x/sync/singleflight"
	"perkeep.org/internal/images"
	"perkeep.org/internal/magic"
	"perkeep.org/internal/media"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
)

func init() {
	t, err := time.Parse(time.RFC3339, msdosEpoch)
	if err != nil {
		panic(fmt.Sprintf("Cannot parse MSDOS epoch: %v", err))
	}
	msdosEpochTime = t
}

type mutationMap struct {
	// When the mutations are from a claim, signerBlobRef is the signer of the
	// claim, and signerID is its matching GPG key ID. They are copied out of kv because,
	// when adding the corresponding entries in the corpus, the signerBlobRef-signerID
	// relation needs to be known before the claim mutations themselves, so we need to
	// make sure the keySignerKeyID entry is always added first.
	signerBlobRef blob.Ref
	signerID      string
	kv            map[string]string // the keys and values we populate

	// We record if we get a delete claim, so we can update
	// the deletes cache right after committing the mutation.
	//
	// TODO(mpl): we only need to keep track of one claim so far,
	// but I chose a slice for when we need to do multi-claims?
	deletes []schema.Claim
}

func (mm *mutationMap) Set(k, v string) {
	if mm.kv == nil {
		mm.kv = make(map[string]string)
	}
	mm.kv[k] = v
}

func (mm *mutationMap) noteDelete(deleteClaim schema.Claim) {
	mm.deletes = append(mm.deletes, deleteClaim)
}

func blobsFilteringOut(v []blob.Ref, x blob.Ref) []blob.Ref {
	switch len(v) {
	case 0:
		return nil
	case 1:
		if v[0] == x {
			return nil
		}
		return v
	}
	nl := v[:0]
	for _, vb := range v {
		if vb != x {
			nl = append(nl, vb)
		}
	}
	return nl
}

func (ix *Index) indexBlob(ctx context.Context, br blob.Ref) error {
	rc, _, err := ix.blobSource.Fetch(ctx, br)
	if err != nil {
		return fmt.Errorf("index: failed to fetch %v for reindexing: %v", br, err)
	}
	defer rc.Close()
	if _, err := blobserver.Receive(ctx, ix, br, rc); err != nil {
		return err
	}
	return nil
}

// indexReadyBlobs indexes blobs that have been recently marked as ready to be
// reindexed, after the blobs they depend on eventually were indexed.
func (ix *Index) indexReadyBlobs(ctx context.Context) {
	defer ix.reindexWg.Done()

	popReadyReindex := func() (blob.Ref, bool) {
		ix.Lock()
		defer ix.Unlock()

		if len(ix.readyReindex) == 0 {
			return blob.Ref{}, false
		}
		var br blob.Ref
		for br = range ix.readyReindex {
			break
		}
		delete(ix.readyReindex, br)

		return br, true
	}

	failed := make(map[blob.Ref]bool)
	for br, ok := popReadyReindex(); ok; br, ok = popReadyReindex() {
		if err := ix.indexBlob(ctx, br); err != nil {
			log.Printf("out-of-order indexBlob(%v) = %v", br, err)
			failed[br] = true
		}
	}

	ix.Lock()
	defer ix.Unlock()
	for br := range failed {
		ix.readyReindex[br] = true
	}
}

// noteBlobIndexed checks if the recent indexing of br now allows the blobs that
// were depending on br, to be indexed in turn. If yes, they're reindexed
// asynchronously by indexReadyBlobs.
func (ix *Index) noteBlobIndexed(br blob.Ref) {
	for _, needer := range ix.neededBy[br] {
		newNeeds := blobsFilteringOut(ix.needs[needer], br)
		if len(newNeeds) == 0 {
			ix.readyReindex[needer] = true
			delete(ix.needs, needer)
			ix.reindexWg.Add(1)
			go ix.indexReadyBlobs(context.Background())
		} else {
			ix.needs[needer] = newNeeds
		}
	}
	delete(ix.neededBy, br)
}

func (ix *Index) removeAllMissingEdges(br blob.Ref) {
	var toDelete []string
	it := ix.queryPrefix(keyMissing, br)
	for it.Next() {
		toDelete = append(toDelete, it.Key())
	}
	if err := it.Close(); err != nil {
		// TODO: Care? Can lazily clean up later.
		log.Printf("Iterator close error: %v", err)
	}
	for _, k := range toDelete {
		if err := ix.s.Delete(k); err != nil {
			log.Printf("Error deleting key %s: %v", k, err)
		}
	}
}

var g singleflight.Group

func (ix *Index) ReceiveBlob(ctx context.Context, blobRef blob.Ref, source io.Reader) (blob.SizedRef, error) {
	res, err, _ := g.Do(blobRef.String(), func() (interface{}, error) {
		return ix.receiveBlob(ctx, blobRef, source)
	})
	io.Copy(io.Discard, source)

	if err != nil {
		return blob.SizedRef{}, err
	}
	return res.(blob.SizedRef), nil
}

func (ix *Index) receiveBlob(ctx context.Context, blobRef blob.Ref, source io.Reader) (blob.SizedRef, error) {
	// Read from source before acquiring ix.Lock (Issue 878):
	sniffer := NewBlobSniffer(blobRef)
	written, err := io.Copy(sniffer, source)
	if err != nil {
		return blob.SizedRef{}, err
	}
	sbr := blob.SizedRef{Ref: blobRef, Size: uint32(written)}

	ix.Lock()
	defer ix.Unlock()

	missingDeps := false
	defer func() {
		if err == nil {
			ix.noteBlobIndexed(blobRef)
			if !missingDeps {
				ix.removeAllMissingEdges(blobRef)
			}
		}
	}()

	// By default, return immediately if it looks like we already
	// have indexed this blob before.  But if the user has
	// CAMLI_REDO_INDEX_ON_RECEIVE set in their environment,
	// always index it. This is generally only useful when working
	// on the indexing code and retroactively indexing a subset of
	// content without forcing a global reindexing.
	if haveVal, haveErr := ix.s.Get("have:" + blobRef.String()); haveErr == nil {
		if strings.HasSuffix(haveVal, "|indexed") {
			if allowReindex, _ := strconv.ParseBool(os.Getenv("CAMLI_REDO_INDEX_ON_RECEIVE")); allowReindex {
				if debugEnv {
					log.Printf("index: reindexing %v", sbr)
				}
			} else {
				if debugEnv {
					log.Printf("index: ignoring upload of already-indexed %v", sbr)
				}
				return sbr, nil
			}
		}
	}

	sniffer.Parse()

	fetcher := &missTrackFetcher{
		fetcher: ix.blobSource,
	}

	mm, err := ix.populateMutationMap(ctx, fetcher, blobRef, sniffer)
	if debugEnv {
		log.Printf("index of %v: mm=%v, err=%v", blobRef, mm, err)
	}
	if err != nil {
		if err != errMissingDep {
			return blob.SizedRef{}, err
		}
		fetcher.mu.Lock()
		defer fetcher.mu.Unlock()
		if len(fetcher.missing) == 0 {
			panic("errMissingDep happened, but no fetcher.missing recorded")
		}
		missingDeps = true
		allRecorded := true
		for _, missing := range fetcher.missing {
			if err := ix.noteNeeded(blobRef, missing); err != nil {
				allRecorded = false
			}
		}
		if allRecorded {
			// Lie and say things are good. We've
			// successfully recorded that the blob isn't
			// indexed, but we'll reindex it later once
			// the dependent blobs arrive.
			return sbr, nil
		}
		return blob.SizedRef{}, err
	}

	if err := ix.commit(mm); err != nil {
		return blob.SizedRef{}, err
	}

	if c := ix.corpus; c != nil {
		if err = c.addBlob(ctx, blobRef, mm); err != nil {
			return blob.SizedRef{}, err
		}
	}

	// TODO(bradfitz): log levels? These are generally noisy
	// (especially in tests, like search/handler_test), but I
	// could see it being useful in production. For now, disabled:
	//
	// mimeType := sniffer.MIMEType()
	// log.Printf("indexer: received %s; type=%v; truncated=%v", blobRef, mimeType, sniffer.IsTruncated())

	return blob.SizedRef{Ref: blobRef, Size: uint32(written)}, nil
}

// commit writes the contents of the mutationMap on a batch
// mutation and commits that batch. It also updates the deletes
// cache.
func (ix *Index) commit(mm *mutationMap) error {
	// We want the update of the deletes cache to be atomic
	// with the transaction commit, so we lock here instead
	// of within updateDeletesCache.
	ix.deletes.Lock()
	defer ix.deletes.Unlock()
	bm := ix.s.BeginBatch()
	for k, v := range mm.kv {
		bm.Set(k, v)
	}
	err := ix.s.CommitBatch(bm)
	if err != nil {
		return err
	}
	for _, cl := range mm.deletes {
		if err := ix.updateDeletesCache(cl); err != nil {
			return fmt.Errorf("Could not update the deletes cache after deletion from %v: %v", cl, err)
		}
	}
	return nil
}

func (ix *Index) verifySignature(ctx context.Context, fetcher *missTrackFetcher, schemaBlob *schema.Blob) (*jsonsign.VerifyRequest, error) {
	tf := &trackErrorsFetcher{f: fetcher}
	vr := jsonsign.NewVerificationRequest(schemaBlob.JSON(), blob.NewSerialFetcher(ix.KeyFetcher, tf))
	_, err := vr.Verify(ctx)

	if err != nil {
		// TODO(bradfitz): ask if the vr.Err.(jsonsign.Error).IsPermanent() and retry
		// later if it's not permanent?
		if tf.hasErrNotExist() {
			return nil, errMissingDep
		}
		return nil, err
	}

	return vr, nil
}

func (ix *Index) populateMutationMapForSchema(ctx context.Context, fetcher *missTrackFetcher, schemaBlob *schema.Blob, mm *mutationMap) error {
	switch schemaBlob.Type() {
	case schema.TypePermanode:
		_, err := ix.verifySignature(ctx, fetcher, schemaBlob)
		return err
	case schema.TypeClaim:
		vr, err := ix.verifySignature(ctx, fetcher, schemaBlob)
		if err != nil {
			return err
		}
		return ix.populateClaim(ctx, fetcher, schemaBlob, vr, mm)
	case schema.TypeFile:
		return ix.populateFile(ctx, fetcher, schemaBlob, mm)
	case schema.TypeDirectory:
		return ix.populateDir(ctx, fetcher, schemaBlob, mm)
	default:
		return nil
	}
}

// populateMutationMap populates keys & values that will be committed
// into the returned map.
//
// the blobref can be trusted at this point (it's been fully consumed
// and verified to match), and the sniffer has been populated.
func (ix *Index) populateMutationMap(ctx context.Context, fetcher *missTrackFetcher, br blob.Ref, sniffer *BlobSniffer) (*mutationMap, error) {
	mm := &mutationMap{
		kv: map[string]string{
			"meta:" + br.String(): fmt.Sprintf("%d|%s", sniffer.Size(), sniffer.MIMEType()),
		},
	}
	var err error
	if schemaBlob, ok := sniffer.SchemaBlob(); ok {
		err = ix.populateMutationMapForSchema(ctx, fetcher, schemaBlob, mm)
	}

	if err != nil && err != errMissingDep {
		return nil, err
	}
	var haveVal string
	if err == errMissingDep {
		haveVal = fmt.Sprintf("%d", sniffer.Size())
	} else {
		haveVal = fmt.Sprintf("%d|indexed", sniffer.Size())
	}
	mm.kv["have:"+br.String()] = haveVal
	if len(fetcher.missing) == 0 {
		// If err == nil, we're good. Else (err == errMissingDep), we
		// know the error did not come from a fetching miss (because
		// len(fetcher.missing) == 0) , but from an index miss. Therefore
		// we know the miss has already been noted and will be dealt with
		// later, so we can also pretend everything's fine.
		return mm, nil
	}
	return mm, err
}

// keepFirstN keeps the first N bytes written to it in Bytes.
type keepFirstN struct {
	N     int
	Bytes []byte
}

func (w *keepFirstN) Write(p []byte) (n int, err error) {
	if n := w.N - len(w.Bytes); n > 0 {
		if n > len(p) {
			n = len(p)
		}
		w.Bytes = append(w.Bytes, p[:n]...)
	}
	return len(p), nil
}

// missTrackFetcher is a blob.Fetcher that records which blob(s) it
// failed to load from src.
type missTrackFetcher struct {
	fetcher blob.Fetcher

	mu      sync.Mutex // guards missing
	missing []blob.Ref
}

func (f *missTrackFetcher) Fetch(ctx context.Context, br blob.Ref) (blob io.ReadCloser, size uint32, err error) {
	blob, size, err = f.fetcher.Fetch(ctx, br)
	if err == os.ErrNotExist {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.missing = append(f.missing, br)
	}
	return
}

// trackErrorsFetcher is a blob.Fetcher that records to errs all Fetch errors.
type trackErrorsFetcher struct {
	mu   sync.RWMutex
	errs []error

	f blob.Fetcher
}

func (tf *trackErrorsFetcher) Fetch(ctx context.Context, br blob.Ref) (blob io.ReadCloser, size uint32, err error) {
	blob, size, err = tf.f.Fetch(ctx, br)
	if err != nil {
		tf.mu.Lock()
		defer tf.mu.Unlock()
		tf.errs = append(tf.errs, err)
	}
	return
}

// hasErrNotExist reports whether tf recorded any error and if all of them are
// os.ErrNotExist errors.
func (tf *trackErrorsFetcher) hasErrNotExist() bool {
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	if len(tf.errs) == 0 {
		return false
	}
	for _, v := range tf.errs {
		if v != os.ErrNotExist {
			return false
		}
	}
	return true
}

// filePrefixReader is both a *bytes.Reader and a *schema.FileReader for use in readPrefixOrFile
type filePrefixReader interface {
	io.Reader
	io.ReaderAt
}

// readPrefixOrFile executes a given func with a reader on the passed prefix and
// falls back to passing a reader on the whole file if the func returns an error.
func readPrefixOrFile(prefix []byte, fetcher blob.Fetcher, b *schema.Blob, fn func(filePrefixReader) error) (err error) {
	pr := bytes.NewReader(prefix)
	err = fn(pr)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		var fr *schema.FileReader
		fr, err = b.NewFileReader(fetcher)
		if err == nil {
			err = fn(fr)
			fr.Close()
		}
	}
	return err
}

const msdosEpoch = "1980-01-01T00:00:00Z"

var (
	exifDebug, _   = strconv.ParseBool(os.Getenv("CAMLI_DEBUG_IMAGES"))
	debugEnv, _    = strconv.ParseBool(os.Getenv("CAMLI_DEBUG"))
	msdosEpochTime time.Time
)

// b: the parsed file schema blob
// mm: keys to populate
func (ix *Index) populateFile(ctx context.Context, fetcher blob.Fetcher, b *schema.Blob, mm *mutationMap) (err error) {
	var times []time.Time // all creation or mod times seen; may be zero
	times = append(times, b.ModTime())

	blobRef := b.BlobRef()
	tf := &trackErrorsFetcher{f: fetcher.(*missTrackFetcher)}
	fr, err := b.NewFileReader(tf)
	if err != nil {
		return err
	}
	defer fr.Close()
	mimeType, mr := magic.MIMETypeFromReader(fr)
	if mimeType == "" {
		mimeType = magic.MIMETypeByExtension(filepath.Ext(b.FileName()))
	}

	h := blob.NewHash()
	var copyDest io.Writer = h
	var imageBuf *keepFirstN // or nil
	if strings.HasPrefix(mimeType, "image/") {
		imageBuf = &keepFirstN{N: 512 << 10}
		copyDest = io.MultiWriter(copyDest, imageBuf)
	}
	size, err := io.Copy(copyDest, mr)
	if err != nil {
		if tf.hasErrNotExist() {
			return errMissingDep
		}
		return err
	}
	wholeRef := blob.RefFromHash(h)

	if imageBuf != nil {
		var conf images.Config
		decodeConfig := func(r filePrefixReader) error {
			conf, err = images.DecodeConfig(r)
			return err
		}
		if err := readPrefixOrFile(imageBuf.Bytes, fetcher, b, decodeConfig); err == nil {
			mm.Set(keyImageSize.Key(blobRef), keyImageSize.Val(fmt.Sprint(conf.Width), fmt.Sprint(conf.Height)))
		} else if debugEnv {
			log.Printf("index: WARNING: image decodeConfig: %v", err)
		}

		exifData := imageBuf.Bytes
		if conf.HEICEXIF != nil {
			exifData = conf.HEICEXIF
		}
		var ft time.Time
		fileTime := func(r filePrefixReader) error {
			ft, err = schema.FileTime(r)
			return err
		}

		if err = readPrefixOrFile(exifData, fetcher, b, fileTime); err == nil {
			times = append(times, ft)
		} else if debugEnv {
			log.Printf("index: WARNING: image fileTime: %v", err)

		}
		if exifDebug {
			log.Printf("filename %q exif = %v, %v", b.FileName(), ft, err)
		}

		// TODO(mpl): find (generate?) more broken EXIF images to experiment with.
		indexEXIFData := func(r filePrefixReader) error {
			return indexEXIF(wholeRef, r, mm)
		}
		if err = readPrefixOrFile(exifData, fetcher, b, indexEXIFData); err != nil {
			if exifDebug {
				log.Printf("error parsing EXIF: %v", err)
			}
		}
	}

	var sortTimes []time.Time
	for _, t := range times {
		if !t.IsZero() {
			sortTimes = append(sortTimes, t)
		}
	}
	sort.Sort(types.ByTime(sortTimes))
	var time3339s string
	switch {
	case len(sortTimes) == 1:
		time3339s = types.Time3339(sortTimes[0]).String()
	case len(sortTimes) >= 2:
		oldest, newest := sortTimes[0], sortTimes[len(sortTimes)-1]
		// Common enough exception: unset creation time from an MSDOS
		// system (which is the default in zip files). So if we have
		// another time to use, just ignore the MSDOS epoch one.
		if oldest.After(msdosEpochTime) {
			time3339s = types.Time3339(oldest).String() + "," + types.Time3339(newest).String()
		} else {
			time3339s = types.Time3339(newest).String()
		}
	}

	mm.Set(keyWholeToFileRef.Key(wholeRef, blobRef), "1")
	mm.Set(keyFileInfo.Key(blobRef), keyFileInfo.Val(size, b.FileName(), mimeType, wholeRef))
	mm.Set(keyFileTimes.Key(blobRef), keyFileTimes.Val(time3339s))

	if strings.HasPrefix(mimeType, "audio/") {
		indexMusic(io.NewSectionReader(fr, 0, fr.Size()), wholeRef, mm)
	}

	return nil
}

func tagFormatString(tag *tiff.Tag) string {
	switch tag.Format() {
	case tiff.IntVal:
		return "int"
	case tiff.RatVal:
		return "rat"
	case tiff.FloatVal:
		return "float"
	case tiff.StringVal:
		return "string"
	}
	return ""
}

type exifWalkFunc func(name exif.FieldName, tag *tiff.Tag) error

func (f exifWalkFunc) Walk(name exif.FieldName, tag *tiff.Tag) error { return f(name, tag) }

var errEXIFPanic = errors.New("EXIF library panicked while walking fields")

func indexEXIF(wholeRef blob.Ref, r io.Reader, mm *mutationMap) (err error) {
	var tiffErr error
	ex, err := exif.Decode(r)
	if err != nil {
		tiffErr = err
		if exif.IsCriticalError(err) {
			if exif.IsShortReadTagValueError(err) {
				return io.ErrUnexpectedEOF // trigger a retry with whole file
			}
			return
		}
		log.Printf("Non critical TIFF decoding error: %v", err)
	}
	defer func() {
		// The EXIF library panics if you access a field past
		// what the file contains.  Be paranoid and just
		// recover here, instead of crashing on an invalid
		// EXIF file.
		if e := recover(); e != nil {
			err = errEXIFPanic
		}
	}()

	err = ex.Walk(exifWalkFunc(func(name exif.FieldName, tag *tiff.Tag) error {
		tagFmt := tagFormatString(tag)
		if tagFmt == "" {
			return nil
		}
		key := keyEXIFTag.Key(wholeRef, fmt.Sprintf("%04x", tag.Id))
		numComp := int(tag.Count)
		if tag.Format() == tiff.StringVal {
			numComp = 1
		}
		var val bytes.Buffer
		val.WriteString(keyEXIFTag.Val(tagFmt, numComp, ""))
		if tag.Format() == tiff.StringVal {
			str, err := tag.StringVal()
			if err != nil {
				log.Printf("Invalid EXIF string data: %v", err)
				return nil
			}
			if containsUnsafeRawStrByte(str) {
				val.WriteString(urle(str))
			} else {
				val.WriteString(str)
			}
		} else {
			for i := 0; i < int(tag.Count); i++ {
				if i > 0 {
					val.WriteByte('|')
				}
				switch tagFmt {
				case "int":
					v, err := tag.Int(i)
					if err != nil {
						log.Printf("Invalid EXIF int data: %v", err)
						return nil
					}
					fmt.Fprintf(&val, "%d", v)
				case "rat":
					n, d, err := tag.Rat2(i)
					if err != nil {
						log.Printf("Invalid EXIF rat data: %v", err)
						return nil
					}
					fmt.Fprintf(&val, "%d/%d", n, d)
				case "float":
					v, err := tag.Float(i)
					if err != nil {
						log.Printf("Invalid EXIF float data: %v", err)
						return nil
					}
					fmt.Fprintf(&val, "%v", v)
				default:
					panic("shouldn't get here")
				}
			}
		}
		valStr := val.String()
		mm.Set(key, valStr)
		return nil
	}))
	if err != nil {
		return
	}

	if exif.IsGPSError(tiffErr) {
		log.Printf("Invalid EXIF GPS data: %v", tiffErr)
		return nil
	}
	if lat, long, err := ex.LatLong(); err == nil {
		if math.Abs(long) > 180.0 || math.Abs(lat) > 90.0 {
			log.Printf("Long, lat outside allowed range: %v, %v", long, lat)
			return nil
		}
		if math.IsNaN(long) || math.IsNaN(lat) {
			log.Print("Latitude or Longitude is NaN")
			return nil
		}
		// index 7 places fixed precision (~10mm worst case at equator)
		// http://stackoverflow.com/a/1947615/114581
		mm.Set(keyEXIFGPS.Key(wholeRef), keyEXIFGPS.Val(fmt.Sprintf("%.7f", lat), fmt.Sprintf("%.7f", long)))
	} else if !exif.IsTagNotPresentError(err) {
		log.Printf("Invalid EXIF GPS data: %v", err)
	}
	return nil
}

// indexMusic adds mutations to index the wholeRef by attached metadata and other properties.
func indexMusic(r readerutil.SizeReaderAt, wholeRef blob.Ref, mm *mutationMap) {
	tag, err := taglib.Decode(r, r.Size())
	if err != nil {
		log.Print("index: error parsing tag: ", err)
		return
	}

	var footerLength int64 = 0
	if hasTag, err := media.HasID3v1Tag(r); err != nil {
		log.Print("index: unable to check for ID3v1 tag: ", err)
		return
	} else if hasTag {
		footerLength = media.ID3v1TagLength
	}

	// Generate a hash of the audio portion of the file (i.e. excluding ID3v1 and v2 tags).
	audioStart := int64(tag.TagSize())
	audioSize := r.Size() - audioStart - footerLength
	hash := blob.NewHash()
	if _, err := io.Copy(hash, io.NewSectionReader(r, audioStart, audioSize)); err != nil {
		log.Print("index: error generating hash of audio data: ", err)
		return
	}
	mediaRef := blob.RefFromHash(hash)

	duration, err := media.GetMPEGAudioDuration(io.NewSectionReader(r, audioStart, audioSize))
	if err != nil {
		log.Print("index: unable to calculate audio duration: ", err)
		duration = 0
	}

	var yearStr, trackStr, discStr, durationStr string
	if !tag.Year().IsZero() {
		const justYearLayout = "2006"
		yearStr = tag.Year().Format(justYearLayout)
	}
	if tag.Track() != 0 {
		trackStr = fmt.Sprintf("%d", tag.Track())
	}
	if tag.Disc() != 0 {
		discStr = fmt.Sprintf("%d", tag.Disc())
	}
	if duration != 0 {
		durationStr = fmt.Sprintf("%d", duration/time.Millisecond)
	}

	// Note: if you add to this map, please update
	// pkg/search/query.go's MediaTagConstraint Tag docs.
	tags := map[string]string{
		"title":              tag.Title(),
		"artist":             tag.Artist(),
		"album":              tag.Album(),
		"genre":              tag.Genre(),
		"musicbrainzalbumid": tag.CustomFrames()["MusicBrainz Album Id"],
		"year":               yearStr,
		"track":              trackStr,
		"disc":               discStr,
		"mediaref":           mediaRef.String(),
		"durationms":         durationStr,
	}

	for tag, value := range tags {
		if value != "" {
			mm.Set(keyMediaTag.Key(wholeRef, tag), keyMediaTag.Val(value))
		}
	}
}

// b: the parsed file schema blob
// mm: keys to populate
func (ix *Index) populateDir(ctx context.Context, fetcher blob.Fetcher, b *schema.Blob, mm *mutationMap) error {
	blobRef := b.BlobRef()
	// TODO(bradfitz): move the NewDirReader and FileName method off *schema.Blob and onto
	// StaticFile/StaticDirectory or something.

	tf := &trackErrorsFetcher{f: fetcher.(*missTrackFetcher)}
	dr, err := b.NewDirReader(ctx, tf)
	if err != nil {
		// TODO(bradfitz): propagate up a transient failure
		// error type, so we can retry indexing files in the
		// future if blobs are only temporarily unavailable.
		log.Printf("index: error indexing directory, creating NewDirReader %s: %v", blobRef, err)
		return nil
	}
	sts, err := dr.StaticSet(ctx)
	if err != nil {
		if tf.hasErrNotExist() {
			return errMissingDep
		}
		log.Printf("index: error indexing directory: can't get StaticSet: %v\n", err)
		return nil
	}

	mm.Set(keyFileInfo.Key(blobRef), keyFileInfo.Val(len(sts), b.FileName(), "", blob.Ref{}))
	for _, br := range sts {
		mm.Set(keyStaticDirChild.Key(blobRef, br.String()), "1")
	}
	return nil
}

var errMissingDep = errors.New("blob was not fully indexed because of a missing dependency")

// populateDeleteClaim adds to mm the entries resulting from the delete claim cl.
// It is assumed cl is a valid claim, and vr has already been verified.
func (ix *Index) populateDeleteClaim(ctx context.Context, cl schema.Claim, vr *jsonsign.VerifyRequest, mm *mutationMap) error {
	br := cl.Blob().BlobRef()
	target := cl.Target()
	if !target.Valid() {
		log.Print(fmt.Errorf("no valid target for delete claim %v", br))
		return nil
	}
	meta, err := ix.GetBlobMeta(ctx, target)
	if err != nil {
		if err == os.ErrNotExist {
			if err := ix.noteNeeded(br, target); err != nil {
				return fmt.Errorf("could not note that delete claim %v depends on %v: %v", br, target, err)
			}
			return errMissingDep
		}
		log.Print(fmt.Errorf("Could not get mime type of target blob %v: %v", target, err))
		return nil
	}

	if meta.CamliType != schema.TypePermanode && meta.CamliType != schema.TypeClaim {
		log.Print(fmt.Errorf("delete claim target in %v is neither a permanode nor a claim: %v", br, meta.CamliType))
		return nil
	}
	mm.Set(keyDeleted.Key(target, cl.ClaimDateString(), br), "")
	if meta.CamliType == schema.TypeClaim {
		return nil
	}
	recentKey := keyRecentPermanode.Key(vr.SignerKeyId, cl.ClaimDateString(), br)
	mm.Set(recentKey, target.String())
	attr, value := cl.Attribute(), cl.Value()
	claimKey := keyPermanodeClaim.Key(target, vr.SignerKeyId, cl.ClaimDateString(), br)
	mm.Set(claimKey, keyPermanodeClaim.Val(cl.ClaimType(), attr, value, vr.CamliSigner))
	return nil
}

func (ix *Index) populateClaim(ctx context.Context, fetcher *missTrackFetcher, b *schema.Blob, vr *jsonsign.VerifyRequest, mm *mutationMap) error {
	br := b.BlobRef()

	claim, ok := b.AsClaim()
	if !ok {
		// Skip bogus claim with malformed permanode.
		return nil
	}

	verifiedKeyId := vr.SignerKeyId
	mm.signerID = verifiedKeyId
	mm.signerBlobRef = vr.CamliSigner
	mm.Set(keySignerKeyID.name+":"+vr.CamliSigner.String(), verifiedKeyId)

	if claim.ClaimType() == string(schema.DeleteClaim) {
		if err := ix.populateDeleteClaim(ctx, claim, vr, mm); err != nil {
			return err
		}
		mm.noteDelete(claim)
		return nil
	}

	pnbr := claim.ModifiedPermanode()
	if !pnbr.Valid() {
		// A different type of claim; not modifying a permanode.
		return nil
	}

	attr, value := claim.Attribute(), claim.Value()
	recentKey := keyRecentPermanode.Key(verifiedKeyId, claim.ClaimDateString(), br)
	mm.Set(recentKey, pnbr.String())
	claimKey := keyPermanodeClaim.Key(pnbr, verifiedKeyId, claim.ClaimDateString(), br)
	mm.Set(claimKey, keyPermanodeClaim.Val(claim.ClaimType(), attr, value, vr.CamliSigner))

	if strings.HasPrefix(attr, "camliPath:") {
		targetRef, ok := blob.Parse(value)
		if ok {
			// TODO: deal with set-attribute vs. del-attribute
			// properly? I think we get it for free when
			// del-attribute has no Value, but we need to deal
			// with the case where they explicitly delete the
			// current value.
			suffix := attr[len("camliPath:"):]
			active := "Y"
			if claim.ClaimType() == "del-attribute" {
				active = "N"
			}
			baseRef := pnbr
			claimRef := br

			key := keyPathBackward.Key(verifiedKeyId, targetRef, claimRef)
			val := keyPathBackward.Val(claim.ClaimDateString(), baseRef, active, suffix)
			mm.Set(key, val)

			key = keyPathForward.Key(verifiedKeyId, baseRef, suffix, claim.ClaimDateString(), claimRef)
			val = keyPathForward.Val(active, targetRef)
			mm.Set(key, val)
		}
	}

	if claim.ClaimType() != string(schema.DelAttributeClaim) && IsIndexedAttribute(attr) {
		key := keySignerAttrValue.Key(verifiedKeyId, attr, value, claim.ClaimDateString(), br)
		mm.Set(key, keySignerAttrValue.Val(pnbr))
	}

	if IsBlobReferenceAttribute(attr) {
		targetRef, ok := blob.Parse(value)
		if ok {
			key := keyEdgeBackward.Key(targetRef, pnbr, br)
			mm.Set(key, keyEdgeBackward.Val("permanode", ""))
		}
	}

	return nil
}

// updateDeletesCache updates the index deletes cache with the cl delete claim.
// deleteClaim is trusted to be a valid delete Claim.
func (ix *Index) updateDeletesCache(deleteClaim schema.Claim) error {
	target := deleteClaim.Target()
	deleter := deleteClaim.Blob()
	when, err := deleter.ClaimDate()
	if err != nil {
		return fmt.Errorf("Could not get date of delete claim %v: %v", deleteClaim, err)
	}
	targetDeletions := append(ix.deletes.m[target],
		deletion{
			deleter: deleter.BlobRef(),
			when:    when,
		})
	sort.Sort(sort.Reverse(byDeletionDate(targetDeletions)))
	ix.deletes.m[target] = targetDeletions
	return nil
}
