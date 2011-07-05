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

package search

import (
	"os"
	"sync"
	"time"

	"camli/blobref"
)

type FakeIndex struct {
	lk          sync.Mutex
	mimeType    map[string]string // blobref -> type
	size        map[string]int64
	ownerClaims map[string]ClaimList // "<permanode>/<owner>" -> ClaimList

	cllk  sync.Mutex
	clock int64
}

var _ Index = (*FakeIndex)(nil)

func NewFakeIndex() *FakeIndex {
	return &FakeIndex{
		mimeType:    make(map[string]string),
		size:        make(map[string]int64),
		ownerClaims: make(map[string]ClaimList),
	}
}

//
// Test methods
//

func (fi *FakeIndex) nextDate() *time.Time {
	fi.cllk.Lock()
	fi.clock++
	clock := fi.clock
	fi.cllk.Unlock()
	return time.SecondsToUTC(clock)
}

func (fi *FakeIndex) AddMeta(blob *blobref.BlobRef, mime string, size int64) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	fi.mimeType[blob.String()] = mime
	fi.size[blob.String()] = size
}

func (fi *FakeIndex) AddClaim(owner, permanode *blobref.BlobRef, claimType, attr, value string) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	date := fi.nextDate()

	claim := &Claim{
		Permanode: permanode,
		Signer:    nil,
		BlobRef:   nil,
		Date:      date,
		Type:      claimType,
		Attr:      attr,
		Value:     value,
	}
	key := permanode.String() + "/" + owner.String()
	fi.ownerClaims[key] = append(fi.ownerClaims[key], claim)
}

//
// Interface implementation
//

func (fi *FakeIndex) GetRecentPermanodes(dest chan *Result,
owner []*blobref.BlobRef,
limit int) os.Error {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetTaggedPermanodes(dest chan<- *blobref.BlobRef,
signer *blobref.BlobRef,
tag string, limit int) os.Error {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetOwnerClaims(permaNode, owner *blobref.BlobRef) (ClaimList, os.Error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	return fi.ownerClaims[permaNode.String()+"/"+owner.String()], nil
}

func (fi *FakeIndex) GetBlobMimeType(blob *blobref.BlobRef) (mime string, size int64, err os.Error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	bs := blob.String()
	mime, ok := fi.mimeType[bs]
	if !ok {
		return "", 0, os.ENOENT
	}
	return mime, fi.size[bs], nil
}

func (fi *FakeIndex) ExistingFileSchemas(bytesRef *blobref.BlobRef) ([]*blobref.BlobRef, os.Error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetFileInfo(fileRef *blobref.BlobRef) (*FileInfo, os.Error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PermanodeOfSignerAttrValue(signer *blobref.BlobRef, attr, val string) (*blobref.BlobRef, os.Error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathsOfSignerTarget(signer, target *blobref.BlobRef) ([]*Path, os.Error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathsLookup(signer, base *blobref.BlobRef, suffix string) ([]*Path, os.Error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathLookup(signer, base *blobref.BlobRef, suffix string, at *time.Time) (*Path, os.Error) {
	panic("NOIMPL")
}
