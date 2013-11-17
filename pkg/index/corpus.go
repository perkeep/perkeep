package index

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types/camtypes"
)

// Corpus is an in-memory summary of all of a user's blobs' metadata.
type Corpus struct {
	mu sync.RWMutex

	// gen is incremented on every blob received.
	// It's used as a query cache invalidator.
	gen int64

	strs  map[string]string // interned strings
	blobs map[blob.Ref]*camtypes.BlobMeta

	// camlBlobs maps from camliType ("file") to blobref to the meta.
	// The value is the same one in blobs.
	camBlobs map[string]map[blob.Ref]*camtypes.BlobMeta

	// TODO: add GoLLRB to third_party; keep sorted BlobMeta
	keyId      map[blob.Ref]string
	files      map[blob.Ref]FileMeta
	permanodes map[blob.Ref]*PermanodeMeta

	// TOOD: use deletedCache instead?
	deletedBy map[blob.Ref]blob.Ref // key is deleted by value
}

type FileMeta struct {
	size      int64
	mimeType  string
	wholeRefs []blob.Ref
}

type PermanodeMeta struct {
	OwnerKeyId string
	// Claims     ClaimList
}

func newCorpus() *Corpus {
	return &Corpus{
		blobs:      make(map[blob.Ref]*camtypes.BlobMeta),
		camBlobs:   make(map[string]map[blob.Ref]*camtypes.BlobMeta),
		files:      make(map[blob.Ref]FileMeta),
		permanodes: make(map[blob.Ref]*PermanodeMeta),
		deletedBy:  make(map[blob.Ref]blob.Ref),
		keyId:      make(map[blob.Ref]string),
	}
}

func NewCorpusFromStorage(s Storage) (*Corpus, error) {
	if s == nil {
		return nil, errors.New("storage is nil")
	}
	c := newCorpus()
	return c, c.scanFromStorage(s)
}

func (x *Index) KeepInMemory() (*Corpus, error) {
	var err error
	x.corpus, err = NewCorpusFromStorage(x.s)
	return x.corpus, err
}

// PreventStorageAccessForTesting causes any access to the index's underlying
// Storage interface to crash, calling Fatal on the provided t.
func (x *Index) PreventStorageAccessForTesting(t *testing.T) {
	x.s = crashStorage{t: t}
}

type crashStorage struct {
	Storage
	t *testing.T
}

func (s crashStorage) Get(key string) (string, error) {
	panic(fmt.Sprintf("unexpected index.Storage.Get(%q) called", key))
}

func (s crashStorage) Find(key string) Iterator {
	panic(fmt.Sprintf("unexpected index.Storage.Find(%q) called", key))
}

// *********** Updating the corpus

func (c *Corpus) scanFromStorage(s Storage) error {
	for _, prefix := range []string{"meta:", "signerkeyid:"} {
		if err := c.scanPrefix(s, prefix); err != nil {
			return err
		}
	}
	return nil
}

func (c *Corpus) scanPrefix(s Storage, prefix string) (err error) {
	fn, ok := corpusMergeFunc[typeOfKey(prefix)]
	if !ok {
		panic("No registered merge func for prefix " + prefix)
	}
	it := queryPrefixString(s, prefix)
	defer closeIterator(it, &err)
	for it.Next() {
		if err := fn(c, it.Key(), it.Value()); err != nil {
			return err
		}
	}
	return nil
}

var corpusMergeFunc = map[string]func(c *Corpus, k, v string) error{
	"have":        nil, // redundant with "meta"
	"meta":        (*Corpus).mergeMetaRow,
	"signerkeyid": (*Corpus).mergeSignerKeyIdRow,
}

func (c *Corpus) addBlob(br blob.Ref, mm mutationMap) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for k, v := range mm {
		kt := typeOfKey(k)
		if fn, ok := corpusMergeFunc[kt]; ok {
			if fn != nil {
				if err := fn(c, k, v); err != nil {
					return err
				}
			}
		} else {
			log.Printf("TODO: receiving blob %v, unsupported key type %q to merge mutation %q -> %q", br, kt, k, v)
		}
	}
	return nil
}

func (c *Corpus) mergeMetaRow(k, v string) error {
	bm, ok := kvBlobMeta(k, v)
	if !ok {
		return fmt.Errorf("bogus meta row: %q -> %q", k, v)
	}
	bm.CamliType = c.strLocked(bm.CamliType)
	c.blobs[bm.Ref] = &bm
	if bm.CamliType != "" {
		m, ok := c.camBlobs[bm.CamliType]
		if !ok {
			m = make(map[blob.Ref]*camtypes.BlobMeta)
			c.camBlobs[bm.CamliType] = m
		}
		m[bm.Ref] = &bm
	}
	return nil
}

func (c *Corpus) mergeSignerKeyIdRow(k, v string) error {
	br, ok := blob.Parse(strings.TrimPrefix(k, "signerkeyid:"))
	if !ok {
		return fmt.Errorf("bogus signerid row: %q -> %q", k, v)
	}
	c.keyId[br] = v
	return nil
}

// str returns s, interned.
func (c *Corpus) str(s string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.strLocked(s)
}

// strLocked returns s, interned.
func (c *Corpus) strLocked(s string) string {
	if s == "" {
		return ""
	}
	if s, ok := c.strs[s]; ok {
		return s
	}
	if c.strs == nil {
		c.strs = make(map[string]string)
	}
	c.strs[s] = s
	return s
}

// *********** Reading from the corpus

// EnumerateCamliBlobs sends just camlistore meta blobs to ch.
// If camType is empty, all camlistore blobs are sent, otherwise it specifies
// the camliType to send.
// ch is closed at the end. It never returns an error.
func (c *Corpus) EnumerateCamliBlobs(camType string, ch chan<- camtypes.BlobMeta) error {
	defer close(ch)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for t, m := range c.camBlobs {
		if camType != "" && camType != t {
			continue
		}
		for _, bm := range m {
			ch <- *bm
		}
	}
	return nil
}

func (c *Corpus) EnumerateBlobMeta(ch chan<- camtypes.BlobMeta) error {
	defer close(ch)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, bm := range c.blobs {
		ch <- *bm
	}
	return nil
}

func (c *Corpus) GetBlobMeta(br blob.Ref) (camtypes.BlobMeta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bm, ok := c.blobs[br]
	if !ok {
		return camtypes.BlobMeta{}, os.ErrNotExist
	}
	return *bm, nil
}

func (c *Corpus) KeyId(signer blob.Ref) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if v, ok := c.keyId[signer]; ok {
		return v, nil
	}
	return "", ErrNotFound
}
