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

package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path"
	"strings"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/jsonsign/signhandler"
	"perkeep.org/pkg/schema"

	"go4.org/readerutil"
)

// CreateBatchUploadHandler returns the handler that receives multi-part form uploads
// to upload many blobs at once. See doc/protocol/blob-upload-protocol.txt.
func CreateBatchUploadHandler(storage blobserver.BlobReceiver, config *blobserver.Config) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		handleMultiPartUpload(rw, req, storage, config)
	})
}

// CreatePutUploadHandler returns the handler that receives a single
// blob at the blob's final URL, via the PUT method.  See
// doc/protocol/blob-upload-protocol.txt.
func CreatePutUploadHandler(storage blobserver.BlobReceiver) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		if req.Method != "PUT" {
			log.Printf("Inconfigured upload handler.")
			httputil.BadRequestError(rw, "Inconfigured handler.")
			return
		}
		// For non-chunked uploads, we catch it here. For chunked uploads, it's caught
		// by blobserver.Receive's LimitReader.
		if req.ContentLength > blobserver.MaxBlobSize {
			httputil.BadRequestError(rw, "blob too big")
			return
		}
		blobrefStr := path.Base(req.URL.Path)
		br, ok := blob.Parse(blobrefStr)
		if !ok {
			log.Printf("Invalid PUT request to %q", req.URL.Path)
			httputil.BadRequestError(rw, "Bad path")
			return
		}
		if !br.IsSupported() {
			httputil.BadRequestError(rw, "unsupported object hash function")
			return
		}
		_, err := blobserver.Receive(ctx, storage, br, req.Body)
		if err == blobserver.ErrCorruptBlob {
			httputil.BadRequestError(rw, "data doesn't match declared digest")
			return
		}
		if err != nil {
			httputil.ServeError(rw, req, err)
			return
		}
		rw.WriteHeader(http.StatusNoContent)
	})
}

// vivify verifies that all the chunks for the file described by fileblob are on the blobserver.
// It makes a planned permanode, signs it, and uploads it. It finally makes a camliContent claim
// on that permanode for fileblob, signs it, and uploads it to the blobserver.
func vivify(ctx context.Context, blobReceiver blobserver.BlobReceiver, config *blobserver.Config, fileblob blob.SizedRef) error {
	sf, ok := blobReceiver.(blob.Fetcher)
	if !ok {
		return fmt.Errorf("BlobReceiver is not a Fetcher")
	}
	fr, err := schema.NewFileReader(ctx, sf, fileblob.Ref)
	if err != nil {
		return fmt.Errorf("Filereader error for blobref %v: %v", fileblob.Ref.String(), err)
	}
	defer fr.Close()

	h := blob.NewHash()
	n, err := io.Copy(h, fr)
	if err != nil {
		return fmt.Errorf("Could not read all file of blobref %v: %v", fileblob.Ref.String(), err)
	}
	if n != fr.Size() {
		return fmt.Errorf("Could not read all file of blobref %v. Wanted %v, got %v", fileblob.Ref.String(), fr.Size(), n)
	}

	hf := config.HandlerFinder
	if hf == nil {
		return errors.New("blobReceiver config has no HandlerFinder")
	}
	JSONSignRoot, sh, err := hf.FindHandlerByType("jsonsign")
	if err != nil || sh == nil {
		return errors.New("jsonsign handler not found")
	}
	sigHelper, ok := sh.(*signhandler.Handler)
	if !ok {
		return errors.New("handler is not a JSON signhandler")
	}
	publicKeyBlobRef := sigHelper.Discovery(JSONSignRoot).PublicKeyBlobRef
	if !publicKeyBlobRef.Valid() {
		return fmt.Errorf("invalid publicKeyBlobRef %v in sign discovery", publicKeyBlobRef)
	}

	// The file schema must have a modtime to vivify, as the modtime is used for all three of:
	// 1) the permanode's signature
	// 2) the camliContent attribute claim's "claimDate"
	// 3) the signature time of 2)
	claimDate := fr.UnixMtime()
	if claimDate.IsZero() {
		return fmt.Errorf("While parsing modtime for file %v: %v", fr.FileName(), err)
	}

	permanodeBB := schema.NewHashPlannedPermanode(h)
	permanodeBB.SetSigner(publicKeyBlobRef)
	permanodeBB.SetClaimDate(claimDate)
	permanodeSigned, err := sigHelper.Sign(ctx, permanodeBB)
	if err != nil {
		return fmt.Errorf("signing permanode %v: %v", permanodeSigned, err)
	}
	permanodeRef := blob.RefFromString(permanodeSigned)
	_, err = blobserver.ReceiveNoHash(ctx, blobReceiver, permanodeRef, strings.NewReader(permanodeSigned))
	if err != nil {
		return fmt.Errorf("while uploading signed permanode %v, %v: %v", permanodeRef, permanodeSigned, err)
	}

	contentClaimBB := schema.NewSetAttributeClaim(permanodeRef, "camliContent", fileblob.Ref.String())
	contentClaimBB.SetSigner(publicKeyBlobRef)
	contentClaimBB.SetClaimDate(claimDate)
	contentClaimSigned, err := sigHelper.Sign(ctx, contentClaimBB)
	if err != nil {
		return fmt.Errorf("signing camliContent claim: %v", err)
	}
	contentClaimRef := blob.RefFromString(contentClaimSigned)
	_, err = blobserver.ReceiveNoHash(ctx, blobReceiver, contentClaimRef, strings.NewReader(contentClaimSigned))
	if err != nil {
		return fmt.Errorf("while uploading signed camliContent claim %v, %v: %v", contentClaimRef, contentClaimSigned, err)
	}
	return nil
}

func handleMultiPartUpload(rw http.ResponseWriter, req *http.Request, blobReceiver blobserver.BlobReceiver, config *blobserver.Config) {
	ctx := req.Context()
	res := new(protocol.UploadResponse)

	if !(req.Method == "POST" && strings.Contains(req.URL.Path, "/camli/upload")) {
		log.Printf("Inconfigured handler upload handler")
		httputil.BadRequestError(rw, "Inconfigured handler.")
		return
	}

	receivedBlobs := make([]blob.SizedRef, 0, 10)

	multipart, err := req.MultipartReader()
	if multipart == nil {
		httputil.BadRequestError(rw, fmt.Sprintf(
			"Expected multipart/form-data POST request; %v", err))
		return
	}

	var errBuf bytes.Buffer
	addError := func(s string) {
		log.Printf("Client error: %s", s)
		if errBuf.Len() > 0 {
			errBuf.WriteByte('\n')
		}
		errBuf.WriteString(s)
	}

	for {
		mimePart, err := multipart.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			addError(fmt.Sprintf("Error reading multipart section: %v", err))
			break
		}

		contentDisposition, params, err := mime.ParseMediaType(mimePart.Header.Get("Content-Disposition"))
		if err != nil {
			addError("invalid Content-Disposition")
			break
		}

		if contentDisposition != "form-data" {
			addError(fmt.Sprintf("Expected Content-Disposition of \"form-data\"; got %q", contentDisposition))
			break
		}

		formName := params["name"]
		ref, ok := blob.Parse(formName)
		if !ok {
			addError(fmt.Sprintf("Ignoring form key %q", formName))
			continue
		}

		var tooBig int64 = blobserver.MaxBlobSize + 1
		var readBytes int64
		blobGot, err := blobserver.Receive(ctx, blobReceiver.(blobserver.BlobReceiver), ref, &readerutil.CountingReader{
			Reader: io.LimitReader(mimePart, tooBig),
			N:      &readBytes,
		})
		if readBytes == tooBig {
			err = fmt.Errorf("blob over the limit of %d bytes", blobserver.MaxBlobSize)
		}
		if err != nil {
			addError(fmt.Sprintf("Error receiving blob %v: %v\n", ref, err))
			break
		}
		log.Printf("Received blob %v\n", blobGot)
		receivedBlobs = append(receivedBlobs, blobGot)
	}

	res.Received = receivedBlobs

	if req.Header.Get("X-Camlistore-Vivify") == "1" {
		// TODO(mpl): In practice, this only works because we upload blobs one by one.
		// If we sent many blobs in one multipart, the code below means
		// all of them would have to be file schema blobs, which is a very
		// particular case. Shouldn't we fix that?
		// Or I suppose we could document that a file schema blob that
		// wants to be vivified should always be sent alone.
		for _, got := range receivedBlobs {
			err := vivify(ctx, blobReceiver, config, got)
			if err != nil {
				addError(fmt.Sprintf("Error vivifying blob %v: %v\n", got.Ref.String(), err))
			} else {
				rw.Header().Add("X-Camlistore-Vivified", got.Ref.String())
			}
		}
	}

	res.ErrorText = errBuf.String()

	httputil.ReturnJSON(rw, res)
}
