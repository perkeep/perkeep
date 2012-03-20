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
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp/armor"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp/packet"
)

const publicKeyMaxSize = 256 * 1024

func VerifyPublicKeyFile(file, keyid string) (bool, error) {
	f, err := os.Open(file)
	if err != nil {
		return false, err
	}

	key, err := openArmoredPublicKeyFile(f)
	if err != nil {
		return false, err
	}
	keyId := fmt.Sprintf("%X", key.Fingerprint[len(key.Fingerprint)-4:])
	if keyId != strings.ToUpper(keyid) {
		return false, errors.New(fmt.Sprintf("Key in file %q has id %q; expected %q",
			file, keyId, keyid))
	}
	return true, nil
}

func openArmoredPublicKeyFile(reader io.ReadCloser) (*packet.PublicKey, error) {
	defer reader.Close()

	var lr = io.LimitReader(reader, publicKeyMaxSize)
	block, _ := armor.Decode(lr)
	if block == nil {
		return nil, errors.New("Couldn't find PGP block in public key file")
	}
	if block.Type != "PGP PUBLIC KEY BLOCK" {
		return nil, errors.New("Invalid public key blob.")
	}
	p, err := packet.Read(block.Body)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Invalid public key blob: %v", err))
	}

	pk, ok := p.(*packet.PublicKey)
	if !ok {
		return nil, errors.New(fmt.Sprintf("Invalid public key blob; not a public key packet"))
	}
	return pk, nil
}

func DefaultSecRingPath() string {
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "secring.gpg")
}

// keyFile defaults to $HOME/.gnupg/secring.gpg
func EntityFromSecring(keyId, keyFile string) (*openpgp.Entity, error) {
	keyId = strings.ToUpper(keyId)
	if keyFile == "" {
		keyFile = DefaultSecRingPath()
	}
	secring, err := os.Open(keyFile)
	if err != nil {
		return nil, fmt.Errorf("jsonsign: failed to open keyring: %v", err)
	}
	defer secring.Close()

	el, err := openpgp.ReadKeyRing(secring)
	if err != nil {
		return nil, fmt.Errorf("openpgp.ReadKeyRing of %q: %v", keyFile, err)
	}
	var entity *openpgp.Entity
	for _, e := range el {
		pk := e.PrivateKey
		if pk == nil || (pk.KeyIdString() != keyId && pk.KeyIdShortString() != keyId) {
			continue
		}
		entity = e
	}
	if entity == nil {
		found := []string{}
		for _, e := range el {
			pk := e.PrivateKey
			if pk == nil {
				continue
			}
			found = append(found, pk.KeyIdShortString())
		}
		return nil, fmt.Errorf("didn't find a key in %q for keyId %q; other keyIds in file = %v", keyFile, keyId, found)
	}
	return entity, nil
}

var newlineBytes = []byte("\n")

func ArmoredPublicKey(entity *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	wc, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", err
	}
	err = entity.PrivateKey.PublicKey.Serialize(wc)
	if err != nil {
		return "", err
	}
	wc.Close()
	if !bytes.HasSuffix(buf.Bytes(), newlineBytes) {
		buf.WriteString("\n")
	}
	return buf.String(), nil
}

// NewEntity returns a new OpenPGP entity.
func NewEntity() (*openpgp.Entity, error) {
	name := "" // intentionally empty
	comment := "camlistore"
	email := "" // intentionally empty
	return openpgp.NewEntity(rand.Reader, time.Now(), name, comment, email)
}

func WriteKeyRing(w io.Writer, el openpgp.EntityList) error {
	return nil
}
