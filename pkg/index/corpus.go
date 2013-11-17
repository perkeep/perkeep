package index

import (
	"errors"
	"sync"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types/camtypes"
)

// Corpus is an in-memory summary of all of a user's blobs' metadata.
type Corpus struct {
	mu    sync.RWMutex
	strs  map[string]string // interned strings
	blobs map[blob.Ref]camtypes.BlobMeta
	// TODO: add GoLLRB to third_party; keep sorted BlobMeta
	files      map[blob.Ref]*FileMeta
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
		blobs:      make(map[blob.Ref]camtypes.BlobMeta),
		files:      make(map[blob.Ref]*FileMeta),
		permanodes: make(map[blob.Ref]*PermanodeMeta),
		deletedBy:  make(map[blob.Ref]blob.Ref),
	}
}

func NewCorpusFromStorage(s Storage) (*Corpus, error) {
	if s == nil {
		return nil, errors.New("storage is nil")
	}
	c := newCorpus()
	err := enumerateBlobMeta(s, func(bm camtypes.BlobMeta) error {
		bm.CamliType = c.strLocked(bm.CamliType)
		c.blobs[bm.Ref] = bm
		// TODO: populate blobref intern table
		return nil
	})
	if err != nil {
		return nil, err
	}
	// TODO: slurp more from storage
	return c, nil
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
	s.t.Fatalf("unexpected index.Storage.Get(%q) called", key)
	panic("")
}

func (s crashStorage) Find(key string) Iterator {
	s.t.Fatalf("unexpected index.Storage.Find(%q) called", key)
	panic("")
}

func (c *Corpus) EnumerateBlobMeta(ch chan<- camtypes.BlobMeta) error {
	defer close(ch)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, bm := range c.blobs {
		ch <- bm
	}
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
