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

// Package encrypt registers the "encrypt" blobserver storage type
// which stores all blobs and metadata with AES encryption into other
// wrapped storage targets (e.g. localdisk, s3, remote, google).
//
// An encrypt storage target is configured with two other storage targets:
// one to hold encrypted blobs, and one to hold encrypted metadata about
// the encrypted blobs. On start-up, all the metadata blobs are read
// to discover the plaintext blobrefs.
//
// Encryption is currently always AES-128.  See code for metadata formats
// and configuration details, which are currently subject to change.
//
// WARNING: work in progress as of 2013-07-13.
package encrypt

import (
	"bufio"
	"bytes"
	"container/heap"
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

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/types"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"
)

// Compaction constants
const (
	// FullMetaBlobSize is the size at which we stop compacting
	// a meta blob.
	FullMetaBlobSize = 512 << 10
)

/*
Dev notes:

$ devcam put --path=/enc/ blob dev-camput
sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ devcam get --path=/enc/ sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ find /tmp/camliroot-$USER/port3179/encblob/
$ ./dev-camtool sync --src=http://localhost:3179/enc/ --dest=stdout

*/

// TODO:
// http://godoc.org/code.google.com/p/go.crypto/scrypt

type storage struct {
	// index is the meta index.
	// it's keyed by plaintext blobref.
	// the value is the meta key (encodeMetaValue)
	index sorted.KeyValue

	// Encryption key.
	key   []byte
	block cipher.Block // aes.NewCipher(key)

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

	// TODO(bradfitz): finish metdata compaction
	/*
		// mu guards the following
		mu sync.Mutex
		// toDelete are the meta blobrefs that are no longer
		// necessary, as they're subsets of others.
		toDelete []blob.Ref
		// plainIn maps from a plaintext blobref to its currently-largest-describing metablob.
		plainIn map[string]*metaBlobInfo
		// smallMeta tracks a heap of meta blobs, sorted by their encrypted size
		smallMeta metaBlobHeap
	*/

	// Hooks for testing
	testRandIV func() []byte
}

func (s *storage) setKey(key []byte) error {
	var err error
	s.block, err = aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("The key must be exactly 16 bytes (currently only AES-128 is supported): %v", err)
	}
	s.key = key
	return nil
}

type metaBlobInfo struct {
	br     blob.Ref // of meta blob
	n      int      // size of meta blob
	plains []blob.Ref
}

type metaBlobHeap []*metaBlobInfo

var _ heap.Interface = (*metaBlobHeap)(nil)

func (s *metaBlobHeap) Push(x interface{}) {
	*s = append(*s, x.(*metaBlobInfo))
}

func (s *metaBlobHeap) Pop() interface{} {
	l := s.Len()
	v := (*s)[l]
	*s = (*s)[:l-1]
	return v
}

func (s *metaBlobHeap) Len() int { return len(*s) }
func (s *metaBlobHeap) Less(i, j int) bool {
	sl := *s
	v := sl[i].n < sl[j].n
	if !v && sl[i].n == sl[j].n {
		v = sl[i].br.String() < sl[j].br.String()
	}
	return v
}

func (s *metaBlobHeap) Swap(i, j int) { (*s)[i], (*s)[j] = (*s)[j], (*s)[i] }

func (s *storage) randIV() []byte {
	if f := s.testRandIV; f != nil {
		return f()
	}
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

func (s *storage) makeSingleMetaBlob(plainBR blob.Ref, meta string) []byte {
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

func (s *storage) RemoveBlobs(blobs []blob.Ref) error {
	panic("TODO: implement")
}

func (s *storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	for _, br := range blobs {
		v, err := s.index.Get(br.String())
		if err == sorted.ErrNotFound {
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
		dest <- blob.SizedRef{br, plainSize}
	}
	return nil
}

func (s *storage) ReceiveBlob(plainBR blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
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

	encBR := blob.SHA1FromBytes(buf.Bytes())
	_, err = blobserver.Receive(s.blobs, encBR, bytes.NewReader(buf.Bytes()))
	if err != nil {
		log.Printf("encrypt: error writing encrypted blob %v (plaintext %v): %v", encBR, plainBR, err)
		return sb, errors.New("encrypt: error writing encrypted blob")
	}

	meta := encodeMetaValue(uint32(plainSize), iv, encBR, buf.Len())
	metaBlob := s.makeSingleMetaBlob(plainBR, meta)
	_, err = blobserver.ReceiveNoHash(s.meta, blob.SHA1FromBytes(metaBlob), bytes.NewReader(metaBlob))
	if err != nil {
		log.Printf("encrypt: error writing encrypted meta for plaintext %v (encrypted blob %v): %v", plainBR, encBR, err)
		return sb, errors.New("encrypt: error writing encrypted meta")
	}

	err = s.index.Set(plainBR.String(), meta)
	if err != nil {
		return sb, fmt.Errorf("encrypt: error updating index for encrypted %v (plaintext %v): %v", encBR, plainBR, err)
	}

	return blob.SizedRef{plainBR, uint32(plainSize)}, nil
}

func (s *storage) Fetch(plainBR blob.Ref) (file io.ReadCloser, size uint32, err error) {
	meta, err := s.fetchMeta(plainBR)
	if err != nil {
		return nil, 0, err
	}
	encData, _, err := s.blobs.Fetch(meta.EncBlobRef)
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
	size = types.U32(plainSize)
	if !plainBR.HashMatches(plainHash) {
		return nil, 0, blobserver.ErrCorruptBlob
	}
	return struct {
		*bytes.Reader
		io.Closer
	}{
		bytes.NewReader(plain.Bytes()),
		types.NopCloser,
	}, uint32(plainSize), nil
}

func (s *storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	iter := s.index.Find(after, "")
	n := 0
	for iter.Next() {
		if iter.Key() == after {
			continue
		}
		br, ok := blob.Parse(iter.Key())
		if !ok {
			panic("Bogus encrypt index key: " + iter.Key())
		}
		plainSize, ok := parseMetaValuePlainSize(iter.Value())
		if !ok {
			panic("Bogus encrypt index value: " + iter.Value())
		}
		select {
		case dest <- blob.SizedRef{br, plainSize}:
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

// processEncryptedMetaBlob decrypts dat (the data for the br meta blob) and parses
// its meta lines, updating the index.
//
// processEncryptedMetaBlob is not thread-safe.
func (s *storage) processEncryptedMetaBlob(br blob.Ref, dat []byte) error {
	mi := &metaBlobInfo{
		br: br,
		n:  len(dat),
	}
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
		mi.plains = append(mi.plains, blob.ParseOrZero(plainBR))
		if err := s.index.Set(plainBR, meta); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *storage) readAllMetaBlobs() error {
	type metaBlob struct {
		br  blob.Ref
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
		enumErrc <- blobserver.EnumerateAll(context.TODO(), s.meta, func(sb blob.SizedRef) error {
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
				rc, _, err := s.meta.Fetch(sb.Ref)
				var all []byte
				if err == nil {
					all, err = ioutil.ReadAll(rc)
					rc.Close()
				}
				metac <- metaBlob{sb.Ref, all, err}
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

func encodeMetaValue(plainSize uint32, iv []byte, encBR blob.Ref, encSize int) string {
	return fmt.Sprintf("%d/%x/%s/%d", plainSize, iv, encBR, encSize)
}

type metaValue struct {
	IV         []byte
	EncBlobRef blob.Ref
	EncSize    uint32
	PlainSize  uint32
}

// returns os.ErrNotExist on cache miss
func (s *storage) fetchMeta(b blob.Ref) (*metaValue, error) {
	v, err := s.index.Get(b.String())
	if err == sorted.ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	return parseMetaValue(v)
}

func parseMetaValuePlainSize(v string) (plainSize uint32, ok bool) {
	slash := strings.Index(v, "/")
	if slash < 0 {
		return
	}
	n, err := strconv.ParseUint(v[:slash], 10, 32)
	if err != nil {
		return
	}
	return uint32(n), true
}

func parseMetaValue(v string) (mv *metaValue, err error) {
	f := strings.Split(v, "/")
	if len(f) != 4 {
		return nil, errors.New("wrong number of fields")
	}
	mv = &metaValue{}
	plainSize, err := strconv.ParseUint(f[0], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("bad plaintext size in meta %q", v)
	}
	mv.PlainSize = uint32(plainSize)
	mv.IV, err = hex.DecodeString(f[1])
	if err != nil {
		return nil, fmt.Errorf("bad iv in meta %q", v)
	}
	var ok bool
	mv.EncBlobRef, ok = blob.Parse(f[2])
	if !ok {
		return nil, fmt.Errorf("bad blobref in meta %q", v)
	}
	encSize, err := strconv.ParseUint(f[3], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("bad encrypted size in meta %q", v)
	}
	mv.EncSize = uint32(encSize)
	return mv, nil
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
		return nil, errors.New("Use of the 'encrypt' target without the proper I_AGREE value.")
	}

	key := config.OptionalString("key", "")
	keyFile := config.OptionalString("keyFile", "")
	var keyb []byte
	switch {
	case key != "":
		keyb, err = hex.DecodeString(key)
		if err != nil || len(keyb) != 16 {
			return nil, fmt.Errorf("The 'key' parameter must be 16 bytes of 32 hex digits. (currently fixed at AES-128)")
		}
	case keyFile != "":
		// TODO: check that keyFile's unix permissions aren't too permissive.
		keyb, err = ioutil.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("Reading key file %v: %v", keyFile, err)
		}
	}
	blobStorage := config.RequiredString("blobs")
	metaStorage := config.RequiredString("meta")
	if err := config.Validate(); err != nil {
		return nil, err
	}

	sto.index, err = sorted.NewKeyValue(metaConf)
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

	if keyb == nil {
		// TODO: add a way to prompt from stdin on start? or keychain support?
		return nil, errors.New("no encryption key set with 'key' or 'keyFile'")
	}

	if err := sto.setKey(keyb); err != nil {
		return nil, err
	}

	start := time.Now()
	log.Printf("Reading encryption metadata...")
	if err := sto.readAllMetaBlobs(); err != nil {
		return nil, fmt.Errorf("Error scanning metadata on start-up: %v", err)
	}
	log.Printf("Read all encryption metadata in %.3f seconds", time.Since(start).Seconds())

	return sto, nil
}
