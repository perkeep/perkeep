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

package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"camlistore.org/pkg/blobref"
)

// A DirReader reads the entries of a "directory" schema blob's
// referenced "static-set" blob.
type DirReader struct {
	fetcher blobref.SeekFetcher
	ss      *Superset

	staticSet []*blobref.BlobRef
	current   int
}

// NewDirReader creates a new directory reader and prepares to
// fetch the static-set entries
func NewDirReader(fetcher blobref.SeekFetcher, dirBlobRef *blobref.BlobRef) (*DirReader, error) {
	ss := new(Superset)
	err := ss.setFromBlobRef(fetcher, dirBlobRef)
	if err != nil {
		return nil, err
	}
	if ss.Type != "directory" {
		return nil, fmt.Errorf("schema/filereader: expected \"directory\" schema blob for %s, got %q", dirBlobRef, ss.Type)
	}
	dr, err := ss.NewDirReader(fetcher)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: creating DirReader for %s: %v", dirBlobRef, err)
	}
	dr.current = 0
	return dr, nil
}

func (b *Blob) NewDirReader(fetcher blobref.SeekFetcher) (*DirReader, error) {
	return b.ss.NewDirReader(fetcher)
}

func (ss *Superset) NewDirReader(fetcher blobref.SeekFetcher) (*DirReader, error) {
	if ss.Type != "directory" {
		return nil, fmt.Errorf("Superset not of type \"directory\"")
	}
	return &DirReader{fetcher: fetcher, ss: ss}, nil
}

func (ss *Superset) setFromBlobRef(fetcher blobref.SeekFetcher, blobRef *blobref.BlobRef) error {
	if blobRef == nil {
		return errors.New("schema/filereader: blobref was nil")
	}
	ss.BlobRef = blobRef
	rsc, _, err := fetcher.Fetch(blobRef)
	if err != nil {
		return fmt.Errorf("schema/filereader: fetching schema blob %s: %v", blobRef, err)
	}
	defer rsc.Close()
	if err = json.NewDecoder(rsc).Decode(ss); err != nil {
		return fmt.Errorf("schema/filereader: decoding schema blob %s: %v", blobRef, err)
	}
	return nil
}

// StaticSet returns the whole of the static set members of that directory
func (dr *DirReader) StaticSet() ([]*blobref.BlobRef, error) {
	if dr.staticSet != nil {
		return dr.staticSet, nil
	}
	staticSetBlobref := blobref.Parse(dr.ss.Entries)
	if staticSetBlobref == nil {
		return nil, fmt.Errorf("schema/filereader: Invalid blobref\n")
	}
	rsc, _, err := dr.fetcher.Fetch(staticSetBlobref)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: fetching schema blob %s: %v", staticSetBlobref, err)
	}
	ss, err := ParseSuperset(rsc)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: decoding schema blob %s: %v", staticSetBlobref, err)
	}
	if ss.Type != "static-set" {
		return nil, fmt.Errorf("schema/filereader: expected \"static-set\" schema blob for %s, got %q", staticSetBlobref, ss.Type)
	}
	for _, s := range ss.Members {
		member := blobref.Parse(s)
		if member == nil {
			return nil, fmt.Errorf("schema/filereader: invalid (static-set member) blobref\n")
		}
		dr.staticSet = append(dr.staticSet, member)
	}
	return dr.staticSet, nil
}

// Readdir implements the Directory interface.
func (dr *DirReader) Readdir(n int) (entries []DirectoryEntry, err error) {
	sts, err := dr.StaticSet()
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: can't get StaticSet: %v\n", err)
	}
	up := dr.current + n
	if n <= 0 {
		dr.current = 0
		up = len(sts)
	} else {
		if n > (len(sts) - dr.current) {
			err = io.EOF
			up = len(sts)
		}
	}
	for _, entryBref := range sts[dr.current:up] {
		entry, err := NewDirectoryEntryFromBlobRef(dr.fetcher, entryBref)
		if err != nil {
			return nil, fmt.Errorf("schema/filereader: can't create dirEntry: %v\n", err)
		}
		entries = append(entries, entry)
	}
	return entries, err
}
