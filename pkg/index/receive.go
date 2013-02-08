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
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/ioutil"
	"log"
	"strings"
	"sync"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
)

func (ix *Index) GetBlobHub() blobserver.BlobHub {
	return ix.SimpleBlobHubPartitionMap.GetBlobHub()
}

var reindexMu sync.Mutex

func (ix *Index) reindex(br *blobref.BlobRef) {
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

func (ix *Index) ReceiveBlob(blobRef *blobref.BlobRef, source io.Reader) (retsb blobref.SizedBlobRef, err error) {
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

	return blobref.SizedBlobRef{blobRef, written}, nil
}

// populateMutation populates keys & values into the provided BatchMutation.
//
// the blobref can be trusted at this point (it's been fully consumed
// and verified to match), and the sniffer has been populated.
func (ix *Index) populateMutation(br *blobref.BlobRef, sniffer *BlobSniffer, bm BatchMutation) error {
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

// blobref: of the file or schema blob
//      ss: the parsed file schema blob
//      bm: keys to populate
func (ix *Index) populateFile(blob *schema.Blob, bm BatchMutation) error {
	// TODO: move the NewFileReader off of blob.

	blobRef := blob.BlobRef()
	seekFetcher := blobref.SeekerFromStreamingFetcher(ix.BlobSource)
	fr, err := blob.NewFileReader(seekFetcher)
	if err != nil {
		// TODO(bradfitz): propagate up a transient failure
		// error type, so we can retry indexing files in the
		// future if blobs are only temporarily unavailable.
		// Basically the same as the TODO just below.
		log.Printf("index: error indexing file, creating NewFileReader %s: %v", blobRef, err)
		return nil
	}
	defer fr.Close()
	mime, reader := magic.MimeTypeFromReader(fr)

	sha1 := sha1.New()
	var copyDest io.Writer = sha1
	var withCopyErr func(error) // or nil
	if strings.HasPrefix(mime, "image/") {
		pr, pw := io.Pipe()
		copyDest = io.MultiWriter(copyDest, pw)
		confc := make(chan *image.Config, 1)
		go func() {
			conf, _, err := image.DecodeConfig(pr)
			defer io.Copy(ioutil.Discard, pr)
			if err == nil {
				confc <- &conf
			} else {
				confc <- nil
			}
		}()
		withCopyErr = func(err error) {
			pw.CloseWithError(err)
			if conf := <-confc; conf != nil {
				bm.Set(keyImageSize.Key(blobRef), keyImageSize.Val(fmt.Sprint(conf.Width), fmt.Sprint(conf.Height)))
			}
		}
	}

	size, err := io.Copy(copyDest, reader)
	if f := withCopyErr; f != nil {
		f(err)
	}
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

	wholeRef := blobref.FromHash(sha1)
	bm.Set(keyWholeToFileRef.Key(wholeRef, blobRef), "1")
	bm.Set(keyFileInfo.Key(blobRef), keyFileInfo.Val(size, blob.FileName(), mime))
	return nil
}

// blobref: of the file or schema blob
//      ss: the parsed file schema blob
//      bm: keys to populate
func (ix *Index) populateDir(blob *schema.Blob, bm BatchMutation) error {
	blobRef := blob.BlobRef()
	// TODO(bradfitz): move the NewDirReader and FileName method off *schema.Blob and onto 

	seekFetcher := blobref.SeekerFromStreamingFetcher(ix.BlobSource)
	dr, err := blob.NewDirReader(seekFetcher)
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

	bm.Set(keyFileInfo.Key(blobRef), keyFileInfo.Val(len(sts), blob.FileName(), ""))
	return nil
}

func (ix *Index) populateClaim(blob *schema.Blob, bm BatchMutation) error {
	br := blob.BlobRef()

	claim, ok := blob.AsClaim()
	if !ok {
		// Skip bogus claim with malformed permanode.
		return nil
	}

	pnbr := claim.ModifiedPermanode()
	if pnbr == nil {
		// A different type of claim; not modifying a permanode.
		return nil
	}
	attr, value := claim.Attribute(), claim.Value()

	vr := jsonsign.NewVerificationRequest(blob.JSON(), ix.KeyFetcher)
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
		targetRef := blobref.Parse(value)
		if targetRef != nil {
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
		targetRef := blobref.Parse(value)
		if targetRef != nil {
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
