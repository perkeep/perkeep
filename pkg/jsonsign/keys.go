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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/osutil"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp/armor"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp/packet"

	"go4.org/wkfs"
)

const publicKeyMaxSize = 256 * 1024

// ParseArmoredPublicKey tries to parse an armored public key from r,
// taking care to bound the amount it reads.
// The returned shortKeyId is 8 capital hex digits.
// The returned armoredKey is a copy of the contents read.
func ParseArmoredPublicKey(r io.Reader) (shortKeyId, armoredKey string, err error) {
	var buf bytes.Buffer
	pk, err := openArmoredPublicKeyFile(ioutil.NopCloser(io.TeeReader(r, &buf)))
	if err != nil {
		return
	}
	return publicKeyId(pk), buf.String(), nil
}

func VerifyPublicKeyFile(file, keyid string) (bool, error) {
	f, err := wkfs.Open(file)
	if err != nil {
		return false, err
	}

	key, err := openArmoredPublicKeyFile(f)
	if err != nil {
		return false, err
	}
	keyId := publicKeyId(key)
	if keyId != strings.ToUpper(keyid) {
		return false, fmt.Errorf("Key in file %q has id %q; expected %q",
			file, keyId, keyid)
	}
	return true, nil
}

// publicKeyId returns the short (8 character) capital hex GPG key ID
// of the provided public key.
func publicKeyId(pubKey *packet.PublicKey) string {
	return fmt.Sprintf("%X", pubKey.Fingerprint[len(pubKey.Fingerprint)-4:])
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
		return nil, fmt.Errorf("Invalid public key blob: %v", err)
	}

	pk, ok := p.(*packet.PublicKey)
	if !ok {
		return nil, fmt.Errorf("Invalid public key blob; not a public key packet")
	}
	return pk, nil
}

// EntityFromSecring returns the openpgp Entity from keyFile that matches keyId.
// If empty, keyFile defaults to osutil.SecretRingFile().
func EntityFromSecring(keyId, keyFile string) (*openpgp.Entity, error) {
	if keyId == "" {
		return nil, errors.New("empty keyId passed to EntityFromSecring")
	}
	keyId = strings.ToUpper(keyId)
	if keyFile == "" {
		keyFile = osutil.SecretRingFile()
	}
	secring, err := wkfs.Open(keyFile)
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
	return openpgp.NewEntity(name, comment, email, nil)
}

func WriteKeyRing(w io.Writer, el openpgp.EntityList) error {
	for _, ent := range el {
		if err := ent.SerializePrivate(w, nil); err != nil {
			return err
		}
	}
	return nil
}

// KeyIdFromRing returns the public keyId contained in the secret
// ring file secRing. It expects only one keyId in this secret ring
// and returns an error otherwise.
func KeyIdFromRing(secRing string) (keyId string, err error) {
	f, err := wkfs.Open(secRing)
	if err != nil {
		return "", fmt.Errorf("Could not open secret ring file %v: %v", secRing, err)
	}
	defer f.Close()
	el, err := openpgp.ReadKeyRing(f)
	if err != nil {
		return "", fmt.Errorf("Could not read secret ring file %s: %v", secRing, err)
	}
	if len(el) != 1 {
		return "", fmt.Errorf("Secret ring file %v contained %d identities; expected 1", secRing, len(el))
	}
	ent := el[0]
	return ent.PrimaryKey.KeyIdShortString(), nil
}

// GenerateNewSecRing creates a new secret ring file secRing, with
// a new GPG identity. It returns the public keyId of that identity.
// It returns an error if the file already exists.
func GenerateNewSecRing(secRing string) (keyId string, err error) {
	ent, err := NewEntity()
	if err != nil {
		return "", fmt.Errorf("generating new identity: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(secRing), 0700); err != nil {
		return "", err
	}
	f, err := wkfs.OpenFile(secRing, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	err = WriteKeyRing(f, openpgp.EntityList([]*openpgp.Entity{ent}))
	if err != nil {
		f.Close()
		return "", fmt.Errorf("Could not write new key ring to %s: %v", secRing, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("Could not close %v: %v", secRing, err)
	}
	return ent.PrimaryKey.KeyIdShortString(), nil
}
