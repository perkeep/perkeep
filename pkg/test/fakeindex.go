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
)

var ClockOrigin = time.Unix(1322443956, 123456)

// A FakeIndex implements parts of search.Index and provides methods
// to controls the results, such as AddMeta, AddClaim,
// AddSignerAttrValue.
type FakeIndex struct {
	lk              sync.Mutex
	mimeType        map[string]string // blobref -> type
	size            map[string]int64
	ownerClaims     map[string]camtypes.ClaimList // "<permanode>/<owner>" -> ClaimList
	signerAttrValue map[string]blob.Ref           // "<signer>\0<attr>\0<value>" -> blobref
	path            map[string]*camtypes.Path     // "<signer>\0<base>\0<suffix>" -> path

	cllk  sync.RWMutex
	clock time.Time
}

func NewFakeIndex() *FakeIndex {
	return &FakeIndex{
		mimeType:        make(map[string]string),
		size:            make(map[string]int64),
		ownerClaims:     make(map[string]camtypes.ClaimList),
		signerAttrValue: make(map[string]blob.Ref),
		path:            make(map[string]*camtypes.Path),
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

func (fi *FakeIndex) AddMeta(blob blob.Ref, mime string, size int64) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	fi.mimeType[blob.String()] = mime
	fi.size[blob.String()] = size
}

func (fi *FakeIndex) AddClaim(owner, permanode blob.Ref, claimType, attr, value string) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	date := fi.nextDate()

	claim := &camtypes.Claim{
		Permanode: permanode,
		Signer:    blob.Ref{},
		BlobRef:   blob.Ref{},
		Date:      date,
		Type:      claimType,
		Attr:      attr,
		Value:     value,
	}
	key := permanode.String() + "/" + owner.String()
	fi.ownerClaims[key] = append(fi.ownerClaims[key], claim)

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
	fi.lk.Lock()
	defer fi.lk.Unlock()
	fi.signerAttrValue[fmt.Sprintf("%s\x00%s\x00%s", signer, attr, val)] = latest
}

//
// Interface implementation
//

func (fi *FakeIndex) GetRecentPermanodes(dest chan<- camtypes.RecentPermanode, owner blob.Ref, limit int) error {
	panic("NOIMPL")
}

// TODO(mpl): write real tests
func (fi *FakeIndex) SearchPermanodesWithAttr(dest chan<- blob.Ref, request *camtypes.PermanodeByAttrRequest) error {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetOwnerClaims(permaNode, owner blob.Ref) (camtypes.ClaimList, error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	return fi.ownerClaims[permaNode.String()+"/"+owner.String()], nil
}

func (fi *FakeIndex) GetBlobMIMEType(blob blob.Ref) (mime string, size int64, err error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	bs := blob.String()
	mime, ok := fi.mimeType[bs]
	if !ok {
		return "", 0, os.ErrNotExist
	}
	return mime, fi.size[bs], nil
}

func (fi *FakeIndex) ExistingFileSchemas(bytesRef blob.Ref) ([]blob.Ref, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetFileInfo(fileRef blob.Ref) (camtypes.FileInfo, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetImageInfo(fileRef blob.Ref) (camtypes.ImageInfo, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetDirMembers(dir blob.Ref, dest chan<- blob.Ref, limit int) error {
	panic("NOIMPL")
}

func (fi *FakeIndex) PermanodeOfSignerAttrValue(signer blob.Ref, attr, val string) (blob.Ref, error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	if b, ok := fi.signerAttrValue[fmt.Sprintf("%s\x00%s\x00%s", signer, attr, val)]; ok {
		return b, nil
	}
	return blob.Ref{}, os.ErrNotExist
}

func (fi *FakeIndex) PathsOfSignerTarget(signer, target blob.Ref) ([]*camtypes.Path, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathsLookup(signer, base blob.Ref, suffix string) ([]*camtypes.Path, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathLookup(signer, base blob.Ref, suffix string, at time.Time) (*camtypes.Path, error) {
	if !at.IsZero() {
		panic("PathLookup with non-zero 'at' time not supported")
	}
	fi.lk.Lock()
	defer fi.lk.Unlock()
	if p, ok := fi.path[fmt.Sprintf("%s\x00%s\x00%s", signer, base, suffix)]; ok {
		return p, nil
	}
	log.Printf("PathLookup miss for signer %q, base %q, suffix %q", signer, base, suffix)
	return nil, os.ErrNotExist
}

func (fi *FakeIndex) EdgesTo(ref blob.Ref, opts *camtypes.EdgesToOpts) ([]*camtypes.Edge, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) EnumerateBlobMeta(ch chan<- camtypes.BlobMeta) error {
	panic("NOIMPL")
}
