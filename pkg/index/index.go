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
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types"
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
}

var _ blobserver.Storage = (*Index)(nil)
var _ search.Index = (*Index)(nil)

func New(s Storage) *Index {
	return &Index{s: s}
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

func (x *Index) queryPrefix(key *keyType, args ...interface{}) *prefixIter {
	return x.queryPrefixString(key.Prefix(args...))
}

func (x *Index) queryPrefixString(prefix string) *prefixIter {
	return &prefixIter{
		prefix:   prefix,
		Iterator: x.s.Find(prefix),
	}
}

func closeIterator(it Iterator, perr *error) {
	err := it.Close()
	if err != nil && *perr == nil {
		*perr = err
	}
}

// isDeleted returns whether br (a blobref or a claim) should be considered deleted.
func (x *Index) isDeleted(br blob.Ref) bool {
	var err error
	it := x.queryPrefix(keyDeleted, br)
	defer closeIterator(it, &err)
	for it.Next() {
		// parts are ["deleted", br.String(), blobref-of-delete-claim].
		// see keyDeleted in keys.go
		parts := strings.SplitN(it.Key(), "|", 3)
		if len(parts) != 3 {
			continue
		}
		delClaimRef, ok := blob.Parse(parts[2])
		if !ok {
			panic(fmt.Errorf("invalid deleted claim for %v", parts[1]))
		}
		// The recursive call on the blobref of the delete claim
		// checks that the claim itself was not deleted, in which case
		// br is not considered deleted anymore.
		// TODO(mpl): Each delete and undo delete adds a level of
		// recursion so this could recurse far. is there a way to
		// go faster in a worst case scenario?
		return !x.isDeleted(delClaimRef)
	}
	return false
}

func (x *Index) GetRecentPermanodes(dest chan *search.Result, owner blob.Ref, limit int) (err error) {
	defer close(dest)

	keyId, err := x.keyId(owner)
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
		var mTimeSec int64
		if mTime, err := time.Parse(time.RFC3339, unreverseTimeString(parts[2])); err == nil {
			mTimeSec = mTime.Unix()
		}
		permaRef, ok := blob.Parse(permaStr)
		if !ok {
			continue
		}
		if x.isDeleted(permaRef) {
			continue
		}
		if seenPermanode.Dup(permaStr) {
			continue
		}
		dest <- &search.Result{
			BlobRef:     permaRef,
			Signer:      owner, // TODO(bradfitz): kinda. usually. for now.
			LastModTime: mTimeSec,
		}
		sent++
		if sent == limit {
			break
		}
	}
	return nil
}

func (x *Index) GetOwnerClaims(permaNode, owner blob.Ref) (cl search.ClaimList, err error) {
	keyId, err := x.keyId(owner)
	if err == ErrNotFound {
		err = nil
		return
	}
	if err != nil {
		return nil, err
	}
	prefix := pipes("claim", permaNode, keyId, "")
	it := x.queryPrefixString(prefix)
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
		cl = append(cl, &search.Claim{
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

func (x *Index) GetBlobMIMEType(blob blob.Ref) (mime string, size int64, err error) {
	key := "meta:" + blob.String()
	meta, err := x.s.Get(key)
	if err == ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return
	}
	pos := strings.Index(meta, "|")
	if pos < 0 {
		panic(fmt.Sprintf("Bogus index row for key %q: got value %q", key, meta))
	}
	size, _ = strconv.ParseInt(meta[:pos], 10, 64)
	mime = meta[pos+1:]
	return
}

// maps from blobref of openpgp ascii-armored public key => gpg keyid like "2931A67C26F5ABDA"
func (x *Index) keyId(signer blob.Ref) (string, error) {
	return x.s.Get("signerkeyid:" + signer.String())
}

func (x *Index) PermanodeOfSignerAttrValue(signer blob.Ref, attr, val string) (permaNode blob.Ref, err error) {
	keyId, err := x.keyId(signer)
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
		if ok && !x.isDeleted(permaRef) {
			return permaRef, nil
		}
	}
	return blob.Ref{}, os.ErrNotExist
}

// This is just like PermanodeOfSignerAttrValue except we return multiple and dup-suppress.
// If request.Query is "", it is not used in the prefix search.
func (x *Index) SearchPermanodesWithAttr(dest chan<- blob.Ref, request *search.PermanodeByAttrRequest) (err error) {
	defer close(dest)
	if request.FuzzyMatch {
		// TODO(bradfitz): remove this for now? figure out how to handle it generically?
		return errors.New("TODO: SearchPermanodesWithAttr: generic indexer doesn't support FuzzyMatch on PermanodeByAttrRequest")
	}
	if request.Attribute == "" {
		return errors.New("index: missing Attribute in SearchPermanodesWithAttr")
	}

	keyId, err := x.keyId(request.Signer)
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
		if x.isDeleted(pn) {
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

func (x *Index) PathsOfSignerTarget(signer, target blob.Ref) (paths []*search.Path, err error) {
	paths = []*search.Path{}
	keyId, err := x.keyId(signer)
	if err != nil {
		if err == ErrNotFound {
			err = nil
		}
		return
	}

	mostRecent := make(map[string]*search.Path)
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
				mostRecent[key] = &search.Path{
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

func (x *Index) PathsLookup(signer, base blob.Ref, suffix string) (paths []*search.Path, err error) {
	paths = []*search.Path{}
	keyId, err := x.keyId(signer)
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

		path := &search.Path{
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

func (x *Index) PathLookup(signer, base blob.Ref, suffix string, at time.Time) (*search.Path, error) {
	paths, err := x.PathsLookup(signer, base, suffix)
	if err != nil {
		return nil, err
	}
	var (
		newest    = int64(0)
		atSeconds = int64(0)
		best      *search.Path
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

func (x *Index) GetFileInfo(fileRef blob.Ref) (*search.FileInfo, error) {
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
		return nil, os.ErrNotExist
	}
	if ierr != nil {
		return nil, ierr
	}
	if terr == ErrNotFound {
		// Old index; retry. TODO: index versioning system.
		x.reindex(fileRef)
		tv, terr = x.s.Get(tkey)
	}
	valPart := strings.Split(iv, "|")
	if len(valPart) < 3 {
		log.Printf("index: bogus key %q = %q", ikey, iv)
		return nil, os.ErrNotExist
	}
	size, err := strconv.ParseInt(valPart[0], 10, 64)
	if err != nil {
		log.Printf("index: bogus integer at position 0 in key %q = %q", ikey, iv)
		return nil, os.ErrNotExist
	}
	fileName := urld(valPart[1])
	fi := &search.FileInfo{
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

func (x *Index) GetImageInfo(fileRef blob.Ref) (*search.ImageInfo, error) {
	// it might be that the key does not exist because image.DecodeConfig failed earlier
	// (because of unsupported JPEG features like progressive mode).
	key := keyImageSize.Key(fileRef.String())
	dim, err := x.s.Get(key)
	if err == ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	valPart := strings.Split(dim, "|")
	if len(valPart) != 2 {
		return nil, fmt.Errorf("index: bogus key %q = %q", key, dim)
	}
	width, err := strconv.Atoi(valPart[0])
	if err != nil {
		return nil, fmt.Errorf("index: bogus integer at position 0 in key %q: %q", key, valPart[0])
	}
	height, err := strconv.Atoi(valPart[1])
	if err != nil {
		return nil, fmt.Errorf("index: bogus integer at position 1 in key %q: %q", key, valPart[1])
	}

	imgInfo := &search.ImageInfo{
		Width:  width,
		Height: height,
	}
	return imgInfo, nil
}

func (x *Index) EdgesTo(ref blob.Ref, opts *search.EdgesToOpts) (edges []*search.Edge, err error) {
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
			edges = append(edges, &search.Edge{
				From:      parentRef,
				FromType:  parentType,
				FromTitle: parentName,
				To:        ref,
			})
		}
	}
	for _, parentRef := range permanodeParents {
		edges = append(edges, &search.Edge{
			From:     parentRef,
			FromType: "permanode",
			To:       ref,
		})
	}
	return edges, nil
}

func (x *Index) Storage() Storage { return x.s }
