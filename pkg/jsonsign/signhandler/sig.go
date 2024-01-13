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

// Package signhandler implements the HTTP interface to signing and verifying
// Perkeep JSON blobs.
package signhandler // import "perkeep.org/pkg/jsonsign/signhandler"

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/gethandler"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"

	"go4.org/jsonconfig"
	"golang.org/x/crypto/openpgp"
)

const maxJSONLength = 1024 * 1024

// Handler implements JSON signing, verification, and discovery.
//
// See https://perkeep.org/doc/protocol/jsonsign
type Handler struct {
	// Optional path to non-standard secret gpg keyring file
	secretRing string

	pubKey        string // armored
	pubKeyBlobRef blob.Ref
	pubKeyFetcher blob.Fetcher

	pubKeyBlobRefServeSuffix string // "camli/sha1-xxxx"
	pubKeyHandler            http.Handler

	pubKeyDest blobserver.Storage // Where our public key is published

	pubKeyUploadMu sync.RWMutex
	pubKeyUploaded bool

	entity *openpgp.Entity
	signer *schema.Signer
}

func (h *Handler) Signer() *schema.Signer { return h.signer }

func (h *Handler) secretRingPath() string {
	if h.secretRing != "" {
		return h.secretRing
	}
	return osutil.SecretRingFile()
}

func init() {
	blobserver.RegisterHandlerConstructor("jsonsign", newJSONSignFromConfig)
}

func newJSONSignFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	var (
		// either a short form ("26F5ABDA") or one the longer forms.
		keyId = conf.RequiredString("keyId")

		pubKeyDestPrefix = conf.OptionalString("publicKeyDest", "")
		secretRing       = conf.OptionalString("secretRing", "")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	h := &Handler{
		secretRing: secretRing,
	}

	var err error
	h.entity, err = jsonsign.EntityFromSecring(keyId, h.secretRingPath())
	if err != nil {
		return nil, err
	}

	h.pubKey, err = jsonsign.ArmoredPublicKey(h.entity)
	if err != nil {
		return nil, err
	}

	ctx := context.Background() // TODO: 15 second or global-configurable start-up limit?

	ms := &memory.Storage{}
	h.pubKeyBlobRef = blob.RefFromString(h.pubKey)
	if _, err := ms.ReceiveBlob(ctx, h.pubKeyBlobRef, strings.NewReader(h.pubKey)); err != nil {
		return nil, fmt.Errorf("could not store pub key blob: %v", err)
	}
	h.pubKeyFetcher = ms

	if pubKeyDestPrefix != "" {
		sto, err := ld.GetStorage(pubKeyDestPrefix)
		if err != nil {
			return nil, err
		}
		h.pubKeyDest = sto
	}
	h.pubKeyBlobRefServeSuffix = "camli/" + h.pubKeyBlobRef.String()
	h.pubKeyHandler = &gethandler.Handler{
		Fetcher: ms,
	}

	h.signer, err = schema.NewSigner(h.pubKeyBlobRef, strings.NewReader(h.pubKey), h.entity)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// UploadPublicKey writes the public key to the destination blobserver
// defined for the handler, if needed.
func (h *Handler) UploadPublicKey(ctx context.Context) error {
	h.pubKeyUploadMu.RLock()
	if h.pubKeyUploaded {
		h.pubKeyUploadMu.RUnlock()
		return nil
	}
	h.pubKeyUploadMu.RUnlock()

	sto := h.pubKeyDest

	h.pubKeyUploadMu.Lock()
	defer h.pubKeyUploadMu.Unlock()
	if h.pubKeyUploaded {
		return nil
	}
	_, err := blobserver.StatBlob(ctx, sto, h.pubKeyBlobRef)
	if err == nil {
		h.pubKeyUploaded = true
		return nil
	}
	_, err = blobserver.Receive(ctx, sto, h.pubKeyBlobRef, strings.NewReader(h.pubKey))
	h.pubKeyUploaded = (err == nil)
	return err
}

// Discovery returns the Discovery response for the signing handler.
func (h *Handler) Discovery(base string) *camtypes.SignDiscovery {
	sd := &camtypes.SignDiscovery{
		PublicKeyID:   h.entity.PrimaryKey.KeyIdString(),
		SignHandler:   base + "camli/sig/sign",
		VerifyHandler: base + "camli/sig/verify",
	}
	if h.pubKeyBlobRef.Valid() {
		sd.PublicKeyBlobRef = h.pubKeyBlobRef
		sd.PublicKey = base + h.pubKeyBlobRefServeSuffix
	}
	return sd
}

func (h *Handler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	base := httputil.PathBase(req)
	subPath := httputil.PathSuffix(req)
	switch req.Method {
	case "GET", "HEAD":
		switch subPath {
		case "":
			http.Redirect(rw, req, base+"camli/sig/discovery", http.StatusFound)
			return
		case h.pubKeyBlobRefServeSuffix:
			h.pubKeyHandler.ServeHTTP(rw, req)
			return
		case "camli/sig/sign":
			fallthrough
		case "camli/sig/verify":
			http.Error(rw, "POST required", http.StatusBadRequest)
			return
		case "camli/sig/discovery":
			httputil.ReturnJSON(rw, h.Discovery(base))
			return
		}
	case "POST":
		switch subPath {
		case "camli/sig/sign":
			h.handleSign(rw, req)
			return
		case "camli/sig/verify":
			h.handleVerify(rw, req)
			return
		}
	}
	http.Error(rw, "Unsupported path or method.", http.StatusBadRequest)
}

func (h *Handler) handleVerify(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	sjson := req.FormValue("sjson")
	if sjson == "" {
		http.Error(rw, "missing \"sjson\" parameter", http.StatusBadRequest)
		return
	}

	// TODO: use a different fetcher here that checks memory, disk,
	// the internet, etc.
	fetcher := h.pubKeyFetcher

	var res camtypes.VerifyResponse
	vreq := jsonsign.NewVerificationRequest(sjson, fetcher)
	_, err := vreq.Verify(req.Context())
	if err != nil {
		res.SignatureValid = false
		res.ErrorMessage = err.Error()
	} else {
		res.SignatureValid = true
		res.SignerKeyId = vreq.SignerKeyId
		res.VerifiedData = vreq.PayloadMap
	}

	rw.WriteHeader(http.StatusOK) // no HTTP response code fun, error info in JSON
	httputil.ReturnJSON(rw, &res)
}

func (h *Handler) handleSign(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	badReq := func(s string) {
		http.Error(rw, s, http.StatusBadRequest)
		log.Printf("bad request: %s", s)
	}

	jsonStr := req.FormValue("json")
	if jsonStr == "" {
		badReq("missing \"json\" parameter")
		return
	}
	if len(jsonStr) > maxJSONLength {
		badReq("parameter \"json\" too large")
		return
	}

	sreq := &jsonsign.SignRequest{
		UnsignedJSON:      jsonStr,
		Fetcher:           h.pubKeyFetcher,
		ServerMode:        true,
		SecretKeyringPath: h.secretRing,
	}
	ctx := req.Context() // TODO: restrict time to 30 seconds?
	signedJSON, err := sreq.Sign(ctx)
	if err != nil {
		// TODO: some aren't really a "bad request"
		badReq(fmt.Sprintf("%v", err))
		return
	}
	if err := h.UploadPublicKey(ctx); err != nil {
		log.Printf("signing handler failed to upload public key: %v", err)
	}
	rw.Write([]byte(signedJSON))
}

func (h *Handler) Sign(ctx context.Context, bb *schema.Builder) (string, error) {
	bb.SetSigner(h.pubKeyBlobRef)
	unsigned, err := bb.JSON()
	if err != nil {
		return "", err
	}
	sreq := &jsonsign.SignRequest{
		UnsignedJSON:      unsigned,
		Fetcher:           h.pubKeyFetcher,
		ServerMode:        true,
		SecretKeyringPath: h.secretRing,
	}
	claimTime, err := bb.Blob().ClaimDate()
	if err != nil {
		if !schema.IsMissingField(err) {
			return "", err
		}
	} else {
		sreq.SignatureTime = claimTime
	}
	if err := h.UploadPublicKey(ctx); err != nil {
		log.Printf("signing handler failed to upload public key: %v", err)
	}
	return sreq.Sign(ctx)
}
