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

package server

import (
	"crypto"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/handlers"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/schema"

	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp"
)

var _ = log.Printf

const kMaxJSONLength = 1024 * 1024

type JSONSignHandler struct {
	// Optional path to non-standard secret gpg keyring file
	secretRing string

	pubKeyBlobRef *blobref.BlobRef
	pubKeyFetcher blobref.StreamingFetcher

	pubKeyBlobRefServeSuffix string // "camli/sha1-xxxx"
	pubKeyHandler            http.Handler

	// Where & if our public key is published
	pubKeyDest    blobserver.Storage
	pubKeyWritten bool

	entity *openpgp.Entity
}

func (h *JSONSignHandler) secretRingPath() string {
	if h.secretRing != "" {
		return h.secretRing
	}
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "secring.gpg")
}

func init() {
	blobserver.RegisterHandlerConstructor("jsonsign", newJSONSignFromConfig)
}

func newJSONSignFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	pubKeyDestPrefix := conf.OptionalString("publicKeyDest", "")

	// either a short form ("26F5ABDA") or one the longer forms.
	keyId := conf.RequiredString("keyId")

	h := &JSONSignHandler{
		secretRing: conf.OptionalString("secretRing", ""),
	}
	var err error
	if err = conf.Validate(); err != nil {
		return nil, err
	}

	h.entity, err = jsonsign.EntityFromSecring(keyId, h.secretRingPath())
	if err != nil {
		return nil, err
	}

	armoredPublicKey, err := jsonsign.ArmoredPublicKey(h.entity)

	ms := new(blobref.MemoryStore)
	h.pubKeyBlobRef, err = ms.AddBlob(crypto.SHA1, armoredPublicKey)
	if err != nil {
		return nil, err
	}
	h.pubKeyFetcher = ms

	if pubKeyDestPrefix != "" {
		sto, err := ld.GetStorage(pubKeyDestPrefix)
		if err != nil {
			return nil, err
		}
		h.pubKeyDest = sto
		if sto != nil {
			if ctxReq, ok := ld.GetRequestContext(); ok {
				if w, ok := sto.(blobserver.ContextWrapper); ok {
					sto = w.WrapContext(ctxReq)
				}
			}
			err := h.uploadPublicKey(sto, armoredPublicKey)
			if err != nil {
				return nil, fmt.Errorf("Error seeding self public key in storage: %v", err)
			}
		}
	}
	h.pubKeyBlobRefServeSuffix = "camli/" + h.pubKeyBlobRef.String()
	h.pubKeyHandler = &handlers.GetHandler{
		Fetcher:           ms,
		AllowGlobalAccess: true, // just public keys
	}

	return h, nil
}

func (h *JSONSignHandler) uploadPublicKey(sto blobserver.Storage, key string) error {
	_, err := blobserver.StatBlob(sto, h.pubKeyBlobRef)
	if err == nil {
		return nil
	}
	_, err = sto.ReceiveBlob(h.pubKeyBlobRef, strings.NewReader(key))
	return err
}

func (h *JSONSignHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	base := req.Header.Get("X-PrefixHandler-PathBase")
	subPath := req.Header.Get("X-PrefixHandler-PathSuffix")
	switch req.Method {
	case "GET":
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
			http.Error(rw, "POST required", 400)
			return
		case "camli/sig/discovery":
			m := map[string]interface{}{
				"publicKeyId":   h.entity.PrimaryKey.KeyIdString(),
				"signHandler":   base + "camli/sig/sign",
				"verifyHandler": base + "camli/sig/verify",
			}
			if h.pubKeyBlobRef != nil {
				m["publicKeyBlobRef"] = h.pubKeyBlobRef.String()
				m["publicKey"] = base + h.pubKeyBlobRefServeSuffix
			}
			httputil.ReturnJSON(rw, m)
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

func (h *JSONSignHandler) handleVerify(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	sjson := req.FormValue("sjson")
	if sjson == "" {
		http.Error(rw, "missing \"sjson\" parameter", http.StatusBadRequest)
		return
	}

	m := make(map[string]interface{})

	// TODO: use a different fetcher here that checks memory, disk,
	// the internet, etc.
	fetcher := h.pubKeyFetcher

	vreq := jsonsign.NewVerificationRequest(sjson, fetcher)
	if vreq.Verify() {
		m["signatureValid"] = 1
		m["signerKeyId"] = vreq.SignerKeyId
		m["verifiedData"] = vreq.PayloadMap
	} else {
		errStr := vreq.Err.Error()
		m["signatureValid"] = 0
		m["errorMessage"] = errStr
	}

	rw.WriteHeader(http.StatusOK) // no HTTP response code fun, error info in JSON
	httputil.ReturnJSON(rw, m)
}

func (h *JSONSignHandler) handleSign(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	badReq := func(s string) {
		http.Error(rw, s, http.StatusBadRequest)
		log.Printf("bad request: %s", s)
		return
	}
	// TODO: SECURITY: auth

	jsonStr := req.FormValue("json")
	if jsonStr == "" {
		badReq("missing \"json\" parameter")
		return
	}
	if len(jsonStr) > kMaxJSONLength {
		badReq("parameter \"json\" too large")
		return
	}

	sreq := &jsonsign.SignRequest{
		UnsignedJSON:      jsonStr,
		Fetcher:           h.pubKeyFetcher,
		ServerMode:        true,
		SecretKeyringPath: h.secretRing,
	}
	signedJSON, err := sreq.Sign()
	if err != nil {
		// TODO: some aren't really a "bad request"
		badReq(fmt.Sprintf("%v", err))
		return
	}
	rw.Write([]byte(signedJSON))
}

func (h *JSONSignHandler) SignMap(m schema.Map) (string, error) {
	m["camliSigner"] = h.pubKeyBlobRef.String()
	unsigned, err := m.JSON()
	if err != nil {
		return "", err
	}
	sreq := &jsonsign.SignRequest{
		UnsignedJSON:      unsigned,
		Fetcher:           h.pubKeyFetcher,
		ServerMode:        true,
		SecretKeyringPath: h.secretRing,
	}
	return sreq.Sign()
}
