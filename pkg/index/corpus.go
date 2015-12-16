/*
Copyright 2013 The Camlistore Authors

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

package index

import (
	"bytes"
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
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/types/camtypes"
	"golang.org/x/net/context"

	"go4.org/strutil"
	"go4.org/syncutil"
)

// Corpus is an in-memory summary of all of a user's blobs' metadata.
type Corpus struct {
	mu sync.RWMutex
	//mu syncutil.RWMutexTracker // when debugging

	// building is true at start while scanning all rows in the
	// index.  While building, certain invariants (like things
	// being sorted) can be temporarily violated and fixed at the
	// end of scan.
	building bool

	// gen is incremented on every blob received.
	// It's used as a query cache invalidator.
	gen int64

	strs      map[string]string   // interned strings
	brOfStr   map[string]blob.Ref // blob.Parse fast path
	brInterns int64               // blob.Ref -> blob.Ref, via br method

	blobs        map[blob.Ref]*camtypes.BlobMeta
	sumBlobBytes int64

	// camlBlobs maps from camliType ("file") to blobref to the meta.
	// The value is the same one in blobs.
	camBlobs map[string]map[blob.Ref]*camtypes.BlobMeta

	// TODO: add GoLLRB to third_party; keep sorted BlobMeta
	keyId        map[blob.Ref]string
	files        map[blob.Ref]camtypes.FileInfo
	permanodes   map[blob.Ref]*PermanodeMeta
	imageInfo    map[blob.Ref]camtypes.ImageInfo // keyed by fileref (not wholeref)
	fileWholeRef map[blob.Ref]blob.Ref           // fileref -> its wholeref (TODO: multi-valued?)
	gps          map[blob.Ref]latLong            // wholeRef -> GPS coordinates

	// edge tracks "forward" edges. e.g. from a directory's static-set to
	// its members. Permanodes' camliMembers aren't tracked, since they
	// can be obtained from permanodes.Claims.
	// TODO: implement
	edge map[blob.Ref][]edge

	// edgeBack tracks "backward" edges. e.g. from a file back to
	// any directories it's part of.
	// The map is from target (e.g. file) => owner (static-set).
	// This only tracks static data structures, not permanodes.
	// TODO: implement
	edgeBack map[blob.Ref]map[blob.Ref]bool

	// claimBack allows hopping backwards from a Claim's Value
	// when the Value is a blobref.  It allows, for example,
	// finding the parents of camliMember claims.  If a permanode
	// parent set A has a camliMembers B and C, it allows finding
	// A from either B and C.
	// The slice is not sorted.
	claimBack map[blob.Ref][]*camtypes.Claim

	// TOOD: use deletedCache instead?
	deletedBy map[blob.Ref]blob.Ref // key is deleted by value
	// deletes tracks deletions of claims and permanodes. The key is
	// the blobref of a claim or permanode. The values, sorted newest first,
	// contain the blobref of the claim responsible for the deletion, as well
	// as the date when that deletion happened.
	deletes map[blob.Ref][]deletion

	mediaTags map[blob.Ref]map[string]string // wholeref -> "album" -> "foo"

	permanodesByTime    *lazySortedPermanodes // cache of permanodes sorted by creation time.
	permanodesByModtime *lazySortedPermanodes // cache of permanodes sorted by modtime.

	// scratch string slice
	ss []string
}

type latLong struct {
	lat, long float64
}

// RLock locks the Corpus for reads. It must be used for any "Locked" methods.
func (c *Corpus) RLock() { c.mu.RLock() }

// RUnlock unlocks the Corpus for reads.
func (c *Corpus) RUnlock() { c.mu.RUnlock() }

// IsDeleted reports whether the provided blobref (of a permanode or claim) should be considered deleted.
func (c *Corpus) IsDeleted(br blob.Ref) bool {
	c.RLock()
	defer c.RUnlock()
	return c.IsDeletedLocked(br)
}

// IsDeletedLocked is the version of IsDeleted that assumes the Corpus is already locked with RLock.
func (c *Corpus) IsDeletedLocked(br blob.Ref) bool {
	for _, v := range c.deletes[br] {
		if !c.IsDeletedLocked(v.deleter) {
			return true
		}
	}
	return false
}

type edge struct {
	edgeType string
	peer     blob.Ref
}

type PermanodeMeta struct {
	// TODO: OwnerKeyId string
	Claims []*camtypes.Claim // sorted by camtypes.ClaimsByDate
}

func newCorpus() *Corpus {
	c := &Corpus{
		blobs:        make(map[blob.Ref]*camtypes.BlobMeta),
		camBlobs:     make(map[string]map[blob.Ref]*camtypes.BlobMeta),
		files:        make(map[blob.Ref]camtypes.FileInfo),
		permanodes:   make(map[blob.Ref]*PermanodeMeta),
		imageInfo:    make(map[blob.Ref]camtypes.ImageInfo),
		deletedBy:    make(map[blob.Ref]blob.Ref),
		keyId:        make(map[blob.Ref]string),
		brOfStr:      make(map[string]blob.Ref),
		fileWholeRef: make(map[blob.Ref]blob.Ref),
		gps:          make(map[blob.Ref]latLong),
		mediaTags:    make(map[blob.Ref]map[string]string),
		deletes:      make(map[blob.Ref][]deletion),
		claimBack:    make(map[blob.Ref][]*camtypes.Claim),
	}
	c.permanodesByModtime = &lazySortedPermanodes{
		c:      c,
		pnTime: c.PermanodeModtimeLocked,
	}
	c.permanodesByTime = &lazySortedPermanodes{
		c:      c,
		pnTime: c.PermanodeAnyTimeLocked,
	}
	return c
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

func (crashStorage) Find(start, end string) sorted.Iterator {
	panic(fmt.Sprintf("unexpected KeyValue.Find(%q, %q) called", start, end))
}

// *********** Updating the corpus

var corpusMergeFunc = map[string]func(c *Corpus, k, v []byte) error{
	"have":            nil, // redundant with "meta"
	"recpn":           nil, // unneeded.
	"meta":            (*Corpus).mergeMetaRow,
	"signerkeyid":     (*Corpus).mergeSignerKeyIdRow,
	"claim":           (*Corpus).mergeClaimRow,
	"fileinfo":        (*Corpus).mergeFileInfoRow,
	"filetimes":       (*Corpus).mergeFileTimesRow,
	"imagesize":       (*Corpus).mergeImageSizeRow,
	"wholetofile":     (*Corpus).mergeWholeToFileRow,
	"exifgps":         (*Corpus).mergeEXIFGPSRow,
	"exiftag":         nil, // not using any for now
	"signerattrvalue": nil, // ignoring for now
	"mediatag":        (*Corpus).mergeMediaTag,
}

func memstats() *runtime.MemStats {
	ms := new(runtime.MemStats)
	runtime.GC()
	runtime.ReadMemStats(ms)
	return ms
}

var logCorpusStats = true // set to false in tests

var slurpPrefixes = []string{
	"meta:", // must be first
	"signerkeyid:",
	"claim|",
	"fileinfo|",
	"filetimes|",
	"imagesize|",
	"wholetofile|",
	"exifgps|",
	"mediatag|",
}

// Key types (without trailing punctuation) that we slurp to memory at start.
var slurpedKeyType = make(map[string]bool)

func init() {
	for _, prefix := range slurpPrefixes {
		slurpedKeyType[typeOfKey(prefix)] = true
	}
}

func (c *Corpus) scanFromStorage(s sorted.KeyValue) error {
	c.building = true

	var ms0 *runtime.MemStats
	if logCorpusStats {
		ms0 = memstats()
		log.Printf("Slurping corpus to memory from index...")
		log.Printf("Slurping corpus to memory from index... (1/%d: meta rows)", len(slurpPrefixes))
	}

	// We do the "meta" rows first, before the prefixes below, because it
	// populates the blobs map (used for blobref interning) and the camBlobs
	// map (used for hinting the size of other maps)
	if err := c.scanPrefix(s, "meta:"); err != nil {
		return err
	}
	c.files = make(map[blob.Ref]camtypes.FileInfo, len(c.camBlobs["file"]))
	c.permanodes = make(map[blob.Ref]*PermanodeMeta, len(c.camBlobs["permanode"]))
	cpu0 := osutil.CPUUsage()

	var grp syncutil.Group
	for i, prefix := range slurpPrefixes[1:] {
		if logCorpusStats {
			log.Printf("Slurping corpus to memory from index... (%d/%d: prefix %q)", i+2, len(slurpPrefixes),
				prefix[:len(prefix)-1])
		}
		prefix := prefix
		grp.Go(func() error { return c.scanPrefix(s, prefix) })
	}
	if err := grp.Err(); err != nil {
		return err
	}

	// Post-load optimizations and restoration of invariants.
	for _, pm := range c.permanodes {
		// Restore invariants violated during building:
		sort.Sort(camtypes.ClaimPtrsByDate(pm.Claims))

		// And intern some stuff.
		for _, cl := range pm.Claims {
			cl.BlobRef = c.br(cl.BlobRef)
			cl.Signer = c.br(cl.Signer)
			cl.Permanode = c.br(cl.Permanode)
			cl.Target = c.br(cl.Target)
		}

	}
	c.brOfStr = nil // drop this now.
	c.building = false
	// log.V(1).Printf("interned blob.Ref = %d", c.brInterns)

	if err := c.initDeletes(s); err != nil {
		return fmt.Errorf("Could not populate the corpus deletes: %v", err)
	}

	if logCorpusStats {
		cpu := osutil.CPUUsage() - cpu0
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
		log.Printf("Corpus scanning CPU usage: %v", cpu)
	}

	return nil
}

// initDeletes populates the corpus deletes from the delete entries in s.
func (c *Corpus) initDeletes(s sorted.KeyValue) (err error) {
	it := queryPrefix(s, keyDeleted)
	defer closeIterator(it, &err)
	for it.Next() {
		cl, ok := kvDeleted(it.Key())
		if !ok {
			return fmt.Errorf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key())
		}
		targetDeletions := append(c.deletes[cl.Target],
			deletion{
				deleter: cl.BlobRef,
				when:    cl.Date,
			})
		sort.Sort(sort.Reverse(byDeletionDate(targetDeletions)))
		c.deletes[cl.Target] = targetDeletions
	}
	return err
}

func (c *Corpus) numSchemaBlobsLocked() (n int64) {
	for _, m := range c.camBlobs {
		n += int64(len(m))
	}
	return
}

func (c *Corpus) scanPrefix(s sorted.KeyValue, prefix string) (err error) {
	typeKey := typeOfKey(prefix)
	fn, ok := corpusMergeFunc[typeKey]
	if !ok {
		panic("No registered merge func for prefix " + prefix)
	}

	n, t0 := 0, time.Now()
	it := queryPrefixString(s, prefix)
	defer closeIterator(it, &err)
	for it.Next() {
		n++
		if n == 1 {
			// Let the query be sent off and responses start flowing in before
			// we take the lock. And if no rows: no lock.
			c.mu.Lock()
			defer c.mu.Unlock()
		}
		if err := fn(c, it.KeyBytes(), it.ValueBytes()); err != nil {
			return err
		}
	}
	if logCorpusStats {
		d := time.Since(t0)
		log.Printf("Scanned prefix %q: %d rows, %v", prefix[:len(prefix)-1], n, d)
	}
	return nil
}

func (c *Corpus) addBlob(br blob.Ref, mm *mutationMap) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.blobs[br]; dup {
		return nil
	}
	c.gen++
	for k, v := range mm.kv {
		kt := typeOfKey(k)
		if !slurpedKeyType[kt] {
			continue
		}
		if err := corpusMergeFunc[kt](c, []byte(k), []byte(v)); err != nil {
			return err
		}
	}
	for _, cl := range mm.deletes {
		if err := c.updateDeletes(cl); err != nil {
			return fmt.Errorf("Could not update the deletes cache after deletion from %v: %v", cl, err)
		}
	}
	return nil
}

// updateDeletes updates the corpus deletes with the delete claim deleteClaim.
// deleteClaim is trusted to be a valid delete Claim.
func (c *Corpus) updateDeletes(deleteClaim schema.Claim) error {
	target := c.br(deleteClaim.Target())
	deleter := deleteClaim.Blob()
	when, err := deleter.ClaimDate()
	if err != nil {
		return fmt.Errorf("Could not get date of delete claim %v: %v", deleteClaim, err)
	}
	del := deletion{
		deleter: c.br(deleter.BlobRef()),
		when:    when,
	}
	for _, v := range c.deletes[target] {
		if v == del {
			return nil
		}
	}
	targetDeletions := append(c.deletes[target], del)
	sort.Sort(sort.Reverse(byDeletionDate(targetDeletions)))
	c.deletes[target] = targetDeletions
	return nil
}

func (c *Corpus) mergeMetaRow(k, v []byte) error {
	bm, ok := kvBlobMeta_bytes(k, v)
	if !ok {
		return fmt.Errorf("bogus meta row: %q -> %q", k, v)
	}
	return c.mergeBlobMeta(bm)
}

func (c *Corpus) mergeBlobMeta(bm camtypes.BlobMeta) error {
	if _, dup := c.blobs[bm.Ref]; dup {
		panic("dup blob seen")
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

func (c *Corpus) mergeSignerKeyIdRow(k, v []byte) error {
	br, ok := blob.ParseBytes(k[len("signerkeyid:"):])
	if !ok {
		return fmt.Errorf("bogus signerid row: %q -> %q", k, v)
	}
	c.keyId[br] = string(v)
	return nil
}

func (c *Corpus) mergeClaimRow(k, v []byte) error {
	// TODO: update kvClaim to take []byte instead of string
	cl, ok := kvClaim(string(k), string(v), c.blobParse)
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
	pm.Claims = append(pm.Claims, &cl)
	if !c.building {
		// Unless we're still starting up (at which we sort at
		// the end instead), keep this sorted.
		sort.Sort(camtypes.ClaimPtrsByDate(pm.Claims))
	}

	if vbr, ok := blob.Parse(cl.Value); ok {
		c.claimBack[vbr] = append(c.claimBack[vbr], &cl)
	}
	return nil
}

func (c *Corpus) mergeFileInfoRow(k, v []byte) error {
	// fileinfo|sha1-579f7f246bd420d486ddeb0dadbb256cfaf8bf6b" "5|some-stuff.txt|"
	pipe := bytes.IndexByte(k, '|')
	if pipe < 0 {
		return fmt.Errorf("unexpected fileinfo key %q", k)
	}
	br, ok := blob.ParseBytes(k[pipe+1:])
	if !ok {
		return fmt.Errorf("unexpected fileinfo blobref in key %q", k)
	}

	// TODO: could at least use strutil.ParseUintBytes to not stringify and retain
	// the length bytes of v.
	c.ss = strutil.AppendSplitN(c.ss[:0], string(v), "|", 4)
	if len(c.ss) != 3 && len(c.ss) != 4 {
		return fmt.Errorf("unexpected fileinfo value %q", v)
	}
	size, err := strconv.ParseInt(c.ss[0], 10, 64)
	if err != nil {
		return fmt.Errorf("unexpected fileinfo value %q", v)
	}
	var wholeRef blob.Ref
	if len(c.ss) == 4 && c.ss[3] != "" { // checking for "" because of special files such as symlinks.
		var ok bool
		wholeRef, ok = blob.Parse(urld(c.ss[3]))
		if !ok {
			return fmt.Errorf("invalid wholeRef blobref in value %q for fileinfo key %q", v, k)
		}
	}
	c.mutateFileInfo(br, func(fi *camtypes.FileInfo) {
		fi.Size = size
		fi.FileName = c.str(urld(c.ss[1]))
		fi.MIMEType = c.str(urld(c.ss[2]))
		fi.WholeRef = wholeRef
	})
	return nil
}

func (c *Corpus) mergeFileTimesRow(k, v []byte) error {
	if len(v) == 0 {
		return nil
	}
	// "filetimes|sha1-579f7f246bd420d486ddeb0dadbb256cfaf8bf6b" "1970-01-01T00%3A02%3A03Z"
	pipe := bytes.IndexByte(k, '|')
	if pipe < 0 {
		return fmt.Errorf("unexpected fileinfo key %q", k)
	}
	br, ok := blob.ParseBytes(k[pipe+1:])
	if !ok {
		return fmt.Errorf("unexpected filetimes blobref in key %q", k)
	}
	c.ss = strutil.AppendSplitN(c.ss[:0], urld(string(v)), ",", -1)
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

func (c *Corpus) mergeImageSizeRow(k, v []byte) error {
	br, okk := blob.ParseBytes(k[len("imagesize|"):])
	ii, okv := kvImageInfo(v)
	if !okk || !okv {
		return fmt.Errorf("bogus row %q = %q", k, v)
	}
	br = c.br(br)
	c.imageInfo[br] = ii
	return nil
}

// "wholetofile|sha1-17b53c7c3e664d3613dfdce50ef1f2a09e8f04b5|sha1-fb88f3eab3acfcf3cfc8cd77ae4366f6f975d227" -> "1"
func (c *Corpus) mergeWholeToFileRow(k, v []byte) error {
	pair := k[len("wholetofile|"):]
	pipe := bytes.IndexByte(pair, '|')
	if pipe < 0 {
		return fmt.Errorf("bogus row %q = %q", k, v)
	}
	wholeRef, ok1 := blob.ParseBytes(pair[:pipe])
	fileRef, ok2 := blob.ParseBytes(pair[pipe+1:])
	if !ok1 || !ok2 {
		return fmt.Errorf("bogus row %q = %q", k, v)
	}
	c.fileWholeRef[fileRef] = wholeRef
	return nil
}

// "mediatag|sha1-2b219be9d9691b4f8090e7ee2690098097f59566|album" = "Some+Album+Name"
func (c *Corpus) mergeMediaTag(k, v []byte) error {
	f := strings.Split(string(k), "|")
	if len(f) != 3 {
		return fmt.Errorf("unexpected key %q", k)
	}
	wholeRef, ok := blob.Parse(f[1])
	if !ok {
		return fmt.Errorf("failed to parse wholeref from key %q", k)
	}
	tm, ok := c.mediaTags[wholeRef]
	if !ok {
		tm = make(map[string]string)
		c.mediaTags[wholeRef] = tm
	}
	tm[c.str(f[2])] = c.str(urld(string(v)))
	return nil
}

// "exifgps|sha1-17b53c7c3e664d3613dfdce50ef1f2a09e8f04b5" -> "-122.39897155555556|37.61952208333334"
func (c *Corpus) mergeEXIFGPSRow(k, v []byte) error {
	wholeRef, ok := blob.ParseBytes(k[len("exifgps|"):])
	pipe := bytes.IndexByte(v, '|')
	if pipe < 0 || !ok {
		return fmt.Errorf("bogus row %q = %q", k, v)
	}
	lat, err := strconv.ParseFloat(string(v[:pipe]), 64)
	long, err1 := strconv.ParseFloat(string(v[pipe+1:]), 64)
	if err != nil || err1 != nil {
		return fmt.Errorf("bogus row %q = %q", k, v)
	}
	c.gps[wholeRef] = latLong{lat, long}
	return nil
}

// This enables the blob.Parse fast path cache, which reduces CPU (via
// reduced GC from new garbage), but increases memory usage, even
// though it shouldn't.  The GC should fully discard the brOfStr map
// (which we nil out at the end of parsing), but the Go GC doesn't
// seem to clear it all.
// TODO: investigate / file bugs.
const useBlobParseCache = false

func (c *Corpus) blobParse(v string) (br blob.Ref, ok bool) {
	if useBlobParseCache {
		br, ok = c.brOfStr[v]
		if ok {
			return
		}
	}
	return blob.Parse(v)
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

// EnumerateCamliBlobsLocked sends just camlistore meta blobs to ch.
//
// The Corpus must already be locked with RLock.
//
// If camType is empty, all camlistore blobs are sent, otherwise it specifies
// the camliType to send.
// ch is closed at the end. The err will either be nil or context.Canceled.
func (c *Corpus) EnumerateCamliBlobsLocked(ctx context.Context, camType string, ch chan<- camtypes.BlobMeta) error {
	defer close(ch)
	for t, m := range c.camBlobs {
		if camType != "" && camType != t {
			continue
		}
		for _, bm := range m {
			select {
			case ch <- *bm:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// EnumerateBlobMetaLocked sends all known blobs to ch, or until the context is canceled.
//
// The Corpus must already be locked with RLock.
func (c *Corpus) EnumerateBlobMetaLocked(ctx context.Context, ch chan<- camtypes.BlobMeta) error {
	defer close(ch)
	for _, bm := range c.blobs {
		select {
		case ch <- *bm:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// pnAndTime is a value type wrapping a permanode blobref and its modtime.
// It's used by EnumeratePermanodesLastModified and EnumeratePermanodesCreated.
type pnAndTime struct {
	pn blob.Ref
	t  time.Time
}

type byPermanodeTime []pnAndTime

func (s byPermanodeTime) Len() int      { return len(s) }
func (s byPermanodeTime) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s byPermanodeTime) Less(i, j int) bool {
	if s[i].t.Equal(s[j].t) {
		return s[i].pn.Less(s[j].pn)
	}
	return s[i].t.Before(s[j].t)
}

type lazySortedPermanodes struct {
	c      *Corpus
	pnTime func(blob.Ref) (time.Time, bool) // returns permanode's time (if any) to sort on

	mu                  sync.Mutex  // guards sortedCache and ofGen
	sortedCache         []pnAndTime // nil if invalidated
	sortedCacheReversed []pnAndTime // nil if invalidated
	ofGen               int64       // the Corpus.gen from which sortedCache was built
}

func reversedCopy(original []pnAndTime) []pnAndTime {
	l := len(original)
	reversed := make([]pnAndTime, l)
	for k, v := range original {
		reversed[l-1-k] = v
	}
	return reversed
}

// The Corpus must already be locked with RLock.
func (lsp *lazySortedPermanodes) sorted(reverse bool) []pnAndTime {
	lsp.mu.Lock()
	defer lsp.mu.Unlock()
	if lsp.ofGen == lsp.c.gen {
		// corpus hasn't changed -> caches are still valid, if they exist.
		if reverse {
			if lsp.sortedCacheReversed != nil {
				return lsp.sortedCacheReversed
			}
			if lsp.sortedCache != nil {
				// using sortedCache to quickly build sortedCacheReversed
				lsp.sortedCacheReversed = reversedCopy(lsp.sortedCache)
				return lsp.sortedCacheReversed
			}
		}
		if !reverse {
			if lsp.sortedCache != nil {
				return lsp.sortedCache
			}
			if lsp.sortedCacheReversed != nil {
				// using sortedCacheReversed to quickly build sortedCache
				lsp.sortedCache = reversedCopy(lsp.sortedCacheReversed)
				return lsp.sortedCache
			}
		}
	}
	// invalidate the caches
	lsp.sortedCache = nil
	lsp.sortedCacheReversed = nil
	pns := make([]pnAndTime, 0, len(lsp.c.permanodes))
	for pn := range lsp.c.permanodes {
		if lsp.c.IsDeletedLocked(pn) {
			continue
		}
		if pt, ok := lsp.pnTime(pn); ok {
			pns = append(pns, pnAndTime{pn, pt})
		}
	}
	// and rebuild one of them
	if reverse {
		sort.Sort(sort.Reverse(byPermanodeTime(pns)))
		lsp.sortedCacheReversed = pns
	} else {
		sort.Sort(byPermanodeTime(pns))
		lsp.sortedCache = pns
	}
	lsp.ofGen = lsp.c.gen
	return pns
}

// corpus must be (read) locked.
func (c *Corpus) sendPermanodes(ctx context.Context, ch chan<- camtypes.BlobMeta, pns []pnAndTime) error {
	for _, cand := range pns {
		bm := c.blobs[cand.pn]
		if bm == nil {
			continue
		}
		select {
		case ch <- *bm:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// EnumeratePermanodesLastModified sends all permanodes, sorted by most recently modified first, to ch,
// or until ctx is done.
//
// The Corpus must already be locked with RLock.
func (c *Corpus) EnumeratePermanodesLastModifiedLocked(ctx context.Context, ch chan<- camtypes.BlobMeta) error {
	defer close(ch)

	return c.sendPermanodes(ctx, ch, c.permanodesByModtime.sorted(true))
}

// EnumeratePermanodesCreatedLocked sends all permanodes to ch, or until ctx is done.
// They are sorted using the contents creation date if any, the permanode modtime
// otherwise, and in the order specified by newestFirst.
//
// The Corpus must already be locked with RLock.
func (c *Corpus) EnumeratePermanodesCreatedLocked(ctx context.Context, ch chan<- camtypes.BlobMeta, newestFirst bool) error {
	defer close(ch)

	return c.sendPermanodes(ctx, ch, c.permanodesByTime.sorted(newestFirst))
}

func (c *Corpus) GetBlobMeta(br blob.Ref) (camtypes.BlobMeta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.GetBlobMetaLocked(br)
}

func (c *Corpus) GetBlobMetaLocked(br blob.Ref) (camtypes.BlobMeta, error) {
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

var (
	errUnsupportedNodeType = errors.New("unsupported nodeType")
	errNoNodeAttr          = errors.New("attribute not found")
)

func (c *Corpus) pnTimeAttrLocked(pn blob.Ref, attr string) (t time.Time, ok bool) {
	if v := c.PermanodeAttrValueLocked(pn, attr, time.Time{}, blob.Ref{}); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
	}
	return
}

// PermanodeTimeLocked returns the time of the content in permanode.
func (c *Corpus) PermanodeTimeLocked(pn blob.Ref) (t time.Time, ok bool) {
	// TODO(bradfitz): keep this time property cached on the permanode / files
	// TODO(bradfitz): finish implmenting all these

	// Priorities:
	// -- Permanode explicit "camliTime" property
	// -- EXIF GPS time
	// -- Exif camera time - this one is actually already in the FileInfo,
	// because we use schema.FileTime (which returns the EXIF time, if available)
	// to index the time when receiving a file.
	// -- File time
	// -- File modtime
	// -- camliContent claim set time

	if t, ok = c.pnTimeAttrLocked(pn, nodeattr.StartDate); ok {
		return
	}
	if t, ok = c.pnTimeAttrLocked(pn, nodeattr.DateCreated); ok {
		return
	}
	var fi camtypes.FileInfo
	ccRef, ccTime, ok := c.pnCamliContentLocked(pn)
	if ok {
		fi, _ = c.files[ccRef]
	}
	if fi.Time != nil {
		return time.Time(*fi.Time), true
	}

	if t, ok = c.pnTimeAttrLocked(pn, nodeattr.DatePublished); ok {
		return
	}
	if t, ok = c.pnTimeAttrLocked(pn, nodeattr.DateModified); ok {
		return
	}
	if fi.ModTime != nil {
		return time.Time(*fi.ModTime), true
	}
	if ok {
		return ccTime, true
	}
	return time.Time{}, false
}

// PermanodeAnyTimeLocked returns the time that best qualifies the permanode.
// It tries content-specific times first, the permanode modtime otherwise.
func (c *Corpus) PermanodeAnyTimeLocked(pn blob.Ref) (t time.Time, ok bool) {
	if t, ok := c.PermanodeTimeLocked(pn); ok {
		return t, ok
	}
	return c.PermanodeModtimeLocked(pn)
}

func (c *Corpus) pnCamliContentLocked(pn blob.Ref) (cc blob.Ref, t time.Time, ok bool) {
	// TODO(bradfitz): keep this property cached
	pm, ok := c.permanodes[pn]
	if !ok {
		return
	}
	for _, cl := range pm.Claims {
		if cl.Attr != "camliContent" {
			continue
		}
		// TODO: pass down the 'PermanodeConstraint.At' parameter, and then do: if cl.Date.After(at) { continue }
		switch cl.Type {
		case string(schema.DelAttributeClaim):
			cc = blob.Ref{}
			t = time.Time{}
		case string(schema.SetAttributeClaim):
			cc = blob.ParseOrZero(cl.Value)
			t = cl.Date
		}
	}
	return cc, t, cc.Valid()

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
	return c.PermanodeModtimeLocked(pn)
}

// PermanodeModtimeLocked is like PermanodeModtime but for when the Corpus is
// already locked via RLock.
func (c *Corpus) PermanodeModtimeLocked(pn blob.Ref) (t time.Time, ok bool) {
	pm, ok := c.permanodes[pn]
	if !ok {
		return
	}

	// Note: We intentionally don't try to derive any information
	// (except the owner, elsewhere) from the permanode blob
	// itself. Even though the permanode blob sometimes has the
	// GPG signature time, we intentionally ignore it.
	for _, cl := range pm.Claims {
		if c.IsDeletedLocked(cl.BlobRef) {
			continue
		}
		if cl.Date.After(t) {
			t = cl.Date
		}
	}
	return t, !t.IsZero()
}

// AppendPermanodeAttrValues appends to dst all the values for the attribute
// attr set on permaNode.
// signerFilter is optional.
// dst must start with length 0 (laziness, mostly)
func (c *Corpus) AppendPermanodeAttrValues(dst []string,
	permaNode blob.Ref,
	attr string,
	at time.Time,
	signerFilter blob.Ref) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AppendPermanodeAttrValuesLocked(dst, permaNode, attr, at, signerFilter)
}

// PermanodeAttrValueLocked returns a single-valued attribute or "".
func (c *Corpus) PermanodeAttrValueLocked(permaNode blob.Ref,
	attr string,
	at time.Time,
	signerFilter blob.Ref) string {
	pm, ok := c.permanodes[permaNode]
	if !ok {
		return ""
	}
	if at.IsZero() {
		at = time.Now()
	}
	var v string
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
				v = ""
			} else if v == cl.Value {
				v = ""
			}
		case string(schema.SetAttributeClaim):
			v = cl.Value
		case string(schema.AddAttributeClaim):
			if v == "" {
				v = cl.Value
			}
		}
	}
	return v
}

func (c *Corpus) AppendPermanodeAttrValuesLocked(dst []string,
	permaNode blob.Ref,
	attr string,
	at time.Time,
	signerFilter blob.Ref) []string {
	if len(dst) > 0 {
		panic("len(dst) must be 0")
	}
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
		if c.IsDeletedLocked(cl.BlobRef) {
			continue
		}
		if signerFilter.Valid() && cl.Signer != signerFilter {
			continue
		}
		if attrFilter != "" && cl.Attr != attrFilter {
			continue
		}
		dst = append(dst, *cl)
	}
	return dst, nil
}

func (c *Corpus) GetFileInfo(fileRef blob.Ref) (fi camtypes.FileInfo, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.GetFileInfoLocked(fileRef)
}

func (c *Corpus) GetFileInfoLocked(fileRef blob.Ref) (fi camtypes.FileInfo, err error) {
	fi, ok := c.files[fileRef]
	if !ok {
		err = os.ErrNotExist
	}
	return
}

func (c *Corpus) GetImageInfo(fileRef blob.Ref) (ii camtypes.ImageInfo, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.GetImageInfoLocked(fileRef)
}

func (c *Corpus) GetImageInfoLocked(fileRef blob.Ref) (ii camtypes.ImageInfo, err error) {
	ii, ok := c.imageInfo[fileRef]
	if !ok {
		err = os.ErrNotExist
	}
	return
}

func (c *Corpus) GetMediaTags(fileRef blob.Ref) (map[string]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.GetMediaTagsLocked(fileRef)
}

func (c *Corpus) GetMediaTagsLocked(fileRef blob.Ref) (map[string]string, error) {
	wholeRef, ok := c.fileWholeRef[fileRef]
	if !ok {
		return nil, os.ErrNotExist
	}
	tags, ok := c.mediaTags[wholeRef]
	if !ok {
		return nil, os.ErrNotExist
	}
	return tags, nil
}

func (c *Corpus) GetWholeRefLocked(fileRef blob.Ref) (wholeRef blob.Ref, ok bool) {
	wholeRef, ok = c.fileWholeRef[fileRef]
	return
}

func (c *Corpus) FileLatLongLocked(fileRef blob.Ref) (lat, long float64, ok bool) {
	wholeRef, ok := c.fileWholeRef[fileRef]
	if !ok {
		return
	}
	ll, ok := c.gps[wholeRef]
	if !ok {
		return
	}
	return ll.lat, ll.long, true
}

// zero value of at means current
func (c *Corpus) PermanodeLatLongLocked(pn blob.Ref, at time.Time) (lat, long float64, ok bool) {
	nodeType := c.PermanodeAttrValueLocked(pn, "camliNodeType", at, blob.Ref{})
	if nodeType == "" {
		return
	}
	// TODO: make these pluggable, e.g. registered from an importer or something?
	// How will that work when they're out-of-process?
	if nodeType == "foursquare.com:checkin" {
		venuePn, hasVenue := blob.Parse(c.PermanodeAttrValueLocked(pn, "foursquareVenuePermanode", at, blob.Ref{}))
		if !hasVenue {
			return
		}
		return c.PermanodeLatLongLocked(venuePn, at)
	}
	if nodeType == "foursquare.com:venue" || nodeType == "twitter.com:tweet" {
		var err error
		lat, err = strconv.ParseFloat(c.PermanodeAttrValueLocked(pn, "latitude", at, blob.Ref{}), 64)
		if err != nil {
			return
		}
		long, err = strconv.ParseFloat(c.PermanodeAttrValueLocked(pn, "longitude", at, blob.Ref{}), 64)
		if err != nil {
			return
		}
		return lat, long, true
	}
	return
}

// ForeachClaimBackLocked calls fn for each claim with a value referencing br.
// If at is zero, all claims are yielded.
// If at is non-zero, claims after that point are skipped.
// If fn returns false, iteration ends.
// Iteration is in an undefined order.
func (c *Corpus) ForeachClaimBackLocked(value blob.Ref, at time.Time, fn func(*camtypes.Claim) bool) {
	for _, cl := range c.claimBack[value] {
		if !at.IsZero() && cl.Date.After(at) {
			continue
		}
		if !fn(cl) {
			return
		}
	}
}

// PermanodeHasAttrValueLocked reports whether the permanode pn at
// time at (zero means now) has the given attribute with the given
// value. If the attribute is multi-valued, any may match.
func (c *Corpus) PermanodeHasAttrValueLocked(pn blob.Ref, at time.Time, attr, val string) bool {
	pm, ok := c.permanodes[pn]
	if !ok {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	ret := false
	for _, cl := range pm.Claims {
		if cl.Attr != attr {
			continue
		}
		if cl.Date.After(at) {
			break
		}
		switch cl.Type {
		case string(schema.DelAttributeClaim):
			if cl.Value == "" || cl.Value == val {
				ret = false
			}
		case string(schema.SetAttributeClaim):
			ret = (cl.Value == val)
		case string(schema.AddAttributeClaim):
			if cl.Value == val {
				return true
			}
		}
	}
	return ret
}

// SetVerboseCorpusLogging controls corpus setup verbosity. It's on by default
// but used to disable verbose logging in tests.
func SetVerboseCorpusLogging(v bool) {
	logCorpusStats = v
}
