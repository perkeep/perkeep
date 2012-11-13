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
	"bytes"
	"fmt"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
)

type Result struct {
	BlobRef     *blobref.BlobRef
	Signer      *blobref.BlobRef // may be nil
	LastModTime int64            // seconds since epoch
}

// Results exists mostly for debugging, to provide a String method on
// a slice of Result.
type Results []*Result

func (s Results) String() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%d search results: ", len(s))
	for _, r := range s {
		fmt.Fprintf(&buf, "{BlobRef: %s, Signer: %s, LastModTime: %d}",
			r.BlobRef, r.Signer, r.LastModTime)
	}
	buf.WriteString("]")
	return buf.String()
}

// TODO: move this to schema or something?
type Claim struct {
	BlobRef, Signer, Permanode *blobref.BlobRef

	Date time.Time
	Type string // "set-attribute", "add-attribute", etc

	// If an attribute modification
	Attr, Value string
}

func (c *Claim) String() string {
	return fmt.Sprintf(
		"search.Claim{BlobRef: %s, Signer: %s, Permanode: %s, Date: %s, Type: %s, Attr: %s, Value: %s}",
		c.BlobRef, c.Signer, c.Permanode, c.Date, c.Type, c.Attr, c.Value)
}

type ClaimList []*Claim

func (cl ClaimList) Len() int {
	return len(cl)
}

func (cl ClaimList) Less(i, j int) bool {
	// TODO: memoize Seconds in unexported Claim field
	return cl[i].Date.Unix() < cl[j].Date.Unix()
}

func (cl ClaimList) Swap(i, j int) {
	cl[i], cl[j] = cl[j], cl[i]
}

func (cl ClaimList) String() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%d claims: ", len(cl))
	for _, r := range cl {
		buf.WriteString(r.String())
	}
	buf.WriteString("]")
	return buf.String()
}

type FileInfo struct {
	Size     int64  `json:"size"`
	FileName string `json:"fileName"`
	MimeType string `json:"mimeType"`
}

func (fi *FileInfo) IsImage() bool {
	return strings.HasPrefix(fi.MimeType, "image/")
}

type Path struct {
	Claim, Base, Target *blobref.BlobRef
	ClaimDate           string
	Suffix              string
}

func (p *Path) String() string {
	return fmt.Sprintf("Path{Claim: %v, %v; Base: %v + Suffix %q => Target %v}",
		p.Claim, p.ClaimDate, p.Base, p.Suffix, p.Target)
}

type PermanodeByAttrRequest struct {
	Signer *blobref.BlobRef

	// Attribute to search. currently supported: "tag", "title"
	// If FuzzyMatch is set, this can be blank to search all
	// attributes.
	Attribute string

	// The attribute value to find exactly (or roughly, if
	// FuzzyMatch is set)
	// If blank, the permanodes with Attribute as an attribute
	// (set to any value) are searched.
	Query string

	FuzzyMatch bool // by default, an exact match is required
	MaxResults int  // optional max results
}

type EdgesToOpts struct {
	Max int
	// TODO: filter by type?
}

type Edge struct {
	From      *blobref.BlobRef
	FromType  string // "permanode", "directory", etc
	FromTitle string // name of source permanode or directory
	To        *blobref.BlobRef
}

func (e *Edge) String() string {
	return fmt.Sprintf("[edge from:%s to:%s type:%s title:%s]", e.From, e.To, e.FromType, e.FromTitle)
}

type Index interface {
	// dest must be closed, even when returning an error.
	// limit is <= 0 for default.  smallest possible default is 0
	GetRecentPermanodes(dest chan *Result,
		owner *blobref.BlobRef,
		limit int) error

	// SearchPermanodes finds permanodes matching the provided
	// request and sends unique permanode blobrefs to dest.
	// In particular, if request.FuzzyMatch is true, a fulltext
	// search is performed (if supported by the attribute(s))
	// instead of an exact match search.
	// If request.Query is blank, the permanodes which have
	// request.Attribute as an attribute (regardless of its value)
	// are searched.
	// Additionally, if request.Attribute is blank, all attributes
	// are searched (as fulltext), otherwise the search is
	// restricted  to the named attribute.
	//
	// dest is always closed, regardless of the error return value.
	SearchPermanodesWithAttr(dest chan<- *blobref.BlobRef,
		request *PermanodeByAttrRequest) error

	GetOwnerClaims(permaNode, owner *blobref.BlobRef) (ClaimList, error)

	// os.ErrNotExist should be returned if the blob isn't known
	GetBlobMimeType(blob *blobref.BlobRef) (mime string, size int64, err error)

	// ExistingFileSchemas returns 0 or more blobrefs of "bytes"
	// (TODO(bradfitz): or file?) schema blobs that represent the
	// bytes of a file given in bytesRef.  The file schema blobs
	// returned are not guaranteed to reference chunks that still
	// exist on the blobservers, though.  It's purely a hint for
	// clients to avoid uploads if possible.  Before re-using any
	// returned blobref they should be checked.
	//
	// Use case: a user drag & drops a large file onto their
	// browser to upload.  (imagine that "large" means anything
	// larger than a blobserver's max blob size) JavaScript can
	// first SHA-1 the large file locally, then send the
	// wholeFileRef to this call and see if they'd previously
	// uploaded the same file in the past.  If so, the upload
	// can be avoided if at least one of the returned schemaRefs
	// can be validated (with a validating HEAD request) to still
	// all exist on the blob server.
	ExistingFileSchemas(wholeFileRef *blobref.BlobRef) (schemaRefs []*blobref.BlobRef, err error)

	// Should return os.ErrNotExist if not found.
	GetFileInfo(fileRef *blobref.BlobRef) (*FileInfo, error)

	// Given an owner key, a camliType 'claim', 'attribute' name,
	// and specific 'value', find the most recent permanode that has
	// a corresponding 'set-attribute' claim attached.
	// Returns os.ErrNotExist if none is found.
	// TODO(bradfitz): ErrNotExist here is a weird error message ("file" not found). change.
	// Only attributes white-listed by IsIndexedAttribute are valid.
	PermanodeOfSignerAttrValue(signer *blobref.BlobRef, attr, val string) (*blobref.BlobRef, error)

	// PathsOfSignerTarget queries the index about "camliPath:"
	// URL-dispatch attributes.
	//
	// It returns a list of all the path claims that have been signed
	// by the provided signer and point at the given target.
	//
	// This is used when editing a permanode, to figure work up
	// the name resolution tree backwards ultimately to a
	// camliRoot permanode (which should know its base URL), and
	// then the complete URL(s) of a target can be found.
	PathsOfSignerTarget(signer, target *blobref.BlobRef) ([]*Path, error)

	// All Path claims for (signer, base, suffix)
	PathsLookup(signer, base *blobref.BlobRef, suffix string) ([]*Path, error)

	// Most recent Path claim for (signer, base, suffix) as of
	// provided time 'at', or most recent if 'at' is nil.
	PathLookup(signer, base *blobref.BlobRef, suffix string, at time.Time) (*Path, error)

	// EdgesTo finds references to the provided ref.
	//
	// For instance, if ref is a permanode, it might find the parent permanodes
	// that have ref as a member.
	// Or, if ref is a static file, it might find static directories which contain
	// that file.
	// This is a way to go "up" or "back" in a hierarchy.
	//
	// opts may be nil to accept the defaults.
	EdgesTo(ref *blobref.BlobRef, opts *EdgesToOpts) ([]*Edge, error)
}

// TODO(bradfitz): rename this? This is really about signer-attr-value
// (PermanodeOfSignerAttrValue), and not about indexed attributes in general.
func IsIndexedAttribute(attr string) bool {
	switch attr {
	case "camliRoot", "tag", "title":
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
