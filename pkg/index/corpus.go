package index

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/strutil"
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
	files      map[blob.Ref]camtypes.FileInfo
	permanodes map[blob.Ref]*PermanodeMeta

	// TOOD: use deletedCache instead?
	deletedBy map[blob.Ref]blob.Ref // key is deleted by value

	// scratch string slice
	ss []string
}

type PermanodeMeta struct {
	// TODO: OwnerKeyId string
	Claims []camtypes.Claim
}

func newCorpus() *Corpus {
	return &Corpus{
		blobs:      make(map[blob.Ref]*camtypes.BlobMeta),
		camBlobs:   make(map[string]map[blob.Ref]*camtypes.BlobMeta),
		files:      make(map[blob.Ref]camtypes.FileInfo),
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

var corpusMergeFunc = map[string]func(c *Corpus, k, v string) error{
	"have":        nil, // redundant with "meta"
	"meta":        (*Corpus).mergeMetaRow,
	"signerkeyid": (*Corpus).mergeSignerKeyIdRow,
	"claim":       (*Corpus).mergeClaimRow,
	"fileinfo":    (*Corpus).mergeFileInfoRow,
	"filetimes":   (*Corpus).mergeFileTimesRow,
}

func (c *Corpus) scanFromStorage(s Storage) error {
	for _, prefix := range []string{
		"meta:",
		"signerkeyid:",
		"claim|",
		"fileinfo|",
		"filetimes|",
	} {
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
	bm.CamliType = c.str(bm.CamliType)
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

func (c *Corpus) mergeClaimRow(k, v string) error {
	cl, ok := kvClaim(k, v)
	if !ok || !cl.Permanode.Valid() {
		return fmt.Errorf("bogus claim row: %q -> %q", k, v)
	}
	cl.Type = c.str(cl.Type)
	cl.Attr = c.str(cl.Attr)
	cl.Value = c.str(cl.Value) // less likely to intern, but some (tags) do

	pn := cl.Permanode
	pm, ok := c.permanodes[pn]
	if !ok {
		pm = new(PermanodeMeta)
		c.permanodes[pn] = pm
	}
	pm.Claims = append(pm.Claims, cl)
	return nil
}

func (c *Corpus) mergeFileInfoRow(k, v string) error {
	// fileinfo|sha1-579f7f246bd420d486ddeb0dadbb256cfaf8bf6b" "5|some-stuff.txt|"
	c.ss = strutil.AppendSplitN(c.ss[:0], k, "|", 2)
	if len(c.ss) != 2 {
		return fmt.Errorf("unexpected fileinfo key %q", k)
	}
	br, ok := blob.Parse(c.ss[1])
	if !ok {
		return fmt.Errorf("unexpected fileinfo blobref in key %q", k)
	}
	c.ss = strutil.AppendSplitN(c.ss[:0], v, "|", 3)
	if len(c.ss) != 3 {
		return fmt.Errorf("unexpected fileinfo value %q", k)
	}
	size, err := strconv.ParseInt(c.ss[0], 10, 64)
	if err != nil {
		return fmt.Errorf("unexpected fileinfo value %q", k)
	}
	c.mutateFileInfo(br, func(fi *camtypes.FileInfo) {
		fi.Size = size
		fi.FileName = c.str(c.ss[1])
		fi.MIMEType = c.str(c.ss[2])
	})
	return nil
}

func (c *Corpus) mergeFileTimesRow(k, v string) error {
	if v == "" {
		return nil
	}
	// "filetimes|sha1-579f7f246bd420d486ddeb0dadbb256cfaf8bf6b" "1970-01-01T00%3A02%3A03Z"
	c.ss = strutil.AppendSplitN(c.ss[:0], k, "|", 2)
	if len(c.ss) != 2 {
		return fmt.Errorf("unexpected filetimes key %q", k)
	}
	br, ok := blob.Parse(c.ss[1])
	if !ok {
		return fmt.Errorf("unexpected filetimes blobref in key %q", k)
	}
	c.ss = strutil.AppendSplitN(c.ss[:0], v, ",", -1)
	times := c.ss
	c.mutateFileInfo(br, func(fi *camtypes.FileInfo) {
		updateFileInfoTimes(fi, times)
	})
	return nil
}

func (c *Corpus) mutateFileInfo(br blob.Ref, fn func(*camtypes.FileInfo)) {
	fi := c.files[br] // use zero value if not present
	fn(&fi)
	c.files[br] = fi
}

// str returns s, interned.
func (c *Corpus) str(s string) string {
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

func (c *Corpus) isDeletedLocked(br blob.Ref) bool {
	// TODO: implement
	return false
}

func (c *Corpus) AppendClaims(dst []camtypes.Claim, permaNode blob.Ref,
	signerFilter blob.Ref,
	attrFilter string) ([]camtypes.Claim, error) {
	needSort := false
	defer func() {
		if needSort {
			// TODO: schedule sort of these.  It's not
			// required by the interface, but we know our
			// caller will want to do it, so make their
			// job easier and give it to them
			// pre-sorted. We do it here rather than
			// during on-start scanning to save CPU, to do
			// it fewer times per permanode.
		}
	}()
	c.mu.RLock()
	defer c.mu.RUnlock()
	pm, ok := c.permanodes[permaNode]
	if !ok {
		return nil, nil
	}
	for _, cl := range pm.Claims {
		if c.isDeletedLocked(cl.BlobRef) {
			continue
		}
		if signerFilter.Valid() && cl.Signer != signerFilter {
			continue
		}
		if attrFilter != "" && cl.Attr != attrFilter {
			continue
		}
		dst = append(dst, cl)
	}
	return dst, nil
}

func (c *Corpus) GetFileInfo(fileRef blob.Ref) (fi camtypes.FileInfo, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fi, ok := c.files[fileRef]
	if !ok {
		err = os.ErrNotExist
	}
	return
}
