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
	"camli/blobref"

	"os"
	"time"
)

type Result struct {
	BlobRef     *blobref.BlobRef
	Signer      *blobref.BlobRef // may be nil
	LastModTime int64            // seconds since epoch
}

// TODO: move this to schema or something?
type Claim struct {
	BlobRef, Signer, Permanode *blobref.BlobRef

	Date *time.Time
	Type string // "set-attribute", "add-attribute", etc

	// If an attribute modification
	Attr, Value string
}

type Index interface {
	// dest is closed
	// limit is <= 0 for default.  smallest possible default is 0
	GetRecentPermanodes(dest chan *Result,
	owner []*blobref.BlobRef,
	limit int) os.Error

	GetOwnerClaims(permaNode, owner *blobref.BlobRef) ([]*Claim, os.Error)
}
