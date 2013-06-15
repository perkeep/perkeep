/*
Copyright 2013 Google Inc.

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

package encrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
)

// TODO:
// http://godoc.org/code.google.com/p/go.crypto/scrypt
// crypto/aes
// index.Storage (initially: memindex) for all metadata.

/*
TODO: decide meta format. One argument is to stick with JSON, like
option (a) below. But that means we can't easily read it incrementally
during enumerate, which argues for a line-based format, with a magic
header.

Option a)
{"camliVersion": 1,
"camliType": "encryptedMeta",
"encryptedBlobs": [
  ["sha1-plainplainplain", "sha1-encencencenc", "iviviviviviviv" (%xx)]
],
}

*/

type storage struct {
	*blobserver.SimpleBlobHubPartitionMap

	block cipher.Block

	// Encryption key.
	key []byte

	// blobs holds encrypted versions of all plaintext blobs.
	blobs blobserver.Storage

	// meta holds metadata mapping between the names of plaintext
	// blobs and their after-encryption name, as well as their
	// IV. Each blob in meta contains 1 or more blob
	// description. All new insertions generate both a new
	// encrypted blob in 'blobs' and one single-meta blob in
	// 'meta'. The small metadata blobs are occasionally rolled up
	// into bigger blobs with multiple blob descriptions.
	//
	// TODO: which IV are these written with? safe to just use key
	// with a zero IV? ask experts.
	meta blobserver.Storage
}

func (s *storage) randIV() []byte {
	iv := make([]byte, s.block.BlockSize())
	n, err := rand.Read(iv)
	if err != nil {
		panic(err)
	}
	if n != len(iv) {
		panic("short read from crypto/rand")
	}
	return iv
}

func (s *storage) RemoveBlobs(blobs []*blobref.BlobRef) error {
	panic("TODO: implement")
}

func (s *storage) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, wait time.Duration) error {
	panic("TODO: implement")
}

func (s *storage) ReceiveBlob(plainBR *blobref.BlobRef, source io.Reader) (sb blobref.SizedBlobRef, err error) {
	iv := s.randIV()
	stream := cipher.NewCTR(s.block, iv)

	hash := plainBR.Hash()
	var buf bytes.Buffer
	buf.Write(iv) // TODO: write more structured header w/ version & IV length? or does that weaken it?
	sw := cipher.StreamWriter{S: stream, W: &buf}
	n, err := io.Copy(io.MultiWriter(sw, hash), source)
	if err != nil {
		return sb, err
	}
	if err := sw.Close(); err != nil {
		return sb, err
	}
	if !plainBR.HashMatches(hash) {
		return sb, blobserver.ErrCorruptBlob
	}

	encBR := blobref.SHA1FromBytes(buf.Bytes())
	_, err = s.blobs.ReceiveBlob(encBR, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return sb, fmt.Errorf("encrypt: error writing encrypted %v (plaintext %v): %v", encBR, plainBR, err)
	}
	// TODO: upload buf.Bytes() to s.blobs
	// TODO: upload meta blob with two blobrefs & IV to s.meta
	// TODO: update index with mapping

	return blobref.SizedBlobRef{plainBR, n}, nil
}

func (s *storage) FetchStreaming(b *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	panic("TODO: implement")
}

func (s *storage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit int, wait time.Duration) error {
	panic("TODO: implement")
}

func init() {
	blobserver.RegisterStorageConstructor("encrypt", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (bs blobserver.Storage, err error) {
	sto := &storage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
	}

	key := config.OptionalString("key", "")
	keyFile := config.OptionalString("keyFile", "")
	switch {
	case key != "":
		sto.key, err = hex.DecodeString(key)
		if err != nil || len(sto.key) != 16 {
			return nil, fmt.Errorf("The 'key' parameter must be 16 bytes of 32 hex digits. (currently fixed at AES-128)")
		}
	case keyFile != "":
		// TODO: check that keyFile's unix permissions aren't too permissive.
		sto.key, err = ioutil.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("Reading key file %v: %v", keyFile, err)
		}
	}
	blobStorage := config.RequiredString("blobs")
	metaStorage := config.RequiredString("meta")
	if err := config.Validate(); err != nil {
		return nil, err
	}

	sto.blobs, err = ld.GetStorage(blobStorage)
	if err != nil {
		return
	}
	sto.meta, err = ld.GetStorage(metaStorage)
	if err != nil {
		return
	}
	if sto.key == nil {
		// TODO: add a way to prompt from stdin on start? or keychain support?
		return nil, errors.New("no encryption key set with 'key' or 'keyFile'")
	}
	sto.block, err = aes.NewCipher(sto.key)
	if err != nil {
		return nil, fmt.Errorf("The key must be exactly 16 bytes (currently only AES-128 is supported): %v", err)
	}
	return sto, nil
}
