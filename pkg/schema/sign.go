/*
Copyright 2013 The Camlistore Authors

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

package schema

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp"
)

// A Signer signs the JSON schema blobs that require signing, such as claims
// and permanodes.
type Signer struct {
	keyId      string // short one; 8 capital hex digits
	pubref     blob.Ref
	privEntity *openpgp.Entity

	// baseSigReq is the prototype signing request used with the jsonsig
	// package.
	baseSigReq jsonsign.SignRequest
}

func (s *Signer) String() string {
	return fmt.Sprintf("[*schema.Signer for key=%s pubkey=%s]", s.keyId, s.pubref)
}

// KeyID returns the short 8 capital hex digit GPG key ID
func (s *Signer) KeyID() string {
	return s.keyId
}

// NewSigner returns an Signer given an armored public key's blobref,
// its armored content, and its associated private key entity.
// The privateKeySource must be either an *openpgp.Entity or a string filename to a secret key.
func NewSigner(pubKeyRef blob.Ref, armoredPubKey io.Reader, privateKeySource interface{}) (*Signer, error) {
	hash := pubKeyRef.Hash()
	keyId, armoredPubKeyString, err := jsonsign.ParseArmoredPublicKey(io.TeeReader(armoredPubKey, hash))
	if err != nil {
		return nil, err
	}
	if !pubKeyRef.HashMatches(hash) {
		return nil, fmt.Errorf("pubkey ref of %v doesn't match provided armored public key", pubKeyRef)
	}

	var privateKey *openpgp.Entity
	switch v := privateKeySource.(type) {
	case *openpgp.Entity:
		privateKey = v
	case string:
		privateKey, err = jsonsign.EntityFromSecring(keyId, v)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid privateKeySource type %T", v)
	}
	if privateKey == nil {
		return nil, errors.New("nil privateKey")
	}

	return &Signer{
		keyId:      keyId,
		pubref:     pubKeyRef,
		privEntity: privateKey,
		baseSigReq: jsonsign.SignRequest{
			ServerMode: true, // shouldn't matter, since we're supplying the rest of the fields
			Fetcher: memoryBlobFetcher{
				pubKeyRef: func() (uint32, io.ReadCloser) {
					return uint32(len(armoredPubKeyString)), ioutil.NopCloser(strings.NewReader(armoredPubKeyString))
				},
			},
			EntityFetcher: entityFetcherFunc(func(wantKeyId string) (*openpgp.Entity, error) {
				if privateKey.PrivateKey.KeyIdString() != wantKeyId &&
					privateKey.PrivateKey.KeyIdShortString() != wantKeyId {
					return nil, fmt.Errorf("jsonsign code unexpectedly requested keyId %q; only have %q",
						wantKeyId, keyId)
				}
				return privateKey, nil
			}),
		},
	}, nil
}

// SignJSON signs the provided json at the optional time t.
// If t is the zero Time, the current time is used.
func (s *Signer) SignJSON(json string, t time.Time) (string, error) {
	sr := s.baseSigReq
	sr.UnsignedJSON = json
	sr.SignatureTime = t
	return sr.Sign()
}

type memoryBlobFetcher map[blob.Ref]func() (size uint32, rc io.ReadCloser)

func (m memoryBlobFetcher) Fetch(br blob.Ref) (file io.ReadCloser, size uint32, err error) {
	fn, ok := m[br]
	if !ok {
		return nil, 0, os.ErrNotExist
	}
	size, file = fn()
	return
}

type entityFetcherFunc func(keyId string) (*openpgp.Entity, error)

func (f entityFetcherFunc) FetchEntity(keyId string) (*openpgp.Entity, error) {
	return f(keyId)
}
