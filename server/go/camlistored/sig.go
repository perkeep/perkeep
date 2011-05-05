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
	"camli/httputil"
	"camli/jsonconfig"
	"camli/jsonsign"
)

var _ = log.Printf

const kMaxJsonLength = 1024 * 1024

type JSONSignHandler struct {
	// Optional path to non-standard secret gpg keyring file
	secretRing string

	// Required keyId, either a short form ("26F5ABDA") or one
	// of the longer forms.
	keyId string

	pubKeyBlobRef *blobref.BlobRef
	pubKeyFetcher blobref.StreamingFetcher

	entity *openpgp.Entity
}

func (h *JSONSignHandler) secretRingPath() string {
	if h.secretRing != "" {
		return h.secretRing
	}
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "secring.gog")
}

func createJSONSignHandler(conf jsonconfig.Obj) (http.Handler, os.Error) {
	h := &JSONSignHandler{
		keyId:      strings.ToUpper(conf.RequiredString("keyId")),
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
	log.Printf("got key: %s", buf.String())

	ms := new(blobref.MemoryStore)
	h.pubKeyBlobRef, err = ms.AddBlob(crypto.SHA1, buf.String())
	if err != nil {
		return nil, err
	}
	h.pubKeyFetcher = ms

	return h, nil
}

func (h *JSONSignHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "GET":
		if strings.HasSuffix(req.URL.Path, "/camli/sig/discovery") {
			m := map[string]interface{}{}
			if h.pubKeyBlobRef != nil {
				m["publicKeyBlobRef"] = h.pubKeyBlobRef.String()
			}
			httputil.ReturnJson(rw, m)
			return
		}
	case "POST":
		switch {
		case strings.HasSuffix(req.URL.Path, "/camli/sig/sign"):
			h.handleSign(rw, req)
			return
		case strings.HasSuffix(req.URL.Path, "/camli/sig/verify"):
			h.handleVerify(rw, req)
			return
		}
	}
	http.Error(rw, "Unsupported path or method.", http.StatusBadRequest)
}

func (h *JSONSignHandler) handleVerify(rw http.ResponseWriter, req *http.Request) {
	http.Error(rw, "TODO: finish moving this code over from camsigd", http.StatusBadRequest)
}

func (h *JSONSignHandler) handleSign(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	jsonStr := req.FormValue("json")
	if jsonStr == "" {
		http.Error(rw, "Missing json parameter", http.StatusBadRequest)
		return
	}
	if len(jsonStr) > kMaxJsonLength {
		http.Error(rw, "json parameter too large", http.StatusBadRequest)
		return
	}

	sreq := &jsonsign.SignRequest{UnsignedJson: jsonStr, Fetcher: h.pubKeyFetcher}
	signedJson, err := sreq.Sign()
	if err != nil {
		// TODO: some aren't really a "bad request"
		http.Error(rw, fmt.Sprintf("%v", err), http.StatusBadRequest)
		return
	}
	rw.Write([]byte(signedJson))
}
