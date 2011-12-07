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
	"fmt"
	"os"
	"io"
	"crypto/openpgp"
	"crypto/openpgp/armor"
	"crypto/openpgp/packet"
	"path/filepath"
	"strings"
)

const publicKeyMaxSize = 256 * 1024

func VerifyPublicKeyFile(file, keyid string) (bool, os.Error) {
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
		return false, os.NewError(fmt.Sprintf("Key in file %q has id %q; expected %q",
			file, keyId, keyid))
	}
	return true, nil
}

func openArmoredPublicKeyFile(reader io.ReadCloser) (*packet.PublicKey, os.Error) {
	defer reader.Close()

	var lr = io.LimitReader(reader, publicKeyMaxSize)
	block, _ := armor.Decode(lr)
	if block == nil {
		return nil, os.NewError("Couldn't find PGP block in public key file")
	}
	if block.Type != "PGP PUBLIC KEY BLOCK" {
		return nil, os.NewError("Invalid public key blob.")
	}
	p, err := packet.Read(block.Body)
	if err != nil {
		return nil, os.NewError(fmt.Sprintf("Invalid public key blob: %v", err))
	}

	pk, ok := p.(*packet.PublicKey)
	if !ok {
		return nil, os.NewError(fmt.Sprintf("Invalid public key blob; not a public key packet"))
	}
	return pk, nil
}

func DefaultSecRingPath() string {
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "secring.gpg")
}

// keyFile defaults to $HOME/.gnupg/secring.gpg
func EntityFromSecring(keyId, keyFile string) (*openpgp.Entity, os.Error) {
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

func ArmoredPublicKey(entity *openpgp.Entity) (string, os.Error) {
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
