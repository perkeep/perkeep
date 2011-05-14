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

package main

import (
	"bytes"
	"crypto"
	"crypto/openpgp"
	"crypto/openpgp/armor"
	"fmt"
	"http"
	"log"
	"os"
	"path/filepath"
	"strings"

	"camli/blobref"
	"camli/blobserver/handlers"
	"camli/httputil"
	"camli/jsonconfig"
	"camli/jsonsign"
)

var _ = log.Printf

const kMaxJsonLength = 1024 * 1024

type JSONSignHandler struct {
	// Optional path to non-standard secret gpg keyring file
	keyRing, secretRing string

	// Required keyId, either a short form ("26F5ABDA") or one
	// of the longer forms.
	keyId string

	pubKeyBlobRef *blobref.BlobRef
	pubKeyFetcher blobref.StreamingFetcher

	pubKeyBlobRefServeSuffix string // "camli/sha1-xxxx"
	pubKeyHandler            http.Handler

	entity *openpgp.Entity
}

func (h *JSONSignHandler) secretRingPath() string {
	if h.secretRing != "" {
		return h.secretRing
	}
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "secring.gog")
}

func (hl *handlerLoader) createJSONSignHandler(conf jsonconfig.Obj) (http.Handler, os.Error) {
	h := &JSONSignHandler{
		keyId:      strings.ToUpper(conf.RequiredString("keyId")),
		keyRing:    conf.OptionalString("keyRing", ""),
		secretRing: conf.OptionalString("secretRing", ""),
	}
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	secring, err := os.Open(h.secretRingPath())
	if err != nil {
		return nil, fmt.Errorf("secretRing file: %v", err)
	}
	defer secring.Close()
	el, err := openpgp.ReadKeyRing(secring)
	if err != nil {
		return nil, fmt.Errorf("openpgp.ReadKeyRing of %q: %v", h.secretRingPath(), err)
	}
	for _, e := range el {
		pk := e.PrivateKey
		if pk == nil || (pk.KeyIdString() != h.keyId && pk.KeyIdShortString() != h.keyId) {
			continue
		}
		h.entity = e
	}
	if h.entity == nil {
		return nil, fmt.Errorf("didn't find a key in %q for keyId %q", h.secretRingPath(), h.keyId)
	}
	if h.entity.PrivateKey.Encrypted {
		// TODO: support decrypting this
		return nil, fmt.Errorf("Encrypted keys aren't yet supported")
	}

	var buf bytes.Buffer
	wc, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	h.entity.PrivateKey.PublicKey.Serialize(wc)
	wc.Close()

	armoredPublicKey := buf.String()
	ms := new(blobref.MemoryStore)
	h.pubKeyBlobRef, err = ms.AddBlob(crypto.SHA1, armoredPublicKey)
	if err != nil {
		return nil, err
	}
	h.pubKeyFetcher = ms

	h.pubKeyBlobRefServeSuffix = "camli/" + h.pubKeyBlobRef.String()
	h.pubKeyHandler = &handlers.GetHandler{
		Fetcher:           ms,
		AllowGlobalAccess: true, // just public keys
	}

	return h, nil
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
				"publicKeyId":   h.keyId,
				"signHandler":   base + "camli/sig/sign",
				"verifyHandler": base + "camli/sig/verify",
			}
			if h.pubKeyBlobRef != nil {
				m["publicKeyBlobRef"] = h.pubKeyBlobRef.String()
				m["publicKey"] = base + h.pubKeyBlobRefServeSuffix
			}
			httputil.ReturnJson(rw, m)
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
		errStr := vreq.Err.String()
		m["signatureValid"] = 0
		m["errorMessage"] = errStr
	}

	rw.WriteHeader(http.StatusOK)  // no HTTP response code fun, error info in JSON
	httputil.ReturnJson(rw, m)
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
	if len(jsonStr) > kMaxJsonLength {
		badReq("parameter \"json\" too large")
		return
	}

	sreq := &jsonsign.SignRequest{
		UnsignedJson:      jsonStr,
		Fetcher:           h.pubKeyFetcher,
		ServerMode:        true,
		SecretKeyringPath: h.secretRing,
		KeyringPath: h.keyRing,
	}
	signedJson, err := sreq.Sign()
	if err != nil {
		// TODO: some aren't really a "bad request"
		badReq(fmt.Sprintf("%v", err))
		return
	}
	rw.Write([]byte(signedJson))
}
