/*
Copyright 2013 The Perkeep Authors

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
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"

	"go4.org/strutil"
	"go4.org/syncutil"
)

// Corpus is an in-memory summary of all of a user's blobs' metadata.
//
// A Corpus is not safe for concurrent use. Callers should use Lock or RLock
// on the parent index instead.
type Corpus struct {
	// building is true at start while scanning all rows in the
	// index.  While building, certain invariants (like things
	// being sorted) can be temporarily violated and fixed at the
	// end of scan.
	building bool

	// hasLegacySHA1 reports whether some SHA-1 blobs are indexed. It is set while
	//building the corpus from the initial index scan.
	hasLegacySHA1 bool

	// gen is incremented on every blob received.
	// It's used as a query cache invalidator.
	gen int64

	strs      map[string]string   // interned strings
	brOfStr   map[string]blob.Ref // blob.Parse fast path
	brInterns int64               // blob.Ref -> blob.Ref, via br method

	blobs        map[blob.Ref]*camtypes.BlobMeta
	sumBlobBytes int64

	// camBlobs maps from camliType ("file") to blobref to the meta.
	// The value is the same one in blobs.
	camBlobs map[schema.CamliType]map[blob.Ref]*camtypes.BlobMeta

	// TODO: add GoLLRB to vendor; keep sorted BlobMeta
	keyId signerFromBlobrefMap

	// signerRefs maps a signer GPG ID to all its signer blobs (because different hashes).
	signerRefs   map[string]SignerRefSet
	files        map[blob.Ref]camtypes.FileInfo // keyed by file or directory schema blob
	permanodes   map[blob.Ref]*PermanodeMeta
	imageInfo    map[blob.Ref]camtypes.ImageInfo // keyed by fileref (not wholeref)
	fileWholeRef map[blob.Ref]blob.Ref           // fileref -> its wholeref (TODO: multi-valued?)
	gps          map[blob.Ref]latLong            // wholeRef -> GPS coordinates
	// dirChildren maps a directory to its (direct) children (static-set entries).
	dirChildren map[blob.Ref]map[blob.Ref]struct{}
	// fileParents maps a file or directory to its (direct) parents.
	fileParents map[blob.Ref]map[blob.Ref]struct{}

	// Lack of edge tracking implementation is issue #707
	// (https://github.com/perkeep/perkeep/issues/707)

	// claimBack allows hopping backwards from a Claim's Value
	// when the Value is a blobref.  It allows, for example,
	// finding the parents of camliMember claims.  If a permanode
	// parent set A has a camliMembers B and C, it allows finding
	// A from either B and C.
	// The slice is not sorted.
	claimBack map[blob.Ref][]*camtypes.Claim

	// TODO: use deletedCache instead?
	deletedBy map[blob.Ref]blob.Ref // key is deleted by value
	// deletes tracks deletions of claims and permanodes. The key is
	// the blobref of a claim or permanode. The values, sorted newest first,
	// contain the blobref of the claim responsible for the deletion, as well
	// as the date when that deletion happened.
	deletes map[blob.Ref][]deletion

	mediaTags map[blob.Ref]map[string]string // wholeref -> "album" -> "foo"

	permanodesByTime    *lazySortedPermanodes // cache of permanodes sorted by creation time.
	permanodesByModtime *lazySortedPermanodes // cache of permanodes sorted by modtime.

	// permanodesSetByNodeType maps from a camliNodeType attribute
	// value to the set of permanodes that ever had that
	// value. The bool is always true.
	permanodesSetByNodeType map[string]map[blob.Ref]bool

	// scratch string slice
	ss []string
}

func (c *Corpus) logf(format string, args ...any) {
	log.Printf("index/corpus: "+format, args...)
}

// blobMatches reports whether br is in the set.
func (srs SignerRefSet) blobMatches(br blob.Ref) bool {
	return slices.ContainsFunc(srs, br.EqualString)
}

// signerFromBlobrefMap maps a signer blobRef to the signer's GPG ID (e.g.
// 2931A67C26F5ABDA). It is needed because the signer on a claim is represented by
// its blobRef, but the same signer could have created claims with different hashes
// (e.g. with sha1 and with sha224), so these claims would look as if created by
// different signers (because different blobRefs). signerID thus allows the
// algorithms to rely on the unique GPG ID of a signer instead of the different
// blobRef representations of it. Its value is usually the corpus keyId.
type signerFromBlobrefMap map[blob.Ref]string

type latLong struct {
	lat, long float64
}

// IsDeleted reports whether the provided blobref (of a permanode or claim) should be considered deleted.
func (c *Corpus) IsDeleted(br blob.Ref) bool {
	for _, v := range c.deletes[br] {
		if !c.IsDeleted(v.deleter) {
			return true
		}
	}
	return false
}

type PermanodeMeta struct {
	Claims []*camtypes.Claim // sorted by camtypes.ClaimsByDate

	attr attrValues // attributes from all signers

	// signer maps a signer's GPG ID (e.g. 2931A67C26F5ABDA) to the attrs for this
	// signer.
	signer map[string]attrValues
}

type attrValues map[string][]string

// cacheAttrClaim applies attribute changes from cl.
func (m attrValues) cacheAttrClaim(cl *camtypes.Claim) {
	switch cl.Type {
	case string(schema.SetAttributeClaim):
		m[cl.Attr] = []string{cl.Value}
	case string(schema.AddAttributeClaim):
		m[cl.Attr] = append(m[cl.Attr], cl.Value)
	case string(schema.DelAttributeClaim):
		if cl.Value == "" {
			delete(m, cl.Attr)
		} else {
			a, i := m[cl.Attr], 0
			for _, v := range a {
				if v != cl.Value {
					a[i] = v
					i++
				}
			}
			m[cl.Attr] = a[:i]
		}
	}
}

// restoreInvariants sorts claims by date and
// recalculates latest attributes.
func (pm *PermanodeMeta) restoreInvariants(signers signerFromBlobrefMap) error {
	sort.Sort(camtypes.ClaimPtrsByDate(pm.Claims))
	pm.attr = make(attrValues)
	pm.signer = make(map[string]attrValues)
	for _, cl := range pm.Claims {
		if err := pm.appendAttrClaim(cl, signers); err != nil {
			return err
		}
	}
	return nil
}

// fixupLastClaim fixes invariants on the assumption
// that the all but the last element in Claims are sorted by date
// and the last element is the only one not yet included in Attrs.
func (pm *PermanodeMeta) fixupLastClaim(signers signerFromBlobrefMap) error {
	if pm.attr != nil {
		n := len(pm.Claims)
		if n < 2 || camtypes.ClaimPtrsByDate(pm.Claims).Less(n-2, n-1) {
			// already sorted, update Attrs from new Claim
			return pm.appendAttrClaim(pm.Claims[n-1], signers)
		}
	}
	return pm.restoreInvariants(signers)
}

// appendAttrClaim stores permanode attributes
// from cl in pm.attr and pm.signer[signerID[cl.Signer]].
// The caller of appendAttrClaim is responsible for calling
// it with claims sorted in camtypes.ClaimPtrsByDate order.
func (pm *PermanodeMeta) appendAttrClaim(cl *camtypes.Claim, signers signerFromBlobrefMap) error {
	signer, ok := signers[cl.Signer]
	if !ok {
		return fmt.Errorf("claim %v has unknown signer %q", cl.BlobRef, cl.Signer)
	}
	sc, ok := pm.signer[signer]
	if !ok {
		// Optimize for the case where cl.Signer of all claims are the same.
		// Instead of having two identical attrValues copies in
		// pm.attr and pm.signer[cl.Signer],
		// use a single attrValues
		// until there is at least a second signer.
		switch len(pm.signer) {
		case 0:
			// Set up signer cache to reference
			// the existing attrValues.
			pm.attr.cacheAttrClaim(cl)
			pm.signer[signer] = pm.attr
			return nil

		case 1:
			// pm.signer has exactly one other signer,
			// and its attrValues entry references pm.attr.
			// Make a copy of pm.attr
			// for this other signer now.
			m := make(attrValues)
			for a, v := range pm.attr {
				xv := make([]string, len(v))
				copy(xv, v)
				m[a] = xv
			}

			for sig := range pm.signer {
				pm.signer[sig] = m
				break
			}
		}
		sc = make(attrValues)
		pm.signer[signer] = sc
	}

	pm.attr.cacheAttrClaim(cl)

	// Cache claim in sc only if sc != pm.attr.
	if len(pm.signer) > 1 {
		sc.cacheAttrClaim(cl)
	}
	return nil
}

// valuesAtSigner returns an attrValues to query permanode attr values at the
// given time for the signerFilter, which is the GPG ID of a signer (e.g. 2931A67C26F5ABDA).
// It returns (nil, true) if signerFilter is not empty but pm has no
// attributes for it (including if signerFilter is unknown).
// It returns ok == true if v represents attrValues valid for the specified
// parameters.
// It returns (nil, false) if neither pm.attr nor pm.signer should be used for
// the given time, because e.g. some claims are more recent than this time. In
// which case, the caller should resort to querying another source, such as pm.Claims.
// The returned map must not be changed by the caller.
func (pm *PermanodeMeta) valuesAtSigner(at time.Time,
	signerFilter string) (v attrValues, ok bool) {

	if pm.attr == nil {
		return nil, false
	}

	var m attrValues
	if signerFilter != "" {
		m = pm.signer[signerFilter]
		if m == nil {
			return nil, true
		}
	} else {
		m = pm.attr
	}
	if at.IsZero() {
		return m, true
	}
	if n := len(pm.Claims); n == 0 || !pm.Claims[n-1].Date.After(at) {
		return m, true
	}
	return nil, false
}

func newCorpus() *Corpus {
	c := &Corpus{
		blobs:                   make(map[blob.Ref]*camtypes.BlobMeta),
		camBlobs:                make(map[schema.CamliType]map[blob.Ref]*camtypes.BlobMeta),
		files:                   make(map[blob.Ref]camtypes.FileInfo),
		permanodes:              make(map[blob.Ref]*PermanodeMeta),
		imageInfo:               make(map[blob.Ref]camtypes.ImageInfo),
		deletedBy:               make(map[blob.Ref]blob.Ref),
		keyId:                   make(map[blob.Ref]string),
		signerRefs:              make(map[string]SignerRefSet),
		brOfStr:                 make(map[string]blob.Ref),
		fileWholeRef:            make(map[blob.Ref]blob.Ref),
		gps:                     make(map[blob.Ref]latLong),
		mediaTags:               make(map[blob.Ref]map[string]string),
		deletes:                 make(map[blob.Ref][]deletion),
		claimBack:               make(map[blob.Ref][]*camtypes.Claim),
		permanodesSetByNodeType: make(map[string]map[blob.Ref]bool),
		dirChildren:             make(map[blob.Ref]map[blob.Ref]struct{}),
		fileParents:             make(map[blob.Ref]map[blob.Ref]struct{}),
	}
	c.permanodesByModtime = &lazySortedPermanodes{
		c:      c,
		pnTime: c.PermanodeModtime,
	}
	c.permanodesByTime = &lazySortedPermanodes{
		c:      c,
		pnTime: c.PermanodeAnyTime,
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
	"have":                 nil, // redundant with "meta"
	"recpn":                nil, // unneeded.
	"meta":                 (*Corpus).mergeMetaRow,
	keySignerKeyID.name:    (*Corpus).mergeSignerKeyIdRow,
	"claim":                (*Corpus).mergeClaimRow,
	"fileinfo":             (*Corpus).mergeFileInfoRow,
	keyFileTimes.name:      (*Corpus).mergeFileTimesRow,
	"imagesize":            (*Corpus).mergeImageSizeRow,
	"wholetofile":          (*Corpus).mergeWholeToFileRow,
	"exifgps":              (*Corpus).mergeEXIFGPSRow,
	"exiftag":              nil, // not using any for now
	"signerattrvalue":      nil, // ignoring for now
	"mediatag":             (*Corpus).mergeMediaTag,
	keyStaticDirChild.name: (*Corpus).mergeStaticDirChildRow,
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
	keySignerKeyID.name + ":",

	// the first two above are loaded serially first for dependency reasons, whereas
	// the others below are loaded concurrently afterwards.
	"claim|",
	"fileinfo|",
	keyFileTimes.name + "|",
	"imagesize|",
	"wholetofile|",
	"exifgps|",
	"mediatag|",
	keyStaticDirChild.name + "|",
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
		c.logf("loading into memory...")
		c.logf("loading into memory... (1/%d: meta rows)", len(slurpPrefixes))
	}

	scanmu := new(sync.Mutex)

	// We do the "meta" rows first, before the prefixes below, because it
	// populates the blobs map (used for blobref interning) and the camBlobs
	// map (used for hinting the size of other maps)
	if err := c.scanPrefix(scanmu, s, "meta:"); err != nil {
		return err
	}

	// we do the keyIDs first, because they're necessary to properly merge claims
	if err := c.scanPrefix(scanmu, s, keySignerKeyID.name+":"); err != nil {
		return err
	}

	c.files = make(map[blob.Ref]camtypes.FileInfo, len(c.camBlobs[schema.TypeFile]))
	c.permanodes = make(map[blob.Ref]*PermanodeMeta, len(c.camBlobs[schema.TypePermanode]))
	cpu0 := osutil.CPUUsage()

	var grp syncutil.Group
	for i, prefix := range slurpPrefixes[2:] {
		if logCorpusStats {
			c.logf("loading into memory... (%d/%d: prefix %q)", i+2, len(slurpPrefixes),
				prefix[:len(prefix)-1])
		}
		prefix := prefix
		grp.Go(func() error { return c.scanPrefix(scanmu, s, prefix) })
	}
	if err := grp.Err(); err != nil {
		return err
	}

	// Post-load optimizations and restoration of invariants.
	for _, pm := range c.permanodes {
		// Restore invariants violated during building:
		if err := pm.restoreInvariants(c.keyId); err != nil {
			return err
		}

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
		c.logf("stats: %.3f MiB mem: %d blobs (%.3f GiB) (%d schema (%d permanode, %d file (%d image), ...)",
			float64(memUsed)/(1<<20),
			len(c.blobs),
			float64(c.sumBlobBytes)/(1<<30),
			c.numSchemaBlobs(),
			len(c.permanodes),
			len(c.files),
			len(c.imageInfo))
		c.logf("scanning CPU usage: %v", cpu)
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

func (c *Corpus) numSchemaBlobs() (n int64) {
	for _, m := range c.camBlobs {
		n += int64(len(m))
	}
	return
}

func (c *Corpus) scanPrefix(mu *sync.Mutex, s sorted.KeyValue, prefix string) (err error) {
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
			mu.Lock()
			defer mu.Unlock()
		}
		if typeKey == keySignerKeyID.name {
			signerBlobRef, ok := blob.Parse(strings.TrimPrefix(it.Key(), keySignerKeyID.name+":"))
			if !ok {
				c.logf("WARNING: bogus signer blob in %v row: %q", keySignerKeyID.name, it.Key())
				continue
			}
			if err := c.addKeyID(&mutationMap{
				signerBlobRef: signerBlobRef,
				signerID:      it.Value(),
			}); err != nil {
				return err
			}
		} else {
			if err := fn(c, it.KeyBytes(), it.ValueBytes()); err != nil {
				return err
			}
		}
	}
	if logCorpusStats {
		d := time.Since(t0)
		c.logf("loaded prefix %q: %d rows, %v", prefix[:len(prefix)-1], n, d)
	}
	return nil
}

func (c *Corpus) addKeyID(mm *mutationMap) error {
	if mm.signerID == "" || !mm.signerBlobRef.Valid() {
		return nil
	}
	id, ok := c.keyId[mm.signerBlobRef]
	// only add it if we don't already have it, to save on allocs.
	if ok {
		if id != mm.signerID {
			return fmt.Errorf("GPG ID mismatch for signer %q: refusing to overwrite %v with %v", mm.signerBlobRef, id, mm.signerID)
		}
		return nil
	}
	c.signerRefs[mm.signerID] = append(c.signerRefs[mm.signerID], mm.signerBlobRef.String())
	return c.mergeSignerKeyIdRow([]byte("signerkeyid:"+mm.signerBlobRef.String()), []byte(mm.signerID))
}

func (c *Corpus) addBlob(ctx context.Context, br blob.Ref, mm *mutationMap) error {
	if _, dup := c.blobs[br]; dup {
		return nil
	}
	c.gen++
	// make sure keySignerKeyID is done first before the actual mutations, even
	// though it's also going to be done in the loop below.
	if err := c.addKeyID(mm); err != nil {
		return err
	}
	for k, v := range mm.kv {
		kt := typeOfKey(k)
		if kt == keySignerKeyID.name {
			// because we already took care of it in addKeyID
			continue
		}
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
	if slices.Contains(c.deletes[target], del) {
		return nil
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
	bm.CamliType = schema.CamliType((c.str(string(bm.CamliType))))

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
		// the end instead), keep claims sorted and attrs in sync.
		if err := pm.fixupLastClaim(c.keyId); err != nil {
			return err
		}
	}

	if vbr, ok := blob.Parse(cl.Value); ok {
		c.claimBack[vbr] = append(c.claimBack[vbr], &cl)
	}
	if cl.Attr == "camliNodeType" {
		set := c.permanodesSetByNodeType[cl.Value]
		if set == nil {
			set = make(map[blob.Ref]bool)
			c.permanodesSetByNodeType[cl.Value] = set
		}
		set[pn] = true
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

func (c *Corpus) mergeStaticDirChildRow(k, v []byte) error {
	// dirchild|sha1-dir|sha1-child" "1"
	// strip the key name
	sk := k[len(keyStaticDirChild.name)+1:]
	pipe := bytes.IndexByte(sk, '|')
	if pipe < 0 {
		return fmt.Errorf("invalid dirchild key %q, missing second pipe", k)
	}
	parent, ok := blob.ParseBytes(sk[:pipe])
	if !ok {
		return fmt.Errorf("invalid dirchild parent blobref in key %q", k)
	}
	child, ok := blob.ParseBytes(sk[pipe+1:])
	if !ok {
		return fmt.Errorf("invalid dirchild child blobref in key %q", k)
	}
	parent = c.br(parent)
	child = c.br(child)
	children, ok := c.dirChildren[parent]
	if !ok {
		children = make(map[blob.Ref]struct{})
	}
	children[child] = struct{}{}
	c.dirChildren[parent] = children
	parents, ok := c.fileParents[child]
	if !ok {
		parents = make(map[blob.Ref]struct{})
	}
	parents[parent] = struct{}{}
	c.fileParents[child] = parents
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

var sha1Prefix = []byte("sha1-")

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
	if c.building && !c.hasLegacySHA1 {
		if bytes.HasPrefix(pair, sha1Prefix) {
			c.hasLegacySHA1 = true
		}
	}
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
		if err != nil {
			log.Printf("index: bogus latitude in value of row %q = %q", k, v)
		} else {
			log.Printf("index: bogus longitude in value of row %q = %q", k, v)
		}
		return nil
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

// EnumerateCamliBlobs calls fn for all known meta blobs.
//
// If camType is not empty, it specifies a filter for which meta blob
// types to call fn for. If empty, all are emitted.
//
// If fn returns false, iteration ends.
func (c *Corpus) EnumerateCamliBlobs(camType schema.CamliType, fn func(camtypes.BlobMeta) bool) {
	if camType != "" {
		for _, bm := range c.camBlobs[camType] {
			if !fn(*bm) {
				return
			}
		}
		return
	}
	for _, m := range c.camBlobs {
		for _, bm := range m {
			if !fn(*bm) {
				return
			}
		}
	}
}

// EnumerateBlobMeta calls fn for all known meta blobs in an undefined
// order.
// If fn returns false, iteration ends.
func (c *Corpus) EnumerateBlobMeta(fn func(camtypes.BlobMeta) bool) {
	for _, bm := range c.blobs {
		if !fn(*bm) {
			return
		}
	}
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
		if lsp.c.IsDeleted(pn) {
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

func (c *Corpus) enumeratePermanodes(fn func(camtypes.BlobMeta) bool, pns []pnAndTime) {
	for _, cand := range pns {
		bm := c.blobs[cand.pn]
		if bm == nil {
			continue
		}
		if !fn(*bm) {
			return
		}
	}
}

// EnumeratePermanodesLastModified calls fn for all permanodes, sorted by most recently modified first.
// Iteration ends prematurely if fn returns false.
func (c *Corpus) EnumeratePermanodesLastModified(fn func(camtypes.BlobMeta) bool) {
	c.enumeratePermanodes(fn, c.permanodesByModtime.sorted(true))
}

// EnumeratePermanodesCreated calls fn for all permanodes.
// They are sorted using the contents creation date if any, the permanode modtime
// otherwise, and in the order specified by newestFirst.
// Iteration ends prematurely if fn returns false.
func (c *Corpus) EnumeratePermanodesCreated(fn func(camtypes.BlobMeta) bool, newestFirst bool) {
	c.enumeratePermanodes(fn, c.permanodesByTime.sorted(newestFirst))
}

// EnumerateSingleBlob calls fn with br's BlobMeta if br exists in the corpus.
func (c *Corpus) EnumerateSingleBlob(fn func(camtypes.BlobMeta) bool, br blob.Ref) {
	if bm := c.blobs[br]; bm != nil {
		fn(*bm)
	}
}

// EnumeratePermanodesByNodeTypes enumerates over all permanodes that might
// have one of the provided camliNodeType values, calling fn for each. If fn returns false,
// enumeration ends.
func (c *Corpus) EnumeratePermanodesByNodeTypes(fn func(camtypes.BlobMeta) bool, camliNodeTypes []string) {
	for _, t := range camliNodeTypes {
		set := c.permanodesSetByNodeType[t]
		for br := range set {
			if bm := c.blobs[br]; bm != nil {
				if !fn(*bm) {
					return
				}
			}
		}
	}
}

func (c *Corpus) GetBlobMeta(ctx context.Context, br blob.Ref) (camtypes.BlobMeta, error) {
	bm, ok := c.blobs[br]
	if !ok {
		return camtypes.BlobMeta{}, os.ErrNotExist
	}
	return *bm, nil
}

func (c *Corpus) KeyId(ctx context.Context, signer blob.Ref) (string, error) {
	if v, ok := c.keyId[signer]; ok {
		return v, nil
	}
	return "", sorted.ErrNotFound
}

func (c *Corpus) pnTimeAttr(pn blob.Ref, attr string) (t time.Time, ok bool) {
	if v := c.PermanodeAttrValue(pn, attr, time.Time{}, ""); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
	}
	return
}

// PermanodeTime returns the time of the content in permanode.
func (c *Corpus) PermanodeTime(pn blob.Ref) (t time.Time, ok bool) {
	// TODO(bradfitz): keep this time property cached on the permanode / files
	// TODO(bradfitz): finish implementing all these

	// Priorities:
	// -- Permanode explicit "camliTime" property
	// -- EXIF GPS time
	// -- Exif camera time - this one is actually already in the FileInfo,
	// because we use schema.FileTime (which returns the EXIF time, if available)
	// to index the time when receiving a file.
	// -- File time
	// -- File modtime
	// -- camliContent claim set time

	if t, ok = c.pnTimeAttr(pn, nodeattr.PaymentDueDate); ok {
		return
	}
	if t, ok = c.pnTimeAttr(pn, nodeattr.StartDate); ok {
		return
	}
	if t, ok = c.pnTimeAttr(pn, nodeattr.DateCreated); ok {
		return
	}
	var fi camtypes.FileInfo
	ccRef, ccTime, ok := c.pnCamliContent(pn)
	if ok {
		fi = c.files[ccRef]
	}
	if fi.Time != nil {
		return time.Time(*fi.Time), true
	}

	if t, ok = c.pnTimeAttr(pn, nodeattr.DatePublished); ok {
		return
	}
	if t, ok = c.pnTimeAttr(pn, nodeattr.DateModified); ok {
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

// PermanodeAnyTime returns the time that best qualifies the permanode.
// It tries content-specific times first, the permanode modtime otherwise.
func (c *Corpus) PermanodeAnyTime(pn blob.Ref) (t time.Time, ok bool) {
	if t, ok := c.PermanodeTime(pn); ok {
		return t, ok
	}
	return c.PermanodeModtime(pn)
}

func (c *Corpus) pnCamliContent(pn blob.Ref) (cc blob.Ref, t time.Time, ok bool) {
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
	pm, ok := c.permanodes[pn]
	if !ok {
		return
	}

	// Note: We intentionally don't try to derive any information
	// (except the owner, elsewhere) from the permanode blob
	// itself. Even though the permanode blob sometimes has the
	// GPG signature time, we intentionally ignore it.
	for _, cl := range pm.Claims {
		if c.IsDeleted(cl.BlobRef) {
			continue
		}
		if cl.Date.After(t) {
			t = cl.Date
		}
	}
	return t, !t.IsZero()
}

// PermanodeAttrValue returns a single-valued attribute or "".
// signerFilter, if set, should be the GPG ID of a signer
// (e.g. 2931A67C26F5ABDA).
func (c *Corpus) PermanodeAttrValue(permaNode blob.Ref,
	attr string,
	at time.Time,
	signerFilter string) string {
	pm, ok := c.permanodes[permaNode]
	if !ok {
		return ""
	}
	var signerRefs SignerRefSet
	if signerFilter != "" {
		signerRefs, ok = c.signerRefs[signerFilter]
		if !ok {
			return ""
		}
	}

	if values, ok := pm.valuesAtSigner(at, signerFilter); ok {
		v := values[attr]
		if len(v) == 0 {
			return ""
		}
		return v[0]
	}

	return claimPtrsAttrValue(pm.Claims, attr, at, signerRefs)
}

// permanodeAttrsOrClaims returns the best available source
// to query attr values of permaNode at the given time
// for the signerID, which is either:
// a. m that represents attr values for the parameters, or
// b. all claims of the permanode.
// Only one of m or claims will be non-nil.
//
// (m, nil) is returned if m represents attrValues
// valid for the specified parameters.
//
// (nil, claims) is returned if
// no cached attribute map is valid for the given time,
// because e.g. some claims are more recent than this time. In which
// case the caller should resort to query claims directly.
//
// (nil, nil) is returned if the permaNode does not exist,
// or permaNode exists and signerID is valid,
// but permaNode has no attributes for it.
//
// The returned values must not be changed by the caller.
func (c *Corpus) permanodeAttrsOrClaims(permaNode blob.Ref,
	at time.Time, signerID string) (m map[string][]string, claims []*camtypes.Claim) {

	pm, ok := c.permanodes[permaNode]
	if !ok {
		return nil, nil
	}

	m, ok = pm.valuesAtSigner(at, signerID)
	if ok {
		return m, nil
	}
	return nil, pm.Claims
}

// AppendPermanodeAttrValues appends to dst all the values for the attribute
// attr set on permaNode.
// signerFilter, if set, should be the GPG ID of a signer (e.g. 2931A67C26F5ABDA).
// dst must start with length 0 (laziness, mostly)
func (c *Corpus) AppendPermanodeAttrValues(dst []string,
	permaNode blob.Ref,
	attr string,
	at time.Time,
	signerFilter string) []string {
	if len(dst) > 0 {
		panic("len(dst) must be 0")
	}
	pm, ok := c.permanodes[permaNode]
	if !ok {
		return dst
	}
	var signerRefs SignerRefSet
	if signerFilter != "" {
		signerRefs, ok = c.signerRefs[signerFilter]
		if !ok {
			return dst
		}
	}
	if values, ok := pm.valuesAtSigner(at, signerFilter); ok {
		return append(dst, values[attr]...)
	}
	if at.IsZero() {
		at = time.Now()
	}
	for _, cl := range pm.Claims {
		if cl.Attr != attr || cl.Date.After(at) {
			continue
		}
		if len(signerRefs) > 0 && !signerRefs.blobMatches(cl.Signer) {
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

func (c *Corpus) AppendClaims(ctx context.Context, dst []camtypes.Claim, permaNode blob.Ref,
	signerFilter string,
	attrFilter string) ([]camtypes.Claim, error) {
	pm, ok := c.permanodes[permaNode]
	if !ok {
		return nil, nil
	}

	var signerRefs SignerRefSet
	if signerFilter != "" {
		signerRefs, ok = c.signerRefs[signerFilter]
		if !ok {
			return dst, nil
		}
	}

	for _, cl := range pm.Claims {
		if c.IsDeleted(cl.BlobRef) {
			continue
		}

		if len(signerRefs) > 0 && !signerRefs.blobMatches(cl.Signer) {
			continue
		}

		if attrFilter != "" && cl.Attr != attrFilter {
			continue
		}
		dst = append(dst, *cl)
	}
	return dst, nil
}

func (c *Corpus) GetFileInfo(ctx context.Context, fileRef blob.Ref) (fi camtypes.FileInfo, err error) {
	fi, ok := c.files[fileRef]
	if !ok {
		err = os.ErrNotExist
	}
	return
}

// GetDirChildren returns the direct children (static-set entries) of the directory dirRef.
// It only returns an error if dirRef does not exist.
func (c *Corpus) GetDirChildren(ctx context.Context, dirRef blob.Ref) (map[blob.Ref]struct{}, error) {
	children, ok := c.dirChildren[dirRef]
	if !ok {
		if _, ok := c.files[dirRef]; !ok {
			return nil, os.ErrNotExist
		}
		return nil, nil
	}
	return children, nil
}

// GetParentDirs returns the direct parents (directories) of the file or directory childRef.
// It only returns an error if childRef does not exist.
func (c *Corpus) GetParentDirs(ctx context.Context, childRef blob.Ref) (map[blob.Ref]struct{}, error) {
	parents, ok := c.fileParents[childRef]
	if !ok {
		if _, ok := c.files[childRef]; !ok {
			return nil, os.ErrNotExist
		}
		return nil, nil
	}
	return parents, nil
}

func (c *Corpus) GetImageInfo(ctx context.Context, fileRef blob.Ref) (ii camtypes.ImageInfo, err error) {
	ii, ok := c.imageInfo[fileRef]
	if !ok {
		err = os.ErrNotExist
	}
	return
}

func (c *Corpus) GetMediaTags(ctx context.Context, fileRef blob.Ref) (map[string]string, error) {
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

func (c *Corpus) GetWholeRef(ctx context.Context, fileRef blob.Ref) (wholeRef blob.Ref, ok bool) {
	wholeRef, ok = c.fileWholeRef[fileRef]
	return
}

func (c *Corpus) FileLatLong(fileRef blob.Ref) (lat, long float64, ok bool) {
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

// ForeachClaim calls fn for each claim of permaNode.
// If at is zero, all claims are yielded.
// If at is non-zero, claims after that point are skipped.
// If fn returns false, iteration ends.
// Iteration is in an undefined order.
func (c *Corpus) ForeachClaim(permaNode blob.Ref, at time.Time, fn func(*camtypes.Claim) bool) {
	pm, ok := c.permanodes[permaNode]
	if !ok {
		return
	}
	for _, cl := range pm.Claims {
		if !at.IsZero() && cl.Date.After(at) {
			continue
		}
		if !fn(cl) {
			return
		}
	}
}

// ForeachClaimBack calls fn for each claim with a value referencing br.
// If at is zero, all claims are yielded.
// If at is non-zero, claims after that point are skipped.
// If fn returns false, iteration ends.
// Iteration is in an undefined order.
func (c *Corpus) ForeachClaimBack(value blob.Ref, at time.Time, fn func(*camtypes.Claim) bool) {
	for _, cl := range c.claimBack[value] {
		if !at.IsZero() && cl.Date.After(at) {
			continue
		}
		if !fn(cl) {
			return
		}
	}
}

// PermanodeHasAttrValue reports whether the permanode pn at
// time at (zero means now) has the given attribute with the given
// value. If the attribute is multi-valued, any may match.
func (c *Corpus) PermanodeHasAttrValue(pn blob.Ref, at time.Time, attr, val string) bool {
	pm, ok := c.permanodes[pn]
	if !ok {
		return false
	}
	if values, ok := pm.valuesAtSigner(at, ""); ok {
		return slices.Contains(values[attr], val)
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
				ret = true
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
