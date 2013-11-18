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

	BlobRef, Signer, Permanode blob.Ref

	Date time.Time
	Type string // "set-attribute", "add-attribute", etc

	// If an attribute modification
	Attr, Value string
}

func (c *Claim) String() string {
	return fmt.Sprintf(
		"camtypes.Claim{BlobRef: %s, Signer: %s, Permanode: %s, Date: %s, Type: %s, Attr: %s, Value: %s}",
		c.BlobRef, c.Signer, c.Permanode, c.Date, c.Type, c.Attr, c.Value)
}

type ClaimsByDate []Claim

func (cl ClaimsByDate) Len() int {
	return len(cl)
}

func (cl ClaimsByDate) Less(i, j int) bool {
	return cl[i].Date.Before(cl[j].Date)
}

func (cl ClaimsByDate) Swap(i, j int) {
	cl[i], cl[j] = cl[j], cl[i]
}

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
	FileName string `json:"fileName"`

	// Size is the size of files. It is not set for directories.
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
}

func (fi *FileInfo) IsImage() bool {
	return strings.HasPrefix(fi.MIMEType, "image/")
}

// ImageInfo describes an image file.
type ImageInfo struct {
	// Width is the visible width of the image (after any necessary EXIF rotation).
	Width int `json:"width"`
	// Height is the visible height of the image (after any necessary EXIF rotation).
	Height int `json:"height"`
}

type Path struct {
	Claim, Base, Target blob.Ref
	ClaimDate           string // TODO: why is this a string?
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
}

func (e *Edge) String() string {
	return fmt.Sprintf("[edge from:%s to:%s type:%s title:%s]", e.From, e.To, e.FromType, e.FromTitle)
}

type BlobMeta struct {
	Ref       blob.Ref
	Size      int
	CamliType string
}
