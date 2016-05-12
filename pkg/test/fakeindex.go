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

package test

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types/camtypes"
	"golang.org/x/net/context"
)

var ClockOrigin = time.Unix(1322443956, 123456)

// A FakeIndex implements parts of search.Index and provides methods
// to controls the results, such as AddMeta, AddClaim,
// AddSignerAttrValue.
type FakeIndex struct {
	mu              sync.RWMutex
	meta            map[blob.Ref]camtypes.BlobMeta
	claims          map[blob.Ref][]camtypes.Claim // permanode -> claims
	signerAttrValue map[string]blob.Ref           // "<signer>\0<attr>\0<value>" -> blobref
	path            map[string]*camtypes.Path     // "<signer>\0<base>\0<suffix>" -> path

	fileLocation map[string]*camtypes.Location

	cllk  sync.RWMutex
	clock time.Time
}

func (x *FakeIndex) Lock()    { x.mu.Lock() }
func (x *FakeIndex) Unlock()  { x.mu.Unlock() }
func (x *FakeIndex) RLock()   { x.mu.RLock() }
func (x *FakeIndex) RUnlock() { x.mu.RUnlock() }

func NewFakeIndex() *FakeIndex {
	return &FakeIndex{
		meta:            make(map[blob.Ref]camtypes.BlobMeta),
		claims:          make(map[blob.Ref][]camtypes.Claim),
		signerAttrValue: make(map[string]blob.Ref),
		path:            make(map[string]*camtypes.Path),
		fileLocation:    make(map[string]*camtypes.Location),
		clock:           ClockOrigin,
	}
}

//
// Test methods
//

func (fi *FakeIndex) nextDate() time.Time {
	fi.cllk.Lock()
	defer fi.cllk.Unlock()
	fi.clock = fi.clock.Add(1 * time.Second)
	return fi.clock.UTC()
}

func (fi *FakeIndex) LastTime() time.Time {
	fi.cllk.RLock()
	defer fi.cllk.RUnlock()
	return fi.clock
}

func (fi *FakeIndex) AddMeta(br blob.Ref, camliType string, size uint32) {
	fi.meta[br] = camtypes.BlobMeta{
		Ref:       br,
		Size:      size,
		CamliType: camliType,
	}
}

func (fi *FakeIndex) AddClaim(owner, permanode blob.Ref, claimType, attr, value string) {
	date := fi.nextDate()

	claim := camtypes.Claim{
		Permanode: permanode,
		Signer:    owner,
		BlobRef:   blob.Ref{},
		Date:      date,
		Type:      claimType,
		Attr:      attr,
		Value:     value,
	}
	fi.claims[permanode] = append(fi.claims[permanode], claim)

	if claimType == "set-attribute" && strings.HasPrefix(attr, "camliPath:") {
		suffix := attr[len("camliPath:"):]
		path := &camtypes.Path{
			Target: blob.MustParse(value),
			Suffix: suffix,
		}
		fi.path[fmt.Sprintf("%s\x00%s\x00%s", owner, permanode, suffix)] = path
	}
}

func (fi *FakeIndex) AddSignerAttrValue(signer blob.Ref, attr, val string, latest blob.Ref) {
	fi.signerAttrValue[fmt.Sprintf("%s\x00%s\x00%s", signer, attr, val)] = latest
}

func (fi *FakeIndex) AddFileLocation(fileRef blob.Ref, loc camtypes.Location) {
	fi.fileLocation[fileRef.String()] = &loc
}

//
// Interface implementation
//

func (fi *FakeIndex) KeyId(context.Context, blob.Ref) (string, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetRecentPermanodes(ctx context.Context, dest chan<- camtypes.RecentPermanode, owner blob.Ref, limit int, before time.Time) error {
	panic("NOIMPL")
}

// TODO(mpl): write real tests
func (fi *FakeIndex) SearchPermanodesWithAttr(ctx context.Context, dest chan<- blob.Ref, request *camtypes.PermanodeByAttrRequest) error {
	panic("NOIMPL")
}

func (fi *FakeIndex) AppendClaims(ctx context.Context, dst []camtypes.Claim, permaNode blob.Ref,
	signerFilter blob.Ref,
	attrFilter string) ([]camtypes.Claim, error) {

	for _, cl := range fi.claims[permaNode] {
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

func (fi *FakeIndex) GetBlobMeta(ctx context.Context, br blob.Ref) (camtypes.BlobMeta, error) {
	bm, ok := fi.meta[br]
	if !ok {
		return camtypes.BlobMeta{}, os.ErrNotExist
	}
	return bm, nil
}

func (fi *FakeIndex) ExistingFileSchemas(bytesRef blob.Ref) ([]blob.Ref, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetFileInfo(ctx context.Context, fileRef blob.Ref) (camtypes.FileInfo, error) {
	_, ok := fi.meta[fileRef]
	if !ok {
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	return camtypes.FileInfo{}, nil
}

func (fi *FakeIndex) GetImageInfo(ctx context.Context, fileRef blob.Ref) (camtypes.ImageInfo, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetMediaTags(ctx context.Context, fileRef blob.Ref) (tags map[string]string, err error) {
	_, ok := fi.meta[fileRef]
	if !ok {
		return nil, os.ErrNotExist
	}
	return nil, nil
}

func (fi *FakeIndex) GetFileLocation(ctx context.Context, fileRef blob.Ref) (camtypes.Location, error) {
	if loc, ok := fi.fileLocation[fileRef.String()]; ok {
		return *loc, nil
	}
	return camtypes.Location{}, os.ErrNotExist
}

func (fi *FakeIndex) GetDirMembers(dir blob.Ref, dest chan<- blob.Ref, limit int) error {
	panic("NOIMPL")
}

func (fi *FakeIndex) PermanodeOfSignerAttrValue(ctx context.Context, signer blob.Ref, attr, val string) (blob.Ref, error) {
	if b, ok := fi.signerAttrValue[fmt.Sprintf("%s\x00%s\x00%s", signer, attr, val)]; ok {
		return b, nil
	}
	return blob.Ref{}, os.ErrNotExist
}

func (fi *FakeIndex) PathsOfSignerTarget(ctx context.Context, signer, target blob.Ref) ([]*camtypes.Path, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathsLookup(ctx context.Context, signer, base blob.Ref, suffix string) ([]*camtypes.Path, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathLookup(ctx context.Context, signer, base blob.Ref, suffix string, at time.Time) (*camtypes.Path, error) {
	if !at.IsZero() {
		panic("PathLookup with non-zero 'at' time not supported")
	}
	if p, ok := fi.path[fmt.Sprintf("%s\x00%s\x00%s", signer, base, suffix)]; ok {
		return p, nil
	}
	log.Printf("PathLookup miss for signer %q, base %q, suffix %q", signer, base, suffix)
	return nil, os.ErrNotExist
}

func (fi *FakeIndex) EdgesTo(ref blob.Ref, opts *camtypes.EdgesToOpts) ([]*camtypes.Edge, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) EnumerateBlobMeta(ctx context.Context, ch chan<- camtypes.BlobMeta) error {
	panic("NOIMPL")
}
