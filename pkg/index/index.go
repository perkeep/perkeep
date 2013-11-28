/*
Copyright 2011 Google Inc.

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
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
)

type Index struct {
	*blobserver.NoImplStorage

	s sorted.KeyValue

	KeyFetcher blob.StreamingFetcher // for verifying claims

	// Used for fetching blobs to find the complete sha1s of file & bytes
	// schema blobs.
	BlobSource blob.StreamingFetcher

	// deletes is a cache to keep track of the deletion status (deleted vs undeleted)
	// of the blobs in the index. It makes for faster reads than the otherwise
	// recursive calls on the index.
	deletes *deletionCache

	corpus *Corpus // or nil, if not being kept in memory
}

var (
	_ blobserver.Storage = (*Index)(nil)
	_ Interface          = (*Index)(nil)
)

// New returns a new index using the provided key/value storage implementation.
func New(s sorted.KeyValue) *Index {
	idx := &Index{s: s}
	schemaVersion := idx.schemaVersion()
	switch {
	case schemaVersion == 0 && idx.isEmpty():
		// New index.
		err := idx.s.Set(keySchemaVersion.name, fmt.Sprintf("%d", requiredSchemaVersion))
		if err != nil {
			panic(fmt.Sprintf("Could not write index schema version %q: %v", requiredSchemaVersion, err))
		}
	case schemaVersion != requiredSchemaVersion:
		tip := ""
		if os.Getenv("CAMLI_DEV_CAMLI_ROOT") != "" {
			// Good signal that we're using the devcam server, so help out
			// the user with a more useful tip:
			tip = `(For the dev server, run "devcam server --wipe" to wipe both your blobs and index)`
		} else {
			tip = "See 'camtool dbinit' (or just delete the file for a file based index), and then 'camtool sync --all'"
		}
		log.Fatalf("index schema version is %d; required one is %d. You need to reindex. %s",
			schemaVersion, requiredSchemaVersion, tip)
	}
	if err := idx.initDeletesCache(); err != nil {
		panic(fmt.Sprintf("Could not initialize index's deletes cache: %v", err))
	}
	return idx
}

func (x *Index) isEmpty() bool {
	iter := x.s.Find("")
	hasRows := iter.Next()
	if err := iter.Close(); err != nil {
		panic(err)
	}
	return !hasRows
}

type prefixIter struct {
	sorted.Iterator
	prefix string
}

func (p *prefixIter) Next() bool {
	v := p.Iterator.Next()
	if v && !strings.HasPrefix(p.Key(), p.prefix) {
		return false
	}
	return v
}

func queryPrefixString(s sorted.KeyValue, prefix string) *prefixIter {
	return &prefixIter{
		prefix:   prefix,
		Iterator: s.Find(prefix),
	}
}

func (x *Index) queryPrefixString(prefix string) *prefixIter {
	return queryPrefixString(x.s, prefix)
}

func queryPrefix(s sorted.KeyValue, key *keyType, args ...interface{}) *prefixIter {
	return queryPrefixString(s, key.Prefix(args...))
}

func (x *Index) queryPrefix(key *keyType, args ...interface{}) *prefixIter {
	return x.queryPrefixString(key.Prefix(args...))
}

func closeIterator(it sorted.Iterator, perr *error) {
	err := it.Close()
	if err != nil && *perr == nil {
		*perr = err
	}
}

// schemaVersion returns the version of schema as it is found
// in the currently used index. If not found, it returns 0.
func (x *Index) schemaVersion() int {
	schemaVersionStr, err := x.s.Get(keySchemaVersion.name)
	if err != nil {
		if err == sorted.ErrNotFound {
			return 0
		}
		panic(fmt.Sprintf("Could not get index schema version: %v", err))
	}
	schemaVersion, err := strconv.Atoi(schemaVersionStr)
	if err != nil {
		panic(fmt.Sprintf("Bogus index schema version: %q", schemaVersionStr))
	}
	return schemaVersion
}

type deletion struct {
	deleter blob.Ref
	when    time.Time
}

type byDeletionDate []deletion

func (d byDeletionDate) Len() int           { return len(d) }
func (d byDeletionDate) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }
func (d byDeletionDate) Less(i, j int) bool { return d[i].when.Before(d[j].when) }

type deletionCache struct {
	sync.RWMutex
	m map[blob.Ref][]deletion
}

// initDeletesCache creates and populates the deletion status cache used by the index
// for faster calls to IsDeleted and DeletedAt. It is called by New.
func (x *Index) initDeletesCache() error {
	x.deletes = &deletionCache{
		m: make(map[blob.Ref][]deletion),
	}
	var err error
	it := x.queryPrefix(keyDeleted)
	defer closeIterator(it, &err)
	for it.Next() {
		cl, ok := kvDeleted(it.Key())
		if !ok {
			return fmt.Errorf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key())
		}
		targetDeletions := append(x.deletes.m[cl.Target],
			deletion{
				deleter: cl.BlobRef,
				when:    cl.Date,
			})
		sort.Sort(sort.Reverse(byDeletionDate(targetDeletions)))
		x.deletes.m[cl.Target] = targetDeletions
	}
	return err
}

func kvDeleted(k string) (c camtypes.Claim, ok bool) {
	// TODO(bradfitz): garbage
	keyPart := strings.Split(k, "|")
	if len(keyPart) != 4 {
		return
	}
	if keyPart[0] != "deleted" {
		return
	}
	target, ok := blob.Parse(keyPart[1])
	if !ok {
		return
	}
	claimRef, ok := blob.Parse(keyPart[3])
	if !ok {
		return
	}
	date, err := time.Parse(time.RFC3339, unreverseTimeString(keyPart[2]))
	if err != nil {
		return
	}
	return camtypes.Claim{
		BlobRef: claimRef,
		Target:  target,
		Date:    date,
		Type:    string(schema.DeleteClaim),
	}, true
}

// DeletedAt returns whether br (a blobref or a claim) should be considered deleted,
// and if yes, at what time the latest deletion occured. If it was never deleted,
// it returns false, time.Time{}.
func (x *Index) DeletedAt(br blob.Ref) (bool, time.Time) {
	if x.deletes == nil {
		// We still allow the slow path, in case someone creates
		// their own Index without a deletes cache.
		return x.deletedAtNoCache(br)
	}
	x.deletes.RLock()
	defer x.deletes.RUnlock()
	return x.deletedAt(br)
}

// The caller must hold x.deletes.mu for read.
func (x *Index) deletedAt(br blob.Ref) (bool, time.Time) {
	deletes, ok := x.deletes.m[br]
	if !ok {
		return false, time.Time{}
	}
	for _, v := range deletes {
		if deleterIsDeleted, _ := x.deletedAt(v.deleter); !deleterIsDeleted {
			// We can exit early because the deletes are time sorted
			return true, v.when
		}
	}
	return false, time.Time{}
}

// Used when the Index has no deletes cache (x.deletes is nil).
func (x *Index) deletedAtNoCache(br blob.Ref) (bool, time.Time) {
	var err error
	it := x.queryPrefix(keyDeleted, br)
	deleted := false
	var mostRecentDeletion time.Time
	for it.Next() {
		cl, ok := kvDeleted(it.Key())
		if !ok {
			panic(fmt.Sprintf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key()))
		}
		if deleterIsDeleted, _ := x.deletedAtNoCache(cl.BlobRef); !deleterIsDeleted {
			deleted = true
			mostRecentDeletion = cl.Date
			// we can exit early because the iterator gives use time sorted entries for keyDeleted
			break
		}
	}
	closeIterator(it, &err)
	if err != nil {
		// TODO: Do better?
		panic(fmt.Sprintf("Could not close iterator on keyDeleted: %v", err))
	}
	return deleted, mostRecentDeletion
}

// IsDeleted reports whether the provided blobref (of a permanode or
// claim) should be considered deleted.
func (x *Index) IsDeleted(br blob.Ref) bool {
	if x.deletes == nil {
		// We still allow the slow path, in case someone creates
		// their own Index without a deletes cache.
		return x.isDeletedNoCache(br)
	}
	x.deletes.RLock()
	defer x.deletes.RUnlock()
	return x.isDeleted(br)
}

// The caller must hold x.deletes.mu for read.
func (x *Index) isDeleted(br blob.Ref) bool {
	deletes, ok := x.deletes.m[br]
	if !ok {
		return false
	}
	for _, v := range deletes {
		if !x.isDeleted(v.deleter) {
			return true
		}
	}
	return false
}

// Used when the Index has no deletes cache (x.deletes is nil).
func (x *Index) isDeletedNoCache(br blob.Ref) bool {
	var err error
	it := x.queryPrefix(keyDeleted, br)
	for it.Next() {
		cl, ok := kvDeleted(it.Key())
		if !ok {
			panic(fmt.Sprintf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key()))
		}
		if !x.isDeletedNoCache(cl.BlobRef) {
			closeIterator(it, &err)
			if err != nil {
				// TODO: Do better?
				panic(fmt.Sprintf("Could not close iterator on keyDeleted: %v", err))
			}
			return true
		}
	}
	closeIterator(it, &err)
	if err != nil {
		// TODO: Do better?
		panic(fmt.Sprintf("Could not close iterator on keyDeleted: %v", err))
	}
	return false
}

// GetRecentPermanodes sends results to dest filtered by owner, limit, and
// before.  A zero value for before will default to the current time.  The
// results will have duplicates supressed, with most recent permanode
// returned.
// Note, permanodes more recent than before will still be fetched from the
// index then skipped. This means runtime scales linearly with the number of
// nodes more recent than before.
func (x *Index) GetRecentPermanodes(dest chan<- camtypes.RecentPermanode, owner blob.Ref, limit int, before time.Time) (err error) {
	defer close(dest)

	keyId, err := x.KeyId(owner)
	if err == sorted.ErrNotFound {
		log.Printf("No recent permanodes because keyId for owner %v not found", owner)
		return nil
	}
	if err != nil {
		log.Printf("Error fetching keyId for owner %v: %v", owner, err)
		return err
	}

	sent := 0
	var seenPermanode dupSkipper

	if before.IsZero() {
		before = time.Now()
	}
	// TODO(bradfitz): handle before efficiently. don't use queryPrefix.
	it := x.queryPrefix(keyRecentPermanode, keyId)
	defer closeIterator(it, &err)
	for it.Next() {
		permaStr := it.Value()
		parts := strings.SplitN(it.Key(), "|", 4)
		if len(parts) != 4 {
			continue
		}
		mTime, _ := time.Parse(time.RFC3339, unreverseTimeString(parts[2]))
		permaRef, ok := blob.Parse(permaStr)
		if !ok {
			continue
		}
		if x.IsDeleted(permaRef) {
			continue
		}
		if seenPermanode.Dup(permaStr) {
			continue
		}
		// Skip entries with an mTime less than or equal to before.
		if !mTime.Before(before) {
			continue
		}
		dest <- camtypes.RecentPermanode{
			Permanode:   permaRef,
			Signer:      owner, // TODO(bradfitz): kinda. usually. for now.
			LastModTime: mTime,
		}
		sent++
		if sent == limit {
			break
		}
	}
	return nil
}

func (x *Index) AppendClaims(dst []camtypes.Claim, permaNode blob.Ref,
	signerFilter blob.Ref,
	attrFilter string) ([]camtypes.Claim, error) {
	if x.corpus != nil {
		return x.corpus.AppendClaims(dst, permaNode, signerFilter, attrFilter)
	}
	var (
		keyId string
		err   error
		it    *prefixIter
	)
	if signerFilter.Valid() {
		keyId, err = x.KeyId(signerFilter)
		if err == sorted.ErrNotFound {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		it = x.queryPrefix(keyPermanodeClaim, permaNode, keyId)
	} else {
		it = x.queryPrefix(keyPermanodeClaim, permaNode)
	}
	defer closeIterator(it, &err)

	// In the common case, an attribute filter is just a plain
	// token ("camliContent") unescaped. If so, fast path that
	// check to skip the row before we even split it.
	var mustHave string
	if attrFilter != "" && urle(attrFilter) == attrFilter {
		mustHave = attrFilter
	}

	for it.Next() {
		val := it.Value()
		if mustHave != "" && !strings.Contains(val, mustHave) {
			continue
		}
		cl, ok := kvClaim(it.Key(), val)
		if !ok {
			continue
		}
		if attrFilter != "" && cl.Attr != attrFilter {
			continue
		}
		if signerFilter.Valid() && cl.Signer != signerFilter {
			continue
		}
		dst = append(dst, cl)
	}
	return dst, nil
}

func kvClaim(k, v string) (c camtypes.Claim, ok bool) {
	// TODO(bradfitz): remove the strings.Split calls to reduce allocations.
	keyPart := strings.Split(k, "|")
	valPart := strings.Split(v, "|")
	if len(keyPart) < 5 || len(valPart) < 4 {
		return
	}
	signerRef, ok := blob.Parse(valPart[3])
	if !ok {
		return
	}
	permaNode, ok := blob.Parse(keyPart[1])
	if !ok {
		return
	}
	claimRef, ok := blob.Parse(keyPart[4])
	if !ok {
		return
	}
	date, err := time.Parse(time.RFC3339, keyPart[3])
	if err != nil {
		return
	}
	return camtypes.Claim{
		BlobRef:   claimRef,
		Signer:    signerRef,
		Permanode: permaNode,
		Date:      date,
		Type:      urld(valPart[0]),
		Attr:      urld(valPart[1]),
		Value:     urld(valPart[2]),
	}, true
}

func (x *Index) GetBlobMeta(br blob.Ref) (camtypes.BlobMeta, error) {
	if x.corpus != nil {
		return x.corpus.GetBlobMeta(br)
	}
	key := "meta:" + br.String()
	meta, err := x.s.Get(key)
	if err == sorted.ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return camtypes.BlobMeta{}, err
	}
	pos := strings.Index(meta, "|")
	if pos < 0 {
		panic(fmt.Sprintf("Bogus index row for key %q: got value %q", key, meta))
	}
	size, _ := strconv.ParseInt(meta[:pos], 10, 64)
	mime := meta[pos+1:]
	return camtypes.BlobMeta{
		Ref:       br,
		Size:      int(size),
		CamliType: camliTypeFromMIME(mime),
	}, nil
}

func (x *Index) KeyId(signer blob.Ref) (string, error) {
	if x.corpus != nil {
		return x.corpus.KeyId(signer)
	}
	return x.s.Get("signerkeyid:" + signer.String())
}

func (x *Index) PermanodeOfSignerAttrValue(signer blob.Ref, attr, val string) (permaNode blob.Ref, err error) {
	keyId, err := x.KeyId(signer)
	if err == sorted.ErrNotFound {
		return blob.Ref{}, os.ErrNotExist
	}
	if err != nil {
		return blob.Ref{}, err
	}
	it := x.queryPrefix(keySignerAttrValue, keyId, attr, val)
	defer closeIterator(it, &err)
	if it.Next() {
		permaRef, ok := blob.Parse(it.Value())
		if ok && !x.IsDeleted(permaRef) {
			return permaRef, nil
		}
	}
	return blob.Ref{}, os.ErrNotExist
}

// This is just like PermanodeOfSignerAttrValue except we return multiple and dup-suppress.
// If request.Query is "", it is not used in the prefix search.
func (x *Index) SearchPermanodesWithAttr(dest chan<- blob.Ref, request *camtypes.PermanodeByAttrRequest) (err error) {
	defer close(dest)
	if request.FuzzyMatch {
		// TODO(bradfitz): remove this for now? figure out how to handle it generically?
		return errors.New("TODO: SearchPermanodesWithAttr: generic indexer doesn't support FuzzyMatch on PermanodeByAttrRequest")
	}
	if request.Attribute == "" {
		return errors.New("index: missing Attribute in SearchPermanodesWithAttr")
	}

	keyId, err := x.KeyId(request.Signer)
	if err == sorted.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	var it *prefixIter
	if request.Query == "" {
		it = x.queryPrefix(keySignerAttrValue, keyId, request.Attribute)
	} else {
		it = x.queryPrefix(keySignerAttrValue, keyId, request.Attribute, request.Query)
	}
	defer closeIterator(it, &err)
	for it.Next() {
		pn, ok := blob.Parse(it.Value())
		if !ok {
			continue
		}
		if x.IsDeleted(pn) {
			continue
		}
		pnstr := pn.String()
		if seen[pnstr] {
			continue
		}
		seen[pnstr] = true

		dest <- pn
		if len(seen) == request.MaxResults {
			break
		}
	}
	return nil
}

func (x *Index) PathsOfSignerTarget(signer, target blob.Ref) (paths []*camtypes.Path, err error) {
	paths = []*camtypes.Path{}
	keyId, err := x.KeyId(signer)
	if err != nil {
		if err == sorted.ErrNotFound {
			err = nil
		}
		return
	}

	mostRecent := make(map[string]*camtypes.Path)
	maxClaimDates := make(map[string]string)

	it := x.queryPrefix(keyPathBackward, keyId, target)
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")[1:]
		valPart := strings.Split(it.Value(), "|")
		if len(keyPart) < 3 || len(valPart) < 4 {
			continue
		}
		claimRef, ok := blob.Parse(keyPart[2])
		if !ok {
			continue
		}
		baseRef, ok := blob.Parse(valPart[1])
		if !ok {
			continue
		}
		claimDate := valPart[0]
		active := valPart[2]
		suffix := urld(valPart[3])
		key := baseRef.String() + "/" + suffix

		if claimDate > maxClaimDates[key] {
			maxClaimDates[key] = claimDate
			if active == "Y" {
				mostRecent[key] = &camtypes.Path{
					Claim:     claimRef,
					ClaimDate: claimDate,
					Base:      baseRef,
					Suffix:    suffix,
					Target:    target,
				}
			} else {
				delete(mostRecent, key)
			}
		}
	}
	for _, v := range mostRecent {
		paths = append(paths, v)
	}
	return paths, nil
}

func (x *Index) PathsLookup(signer, base blob.Ref, suffix string) (paths []*camtypes.Path, err error) {
	paths = []*camtypes.Path{}
	keyId, err := x.KeyId(signer)
	if err != nil {
		if err == sorted.ErrNotFound {
			err = nil
		}
		return
	}

	it := x.queryPrefix(keyPathForward, keyId, base, suffix)
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")[1:]
		valPart := strings.Split(it.Value(), "|")
		if len(keyPart) < 5 || len(valPart) < 2 {
			continue
		}
		claimRef, ok := blob.Parse(keyPart[4])
		if !ok {
			continue
		}
		baseRef, ok := blob.Parse(keyPart[1])
		if !ok {
			continue
		}
		claimDate := unreverseTimeString(keyPart[3])
		suffix := urld(keyPart[2])
		target, ok := blob.Parse(valPart[1])
		if !ok {
			continue
		}

		// TODO(bradfitz): investigate what's up with deleted
		// forward path claims here.  Needs docs with the
		// interface too, and tests.
		active := valPart[0]
		_ = active

		path := &camtypes.Path{
			Claim:     claimRef,
			ClaimDate: claimDate,
			Base:      baseRef,
			Suffix:    suffix,
			Target:    target,
		}
		paths = append(paths, path)
	}
	return
}

func (x *Index) PathLookup(signer, base blob.Ref, suffix string, at time.Time) (*camtypes.Path, error) {
	paths, err := x.PathsLookup(signer, base, suffix)
	if err != nil {
		return nil, err
	}
	var (
		newest    = int64(0)
		atSeconds = int64(0)
		best      *camtypes.Path
	)

	if !at.IsZero() {
		atSeconds = at.Unix()
	}

	for _, path := range paths {
		t, err := time.Parse(time.RFC3339, path.ClaimDate)
		if err != nil {
			continue
		}
		secs := t.Unix()
		if atSeconds != 0 && secs > atSeconds {
			// Too new
			continue
		}
		if newest > secs {
			// Too old
			continue
		}
		// Just right
		newest, best = secs, path
	}
	if best == nil {
		return nil, os.ErrNotExist
	}
	return best, nil
}

func (x *Index) ExistingFileSchemas(wholeRef blob.Ref) (schemaRefs []blob.Ref, err error) {
	it := x.queryPrefix(keyWholeToFileRef, wholeRef)
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")[1:]
		if len(keyPart) < 2 {
			continue
		}
		ref, ok := blob.Parse(keyPart[1])
		if ok {
			schemaRefs = append(schemaRefs, ref)
		}
	}
	return schemaRefs, nil
}

func (x *Index) loadKey(key string, val *string, err *error, wg *sync.WaitGroup) {
	defer wg.Done()
	*val, *err = x.s.Get(key)
}

func (x *Index) GetFileInfo(fileRef blob.Ref) (camtypes.FileInfo, error) {
	if x.corpus != nil {
		return x.corpus.GetFileInfo(fileRef)
	}
	ikey := "fileinfo|" + fileRef.String()
	tkey := "filetimes|" + fileRef.String()
	wg := new(sync.WaitGroup)
	wg.Add(2)
	var iv, tv string // info value, time value
	var ierr, terr error
	go x.loadKey(ikey, &iv, &ierr, wg)
	go x.loadKey(tkey, &tv, &terr, wg)
	wg.Wait()

	if ierr == sorted.ErrNotFound {
		go x.reindex(fileRef) // kinda a hack. Issue 103.
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	if ierr != nil {
		return camtypes.FileInfo{}, ierr
	}
	if terr == sorted.ErrNotFound {
		// Old index; retry. TODO: index versioning system.
		x.reindex(fileRef)
		tv, terr = x.s.Get(tkey)
	}
	valPart := strings.Split(iv, "|")
	if len(valPart) < 3 {
		log.Printf("index: bogus key %q = %q", ikey, iv)
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	size, err := strconv.ParseInt(valPart[0], 10, 64)
	if err != nil {
		log.Printf("index: bogus integer at position 0 in key %q = %q", ikey, iv)
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	fileName := urld(valPart[1])
	fi := camtypes.FileInfo{
		Size:     size,
		FileName: fileName,
		MIMEType: urld(valPart[2]),
	}

	if tv != "" {
		times := strings.Split(urld(tv), ",")
		updateFileInfoTimes(&fi, times)
	}

	return fi, nil
}

func updateFileInfoTimes(fi *camtypes.FileInfo, times []string) {
	if len(times) == 0 {
		return
	}
	fi.Time = types.ParseTime3339OrZil(times[0])
	if len(times) == 2 {
		fi.ModTime = types.ParseTime3339OrZil(times[1])
	}
}

// v is "width|height"
func kvImageInfo(v string) (ii camtypes.ImageInfo, ok bool) {
	pipei := strings.Index(v, "|")
	if pipei < 0 {
		return
	}
	var err error
	ii.Width, err = strconv.Atoi(v[:pipei])
	if err != nil {
		return
	}
	ii.Height, err = strconv.Atoi(v[pipei+1:])
	if err != nil {
		return
	}
	return ii, true
}

func (x *Index) GetImageInfo(fileRef blob.Ref) (camtypes.ImageInfo, error) {
	if x.corpus != nil {
		return x.corpus.GetImageInfo(fileRef)
	}
	// it might be that the key does not exist because image.DecodeConfig failed earlier
	// (because of unsupported JPEG features like progressive mode).
	key := keyImageSize.Key(fileRef.String())
	v, err := x.s.Get(key)
	if err == sorted.ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return camtypes.ImageInfo{}, err
	}
	ii, ok := kvImageInfo(v)
	if !ok {
		return camtypes.ImageInfo{}, fmt.Errorf("index: bogus key %q = %q", key, v)
	}
	return ii, nil
}

func (x *Index) EdgesTo(ref blob.Ref, opts *camtypes.EdgesToOpts) (edges []*camtypes.Edge, err error) {
	it := x.queryPrefix(keyEdgeBackward, ref)
	defer closeIterator(it, &err)
	permanodeParents := map[string]blob.Ref{} // blobref key => blobref set
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")[1:]
		if len(keyPart) < 2 {
			continue
		}
		parent := keyPart[1]
		parentRef, ok := blob.Parse(parent)
		if !ok {
			continue
		}
		valPart := strings.Split(it.Value(), "|")
		if len(valPart) < 2 {
			continue
		}
		parentType, parentName := valPart[0], valPart[1]
		if parentType == "permanode" {
			permanodeParents[parent] = parentRef
		} else {
			edges = append(edges, &camtypes.Edge{
				From:      parentRef,
				FromType:  parentType,
				FromTitle: parentName,
				To:        ref,
			})
		}
	}
	for _, parentRef := range permanodeParents {
		edges = append(edges, &camtypes.Edge{
			From:     parentRef,
			FromType: "permanode",
			To:       ref,
		})
	}
	return edges, nil
}

// GetDirMembers sends on dest the children of the static directory dir.
func (x *Index) GetDirMembers(dir blob.Ref, dest chan<- blob.Ref, limit int) (err error) {
	defer close(dest)

	sent := 0
	it := x.queryPrefix(keyStaticDirChild, dir.String())
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")
		if len(keyPart) != 3 {
			return fmt.Errorf("index: bogus key keyStaticDirChild = %q", it.Key())
		}

		child, ok := blob.Parse(keyPart[2])
		if !ok {
			continue
		}
		dest <- child
		sent++
		if sent == limit {
			break
		}
	}
	return nil
}

func kvBlobMeta(k, v string) (bm camtypes.BlobMeta, ok bool) {
	refStr := strings.TrimPrefix(k, "meta:")
	if refStr == k {
		return // didn't trim
	}
	br, ok := blob.Parse(refStr)
	if !ok {
		return
	}
	pipe := strings.Index(v, "|")
	if pipe < 0 {
		return
	}
	size, err := strconv.Atoi(v[:pipe])
	if err != nil {
		return
	}
	return camtypes.BlobMeta{
		Ref:       br,
		Size:      size,
		CamliType: camliTypeFromMIME(v[pipe+1:]),
	}, true
}

func enumerateBlobMeta(s sorted.KeyValue, cb func(camtypes.BlobMeta) error) (err error) {
	it := queryPrefixString(s, "meta:")
	defer closeIterator(it, &err)
	for it.Next() {
		bm, ok := kvBlobMeta(it.Key(), it.Value())
		if !ok {
			continue
		}
		if err := cb(bm); err != nil {
			return err
		}
	}
	return nil
}

func enumerateSignerKeyId(s sorted.KeyValue, cb func(blob.Ref, string)) (err error) {
	const pfx = "signerkeyid:"
	it := queryPrefixString(s, pfx)
	defer closeIterator(it, &err)
	for it.Next() {
		if br, ok := blob.Parse(strings.TrimPrefix(it.Key(), pfx)); ok {
			cb(br, it.Value())
		}
	}
	return
}

// EnumerateBlobMeta sends all metadata about all known blobs to ch and then closes ch.
func (x *Index) EnumerateBlobMeta(ch chan<- camtypes.BlobMeta) (err error) {
	if x.corpus != nil {
		return x.corpus.EnumerateBlobMeta(ch)
	}
	defer close(ch)
	return enumerateBlobMeta(x.s, func(bm camtypes.BlobMeta) error {
		ch <- bm
		return nil
	})
}

// Storage returns the index's underlying Storage implementation.
func (x *Index) Storage() sorted.KeyValue { return x.s }

// Close closes the underlying sorted.KeyValue, if the storage has a Close method.
// The return value is the return value of the underlying Close, or
// nil otherwise.
func (x *Index) Close() error {
	if cl, ok := x.s.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

// "application/json; camliType=file" => "file"
// "image/gif" => ""
func camliTypeFromMIME(mime string) string {
	if v := strings.TrimPrefix(mime, "application/json; camliType="); v != mime {
		return v
	}
	return ""
}

// TODO(bradfitz): rename this? This is really about signer-attr-value
// (PermanodeOfSignerAttrValue), and not about indexed attributes in general.
func IsIndexedAttribute(attr string) bool {
	switch attr {
	case "camliRoot", "camliImportRoot", "tag", "title":
		return true
	}
	return false
}

// IsBlobReferenceAttribute returns whether attr is an attribute whose
// value is a blob reference (e.g. camliMember) and thus something the
// indexers should keep inverted indexes on for parent/child-type
// relationships.
func IsBlobReferenceAttribute(attr string) bool {
	switch attr {
	case "camliMember":
		return true
	}
	return false
}

func IsFulltextAttribute(attr string) bool {
	switch attr {
	case "tag", "title":
		return true
	}
	return false
}
