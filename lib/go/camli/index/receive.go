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
	"fmt"
	"io"
	"log"
	"os"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonsign"
	"camli/schema"
	"camli/search"
)

func (ix *Index) GetBlobHub() blobserver.BlobHub {
	return ix.SimpleBlobHubPartitionMap.GetBlobHub()
}

func (ix *Index) ReceiveBlob(blobRef *blobref.BlobRef, source io.Reader) (retsb blobref.SizedBlobRef, err os.Error) {
	sniffer := new(BlobSniffer)
	hash := blobRef.Hash()
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, sniffer), source)
	log.Printf("indexer: hashed+sniffed %d bytes; err %v", written, err)
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

	mimeType := sniffer.MimeType()
	log.Printf("indexer: type=%v; truncated=%v", mimeType, sniffer.IsTruncated())

	return blobref.SizedBlobRef{blobRef, written}, nil
}

// populateMutation populates keys & values into the provided BatchMutation.
//
// the blobref can be trusted at this point (it's been fully consumed
// and verified to match), and the sniffer has been populated.
func (ix *Index) populateMutation(br *blobref.BlobRef, sniffer *BlobSniffer, bm BatchMutation) os.Error {
	bm.Set("have:"+br.String(), fmt.Sprintf("%d", sniffer.Size()))
	bm.Set("meta:"+br.String(), fmt.Sprintf("%d|%s", sniffer.Size(), sniffer.MimeType()))

	if camli, ok := sniffer.Superset(); ok {
		switch camli.Type {
		case "claim":
			if err := ix.populateClaim(br, camli, sniffer, bm); err != nil {
				return err
			}
		case "permanode":
			//if err := mi.populatePermanode(blobRef, camli, bm); err != nil {
			//return err
			//}
		case "file":
			// if err := mi.populateFile(blobRef, camli, bm); err != nil {
			//return err
			//}
		}
	}
	return nil
}

func (ix *Index) populateClaim(br *blobref.BlobRef, ss *schema.Superset, sniffer *BlobSniffer, bm BatchMutation) os.Error {
	pnbr := blobref.Parse(ss.Permanode)
	if pnbr == nil {
		// Skip bogus claim with malformed permanode.
		return nil
	}

	rawJson, err := sniffer.Body()
	if err != nil {
		return err
	}

	vr := jsonsign.NewVerificationRequest(rawJson, ix.KeyFetcher)
	if !vr.Verify() {
		// TODO(bradfitz): ask if the vr.Err.(jsonsign.Error).IsPermanent() and retry
		// later if it's not permanent? or maybe do this up a level?
		if vr.Err != nil {
			return vr.Err
		}
		return os.NewError("index: populateClaim verification failure")
	}
	verifiedKeyId := vr.SignerKeyId
	log.Printf("index: verified claim %s from %s", br, verifiedKeyId)

	bm.Set("signerkeyid:"+vr.CamliSigner.String(), verifiedKeyId)

	recentKey := keyRecentPermanode.Key(verifiedKeyId, ss.ClaimDate, br)
	bm.Set(recentKey, pnbr.String())

	claimKey := pipes("claim", pnbr, verifiedKeyId, ss.ClaimDate, br)
	bm.Set(claimKey, pipes(urle(ss.ClaimType), urle(ss.Attribute), urle(ss.Value)))

	if search.IsIndexedAttribute(ss.Attribute) {
		savKey := pipes("signerattrvalue",
			verifiedKeyId, urle(ss.Attribute), urle(ss.Value),
			reverseTimeString(ss.ClaimDate), br)
		bm.Set(savKey, pnbr.String())
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
