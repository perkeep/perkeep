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

package jsonsign

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"go4.org/wkfs"
	"perkeep.org/internal/osutil"
)

const publicKeyMaxSize = 256 * 1024

// ParseArmoredPublicKey tries to parse an armored public key from r,
// taking care to bound the amount it reads.
// The returned fingerprint is 40 capital hex digits.
// The returned armoredKey is a copy of the contents read.
func ParseArmoredPublicKey(r io.Reader) (fingerprint, armoredKey string, err error) {
	var buf bytes.Buffer
	pk, err := openArmoredPublicKeyFile(io.NopCloser(io.TeeReader(r, &buf)))
	if err != nil {
		return
	}
	return fingerprintString(pk), buf.String(), nil
}

// fingerprintString returns the fingerprint (40 characters) capital hex GPG
// key ID of the provided public key.
func fingerprintString(pubKey *packet.PublicKey) string {
	return fmt.Sprintf("%X", pubKey.Fingerprint)
}

func openArmoredPublicKeyFile(reader io.ReadCloser) (*packet.PublicKey, error) {
	defer reader.Close()

	var lr = io.LimitReader(reader, publicKeyMaxSize)
	block, _ := armor.Decode(lr)
	if block == nil {
		return nil, errors.New("Couldn't find PGP block in public key file")
	}
	if block.Type != "PGP PUBLIC KEY BLOCK" {
		return nil, errors.New("invalid public key blob")
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

// EntityFromSecring returns the openpgp Entity from keyFile that matches keyID.
// If empty, keyFile defaults to osutil.SecretRingFile().
func EntityFromSecring(keyID, keyFile string) (*openpgp.Entity, error) {
	if keyID == "" {
		return nil, errors.New("empty keyID passed to EntityFromSecring")
	}
	keyID = strings.ToUpper(keyID)
	if keyFile == "" {
		keyFile = osutil.SecretRingFile()
	}
	secring, err := wkfs.Open(keyFile)
	if err != nil {
		return nil, fmt.Errorf("jsonsign: failed to open keyring: %v", err)
	}
	defer secring.Close()

	el, err := readKeyRing(secring)
	if err != nil {
		return nil, fmt.Errorf("readKeyRing of %q: %v", keyFile, err)
	}
	var entity *openpgp.Entity
	for _, e := range el {
		pk := e.PrivateKey
		if pk == nil || (keyID != fmt.Sprintf("%X", pk.Fingerprint) &&
			pk.KeyIdString() != keyID &&
			pk.KeyIdShortString() != keyID) {
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
			found = append(found, pk.KeyIdString())
		}
		return nil, fmt.Errorf("didn't find a key in %q for keyID %q; other keyIDs in file = %v", keyFile, keyID, found)
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
	armoredWriter, err := armor.Encode(w, openpgp.PrivateKeyType, nil)
	if err != nil {
		return err
	}
	for _, ent := range el {
		if err := ent.SerializePrivate(armoredWriter, nil); err != nil {
			return err
		}
	}
	return armoredWriter.Close()
}

// readKeyRing reads a keyring, armored or not.
func readKeyRing(r io.Reader) (openpgp.EntityList, error) {
	var buffer bytes.Buffer
	if el, err := openpgp.ReadArmoredKeyRing(io.TeeReader(r, &buffer)); err == nil {
		return el, err
	}
	return openpgp.ReadKeyRing(&buffer)
}

// KeyIdFromRing returns the public keyID contained in the secret
// ring file secRing. It expects only one keyID in this secret ring
// and returns an error otherwise.
func KeyIdFromRing(secRing string) (keyID string, err error) {
	f, err := wkfs.Open(secRing)
	if err != nil {
		return "", fmt.Errorf("Could not open secret ring file %v: %v", secRing, err)
	}
	defer f.Close()
	el, err := readKeyRing(f)
	if err != nil {
		return "", fmt.Errorf("Could not read secret ring file %s: %v", secRing, err)
	}
	if len(el) != 1 {
		return "", fmt.Errorf("Secret ring file %v contained %d identities; expected 1", secRing, len(el))
	}
	ent := el[0]
	return ent.PrimaryKey.KeyIdString(), nil
}

// GenerateNewSecRing creates a new secret ring file secRing, with
// a new GPG identity. It returns the public keyID of that identity.
// It returns an error if the file already exists.
func GenerateNewSecRing(secRing string) (keyID string, err error) {
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
	return ent.PrimaryKey.KeyIdString(), nil
}
