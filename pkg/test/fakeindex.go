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

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/search"
)

type FakeIndex struct {
	lk              sync.Mutex
	mimeType        map[string]string // blobref -> type
	size            map[string]int64
	ownerClaims     map[string]search.ClaimList // "<permanode>/<owner>" -> ClaimList
	signerAttrValue map[string]*blobref.BlobRef // "<signer>\0<attr>\0<value>" -> blobref
	path            map[string]*search.Path     // "<signer>\0<base>\0<suffix>" -> path

	cllk  sync.Mutex
	clock int64
}

var _ search.Index = (*FakeIndex)(nil)

func NewFakeIndex() *FakeIndex {
	return &FakeIndex{
		mimeType:        make(map[string]string),
		size:            make(map[string]int64),
		ownerClaims:     make(map[string]search.ClaimList),
		signerAttrValue: make(map[string]*blobref.BlobRef),
		path:            make(map[string]*search.Path),
	}
}

//
// Test methods
//

func (fi *FakeIndex) nextDate() time.Time {
	fi.cllk.Lock()
	fi.clock++
	clock := fi.clock
	fi.cllk.Unlock()
	return time.Unix(clock, 0).UTC()
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

	claim := &search.Claim{
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

	if claimType == "set-attribute" && strings.HasPrefix(attr, "camliPath:") {
		suffix := attr[len("camliPath:"):]
		path := &search.Path{
			Target: blobref.MustParse(value),
			Suffix: suffix,
		}
		fi.path[fmt.Sprintf("%s\x00%s\x00%s", owner, permanode, suffix)] = path
	}
}

func (fi *FakeIndex) AddSignerAttrValue(signer *blobref.BlobRef, attr, val string, latest *blobref.BlobRef) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	fi.signerAttrValue[fmt.Sprintf("%s\x00%s\x00%s", signer, attr, val)] = latest
}

//
// Interface implementation
//

func (fi *FakeIndex) GetRecentPermanodes(dest chan *search.Result, owner *blobref.BlobRef, limit int) error {
	panic("NOIMPL")
}

// TODO(mpl): write real tests
func (fi *FakeIndex) SearchPermanodesWithAttr(dest chan<- *blobref.BlobRef, request *search.PermanodeByAttrRequest) error {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetOwnerClaims(permaNode, owner *blobref.BlobRef) (search.ClaimList, error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	return fi.ownerClaims[permaNode.String()+"/"+owner.String()], nil
}

func (fi *FakeIndex) GetBlobMimeType(blob *blobref.BlobRef) (mime string, size int64, err error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	bs := blob.String()
	mime, ok := fi.mimeType[bs]
	if !ok {
		return "", 0, os.ErrNotExist
	}
	return mime, fi.size[bs], nil
}

func (fi *FakeIndex) ExistingFileSchemas(bytesRef *blobref.BlobRef) ([]*blobref.BlobRef, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) GetFileInfo(fileRef *blobref.BlobRef) (*search.FileInfo, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PermanodeOfSignerAttrValue(signer *blobref.BlobRef, attr, val string) (*blobref.BlobRef, error) {
	fi.lk.Lock()
	defer fi.lk.Unlock()
	if b, ok := fi.signerAttrValue[fmt.Sprintf("%s\x00%s\x00%s", signer, attr, val)]; ok {
		return b, nil
	}
	return nil, os.ErrNotExist
}

func (fi *FakeIndex) PathsOfSignerTarget(signer, target *blobref.BlobRef) ([]*search.Path, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathsLookup(signer, base *blobref.BlobRef, suffix string) ([]*search.Path, error) {
	panic("NOIMPL")
}

func (fi *FakeIndex) PathLookup(signer, base *blobref.BlobRef, suffix string, at time.Time) (*search.Path, error) {
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

func (fi *FakeIndex) EdgesTo(ref *blobref.BlobRef, opts *search.EdgesToOpts) ([]*search.Edge, error) {
	panic("NOIMPL")
}
