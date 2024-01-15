/*
Copyright 2011 The Perkeep Authors

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

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go4.org/syncutil"

	"perkeep.org/pkg/blob"
)

// A DirReader reads the entries of a "directory" schema blob's
// referenced "static-set" blob.
type DirReader struct {
	fetcher blob.Fetcher
	ss      *superset

	staticSet []blob.Ref
	current   int
}

// NewDirReader creates a new directory reader and prepares to
// fetch the static-set entries
func NewDirReader(ctx context.Context, fetcher blob.Fetcher, dirBlobRef blob.Ref) (*DirReader, error) {
	ss := new(superset)
	err := ss.setFromBlobRef(ctx, fetcher, dirBlobRef)
	if err != nil {
		return nil, err
	}
	if ss.Type != "directory" {
		return nil, fmt.Errorf("schema/dirreader: expected \"directory\" schema blob for %s, got %q", dirBlobRef, ss.Type)
	}
	dr, err := ss.NewDirReader(fetcher)
	if err != nil {
		return nil, fmt.Errorf("schema/dirreader: creating DirReader for %s: %v", dirBlobRef, err)
	}
	dr.current = 0
	return dr, nil
}

func (b *Blob) NewDirReader(ctx context.Context, fetcher blob.Fetcher) (*DirReader, error) {
	return b.ss.NewDirReader(fetcher)
}

func (ss *superset) NewDirReader(fetcher blob.Fetcher) (*DirReader, error) {
	if ss.Type != "directory" {
		return nil, fmt.Errorf("Superset not of type \"directory\"")
	}
	return &DirReader{fetcher: fetcher, ss: ss}, nil
}

func (ss *superset) setFromBlobRef(ctx context.Context, fetcher blob.Fetcher, blobRef blob.Ref) error {
	if !blobRef.Valid() {
		return errors.New("schema/dirreader: blobref invalid")
	}
	ss.BlobRef = blobRef
	rc, _, err := fetcher.Fetch(ctx, blobRef)
	if err != nil {
		return fmt.Errorf("schema/dirreader: fetching schema blob %s: %v", blobRef, err)
	}
	defer rc.Close()
	if err := json.NewDecoder(rc).Decode(ss); err != nil {
		return fmt.Errorf("schema/dirreader: decoding schema blob %s: %v", blobRef, err)
	}
	return nil
}

// StaticSet returns the whole of the static set members of that directory
func (dr *DirReader) StaticSet(ctx context.Context) ([]blob.Ref, error) {
	if dr.staticSet != nil {
		return dr.staticSet, nil
	}
	staticSetBlobref := dr.ss.Entries
	if !staticSetBlobref.Valid() {
		return nil, errors.New("schema/dirreader: Invalid blobref")
	}
	members, err := staticSet(ctx, staticSetBlobref, dr.fetcher)
	if err != nil {
		return nil, err
	}
	dr.staticSet = members
	return dr.staticSet, nil
}

func staticSet(ctx context.Context, staticSetBlobref blob.Ref, fetcher blob.Fetcher) ([]blob.Ref, error) {
	rsc, _, err := fetcher.Fetch(ctx, staticSetBlobref)
	if err != nil {
		return nil, fmt.Errorf("schema/dirreader: fetching schema blob %s: %v", staticSetBlobref, err)
	}
	defer rsc.Close()
	ss, err := parseSuperset(rsc)
	if err != nil {
		return nil, fmt.Errorf("schema/dirreader: decoding schema blob %s: %v", staticSetBlobref, err)
	}
	if ss.Type != "static-set" {
		return nil, fmt.Errorf("schema/dirreader: expected \"static-set\" schema blob for %s, got %q", staticSetBlobref, ss.Type)
	}
	var members []blob.Ref
	if len(ss.Members) > 0 {
		// We have fileRefs or dirRefs in ss.Members, so we are either in the static-set
		// of a small directory, or one of the "leaf" subsets of a large directory spread.
		for _, member := range ss.Members {
			if !member.Valid() {
				return nil, fmt.Errorf("schema/dirreader: invalid (static-set member) blobref referred by \"static-set\" schema blob %v", staticSetBlobref)
			}
			members = append(members, member)
		}
		return members, nil
	}
	// We are either at the top static-set of a large directory, or in a "non-leaf"
	// subset of a large directory.
	for _, toMerge := range ss.MergeSets {
		if !toMerge.Valid() {
			return nil, fmt.Errorf("schema/dirreader: invalid (static-set subset) blobref referred by \"static-set\" schema blob %v", staticSetBlobref)
		}
		// TODO(mpl): do it concurrently
		subset, err := staticSet(ctx, toMerge, fetcher)
		if err != nil {
			return nil, fmt.Errorf("schema/dirreader: could not get members of %q, subset of %v: %v", toMerge, staticSetBlobref, err)
		}
		members = append(members, subset...)
	}
	return members, nil
}

// Readdir implements the Directory interface.
func (dr *DirReader) Readdir(ctx context.Context, n int) (entries []DirectoryEntry, err error) {
	sts, err := dr.StaticSet(ctx)
	if err != nil {
		return nil, fmt.Errorf("schema/dirreader: can't get StaticSet: %v", err)
	}
	up := dr.current + n
	if n <= 0 {
		dr.current = 0
		up = len(sts)
	} else {
		if n > (len(sts) - dr.current) {
			up = len(sts)
		}
	}

	// TODO(bradfitz): push down information to the fetcher
	// (e.g. cachingfetcher -> remote client http) that we're
	// going to load a bunch, so the HTTP client (if not using
	// SPDY) can do discovery and see if the server supports a
	// batch handler, then get them all in one round-trip, rather
	// than attacking the server with hundreds of parallel TLS
	// setups.

	type res struct {
		ent DirectoryEntry
		err error
	}
	var cs []chan res

	// Kick off all directory entry loads.
	gate := syncutil.NewGate(20) // Limit IO concurrency
	for _, entRef := range sts[dr.current:up] {
		c := make(chan res, 1)
		cs = append(cs, c)
		gate.Start()
		go func(entRef blob.Ref) {
			defer gate.Done()
			entry, err := NewDirectoryEntryFromBlobRef(ctx, dr.fetcher, entRef)
			c <- res{entry, err}
		}(entRef)
	}

	for _, c := range cs {
		res := <-c
		if res.err != nil {
			return nil, fmt.Errorf("schema/dirreader: can't create dirEntry: %v", res.err)
		}
		entries = append(entries, res.ent)
	}
	return entries, nil
}
