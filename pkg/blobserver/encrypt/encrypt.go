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
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"
)

/*
Dev notes:

$ ./dev-camput --path=/enc/ blob dev-camput
sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ ./dev-camget --path=/enc/ sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ find /tmp/camliroot-$USER/port3179/encblob/
$ ./dev-camtool sync --src=http://localhost:3179/enc/ --dest=stdout

*/

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
	index index.Storage // meta index

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
	for _, br := range blobs {
		v, err := s.index.Get(br.String())
		if err == index.ErrNotFound {
			continue
		}
		if err != nil {
			return err
		}
		plainSize, ok := parseMetaValuePlainSize(v)
		if !ok {
			continue
		}
		if err != nil {
			continue
		}
		dest <- blobref.SizedBlobRef{br, plainSize}
	}
	return nil
}

func (s *storage) ReceiveBlob(plainBR *blobref.BlobRef, source io.Reader) (sb blobref.SizedBlobRef, err error) {
	iv := s.randIV()
	stream := cipher.NewCTR(s.block, iv)

	hash := plainBR.Hash()
	var buf bytes.Buffer
	// TODO: compress before encrypting?
	buf.Write(iv) // TODO: write more structured header w/ version & IV length? or does that weaken it?
	sw := cipher.StreamWriter{S: stream, W: &buf}
	plainSize, err := io.Copy(io.MultiWriter(sw, hash), source)
	if err != nil {
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

	// TODO: upload meta blob with two blobrefs & IV to s.meta
	// ....

	err = s.index.Set(plainBR.String(), encodeMetaValue(plainSize, iv, encBR, buf.Len()))
	if err != nil {
		return sb, fmt.Errorf("encrypt: error updating index for encrypted %v (plaintext %v): %v", err)
	}

	return blobref.SizedBlobRef{plainBR, plainSize}, nil
}

func (s *storage) FetchStreaming(b *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	meta, err := s.fetchMeta(b)
	if err != nil {
		return nil, 0, err
	}
	rc, _, err := s.blobs.FetchStreaming(meta.EncBlobRef)
	if err != nil {
		log.Printf("encrypt: plaintext %s's encrypted %v blob not found", b, meta.EncBlobRef)
		return
	}
	blobIV := make([]byte, len(meta.IV))
	_, err = io.ReadFull(rc, blobIV)
	if err != nil {
		return nil, 0, fmt.Errorf("Error reading off IV header from blob: %v", err)
	}
	if !bytes.Equal(blobIV, meta.IV) {
		return nil, 0, fmt.Errorf("Blob and meta IV don't match")
	}
	return struct {
		io.Reader
		io.Closer
	}{
		Closer: rc,
		Reader: cipher.StreamReader{
			S: cipher.NewCTR(s.block, meta.IV),
			R: rc,
		},
	}, meta.PlainSize, nil
}

func (s *storage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit int, wait time.Duration) error {
	if wait != 0 {
		panic("TODO: support wait in EnumerateBlobs")
	}
	defer close(dest)
	iter := s.index.Find(after)
	n := 0
	for iter.Next() {
		if iter.Key() == after {
			continue
		}
		br := blobref.Parse(iter.Key())
		if br == nil {
			panic("Bogus encrypt index key: " + iter.Key())
		}
		plainSize, ok := parseMetaValuePlainSize(iter.Value())
		if !ok {
			panic("Bogus encrypt index value: " + iter.Value())
		}
		dest <- blobref.SizedBlobRef{br, plainSize}
		n++
		if limit != 0 && n >= limit {
			break
		}
	}
	return iter.Close()
}

func encodeMetaValue(plainSize int64, iv []byte, encBR *blobref.BlobRef, encSize int) string {
	return fmt.Sprintf("%d/%x/%s/%d", plainSize, iv, encBR, encSize)
}

type metaValue struct {
	IV         []byte
	EncBlobRef *blobref.BlobRef
	EncSize    int64
	PlainSize  int64
}

// returns os.ErrNotExist on cache miss
func (s *storage) fetchMeta(b *blobref.BlobRef) (*metaValue, error) {
	v, err := s.index.Get(b.String())
	if err == index.ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	return parseMetaValue(v)
}

func parseMetaValuePlainSize(v string) (plainSize int64, ok bool) {
	slash := strings.Index(v, "/")
	if slash < 0 {
		return
	}
	n, err := strconv.Atoi(v[:slash])
	if err != nil {
		return
	}
	return int64(n), true
}

func parseMetaValue(v string) (mv *metaValue, err error) {
	f := strings.Split(v, "/")
	if len(f) != 4 {
		return nil, errors.New("wrong number of fields")
	}
	mv = &metaValue{}
	mv.PlainSize, err = strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("bad plaintext size in meta %q", v)
	}
	mv.IV, err = hex.DecodeString(f[1])
	if err != nil {
		return nil, fmt.Errorf("bad iv in meta %q", v)
	}
	mv.EncBlobRef = blobref.Parse(f[2])
	if mv.EncBlobRef == nil {
		return nil, fmt.Errorf("bad blobref in meta %q", v)
	}
	mv.EncSize, err = strconv.ParseInt(f[3], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("bad encrypted size in meta %q", v)
	}
	return mv, nil
}

func init() {
	blobserver.RegisterStorageConstructor("encrypt", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (bs blobserver.Storage, err error) {
	sto := &storage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		index: index.NewMemoryStorage(), // TODO: temporary for development; let be configurable (mysql, etc)
	}
	agreement := config.OptionalString("I_AGREE", "")
	const wantAgreement = "that encryption support hasn't been peer-reviewed, isn't finished, and its format might change."
	if agreement != wantAgreement {
		return nil, errors.New("Use of the 'encrypt' target without the proper I_AGREE value.")
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
