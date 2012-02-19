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
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
)

var _ = log.Printf

var ErrNotFound = errors.New("index: key not found")

type IndexStorage interface {
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
	// It returns whether the iterator is exhausted.
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

type mutation struct {
	key    string
	value  string // used if !delete
	delete bool   // if to be deleted
}

type batch struct {
	m []mutation
}

func (b *batch) Delete(key string) {
	b.m = append(b.m, mutation{key: key, delete: true})
}

func (b *batch) Set(key, value string) {
	b.m = append(b.m, mutation{key: key, value: value})
}

type Index struct {
	*blobserver.SimpleBlobHubPartitionMap
	*blobserver.NoImplStorage

	s IndexStorage

	KeyFetcher blobref.StreamingFetcher // for verifying claims

	// Used for fetching blobs to find the complete sha1s of file & bytes
	// schema blobs.
	BlobSource blobref.StreamingFetcher
}

var _ blobserver.Storage = (*Index)(nil)
var _ search.Index = (*Index)(nil)

func New(s IndexStorage) *Index {
	return &Index{
		s: s,
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
	}
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

func (x *Index) GetRecentPermanodes(dest chan *search.Result, owner *blobref.BlobRef, limit int) (err error) {
	defer close(dest)
	// TODO(bradfitz): this will need to be a context wrapper too, like storage

	keyId, err := x.keyId(owner)
	if err == ErrNotFound {
		return nil
	}
	if err != nil {
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
		mTime := unreverseTimeString(parts[2])
		mTimeNs := schema.NanosFromRFC3339(mTime)
		mTimeSec := mTimeNs / 1e9
		permaRef := blobref.Parse(permaStr)
		if permaRef == nil {
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

func (x *Index) GetOwnerClaims(permaNode, owner *blobref.BlobRef) (cl search.ClaimList, err error) {
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
		claimRef := blobref.Parse(keyPart[4])
		if claimRef == nil {
			continue
		}
		nanos := schema.NanosFromRFC3339(keyPart[3])
		cl = append(cl, &search.Claim{
			BlobRef:   claimRef,
			Signer:    owner,
			Permanode: permaNode,
			Date:      time.Unix(0, nanos).UTC(),
			Type:      urld(valPart[0]),
			Attr:      urld(valPart[1]),
			Value:     urld(valPart[2]),
		})
	}
	return
}

func (x *Index) GetBlobMimeType(blob *blobref.BlobRef) (mime string, size int64, err error) {
	meta, err := x.s.Get("meta:" + blob.String())
	if err == ErrNotFound {
		err = os.ENOENT
	}
	if err != nil {
		return
	}
	pos := strings.Index(meta, "|")
	size, _ = strconv.ParseInt(meta[:pos], 10, 64)
	mime = meta[pos+1:]
	return
}

// maps from blobref of openpgp ascii-armored public key => gpg keyid like "2931A67C26F5ABDA"
func (x *Index) keyId(signer *blobref.BlobRef) (string, error) {
	return x.s.Get("signerkeyid:" + signer.String())
}

func (x *Index) PermanodeOfSignerAttrValue(signer *blobref.BlobRef, attr, val string) (permaNode *blobref.BlobRef, err error) {
	keyId, err := x.keyId(signer)
	if err == ErrNotFound {
		return nil, os.ENOENT
	}
	if err != nil {
		return nil, err
	}
	it := x.queryPrefix(keySignerAttrValue, keyId, attr, val)
	defer closeIterator(it, &err)
	if it.Next() {
		return blobref.Parse(it.Value()), nil
	}
	return nil, os.ENOENT
}

// This is just like PermanodeOfSignerAttrValue except we return multiple and dup-suppress.
func (x *Index) SearchPermanodesWithAttr(dest chan<- *blobref.BlobRef, request *search.PermanodeByAttrRequest) (err error) {
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
	it := x.queryPrefix(keySignerAttrValue, keyId, request.Attribute, request.Query)
	defer closeIterator(it, &err)
	for it.Next() {
		pn := blobref.Parse(it.Value())
		if pn == nil {
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

func (x *Index) PathsOfSignerTarget(signer, target *blobref.BlobRef) (paths []*search.Path, err error) {
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
		claimRef := blobref.Parse(keyPart[2])
		baseRef := blobref.Parse(valPart[1])
		if claimRef == nil || baseRef == nil {
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

func (x *Index) PathsLookup(signer, base *blobref.BlobRef, suffix string) (paths []*search.Path, err error) {
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
		claimRef := blobref.Parse(keyPart[4])
		baseRef := blobref.Parse(keyPart[1])
		if claimRef == nil || baseRef == nil {
			continue
		}
		claimDate := unreverseTimeString(keyPart[3])
		suffix := urld(keyPart[2])
		target := blobref.Parse(valPart[1])

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

func (x *Index) PathLookup(signer, base *blobref.BlobRef, suffix string, at time.Time) (*search.Path, error) {
	paths, err := x.PathsLookup(signer, base, suffix)
	if err != nil {
		return nil, err
	}
	var (
		newest    = int64(0)
		atSeconds = int64(0)
		best      *search.Path
	)
	if at != nil {
		atSeconds = at.Unix()
	}
	for _, path := range paths {
		t, err := time.Parse(time.RFC3339, trimRFC3339Subseconds(path.ClaimDate))
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
		return nil, os.ENOENT
	}
	return best, nil
}

// TODO(bradfitz): remove this as of Go 1. shouldn't be needed anymore.
func trimRFC3339Subseconds(s string) string {
	if !strings.HasSuffix(s, "Z") || len(s) < 20 || s[19] != '.' {
		return s
	}
	return s[:19] + "Z"
}

func (x *Index) ExistingFileSchemas(wholeRef *blobref.BlobRef) (schemaRefs []*blobref.BlobRef, err error) {
	it := x.queryPrefix(keyWholeToFileRef, wholeRef)
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")[1:]
		if len(keyPart) < 2 {
			continue
		}
		ref := blobref.Parse(keyPart[1])
		if ref != nil {
			schemaRefs = append(schemaRefs, ref)
		}
	}
	return schemaRefs, nil
}

func (x *Index) GetFileInfo(fileRef *blobref.BlobRef) (*search.FileInfo, error) {
	key := "fileinfo|" + fileRef.String()
	v, err := x.s.Get(key)
	if err == ErrNotFound {
		return nil, os.ENOENT
	}
	valPart := strings.Split(v, "|")
	if len(valPart) < 3 {
		log.Printf("index: bogus key %q = %q", key, v)
		return nil, os.ENOENT
	}
	size, err := strconv.ParseInt(valPart[0], 10, 64)
	if err != nil {
		log.Printf("index: bogus integer at position 0 in key %q = %q", key, v)
		return nil, os.ENOENT
	}
	fi := &search.FileInfo{
		Size:     size,
		FileName: urld(valPart[1]),
		MimeType: urld(valPart[2]),
	}
	return fi, nil
}
