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
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
)

var ErrNotFound = errors.New("index: key not found")

// Storage is the minimal interface that must be implemented by index
// storage implementations. (e.g. mysql, postgres, mongo, sqlite,
// leveldb, dynamo)
type Storage interface {
	// Get gets the value for the given key. It returns ErrNotFound if the DB
	// does not contain the key.
	Get(key string) (string, error)

	Set(key, value string) error
	Delete(key string) error

	BeginBatch() BatchMutation
	CommitBatch(b BatchMutation) error

	// Find returns an iterator positioned before the first key/value pair
	// whose key is 'greater than or equal to' the given key. There may be no
	// such pair, in which case the iterator will return false on Next.
	//
	// Any error encountered will be implicitly returned via the iterator. An
	// error-iterator will yield no key/value pairs and closing that iterator
	// will return that error.
	Find(key string) Iterator
}

// Iterator iterates over an index Storage's key/value pairs in key order.
//
// An iterator must be closed after use, but it is not necessary to read an
// iterator until exhaustion.
//
// An iterator is not necessarily goroutine-safe, but it is safe to use
// multiple iterators concurrently, with each in a dedicated goroutine.
type Iterator interface {
	// Next moves the iterator to the next key/value pair.
	// It returns false when the iterator is exhausted.
	Next() bool

	// Key returns the key of the current key/value pair.
	// Only valid after a call to Next returns true.
	Key() string

	// Value returns the value of the current key/value pair.
	// Only valid after a call to Next returns true.
	Value() string

	// Close closes the iterator and returns any accumulated error. Exhausting
	// all the key/value pairs in a table is not considered to be an error.
	// It is valid to call Close multiple times. Other methods should not be
	// called after the iterator has been closed.
	Close() error
}

type BatchMutation interface {
	Set(key, value string)
	Delete(key string)
}

type Mutation interface {
	Key() string
	Value() string
	IsDelete() bool
}

type mutation struct {
	key    string
	value  string // used if !delete
	delete bool   // if to be deleted
}

func (m mutation) Key() string {
	return m.key
}

func (m mutation) Value() string {
	return m.value
}

func (m mutation) IsDelete() bool {
	return m.delete
}

func NewBatchMutation() BatchMutation {
	return &batch{}
}

type batch struct {
	m []Mutation
}

func (b *batch) Mutations() []Mutation {
	return b.m
}

func (b *batch) Delete(key string) {
	b.m = append(b.m, mutation{key: key, delete: true})
}

func (b *batch) Set(key, value string) {
	b.m = append(b.m, mutation{key: key, value: value})
}

type Index struct {
	*blobserver.NoImplStorage

	s Storage

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

var _ blobserver.Storage = (*Index)(nil)
var _ Interface = (*Index)(nil)

func New(s Storage) *Index {
	idx := &Index{s: s}
	schemaVersion := idx.schemaVersion()
	if schemaVersion != 0 {
		if schemaVersion != requiredSchemaVersion {
			if os.Getenv("CAMLI_DEV_CAMLI_ROOT") != "" {
				// Good signal that we're using the devcam server, so help out
				// the user with a more useful tip:
				log.Fatalf("index schema version is %d; required one is %d (run \"devcam server --wipe\" to wipe both your blobs and reindex.)", schemaVersion, requiredSchemaVersion)
			}
			log.Fatalf("index schema version is %d; required one is %d. You need to reindex. See 'camtool dbinit' (or just delete the file for a file based index), and then 'camtool sync'.)",
				schemaVersion, requiredSchemaVersion)
		}
	} else {
		err := idx.s.Set(keySchemaVersion.name, fmt.Sprintf("%d", requiredSchemaVersion))
		if err != nil {
			panic(fmt.Errorf("Could not write index schema version %q: %v", requiredSchemaVersion, err))
		}
	}
	if err := idx.initDeletesCache(); err != nil {
		panic(fmt.Errorf("Could not initialize index's deletes cache: %v", err))
	}
	return idx
}

type prefixIter struct {
	Iterator
	prefix string
}

func (p *prefixIter) Next() bool {
	v := p.Iterator.Next()
	if v && !strings.HasPrefix(p.Key(), p.prefix) {
		return false
	}
	return v
}

func queryPrefixString(s Storage, prefix string) *prefixIter {
	return &prefixIter{
		prefix:   prefix,
		Iterator: s.Find(prefix),
	}
}

func (x *Index) queryPrefixString(prefix string) *prefixIter {
	return queryPrefixString(x.s, prefix)
}

func queryPrefix(s Storage, key *keyType, args ...interface{}) *prefixIter {
	return queryPrefixString(s, key.Prefix(args...))
}

func (x *Index) queryPrefix(key *keyType, args ...interface{}) *prefixIter {
	return x.queryPrefixString(key.Prefix(args...))
}

func closeIterator(it Iterator, perr *error) {
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
		if err == ErrNotFound {
			return 0
		}
		panic(fmt.Errorf("Could not get index schema version: %v", err))
	}
	schemaVersion, err := strconv.Atoi(schemaVersionStr)
	if err != nil {
		panic(fmt.Errorf("Bogus index schema version: %q", schemaVersionStr))
	}
	return schemaVersion
}

type deletionStatus struct {
	deleted bool      // whether the concerned blob should be considered deleted.
	when    time.Time // time of the most recent (un)deletion
}

type deletionCache struct {
	sync.RWMutex
	m map[blob.Ref]deletionStatus
}

// initDeletesCache creates and populates the deletion status cache used by the index
// for faster calls to IsDeleted and DeletedAt. It is called by New.
func (x *Index) initDeletesCache() error {
	x.deletes = &deletionCache{
		m: make(map[blob.Ref]deletionStatus),
	}
	var err error
	it := x.queryPrefix(keyDeleted)
	defer closeIterator(it, &err)
	for it.Next() {
		parts := strings.SplitN(it.Key(), "|", 4)
		if len(parts) != 4 {
			return fmt.Errorf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key())
		}
		deleter := parts[3]
		deleterRef, ok := blob.Parse(deleter)
		if !ok {
			return fmt.Errorf("invalid deleter blobref %q in keyDeleted entry %q", deleter, it.Key())
		}
		deleted := parts[1]
		deletedRef, ok := blob.Parse(deleted)
		if !ok {
			return fmt.Errorf("invalid deleted blobref %q in keyDeleted entry %q", deleted, it.Key())
		}
		delTimeStr := parts[2]
		delTime, err := time.Parse(time.RFC3339, unreverseTimeString(delTimeStr))
		if err != nil {
			return fmt.Errorf("invalid time %q in keyDeleted entry %q: %v", delTimeStr, it.Key(), err)
		}
		deleterIsDeleted, when := x.deletedAtNoCache(deleterRef)
		if when.IsZero() {
			when = delTime
		}
		previousStatus, ok := x.deletes.m[deletedRef]
		if ok && when.Before(previousStatus.when) {
			// previously noted status wins because it is the most recent
			continue
		}
		x.deletes.m[deletedRef] = deletionStatus{
			deleted: !deleterIsDeleted,
			when:    when,
		}
	}
	return err
}

// UpdateDeletesCache updates the index deletes cache with the deletion
// (and its potential consequences) of deletedRef at when.
func (x *Index) UpdateDeletesCache(deletedRef blob.Ref, when time.Time) error {
	// TODO(mpl): This one will go away as soon as receive.go handles delete claims and is in charge
	// of keeping the cache updated. Or at least it won't have to be public. Now I need it public
	// for the tests.
	var err error
	if x.deletes == nil {
		return fmt.Errorf("Index has no deletes cache")
	}
	x.deletes.Lock()
	defer x.deletes.Unlock()
	previousStatus := x.deletes.m[deletedRef]
	if when.Before(previousStatus.when) {
		// ignore new value because it's older than what's in cache
		return err
	}
	x.deletes.m[deletedRef] = deletionStatus{
		deleted: true,
		when:    when,
	}
	// And now deal with the consequences
	isDeleted := true
	for {
		isDeleted = !isDeleted
		deleterRef := deletedRef
		it := x.queryPrefix(keyDeletes, deleterRef)
		defer closeIterator(it, &err)
		if !it.Next() {
			break
		}
		parts := strings.SplitN(it.Key(), "|", 3)
		if len(parts) != 3 {
			return fmt.Errorf("Bogus keyDeletes entry key: want |\"deletes\"|<deleter claim>|<deleted blob>|, got %q", it.Key())
		}
		deleted := parts[2]
		var ok bool
		deletedRef, ok = blob.Parse(deleted)
		if !ok {
			return fmt.Errorf("invalid deleted blobref %q in keyDeletes entry %q", deleted, it.Key())
		}
		x.deletes.m[deletedRef] = deletionStatus{
			deleted: isDeleted,
			when:    when,
		}
	}
	return err
}

// DeletedAt returns whether br (a blobref or a claim) should be considered deleted,
// and at what time the latest deletion or undeletion occured. If it was never deleted,
// it returns false, time.Time{}.
func (x *Index) DeletedAt(br blob.Ref) (bool, time.Time) {
	if x.deletes == nil {
		// We still allow the slow path, in case someone creates
		// their own Index without a deletes cache.
		return x.deletedAtNoCache(br)
	}
	x.deletes.RLock()
	defer x.deletes.RUnlock()
	st := x.deletes.m[br]
	return st.deleted, st.when
}

func (x *Index) deletedAtNoCache(br blob.Ref) (bool, time.Time) {
	var err error
	it := x.queryPrefix(keyDeleted, br)
	if it.Next() {
		parts := strings.SplitN(it.Key(), "|", 4)
		if len(parts) != 4 {
			panic(fmt.Errorf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key()))
		}
		deleter := parts[3]
		deleterRef, ok := blob.Parse(deleter)
		if !ok {
			panic(fmt.Errorf("invalid deleter blobref %q in keyDeleted entry %q", deleter, it.Key()))
		}
		delTime := parts[2]
		mTime, err := time.Parse(time.RFC3339, unreverseTimeString(delTime))
		if err != nil {
			panic(fmt.Errorf("invalid time %q in keyDeleted entry %q: %v", delTime, it.Key(), err))
		}
		closeIterator(it, &err)
		if err != nil {
			// TODO: Do better?
			panic(fmt.Errorf("Could not close iterator on keyDeleted: %v", err))
		}
		del, when := x.deletedAtNoCache(deleterRef)
		if when.IsZero() {
			when = mTime
		}
		return !del, when
	}
	closeIterator(it, &err)
	if err != nil {
		// TODO: Do better?
		panic(fmt.Errorf("Could not close iterator on keyDeleted: %v", err))
	}
	return false, time.Time{}
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
	st := x.deletes.m[br]
	return st.deleted
}

func (x *Index) isDeletedNoCache(br blob.Ref) bool {
	var err error
	it := x.queryPrefix(keyDeleted, br)
	if it.Next() {
		parts := strings.SplitN(it.Key(), "|", 4)
		if len(parts) != 4 {
			panic(fmt.Errorf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key()))
		}
		deleter := parts[3]
		delClaimRef, ok := blob.Parse(deleter)
		if !ok {
			panic(fmt.Errorf("invalid deleter blobref %q in keyDeleted entry %q", deleter, it.Key()))
		}
		// The recursive call on the blobref of the delete claim
		// checks that the claim itself was not deleted, in which case
		// br is not considered deleted anymore.
		// TODO(mpl): Each delete and undo delete adds a level of
		// recursion so this could recurse far. is there a way to
		// go faster in a worst case scenario?
		closeIterator(it, &err)
		if err != nil {
			// TODO: Do better?
			panic(fmt.Errorf("Could not close iterator on keyDeleted: %v", err))
		}
		return !x.isDeletedNoCache(delClaimRef)
	}
	closeIterator(it, &err)
	if err != nil {
		// TODO: Do better?
		panic(fmt.Errorf("Could not close iterator on keyDeleted: %v", err))
	}
	return false
}

func (x *Index) GetRecentPermanodes(dest chan<- camtypes.RecentPermanode, owner blob.Ref, limit int) (err error) {
	defer close(dest)

	keyId, err := x.KeyId(owner)
	if err == ErrNotFound {
		log.Printf("No recent permanodes because keyId for owner %v not found", owner)
		return nil
	}
	if err != nil {
		log.Printf("Error fetching keyId for owner %v: %v", owner, err)
		return err
	}

	sent := 0
	var seenPermanode dupSkipper

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

func (x *Index) GetOwnerClaims(permaNode, owner blob.Ref) (cl camtypes.ClaimList, err error) {
	keyId, err := x.KeyId(owner)
	if err == ErrNotFound {
		err = nil
		return
	}
	if err != nil {
		return nil, err
	}
	it := x.queryPrefix(keyPermanodeClaim, permaNode, keyId)
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")
		valPart := strings.Split(it.Value(), "|")
		if len(keyPart) < 5 || len(valPart) < 3 {
			continue
		}
		claimRef, ok := blob.Parse(keyPart[4])
		if !ok {
			continue
		}
		date, _ := time.Parse(time.RFC3339, keyPart[3])
		cl = append(cl, &camtypes.Claim{
			BlobRef:   claimRef,
			Signer:    owner,
			Permanode: permaNode,
			Date:      date,
			Type:      urld(valPart[0]),
			Attr:      urld(valPart[1]),
			Value:     urld(valPart[2]),
		})
	}
	return
}

func (x *Index) GetBlobMeta(br blob.Ref) (camtypes.BlobMeta, error) {
	if x.corpus != nil {
		return x.corpus.GetBlobMeta(br)
	}
	key := "meta:" + br.String()
	meta, err := x.s.Get(key)
	if err == ErrNotFound {
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
	if err == ErrNotFound {
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
	if err == ErrNotFound {
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
		if err == ErrNotFound {
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
		if err == ErrNotFound {
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
	ikey := "fileinfo|" + fileRef.String()
	tkey := "filetimes|" + fileRef.String()
	wg := new(sync.WaitGroup)
	wg.Add(2)
	var iv, tv string // info value, time value
	var ierr, terr error
	go x.loadKey(ikey, &iv, &ierr, wg)
	go x.loadKey(tkey, &tv, &terr, wg)
	wg.Wait()

	if ierr == ErrNotFound {
		go x.reindex(fileRef) // kinda a hack. Issue 103.
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	if ierr != nil {
		return camtypes.FileInfo{}, ierr
	}
	if terr == ErrNotFound {
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
		fi.Time = types.ParseTime3339OrZil(times[0])
		if len(times) == 2 {
			fi.ModTime = types.ParseTime3339OrZil(times[1])
		}
	}

	return fi, nil
}

func (x *Index) GetImageInfo(fileRef blob.Ref) (camtypes.ImageInfo, error) {
	// it might be that the key does not exist because image.DecodeConfig failed earlier
	// (because of unsupported JPEG features like progressive mode).
	key := keyImageSize.Key(fileRef.String())
	dim, err := x.s.Get(key)
	if err == ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return camtypes.ImageInfo{}, err
	}
	valPart := strings.Split(dim, "|")
	if len(valPart) != 2 {
		return camtypes.ImageInfo{}, fmt.Errorf("index: bogus key %q = %q", key, dim)
	}
	width, err := strconv.Atoi(valPart[0])
	if err != nil {
		return camtypes.ImageInfo{}, fmt.Errorf("index: bogus integer at position 0 in key %q: %q", key, valPart[0])
	}
	height, err := strconv.Atoi(valPart[1])
	if err != nil {
		return camtypes.ImageInfo{}, fmt.Errorf("index: bogus integer at position 1 in key %q: %q", key, valPart[1])
	}

	return camtypes.ImageInfo{
		Width:  width,
		Height: height,
	}, nil
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

func enumerateBlobMeta(s Storage, cb func(camtypes.BlobMeta) error) (err error) {
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

func enumerateSignerKeyId(s Storage, cb func(blob.Ref, string)) (err error) {
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
func (x *Index) Storage() Storage { return x.s }

// Close closes the underlying Storage, if the storage has a Close method.
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
