package index

import (
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/strutil"
	"camlistore.org/pkg/types/camtypes"
)

// Corpus is an in-memory summary of all of a user's blobs' metadata.
type Corpus struct {
	mu sync.RWMutex

	// building is true at start while scanning all rows in the
	// index.  While building, certain invariants (like things
	// being sorted) can be temporarily violated and fixed at the
	// end of scan.
	building bool

	// gen is incremented on every blob received.
	// It's used as a query cache invalidator.
	gen int64

	strs      map[string]string // interned strings
	brInterns int64

	blobs        map[blob.Ref]*camtypes.BlobMeta
	sumBlobBytes int64

	// camlBlobs maps from camliType ("file") to blobref to the meta.
	// The value is the same one in blobs.
	camBlobs map[string]map[blob.Ref]*camtypes.BlobMeta

	// TODO: add GoLLRB to third_party; keep sorted BlobMeta
	keyId      map[blob.Ref]string
	files      map[blob.Ref]camtypes.FileInfo
	permanodes map[blob.Ref]*PermanodeMeta
	imageInfo  map[blob.Ref]camtypes.ImageInfo

	// TOOD: use deletedCache instead?
	deletedBy map[blob.Ref]blob.Ref // key is deleted by value

	// scratch string slice
	ss []string
}

type PermanodeMeta struct {
	// TODO: OwnerKeyId string
	Claims []camtypes.Claim // sorted by camtypes.ClaimsByDate
}

func newCorpus() *Corpus {
	return &Corpus{
		blobs:      make(map[blob.Ref]*camtypes.BlobMeta),
		camBlobs:   make(map[string]map[blob.Ref]*camtypes.BlobMeta),
		files:      make(map[blob.Ref]camtypes.FileInfo),
		permanodes: make(map[blob.Ref]*PermanodeMeta),
		imageInfo:  make(map[blob.Ref]camtypes.ImageInfo),
		deletedBy:  make(map[blob.Ref]blob.Ref),
		keyId:      make(map[blob.Ref]string),
	}
}

func NewCorpusFromStorage(s sorted.KeyValue) (*Corpus, error) {
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
// Storage interface to panic.
func (x *Index) PreventStorageAccessForTesting() {
	x.s = crashStorage{}
}

type crashStorage struct {
	sorted.KeyValue
}

func (crashStorage) Get(key string) (string, error) {
	panic(fmt.Sprintf("unexpected KeyValue.Get(%q) called", key))
}

func (crashStorage) Find(key string) sorted.Iterator {
	panic(fmt.Sprintf("unexpected KeyValue.Find(%q) called", key))
}

// *********** Updating the corpus

var corpusMergeFunc = map[string]func(c *Corpus, k, v string) error{
	"have":        nil, // redundant with "meta"
	"meta":        (*Corpus).mergeMetaRow,
	"signerkeyid": (*Corpus).mergeSignerKeyIdRow,
	"claim":       (*Corpus).mergeClaimRow,
	"fileinfo":    (*Corpus).mergeFileInfoRow,
	"filetimes":   (*Corpus).mergeFileTimesRow,
	"imagesize":   (*Corpus).mergeImageSizeRow,
}

func memstats() *runtime.MemStats {
	ms := new(runtime.MemStats)
	runtime.GC()
	runtime.ReadMemStats(ms)
	return ms
}

func (c *Corpus) scanFromStorage(s sorted.KeyValue) error {
	c.building = true

	ms0 := memstats()

	log.Printf("Slurping corpus to memory from index...")
	prefixes := []string{
		"meta:", // should be first, for blobref interning
		"signerkeyid:",
		"claim|",
		"fileinfo|",
		"filetimes|",
		"imagesize|",
	}

	for i, prefix := range prefixes {
		log.Printf("Slurping corpus to memory from index... (%d/%d: prefix %q)", i+1, len(prefixes), prefix)
		if err := c.scanPrefix(s, prefix); err != nil {
			return err
		}
	}

	// Post-load optimizations and restoration of invariants.
	for _, pm := range c.permanodes {
		// Restore invariants violated during building:
		sort.Sort(camtypes.ClaimsByDate(pm.Claims))

		// And intern some stuff.
		for i := range pm.Claims {
			cl := &pm.Claims[i]
			cl.BlobRef = c.br(cl.BlobRef)
			cl.Signer = c.br(cl.Signer)
			cl.Permanode = c.br(cl.Permanode)
			cl.Target = c.br(cl.Target)
		}

	}
	c.building = false
	// log.V(1).Printf("interned blob.Ref = %d", c.brInterns)

	ms1 := memstats()
	memUsed := ms1.Alloc - ms0.Alloc
	if ms1.Alloc < ms0.Alloc {
		memUsed = 0
	}
	log.Printf("Corpus stats: %.3f MiB mem: %d blobs (%.3f GiB) (%d schema (%d permanode, %d file (%d image), ...)",
		float64(memUsed)/(1<<20),
		len(c.blobs),
		float64(c.sumBlobBytes)/(1<<30),
		c.numSchemaBlobsLocked(),
		len(c.permanodes),
		len(c.files),
		len(c.imageInfo))
	return nil
}

func (c *Corpus) numSchemaBlobsLocked() (n int64) {
	for _, m := range c.camBlobs {
		n += int64(len(m))
	}
	return
}

func (c *Corpus) scanPrefix(s sorted.KeyValue, prefix string) (err error) {
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

func (c *Corpus) addBlob(br blob.Ref, mm *mutationMap) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for k, v := range mm.kv {
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
	if _, dup := c.blobs[bm.Ref]; dup {
		// Um, shouldn't happen.  TODO(bradfitz): is it
		// guaranteed elsewhere that duplicate blobs are never
		// re-indexed? Do we ever make assumptions that it
		// isn't the case? Summing onto sumBlobBytes below
		// here is one such case.
		return nil
	}
	bm.CamliType = c.str(bm.CamliType)

	c.blobs[bm.Ref] = &bm
	c.sumBlobBytes += int64(bm.Size)
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

	pn := c.br(cl.Permanode)
	pm, ok := c.permanodes[pn]
	if !ok {
		pm = new(PermanodeMeta)
		c.permanodes[pn] = pm
	}
	pm.Claims = append(pm.Claims, cl)
	if !c.building {
		// Unless we're still starting up (at which we sort at
		// the end instead), keep this sorted.
		sort.Sort(camtypes.ClaimsByDate(pm.Claims))
	}
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
		fi.FileName = c.str(urld(c.ss[1]))
		fi.MIMEType = c.str(urld(c.ss[2]))
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
	br = c.br(br)
	fi := c.files[br] // use zero value if not present
	fn(&fi)
	c.files[br] = fi
}

func (c *Corpus) mergeImageSizeRow(k, v string) error {
	br, okk := blob.Parse(k[len("imagesize|"):])
	ii, okv := kvImageInfo(v)
	if !okk || !okv {
		return fmt.Errorf("bogus row %q = %q", k, v)
	}
	br = c.br(br)
	c.imageInfo[br] = ii
	return nil
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

// br returns br, interned.
func (c *Corpus) br(br blob.Ref) blob.Ref {
	if bm, ok := c.blobs[br]; ok {
		c.brInterns++
		return bm.Ref
	}
	return br
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
	return "", sorted.ErrNotFound
}

func (c *Corpus) isDeletedLocked(br blob.Ref) bool {
	// TODO: implement
	return false
}

// PermanodeModtime returns the latest modification time of the given
// permanode.
//
// The ok value is true only if the permanode is known and has any
// non-deleted claims. A deleted claim is ignored and neither its
// claim date nor the date of the delete claim affect the modtime of
// the permanode.
func (c *Corpus) PermanodeModtime(pn blob.Ref) (t time.Time, ok bool) {
	// TODO: figure out behavior wrt mutations by different people
	c.mu.RLock()
	defer c.mu.RUnlock()
	pm, ok := c.permanodes[pn]
	if !ok {
		return
	}
	// Note: We intentionally don't try to derive any information
	// (except the owner, elsewhere) from the permanode blob
	// itself. Even though the permanode blob sometimes has the
	// GPG signature time, we intentionally ignore it.
	for _, cl := range pm.Claims {
		if c.isDeletedLocked(cl.BlobRef) {
			continue
		}
		if cl.Date.After(t) {
			t = cl.Date
		}
	}
	return t, !t.IsZero()
}

// signerFilter is optional.
// dst must start with length 0 (laziness, mostly)
func (c *Corpus) AppendPermanodeAttrValues(dst []string,
	permaNode blob.Ref,
	attr string,
	at time.Time,
	signerFilter blob.Ref) []string {
	if len(dst) > 0 {
		panic("len(dst) must be 0")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	pm, ok := c.permanodes[permaNode]
	if !ok {
		return dst
	}
	if at.IsZero() {
		at = time.Now()
	}
	for _, cl := range pm.Claims {
		if cl.Attr != attr || cl.Date.After(at) {
			continue
		}
		if signerFilter.Valid() && signerFilter != cl.Signer {
			continue
		}
		switch cl.Type {
		case string(schema.DelAttributeClaim):
			if cl.Value == "" {
				dst = dst[:0] // delete all
			} else {
				for i := 0; i < len(dst); i++ {
					v := dst[i]
					if v == cl.Value {
						copy(dst[i:], dst[i+1:])
						dst = dst[:len(dst)-1]
						i--
					}
				}
			}
		case string(schema.SetAttributeClaim):
			dst = append(dst[:0], cl.Value)
		case string(schema.AddAttributeClaim):
			dst = append(dst, cl.Value)
		}
	}
	return dst
}

func (c *Corpus) AppendClaims(dst []camtypes.Claim, permaNode blob.Ref,
	signerFilter blob.Ref,
	attrFilter string) ([]camtypes.Claim, error) {
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

func (c *Corpus) GetImageInfo(fileRef blob.Ref) (ii camtypes.ImageInfo, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ii, ok := c.imageInfo[fileRef]
	if !ok {
		err = os.ErrNotExist
	}
	return
}
