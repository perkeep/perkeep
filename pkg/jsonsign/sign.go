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

package jsonsign

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/osutil"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp/packet"

	"go4.org/wkfs"
)

type EntityFetcher interface {
	FetchEntity(keyId string) (*openpgp.Entity, error)
}

type FileEntityFetcher struct {
	File string
}

func FlagEntityFetcher() *FileEntityFetcher {
	return &FileEntityFetcher{File: osutil.SecretRingFile()}
}

type CachingEntityFetcher struct {
	Fetcher EntityFetcher

	lk sync.Mutex
	m  map[string]*openpgp.Entity
}

func (ce *CachingEntityFetcher) FetchEntity(keyId string) (*openpgp.Entity, error) {
	ce.lk.Lock()
	if ce.m != nil {
		e := ce.m[keyId]
		if e != nil {
			ce.lk.Unlock()
			return e, nil
		}
	}
	ce.lk.Unlock()

	e, err := ce.Fetcher.FetchEntity(keyId)
	if err == nil {
		ce.lk.Lock()
		defer ce.lk.Unlock()
		if ce.m == nil {
			ce.m = make(map[string]*openpgp.Entity)
		}
		ce.m[keyId] = e
	}

	return e, err
}

func (fe *FileEntityFetcher) FetchEntity(keyId string) (*openpgp.Entity, error) {
	f, err := wkfs.Open(fe.File)
	if err != nil {
		return nil, fmt.Errorf("jsonsign: FetchEntity: %v", err)
	}
	defer f.Close()
	el, err := openpgp.ReadKeyRing(f)
	if err != nil {
		return nil, fmt.Errorf("jsonsign: openpgp.ReadKeyRing of %q: %v", fe.File, err)
	}
	for _, e := range el {
		pubk := &e.PrivateKey.PublicKey
		if pubk.KeyIdString() != keyId {
			continue
		}
		if e.PrivateKey.Encrypted {
			if err := fe.decryptEntity(e); err == nil {
				return e, nil
			} else {
				return nil, err
			}
		}
		return e, nil
	}
	return nil, fmt.Errorf("jsonsign: entity for keyid %q not found in %q", keyId, fe.File)
}

type SignRequest struct {
	UnsignedJSON string
	Fetcher      blob.Fetcher
	ServerMode   bool // if true, can't use pinentry or gpg-agent, etc.

	// Optional signature time. If zero, time.Now() is used.
	SignatureTime time.Time

	// Optional function to return an entity (including decrypting
	// the PrivateKey, if necessary)
	EntityFetcher EntityFetcher

	// SecretKeyringPath is only used if EntityFetcher is nil,
	// in which case SecretKeyringPath is used if non-empty.
	// As a final resort, we default to osutil.SecretRingFile().
	SecretKeyringPath string
}

func (sr *SignRequest) secretRingPath() string {
	if sr.SecretKeyringPath != "" {
		return sr.SecretKeyringPath
	}
	return osutil.SecretRingFile()
}

func (sr *SignRequest) Sign() (signedJSON string, err error) {
	trimmedJSON := strings.TrimRightFunc(sr.UnsignedJSON, unicode.IsSpace)

	// TODO: make sure these return different things
	inputfail := func(msg string) (string, error) {
		return "", errors.New(msg)
	}
	execfail := func(msg string) (string, error) {
		return "", errors.New(msg)
	}

	jmap := make(map[string]interface{})
	if err := json.Unmarshal([]byte(trimmedJSON), &jmap); err != nil {
		return inputfail("json parse error")
	}

	camliSigner, hasSigner := jmap["camliSigner"]
	if !hasSigner {
		return inputfail("json lacks \"camliSigner\" key with public key blobref")
	}

	camliSignerStr, _ := camliSigner.(string)
	signerBlob, ok := blob.Parse(camliSignerStr)
	if !ok {
		return inputfail("json \"camliSigner\" key is malformed or unsupported")
	}

	pubkeyReader, _, err := sr.Fetcher.Fetch(signerBlob)
	if err != nil {
		// TODO: not really either an inputfail or an execfail.. but going
		// with exec for now.
		return execfail(fmt.Sprintf("failed to find public key %s: %v", signerBlob.String(), err))
	}

	pubk, err := openArmoredPublicKeyFile(pubkeyReader)
	pubkeyReader.Close()
	if err != nil {
		return execfail(fmt.Sprintf("failed to parse public key from blobref %s: %v", signerBlob.String(), err))
	}

	// This check should be redundant if the above JSON parse succeeded, but
	// for explicitness...
	if len(trimmedJSON) == 0 || trimmedJSON[len(trimmedJSON)-1] != '}' {
		return inputfail("json parameter lacks trailing '}'")
	}
	trimmedJSON = trimmedJSON[0 : len(trimmedJSON)-1]

	// sign it
	entityFetcher := sr.EntityFetcher
	if entityFetcher == nil {
		file := sr.secretRingPath()
		if file == "" {
			return "", errors.New("jsonsign: no EntityFetcher, and no secret ring file defined.")
		}
		secring, err := wkfs.Open(sr.secretRingPath())
		if err != nil {
			return "", fmt.Errorf("jsonsign: failed to open secret ring file %q: %v", sr.secretRingPath(), err)
		}
		secring.Close() // just opened to see if it's readable
		entityFetcher = &FileEntityFetcher{File: file}
	}
	signer, err := entityFetcher.FetchEntity(pubk.KeyIdString())
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = openpgp.ArmoredDetachSign(
		&buf,
		signer,
		strings.NewReader(trimmedJSON),
		&packet.Config{Time: func() time.Time { return sr.SignatureTime }},
	)
	if err != nil {
		return "", err
	}

	output := buf.String()

	index1 := strings.Index(output, "\n\n")
	index2 := strings.Index(output, "\n-----")
	if index1 == -1 || index2 == -1 {
		return execfail("Failed to parse signature from gpg.")
	}
	inner := output[index1+2 : index2]
	signature := strings.Replace(inner, "\n", "", -1)

	return fmt.Sprintf("%s,\"camliSig\":\"%s\"}\n", trimmedJSON, signature), nil
}
