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
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
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

/*
Meta format:
   <16 bytes of IV> (for AES-128)
   <20 bytes of SHA-1 of plaintext>
   <encrypted>

Where encrypted has plaintext of:
   #camlistore/encmeta=1
Then sorted lines, each ending in a newline, like:
   sha1-plain/<metaValue>
See the encodeMetaValue for the definition of metaValue, but in summary:
   sha1-plain/<plaintext size>/<iv as %x>/sha1-encrypted/<encrypted size>
*/

func (s *storage) makeSingleMetaBlob(plainBR *blobref.BlobRef, meta string) []byte {
	iv := s.randIV()

	var plain bytes.Buffer
	plain.WriteString("#camlistore/encmeta=1\n")
	plain.WriteString(plainBR.String())
	plain.WriteByte('/')
	plain.WriteString(meta)
	plain.WriteByte('\n')

	s1 := sha1.New()
	s1.Write(plain.Bytes())

	var final bytes.Buffer
	final.Grow(len(iv) + sha1.Size + plain.Len())
	final.Write(iv)
	final.Write(s1.Sum(final.Bytes()[len(iv):]))

	_, err := io.Copy(cipher.StreamWriter{S: cipher.NewCTR(s.block, iv), W: &final}, &plain)
	if err != nil {
		panic(err)
	}
	return final.Bytes()
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
		log.Printf("encrypt: error writing encrypted blob %v (plaintext %v): %v", encBR, plainBR, err)
		return sb, errors.New("encrypt: error writing encrypted blob")
	}

	meta := encodeMetaValue(plainSize, iv, encBR, buf.Len())
	metaBlob := s.makeSingleMetaBlob(plainBR, meta)
	_, err = s.meta.ReceiveBlob(blobref.SHA1FromBytes(metaBlob), bytes.NewReader(metaBlob))
	if err != nil {
		log.Printf("encrypt: error writing encrypted meta for plaintext %v (encrypted blob %v): %v", plainBR, encBR, err)
		return sb, errors.New("encrypt: error writing encrypted meta")
	}

	err = s.index.Set(plainBR.String(), meta)
	if err != nil {
		return sb, fmt.Errorf("encrypt: error updating index for encrypted %v (plaintext %v): %v", err)
	}

	return blobref.SizedBlobRef{plainBR, plainSize}, nil
}

func (s *storage) FetchStreaming(plainBR *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	meta, err := s.fetchMeta(plainBR)
	if err != nil {
		return nil, 0, err
	}
	encData, _, err := s.blobs.FetchStreaming(meta.EncBlobRef)
	if err != nil {
		log.Printf("encrypt: plaintext %s's encrypted %v blob not found", plainBR, meta.EncBlobRef)
		return
	}
	defer encData.Close()

	// Quick sanity check that the blob begins with the same IV we
	// have in our metadata.
	blobIV := make([]byte, len(meta.IV))
	_, err = io.ReadFull(encData, blobIV)
	if err != nil {
		return nil, 0, fmt.Errorf("Error reading off IV header from blob: %v", err)
	}
	if !bytes.Equal(blobIV, meta.IV) {
		return nil, 0, fmt.Errorf("Blob and meta IV don't match")
	}

	// Slurp the whole blob into memory to validate its plaintext
	// checksum (no tampered bits) before returning it. Clients
	// should be the party doing this in the general case, but
	// we'll be extra paranoid and always do it here, at the cost
	// of sometimes having it be done twice.
	var plain bytes.Buffer
	plainHash := plainBR.Hash()
	plainSize, err := io.Copy(io.MultiWriter(&plain, plainHash), cipher.StreamReader{
		S: cipher.NewCTR(s.block, meta.IV),
		R: encData,
	})
	if err != nil {
		return nil, 0, err
	}
	if !plainBR.HashMatches(plainHash) {
		return nil, 0, blobserver.ErrCorruptBlob
	}
	return struct {
		*bytes.Reader
		io.Closer
	}{
		bytes.NewReader(plain.Bytes()),
		dummyCloser,
	}, plainSize, nil
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

// processEncryptedMetaBlob decrypts dat (the data for the br meta blob) and parses
// its meta lines, updating the index.
//
// processEncryptedMetaBlob is not thread-safe.
func (s *storage) processEncryptedMetaBlob(br *blobref.BlobRef, dat []byte) error {
	log.Printf("processing meta blob %v: %d bytes", br, len(dat))
	ivSize := s.block.BlockSize()
	if len(dat) < ivSize+sha1.Size {
		return errors.New("data size is smaller than IV + SHA-1")
	}
	var (
		iv       = dat[:ivSize]
		wantHash = dat[ivSize : ivSize+sha1.Size]
		enc      = dat[ivSize+sha1.Size:]
	)
	plain := bytes.NewBuffer(make([]byte, 0, len(dat)))
	io.Copy(plain, cipher.StreamReader{
		S: cipher.NewCTR(s.block, iv),
		R: bytes.NewReader(enc),
	})
	s1 := sha1.New()
	s1.Write(plain.Bytes())
	if !bytes.Equal(wantHash, s1.Sum(nil)) {
		return errors.New("hash of encrypted data doesn't match")
	}
	sc := bufio.NewScanner(plain)
	if !sc.Scan() {
		return errors.New("No first line")
	}
	if sc.Text() != "#camlistore/encmeta=1" {
		line := sc.Text()
		if len(line) > 80 {
			line = line[:80]
		}
		return fmt.Errorf("unsupported first line %q", line)
	}
	for sc.Scan() {
		line := sc.Text()
		slash := strings.Index(line, "/")
		if slash < 0 {
			return errors.New("no slash in metaline")
		}
		plainBR, meta := line[:slash], line[slash+1:]
		log.Printf("Adding meta: %q = %q", plainBR, meta)
		if err := s.index.Set(plainBR, meta); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *storage) readAllMetaBlobs() error {
	type metaBlob struct {
		br  *blobref.BlobRef
		dat []byte // encrypted blob
		err error
	}
	metac := make(chan metaBlob, 16)

	const maxInFlight = 50
	var gate = make(chan bool, maxInFlight)

	var stopEnumerate = make(chan bool) // closed on error
	enumErrc := make(chan error, 1)
	go func() {
		var wg sync.WaitGroup
		enumErrc <- blobserver.EnumerateAll(s.meta, func(sb blobref.SizedBlobRef) error {
			select {
			case <-stopEnumerate:
				return errors.New("enumeration stopped")
			default:
			}

			wg.Add(1)
			gate <- true
			go func() {
				defer wg.Done()
				defer func() { <-gate }()
				rc, _, err := s.meta.FetchStreaming(sb.BlobRef)
				var all []byte
				if err == nil {
					all, err = ioutil.ReadAll(rc)
					rc.Close()
				}
				metac <- metaBlob{sb.BlobRef, all, err}
			}()
			return nil
		})
		wg.Wait()
		close(metac)
	}()

	for mi := range metac {
		err := mi.err
		if err == nil {
			err = s.processEncryptedMetaBlob(mi.br, mi.dat)
		}
		if err != nil {
			close(stopEnumerate)
			go func() {
				for _ = range metac {
				}
			}()
			// TODO: advertise in this error message a new option or environment variable
			// to skip a certain or all meta blobs, to allow partial recovery, if some
			// are corrupt. For now, require all to be correct.
			return fmt.Errorf("Error with meta blob %v: %v", mi.br, err)
		}
	}

	return <-enumErrc
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

var dummyCloser io.Closer = ioutil.NopCloser(nil)

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

	log.Printf("Reading encryption metadata...")
	if err := sto.readAllMetaBlobs(); err != nil {
		return nil, fmt.Errorf("Error scanning metadata on start-up: %v", err)
	}
	log.Printf("Read all encryption metadata.")

	return sto, nil
}
