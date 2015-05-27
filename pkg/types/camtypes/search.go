/*
Copyright 2013 The Camlistore Authors

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

package camtypes

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types"
)

type RecentPermanode struct {
	Permanode   blob.Ref
	Signer      blob.Ref // may be zero (!Valid())
	LastModTime time.Time
}

func (a RecentPermanode) Equal(b RecentPermanode) bool {
	return a.Permanode == b.Permanode &&
		a.Signer == b.Signer &&
		a.LastModTime.Equal(b.LastModTime)
}

type Claim struct {
	// TODO: document/decide how to represent "multi" claims here. One Claim each? Add Multi in here?
	// Move/merge this in with the schema package?

	BlobRef, Signer blob.Ref

	Date time.Time
	Type string // "set-attribute", "add-attribute", etc

	// If an attribute modification
	Attr, Value string
	Permanode   blob.Ref

	// If a DeleteClaim or a ShareClaim
	Target blob.Ref
}

func (c *Claim) String() string {
	return fmt.Sprintf(
		"camtypes.Claim{BlobRef: %s, Signer: %s, Permanode: %s, Date: %s, Type: %s, Attr: %s, Value: %s}",
		c.BlobRef, c.Signer, c.Permanode, c.Date, c.Type, c.Attr, c.Value)
}

type ClaimPtrsByDate []*Claim

func (cl ClaimPtrsByDate) Len() int           { return len(cl) }
func (cl ClaimPtrsByDate) Less(i, j int) bool { return cl[i].Date.Before(cl[j].Date) }
func (cl ClaimPtrsByDate) Swap(i, j int)      { cl[i], cl[j] = cl[j], cl[i] }

type ClaimsByDate []Claim

func (cl ClaimsByDate) Len() int           { return len(cl) }
func (cl ClaimsByDate) Less(i, j int) bool { return cl[i].Date.Before(cl[j].Date) }
func (cl ClaimsByDate) Swap(i, j int)      { cl[i], cl[j] = cl[j], cl[i] }

func (cl ClaimsByDate) String() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%d claims: ", len(cl))
	for _, r := range cl {
		buf.WriteString(r.String())
	}
	buf.WriteString("]")
	return buf.String()
}

// FileInfo describes a file or directory.
type FileInfo struct {
	// FileName is the base name of the file or directory.
	FileName string `json:"fileName"`

	// TODO(mpl): I've noticed that Size is actually set to the
	// number of entries in the dir. fix the doc or the behaviour?

	// Size is the size of file. It is not set for directories.
	Size int64 `json:"size"`

	// MIMEType may be set for files, but never for directories.
	MIMEType string `json:"mimeType,omitempty"`

	// Time is the earliest of any modtime, creation time, or EXIF
	// original/modification times found. It may be omitted (zero)
	// if unknown.
	Time *types.Time3339 `json:"time,omitempty"`

	// ModTime is the latest of any modtime, creation time, or EXIF
	// original/modification times found. If ModTime doesn't differ
	// from Time, ModTime is omitted (zero).
	ModTime *types.Time3339 `json:"modTime,omitempty"`

	// WholeRef is the digest of the entire file contents.
	// This will be zero for non-regular files, and may also be zero
	// for files above a certain size threshold.
	WholeRef blob.Ref `json:"wholeRef,omitempty"`
}

func (fi *FileInfo) IsImage() bool {
	return strings.HasPrefix(fi.MIMEType, "image/")
}

var videoExtensions = map[string]bool{
	"3gp":  true,
	"avi":  true,
	"flv":  true,
	"m1v":  true,
	"m2v":  true,
	"m4v":  true,
	"mkv":  true,
	"mov":  true,
	"mp4":  true,
	"mpeg": true,
	"mpg":  true,
	"ogv":  true,
	"wmv":  true,
}

func (fi *FileInfo) IsVideo() bool {
	if strings.HasPrefix(fi.MIMEType, "video/") {
		return true
	}

	var ext string
	if e := filepath.Ext(fi.FileName); strings.HasPrefix(e, ".") {
		ext = e[1:]
	} else {
		return false
	}

	// Case-insensitive lookup.
	// Optimistically assume a short ASCII extension and be
	// allocation-free in that case.
	var buf [10]byte
	lower := buf[:0]
	const utf8RuneSelf = 0x80 // from utf8 package, but not importing it.
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if c >= utf8RuneSelf {
			// Slow path.
			return videoExtensions[strings.ToLower(ext)]
		}
		if 'A' <= c && c <= 'Z' {
			lower = append(lower, c+('a'-'A'))
		} else {
			lower = append(lower, c)
		}
	}
	// The conversion from []byte to string doesn't allocate in
	// a map lookup.
	return videoExtensions[string(lower)]
}

// ImageInfo describes an image file.
//
// The Width and Height are uint16s to save memory in index/corpus.go, and that's
// the max size of a JPEG anyway. If we want to deal with larger sizes, we can use
// MaxUint16 as a sentinel to mean to look elsewhere. Or ditch this optimization.
type ImageInfo struct {
	// Width is the visible width of the image (after any necessary EXIF rotation).
	Width uint16 `json:"width"`
	// Height is the visible height of the image (after any necessary EXIF rotation).
	Height uint16 `json:"height"`
}

type Path struct {
	Claim, Base, Target blob.Ref
	ClaimDate           time.Time
	Suffix              string // ??
}

func (p *Path) String() string {
	return fmt.Sprintf("Path{Claim: %v, %v; Base: %v + Suffix %q => Target %v}",
		p.Claim, p.ClaimDate, p.Base, p.Suffix, p.Target)
}

type PermanodeByAttrRequest struct {
	Signer blob.Ref

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
	From      blob.Ref
	FromType  string // "permanode", "directory", etc
	FromTitle string // name of source permanode or directory
	To        blob.Ref
	BlobRef   blob.Ref // the blob responsible for the edge relationship
}

func (e *Edge) String() string {
	return fmt.Sprintf("[edge from:%s to:%s type:%s title:%s]", e.From, e.To, e.FromType, e.FromTitle)
}

// BlobMeta is the metadata kept for each known blob in the in-memory
// search index. It's kept as small as possible to save memory.
type BlobMeta struct {
	Ref  blob.Ref
	Size uint32

	// CamliType is non-empty if this blob is a Camlistore JSON
	// schema blob. If so, this is its "camliType" attribute.
	CamliType string

	// TODO(bradfitz): change CamliTypethis *string to save 8 bytes
}

// SearchErrorResponse is the JSON error response for a search request.
type SearchErrorResponse struct {
	Error     string `json:"error,omitempty"`     // The error message.
	ErrorType string `json:"errorType,omitempty"` // The type of the error.
}

// FileSearchResponse is the JSON response to a file search request.
type FileSearchResponse struct {
	SearchErrorResponse

	Files []blob.Ref `json:"files"` // Refs of the result files. Never nil.
}
