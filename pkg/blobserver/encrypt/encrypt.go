/*
Copyright 2016 The Perkeep Authors

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

// Package encrypt registers the "encrypt" blobserver storage type
// which stores all blobs and metadata with NaCl encryption into other
// wrapped storage targets (e.g. localdisk, s3, remote, google).
//
// An encrypt storage target is configured with two other storage targets:
// one to hold encrypted blobs, and one to hold encrypted metadata about
// the encrypted blobs. On start-up, all the metadata blobs are read
// to discover the plaintext blobrefs.
//
// Encryption is currently always NaCl SecretBox.  See code for metadata
// formats and configuration details, which are currently subject to change.
package encrypt // import "perkeep.org/pkg/blobserver/encrypt"

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"time"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
	"golang.org/x/crypto/nacl/secretbox"
	"golang.org/x/crypto/scrypt"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/sorted"
)

type storage struct {
	// index is the meta index, populated at startup from the blobs in storage.meta.
	// key: plaintext blob.Ref
	// value: <plaintext length>/<encrypted blob.Ref>
	index sorted.KeyValue

	// Encryption key.
	key [32]byte

	// blobs holds encrypted versions of all plaintext blobs.
	blobs blobserver.Storage

	// meta holds metadata mapping between the names of plaintext blobs and
	// their original size and after-encryption name. Each blob in meta contains
	// 1 or more blob descriptions. All new insertions generate both a new
	// encrypted blob in 'blobs' and one single-meta blob in
	// 'meta'. The small metadata blobs are occasionally rolled up
	// into bigger blobs with multiple blob descriptions.
	meta blobserver.Storage

	// smallMeta tracks a heap of meta blobs smaller than the target size.
	smallMeta *metaBlobHeap

	// Hooks for testing
	testRand func([]byte) (int, error)
}

var scryptN = 1 << 20 // DO NOT change, except in tests

func (s *storage) setPassphrase(passphrase []byte) {
	if len(passphrase) == 0 {
		panic("tried to set empty passphrase")
	}

	// We can't use a random salt as the passphrase wouldn't be enough to recover the
	// data anymore, but we use a custom one so that generic tables are useless.
	salt := []byte("camlistore")

	// "Sensitive storage" reccomended parameters. 5s in 2009, probably less now.
	// https://www.tarsnap.com/scrypt/scrypt-slides.pdf
	key, err := scrypt.Key(passphrase, salt, scryptN, 8, 1, 32)
	if err != nil {
		// This can't happen with good parameters, which are fixed.
		panic("scrypt key derivation failed: " + err.Error())
	}

	if copy(s.key[:], key) != 32 {
		panic("copied wrong key length")
	}
}

func (s *storage) randNonce(nonce *[24]byte) {
	rand := rand.Read
	if s.testRand != nil {
		rand = s.testRand
	}
	_, err := rand(nonce[:])
	if err != nil {
		panic(err)
	}
}

// Format of encrypted blobs:
// versionByte (0x01) || 24 bytes nonce || secretbox(plaintext)
// The plaintext is long len(ciphertext) - 1 - 24 - secretbox.Overhead (16)

const version = 1

const overhead = 1 + 24 + secretbox.Overhead

// encryptBlob encrypts plaintext and appends the result to ciphertext,
// which must not overlap plaintext.
func (s *storage) encryptBlob(ciphertext, plaintext []byte) []byte {
	if s.key == [32]byte{} {
		// Safety check, we really don't want this to happen.
		panic("no passphrase set")
	}
	var nonce [24]byte
	s.randNonce(&nonce)
	ciphertext = append(ciphertext, version)
	ciphertext = append(ciphertext, nonce[:]...)
	return secretbox.Seal(ciphertext, plaintext, &nonce, &s.key)
}

// decryptBlob decrypts ciphertext and appends the result to plaintext,
// which must not overlap ciphertext.
func (s *storage) decryptBlob(plaintext, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < overhead {
		return nil, errors.New("blob too short to be encrypted")
	}
	if ciphertext[0] != version {
		return nil, errors.New("unknown encrypted blob version")
	}
	var nonce [24]byte
	copy(nonce[:], ciphertext[1:])
	plaintext, success := secretbox.Open(plaintext, ciphertext[25:], &nonce, &s.key)
	if !success {
		return nil, errors.New("encrypted blob failed authentication")
	}
	return plaintext, nil
}

func (s *storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	return blobserver.ErrNotImplemented // TODO
}

var statGate = syncutil.NewGate(20) // arbitrary

func (s *storage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(br blob.Ref) (sb blob.SizedRef, err error) {
		plainSize, _, err := s.fetchMeta(ctx, br)
		switch err {
		case nil:
			return blob.SizedRef{Ref: br, Size: plainSize}, nil
		case os.ErrNotExist:
			return sb, nil
		default:
			return sb, err
		}
	})
}

func (s *storage) ReceiveBlob(ctx context.Context, plainBR blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
	// Aggressively check for duplicates since there's nothing else to
	// ensure we don't store blobs twice with different nonces.
	if plainSize, _, err := s.fetchMeta(ctx, plainBR); err == nil {
		log.Println("encrypt: duplicated blob received", plainBR)
		return blob.SizedRef{Ref: plainBR, Size: uint32(plainSize)}, nil
	}

	hash := plainBR.Hash()
	var buf bytes.Buffer
	plainSize, err := io.Copy(io.MultiWriter(&buf, hash), source)
	if err != nil {
		return sb, err
	}
	if !plainBR.HashMatches(hash) {
		return sb, blobserver.ErrCorruptBlob
	}

	enc := s.encryptBlob(nil, buf.Bytes())
	encBR := blob.RefFromBytes(enc)

	_, err = blobserver.ReceiveNoHash(ctx, s.blobs, encBR, bytes.NewReader(enc))
	if err != nil {
		return sb, fmt.Errorf("encrypt: error writing encrypted blob %v (plaintext %v): %v", encBR, plainBR, err)
	}

	metaBytes := s.makeSingleMetaBlob(plainBR, encBR, uint32(plainSize))
	metaSB, err := blobserver.ReceiveNoHash(ctx, s.meta, blob.RefFromBytes(metaBytes), bytes.NewReader(metaBytes))
	if err != nil {
		return sb, fmt.Errorf("encrypt: error writing encrypted meta for plaintext %v (encrypted blob %v): %v", plainBR, encBR, err)
	}
	s.recordMeta(&metaBlob{br: metaSB.Ref, plains: []blob.Ref{plainBR}})

	err = s.index.Set(plainBR.String(), packIndexEntry(uint32(plainSize), encBR))
	if err != nil {
		return sb, fmt.Errorf("encrypt: error updating index for encrypted %v (plaintext %v): %v", encBR, plainBR, err)
	}

	return blob.SizedRef{Ref: plainBR, Size: uint32(plainSize)}, nil
}

func (s *storage) Fetch(ctx context.Context, plainBR blob.Ref) (io.ReadCloser, uint32, error) {
	plainSize, encBR, err := s.fetchMeta(ctx, plainBR)
	if err != nil {
		return nil, 0, err
	}
	encData, _, err := s.blobs.Fetch(ctx, encBR)
	if err != nil {
		return nil, 0, fmt.Errorf("encrypt: error fetching plaintext %s's encrypted %v blob: %v", plainBR, encBR, err)
	}
	defer encData.Close()

	var ciphertext bytes.Buffer
	ciphertext.Grow(int(plainSize + overhead))
	encHash := encBR.Hash()
	_, err = io.Copy(io.MultiWriter(&ciphertext, encHash), encData)
	if err != nil {
		return nil, 0, err
	}

	// We have a signed statement in the meta blob that attests that the
	// ciphertext hash corresponds to the plaintext hash, so no need to check
	// the latter.  However, check the former to make sure the encrypted blob
	// was not swapped for another.
	if !encBR.HashMatches(encHash) {
		return nil, 0, blobserver.ErrCorruptBlob
	}

	plaintext, err := s.decryptBlob(nil, ciphertext.Bytes())
	if err != nil {
		return nil, 0, fmt.Errorf("encrypt: encrypted blob %s failed validation: %s", encBR, err)
	}

	return ioutil.NopCloser(bytes.NewReader(plaintext)), uint32(len(plaintext)), nil
}

func (s *storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	iter := s.index.Find(after, "")
	n := 0
	for iter.Next() {
		if iter.Key() == after {
			continue
		}
		// Both ReceiveBlob and processEncryptedMetaBlob validate this
		br := blob.MustParse(iter.Key())
		plainSize, _, err := unpackIndexEntry(iter.Value())
		if err != nil {
			return fmt.Errorf("bogus encrypt index value %q: %s", iter.Value(), err)
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: plainSize}:
		case <-ctx.Done():
			return ctx.Err()
		}
		n++
		if limit != 0 && n >= limit {
			break
		}
	}
	return iter.Close()
}

func init() {
	blobserver.RegisterStorageConstructor("encrypt", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (bs blobserver.Storage, err error) {
	metaConf := config.RequiredObject("metaIndex")
	sto := &storage{}
	agreement := config.OptionalString("I_AGREE", "")
	const wantAgreement = "that encryption support hasn't been peer-reviewed, isn't finished, and its format might change."
	if agreement != wantAgreement {
		return nil, errors.New("use of the 'encrypt' target without the proper I_AGREE value")
	}

	var keyData []byte
	passphrase := config.OptionalString("passphrase", "")
	keyFile := config.OptionalString("keyFile", "")
	if passphrase != "" && keyFile != "" {
		return nil, errors.New("Can't specify both passphrase and keyFile")
	}
	if passphrase == "" && keyFile == "" {
		return nil, errors.New("Must specify passphrase or keyFile")
	}
	if keyFile != "" {
		// TODO: check that keyFile's unix permissions aren't too permissive.
		keyData, err = ioutil.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("Reading key file %v: %v", keyFile, err)
		}
	} else {
		keyData = []byte(passphrase)
	}

	blobStorage := config.RequiredString("blobs")
	metaStorage := config.RequiredString("meta")
	if err := config.Validate(); err != nil {
		return nil, err
	}

	sto.index, err = sorted.NewKeyValueMaybeWipe(metaConf)
	if err != nil {
		return
	}

	sto.blobs, err = ld.GetStorage(blobStorage)
	if err != nil {
		return
	}
	sto.meta, err = ld.GetStorage(metaStorage)
	if err != nil {
		return
	}

	sto.setPassphrase(keyData)

	start := time.Now()
	log.Printf("Reading encryption metadata...")
	sto.smallMeta = &metaBlobHeap{}
	if err := sto.readAllMetaBlobs(); err != nil {
		return nil, fmt.Errorf("error scanning metadata on start-up: %v", err)
	}
	log.Printf("Read all encryption metadata in %.3f seconds", time.Since(start).Seconds())

	return sto, nil
}
