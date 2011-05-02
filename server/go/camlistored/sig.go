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
	"fmt"
	"http"
	"os"
	"path/filepath"
	"crypto/openpgp"

	"camli/jsonconfig"
)

type JSONSignHandler struct {
	// Optional path to non-standard secret gpg keyring file
	secretRing string

	// Required keyId, either a short form ("26F5ABDA") or one
	// of the longer forms. Case insensitive.
	keyId string
}

func (h *JSONSignHandler) secretRingPath() string {
	if h.secretRing != "" {
		return h.secretRing
	}
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "secring.gog")
}

func createJSONSignHandler(conf jsonconfig.Obj) (http.Handler, os.Error) {
	h := &JSONSignHandler{}
	h.keyId = conf.RequiredString("keyId")
	h.secretRing = conf.OptionalString("secretRing", "")
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
	for idx, entity := range el {
		fmt.Fprintf(os.Stderr, "index %d: %#v\n", idx, entity)
	}

	return h, nil
}

func (h *JSONSignHandler) ServeHTTP(conn http.ResponseWriter, req *http.Request) {
	// TODO
}
