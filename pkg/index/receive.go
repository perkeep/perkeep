/*
Copyright 2011 Google Inc.

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
	"crypto/sha1"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/images"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/media"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/types"

	"github.com/hjfreyer/taglib-go/taglib"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
)

// outOfOrderIndexerLoop asynchronously reindexes blobs received
// out of order. It panics if started more than once or if the
// index has no blobSource.
func (ix *Index) outOfOrderIndexerLoop() {
	ix.mu.RLock()
	if ix.oooRunning == true {
		panic("outOfOrderIndexerLoop is already running")
	}
	if ix.blobSource == nil {
		panic("index has no blobSource")
	}
	ix.oooRunning = true
	ix.mu.RUnlock()
WaitTickle:
	for _ = range ix.tickleOoo {
		for {
			ix.mu.Lock()
			if len(ix.readyReindex) == 0 {
				ix.mu.Unlock()
				continue WaitTickle
			}
			var br blob.Ref
			for br = range ix.readyReindex {
				break
			}
			delete(ix.readyReindex, br)
			ix.mu.Unlock()

			err := ix.indexBlob(br)
			if err != nil {
				log.Printf("out-of-order indexBlob(%v) = %v", br, err)
				ix.mu.Lock()
				if len(ix.needs[br]) == 0 {
					ix.readyReindex[br] = true
				}
				ix.mu.Unlock()
			}
		}
	}
}

func (ix *Index) indexBlob(br blob.Ref) error {
	ix.mu.RLock()
	bs := ix.blobSource
	ix.mu.RUnlock()
	if bs == nil {
		panic(fmt.Sprintf("index: can't re-index %v: no blobSource", br))
	}
	rc, _, err := bs.Fetch(br)
	if err != nil {
		return fmt.Errorf("index: failed to fetch %v for reindexing: %v", br, err)
	}
	defer rc.Close()
	if _, err := blobserver.Receive(ix, br, rc); err != nil {
		return err
	}
	return nil
}

type mutationMap struct {
	kv map[string]string // the keys and values we populate

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

func (ix *Index) noteBlobIndexed(br blob.Ref) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, needer := range ix.neededBy[br] {
		newNeeds := blobsFilteringOut(ix.needs[needer], br)
		if len(newNeeds) == 0 {
			ix.readyReindex[needer] = true
			delete(ix.needs, needer)
			select {
			case ix.tickleOoo <- true:
			default:
			}
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

func (ix *Index) ReceiveBlob(blobRef blob.Ref, source io.Reader) (retsb blob.SizedRef, err error) {
	missingDeps := false
	defer func() {
		if err == nil {
			ix.noteBlobIndexed(blobRef)
			if !missingDeps {
				ix.removeAllMissingEdges(blobRef)
			}
		}
	}()
	sniffer := NewBlobSniffer(blobRef)
	written, err := io.Copy(sniffer, source)
	if err != nil {
		return
	}
	if haveVal, haveErr := ix.s.Get("have:" + blobRef.String()); haveErr == nil {
		if strings.HasSuffix(haveVal, "|indexed") {
			return blob.SizedRef{blobRef, uint32(written)}, nil
		}
	}

	sniffer.Parse()

	fetcher := &missTrackFetcher{
		fetcher: ix.blobSource,
	}

	mm, err := ix.populateMutationMap(fetcher, blobRef, sniffer)
	if err != nil {
		if err != errMissingDep {
			return
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
			return blob.SizedRef{blobRef, uint32(written)}, nil
		}
		return
	}

	if err := ix.commit(mm); err != nil {
		return retsb, err
	}

	if c := ix.corpus; c != nil {
		if err = c.addBlob(blobRef, mm); err != nil {
			return
		}
	}

	// TODO(bradfitz): log levels? These are generally noisy
	// (especially in tests, like search/handler_test), but I
	// could see it being useful in production. For now, disabled:
	//
	// mimeType := sniffer.MIMEType()
	// log.Printf("indexer: received %s; type=%v; truncated=%v", blobRef, mimeType, sniffer.IsTruncated())

	return blob.SizedRef{blobRef, uint32(written)}, nil
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

// populateMutationMap populates keys & values that will be committed
// into the returned map.
//
// the blobref can be trusted at this point (it's been fully consumed
// and verified to match), and the sniffer has been populated.
func (ix *Index) populateMutationMap(fetcher *missTrackFetcher, br blob.Ref, sniffer *BlobSniffer) (*mutationMap, error) {
	mm := &mutationMap{
		kv: map[string]string{
			"meta:" + br.String(): fmt.Sprintf("%d|%s", sniffer.Size(), sniffer.MIMEType()),
		},
	}
	var err error
	if blob, ok := sniffer.SchemaBlob(); ok {
		switch blob.Type() {
		case "claim":
			err = ix.populateClaim(fetcher, blob, mm)
		case "file":
			err = ix.populateFile(fetcher, blob, mm)
		case "directory":
			err = ix.populateDir(fetcher, blob, mm)
		}
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
	ix.mu.Lock()
	defer ix.mu.Unlock()
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

func (f *missTrackFetcher) Fetch(br blob.Ref) (blob io.ReadCloser, size uint32, err error) {
	blob, size, err = f.fetcher.Fetch(br)
	if err == os.ErrNotExist {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.missing = append(f.missing, br)
		err = errMissingDep
	}
	return
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

var exifDebug, _ = strconv.ParseBool(os.Getenv("CAMLI_DEBUG_IMAGES"))

// b: the parsed file schema blob
// mm: keys to populate
func (ix *Index) populateFile(fetcher blob.Fetcher, b *schema.Blob, mm *mutationMap) (err error) {
	var times []time.Time // all creation or mod times seen; may be zero
	times = append(times, b.ModTime())

	blobRef := b.BlobRef()
	fr, err := b.NewFileReader(fetcher)
	if err != nil {
		return err
	}
	defer fr.Close()
	mime, mr := magic.MIMETypeFromReader(fr)

	sha1 := sha1.New()
	var copyDest io.Writer = sha1
	var imageBuf *keepFirstN // or nil
	if strings.HasPrefix(mime, "image/") {
		imageBuf = &keepFirstN{N: 512 << 10}
		copyDest = io.MultiWriter(copyDest, imageBuf)
	}
	size, err := io.Copy(copyDest, mr)
	if err != nil {
		return err
	}
	wholeRef := blob.RefFromHash(sha1)

	if imageBuf != nil {
		var conf images.Config
		decodeConfig := func(r filePrefixReader) error {
			conf, err = images.DecodeConfig(r)
			return err
		}
		if err := readPrefixOrFile(imageBuf.Bytes, fetcher, b, decodeConfig); err == nil {
			mm.Set(keyImageSize.Key(blobRef), keyImageSize.Val(fmt.Sprint(conf.Width), fmt.Sprint(conf.Height)))
		}

		var ft time.Time
		fileTime := func(r filePrefixReader) error {
			ft, err = schema.FileTime(r)
			return err
		}
		if err = readPrefixOrFile(imageBuf.Bytes, fetcher, b, fileTime); err == nil {
			times = append(times, ft)
		}
		if exifDebug {
			log.Printf("filename %q exif = %v, %v", b.FileName(), ft, err)
		}

		// TODO(mpl): find (generate?) more broken EXIF images to experiment with.
		indexEXIFData := func(r filePrefixReader) error {
			return indexEXIF(wholeRef, r, mm)
		}
		if err = readPrefixOrFile(imageBuf.Bytes, fetcher, b, indexEXIFData); err != nil {
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
		time3339s = types.Time3339(oldest).String() + "," + types.Time3339(newest).String()
	}

	mm.Set(keyWholeToFileRef.Key(wholeRef, blobRef), "1")
	mm.Set(keyFileInfo.Key(blobRef), keyFileInfo.Val(size, b.FileName(), mime, wholeRef))
	mm.Set(keyFileTimes.Key(blobRef), keyFileTimes.Val(time3339s))

	if strings.HasPrefix(mime, "audio/") {
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
		mm.Set(keyEXIFGPS.Key(wholeRef), keyEXIFGPS.Val(fmt.Sprint(lat), fmt.Sprint(long)))
	} else if !exif.IsTagNotPresentError(err) {
		log.Printf("Invalid EXIF GPS data: %v", err)
	}
	return nil
}

// indexMusic adds mutations to index the wholeRef by attached metadata and other properties.
func indexMusic(r types.SizeReaderAt, wholeRef blob.Ref, mm *mutationMap) {
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
	hash := sha1.New()
	if _, err := io.Copy(hash, io.NewSectionReader(r, audioStart, audioSize)); err != nil {
		log.Print("index: error generating SHA1 from audio data: ", err)
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
func (ix *Index) populateDir(fetcher blob.Fetcher, b *schema.Blob, mm *mutationMap) error {
	blobRef := b.BlobRef()
	// TODO(bradfitz): move the NewDirReader and FileName method off *schema.Blob and onto
	// StaticFile/StaticDirectory or something.

	dr, err := b.NewDirReader(fetcher)
	if err != nil {
		// TODO(bradfitz): propagate up a transient failure
		// error type, so we can retry indexing files in the
		// future if blobs are only temporarily unavailable.
		log.Printf("index: error indexing directory, creating NewDirReader %s: %v", blobRef, err)
		return nil
	}
	sts, err := dr.StaticSet()
	if err != nil {
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
func (ix *Index) populateDeleteClaim(cl schema.Claim, vr *jsonsign.VerifyRequest, mm *mutationMap) error {
	br := cl.Blob().BlobRef()
	target := cl.Target()
	if !target.Valid() {
		log.Print(fmt.Errorf("no valid target for delete claim %v", br))
		return nil
	}
	meta, err := ix.GetBlobMeta(target)
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

	// TODO(mpl): create consts somewhere for "claim" and "permanode" as camliTypes, and use them,
	// instead of hardcoding. Unless they already exist ? (didn't find them).
	if meta.CamliType != "permanode" && meta.CamliType != "claim" {
		log.Print(fmt.Errorf("delete claim target in %v is neither a permanode nor a claim: %v", br, meta.CamliType))
		return nil
	}
	mm.Set(keyDeleted.Key(target, cl.ClaimDateString(), br), "")
	if meta.CamliType == "claim" {
		return nil
	}
	recentKey := keyRecentPermanode.Key(vr.SignerKeyId, cl.ClaimDateString(), br)
	mm.Set(recentKey, target.String())
	attr, value := cl.Attribute(), cl.Value()
	claimKey := keyPermanodeClaim.Key(target, vr.SignerKeyId, cl.ClaimDateString(), br)
	mm.Set(claimKey, keyPermanodeClaim.Val(cl.ClaimType(), attr, value, vr.CamliSigner))
	return nil
}

func (ix *Index) populateClaim(fetcher *missTrackFetcher, b *schema.Blob, mm *mutationMap) error {
	br := b.BlobRef()

	claim, ok := b.AsClaim()
	if !ok {
		// Skip bogus claim with malformed permanode.
		return nil
	}

	vr := jsonsign.NewVerificationRequest(b.JSON(), blob.NewSerialFetcher(ix.KeyFetcher, fetcher))
	if !vr.Verify() {
		// TODO(bradfitz): ask if the vr.Err.(jsonsign.Error).IsPermanent() and retry
		// later if it's not permanent? or maybe do this up a level?
		if vr.Err != nil {
			return vr.Err
		}
		return errors.New("index: populateClaim verification failure")
	}
	verifiedKeyId := vr.SignerKeyId
	mm.Set("signerkeyid:"+vr.CamliSigner.String(), verifiedKeyId)

	if claim.ClaimType() == string(schema.DeleteClaim) {
		if err := ix.populateDeleteClaim(claim, vr, mm); err != nil {
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
func (x *Index) updateDeletesCache(deleteClaim schema.Claim) error {
	target := deleteClaim.Target()
	deleter := deleteClaim.Blob()
	when, err := deleter.ClaimDate()
	if err != nil {
		return fmt.Errorf("Could not get date of delete claim %v: %v", deleteClaim, err)
	}
	targetDeletions := append(x.deletes.m[target],
		deletion{
			deleter: deleter.BlobRef(),
			when:    when,
		})
	sort.Sort(sort.Reverse(byDeletionDate(targetDeletions)))
	x.deletes.m[target] = targetDeletions
	return nil
}
