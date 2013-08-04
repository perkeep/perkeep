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
	"sort"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/images"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types"
)

func (ix *Index) GetBlobHub() blobserver.BlobHub {
	return ix.SimpleBlobHubPartitionMap.GetBlobHub()
}

var reindexMu sync.Mutex

func (ix *Index) reindex(br blob.Ref) {
	// TODO: cap how many of these can be going at once, probably more than 1,
	// and be more efficient than just blocking goroutines. For now, this:
	reindexMu.Lock()
	defer reindexMu.Unlock()

	bs := ix.BlobSource
	if bs == nil {
		log.Printf("index: can't re-index %v: no BlobSource", br)
		return
	}
	log.Printf("index: starting re-index of %v", br)
	rc, _, err := bs.FetchStreaming(br)
	if err != nil {
		log.Printf("index: failed to fetch %v for reindexing: %v", br, err)
		return
	}
	defer rc.Close()
	sb, err := ix.ReceiveBlob(br, rc)
	if err != nil {
		log.Printf("index: reindex of %v failed: %v", br, err)
		return
	}
	log.Printf("index: successfully reindexed %v", sb)
}

func (ix *Index) ReceiveBlob(blobRef blob.Ref, source io.Reader) (retsb blob.SizedRef, err error) {
	sniffer := NewBlobSniffer(blobRef)
	hash := blobRef.Hash()
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, sniffer), source)
	if err != nil {
		return
	}

	if !blobRef.HashMatches(hash) {
		err = blobserver.ErrCorruptBlob
		return
	}
	sniffer.Parse()

	bm := ix.s.BeginBatch()

	err = ix.populateMutation(blobRef, sniffer, bm)
	if err != nil {
		return
	}

	err = ix.s.CommitBatch(bm)
	if err != nil {
		return
	}

	// TODO(bradfitz): log levels? These are generally noisy
	// (especially in tests, like search/handler_test), but I
	// could see it being useful in production. For now, disabled:
	//
	// mimeType := sniffer.MIMEType()
	// log.Printf("indexer: received %s; type=%v; truncated=%v", blobRef, mimeType, sniffer.IsTruncated())

	return blob.SizedRef{blobRef, written}, nil
}

// populateMutation populates keys & values into the provided BatchMutation.
//
// the blobref can be trusted at this point (it's been fully consumed
// and verified to match), and the sniffer has been populated.
func (ix *Index) populateMutation(br blob.Ref, sniffer *BlobSniffer, bm BatchMutation) error {
	bm.Set("have:"+br.String(), fmt.Sprintf("%d", sniffer.Size()))
	bm.Set("meta:"+br.String(), fmt.Sprintf("%d|%s", sniffer.Size(), sniffer.MIMEType()))

	if blob, ok := sniffer.SchemaBlob(); ok {
		switch blob.Type() {
		case "claim":
			if err := ix.populateClaim(blob, bm); err != nil {
				return err
			}
		case "permanode":
			//if err := mi.populatePermanode(blobRef, camli, bm); err != nil {
			//return err
			//}
		case "file":
			if err := ix.populateFile(blob, bm); err != nil {
				return err
			}
		case "directory":
			if err := ix.populateDir(blob, bm); err != nil {
				return err
			}
		}
	}
	return nil
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

// blobref: of the file or schema blob
//      blob: the parsed file schema blob
//      bm: keys to populate
func (ix *Index) populateFile(b *schema.Blob, bm BatchMutation) error {
	var times []time.Time // all creation or mod times seen; may be zero
	times = append(times, b.ModTime())

	blobRef := b.BlobRef()
	seekFetcher := blob.SeekerFromStreamingFetcher(ix.BlobSource)
	fr, err := b.NewFileReader(seekFetcher)
	if err != nil {
		// TODO(bradfitz): propagate up a transient failure
		// error type, so we can retry indexing files in the
		// future if blobs are only temporarily unavailable.
		// Basically the same as the TODO just below.
		log.Printf("index: error indexing file, creating NewFileReader %s: %v", blobRef, err)
		return nil
	}
	defer fr.Close()
	mime, reader := magic.MIMETypeFromReader(fr)

	sha1 := sha1.New()
	var copyDest io.Writer = sha1
	var imageBuf *keepFirstN // or nil
	if strings.HasPrefix(mime, "image/") {
		imageBuf = &keepFirstN{N: 256 << 10}
		copyDest = io.MultiWriter(copyDest, imageBuf)
	}
	size, err := io.Copy(copyDest, reader)
	if err != nil {
		// TODO: job scheduling system to retry this spaced
		// out max n times.  Right now our options are
		// ignoring this error (forever) or returning the
		// error and making the indexing try again (likely
		// forever failing).  Both options suck.  For now just
		// log and act like all's okay.
		log.Printf("index: error indexing file %s: %v", blobRef, err)
		return nil
	}

	if imageBuf != nil {
		if conf, err := images.DecodeConfig(bytes.NewReader(imageBuf.Bytes)); err == nil {
			bm.Set(keyImageSize.Key(blobRef), keyImageSize.Val(fmt.Sprint(conf.Width), fmt.Sprint(conf.Height)))
		}
		if ft, err := schema.FileTime(bytes.NewReader(imageBuf.Bytes)); err == nil {
			log.Printf("filename %q exif = %v, %v", b.FileName(), ft, err)
			times = append(times, ft)
		} else {
			log.Printf("filename %q exif = %v, %v", b.FileName(), ft, err)
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

	wholeRef := blob.RefFromHash(sha1)
	bm.Set(keyWholeToFileRef.Key(wholeRef, blobRef), "1")
	bm.Set(keyFileInfo.Key(blobRef), keyFileInfo.Val(size, b.FileName(), mime))
	bm.Set(keyFileTimes.Key(blobRef), keyFileTimes.Val(time3339s))
	return nil
}

// blobref: of the file or schema blob
//      ss: the parsed file schema blob
//      bm: keys to populate
func (ix *Index) populateDir(b *schema.Blob, bm BatchMutation) error {
	blobRef := b.BlobRef()
	// TODO(bradfitz): move the NewDirReader and FileName method off *schema.Blob and onto

	seekFetcher := blob.SeekerFromStreamingFetcher(ix.BlobSource)
	dr, err := b.NewDirReader(seekFetcher)
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

	bm.Set(keyFileInfo.Key(blobRef), keyFileInfo.Val(len(sts), b.FileName(), ""))
	return nil
}

func (ix *Index) populateClaim(b *schema.Blob, bm BatchMutation) error {
	br := b.BlobRef()

	claim, ok := b.AsClaim()
	if !ok {
		// Skip bogus claim with malformed permanode.
		return nil
	}

	pnbr := claim.ModifiedPermanode()
	if !pnbr.Valid() {
		// A different type of claim; not modifying a permanode.
		return nil
	}
	attr, value := claim.Attribute(), claim.Value()

	vr := jsonsign.NewVerificationRequest(b.JSON(), ix.KeyFetcher)
	if !vr.Verify() {
		// TODO(bradfitz): ask if the vr.Err.(jsonsign.Error).IsPermanent() and retry
		// later if it's not permanent? or maybe do this up a level?
		if vr.Err != nil {
			return vr.Err
		}
		return errors.New("index: populateClaim verification failure")
	}
	verifiedKeyId := vr.SignerKeyId

	bm.Set("signerkeyid:"+vr.CamliSigner.String(), verifiedKeyId)

	recentKey := keyRecentPermanode.Key(verifiedKeyId, claim.ClaimDateString(), br)
	bm.Set(recentKey, pnbr.String())

	claimKey := pipes("claim", pnbr, verifiedKeyId, claim.ClaimDateString(), br)
	bm.Set(claimKey, pipes(urle(claim.ClaimType()), urle(attr), urle(value)))

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
			bm.Set(key, val)

			key = keyPathForward.Key(verifiedKeyId, baseRef, suffix, claim.ClaimDateString(), claimRef)
			val = keyPathForward.Val(active, targetRef)
			bm.Set(key, val)
		}
	}

	if search.IsIndexedAttribute(attr) {
		key := keySignerAttrValue.Key(verifiedKeyId, attr, value, claim.ClaimDateString(), br)
		bm.Set(key, keySignerAttrValue.Val(pnbr))
	}

	if search.IsBlobReferenceAttribute(attr) {
		targetRef, ok := blob.Parse(value)
		if ok {
			key := keyEdgeBackward.Key(targetRef, pnbr, br)
			bm.Set(key, keyEdgeBackward.Val("permanode", ""))
		}
	}

	return nil
}

// pipes returns args separated by pipes
func pipes(args ...interface{}) string {
	var buf bytes.Buffer
	for n, arg := range args {
		if n > 0 {
			buf.WriteString("|")
		}
		if s, ok := arg.(string); ok {
			buf.WriteString(s)
		} else {
			buf.WriteString(arg.(fmt.Stringer).String())
		}
	}
	return buf.String()
}
