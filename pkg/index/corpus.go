package index

import (
	"sync"

	"camlistore.org/pkg/blob"
)

// Corpus is an in-memory summary of all of a user's blobs' metadata.
type Corpus struct {
	mu    sync.RWMutex
	strs  map[string]string // interned strings
	blobs map[blob.Ref]BlobMeta
	// TODO: add GoLLRB to third_party; keep sorted BlobMeta
	files      map[blob.Ref]*FileMeta
	permanodes map[blob.Ref]*PermanodeMeta
	deletedBy  map[blob.Ref]blob.Ref // key is deleted by value
}

type BlobMeta struct {
	Size      int
	CamliType string
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

func NewCorpusFromStorage(s Storage) (*Corpus, error) {
	panic("TODO")
}

func (x *Index) KeepInMemory() (*Corpus, error) {
	var err error
	x.corpus, err = NewCorpusFromStorage(x.s)
	return x.corpus, err
}
