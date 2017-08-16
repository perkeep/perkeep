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
	"math"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/magic"

	"go4.org/types"
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

func (fi *FileInfo) IsText() bool {
	if strings.HasPrefix(fi.MIMEType, "text/") {
		return true
	}

	return strings.HasPrefix(mime.TypeByExtension(filepath.Ext(fi.FileName)), "text/")
}

func (fi *FileInfo) IsImage() bool {
	if strings.HasPrefix(fi.MIMEType, "image/") {
		return true
	}

	return strings.HasPrefix(mime.TypeByExtension(filepath.Ext(fi.FileName)), "image/")
}

func (fi *FileInfo) IsVideo() bool {
	if strings.HasPrefix(fi.MIMEType, "video/") {
		return true
	}

	if magic.HasExtension(fi.FileName, magic.VideoExtensions) {
		return true
	}

	return strings.HasPrefix(mime.TypeByExtension(filepath.Ext(fi.FileName)), "video/")
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

	// At, if non-zero, specifies that the attribute must have been set at
	// the latest at At.
	At time.Time
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

// Location describes a file or permanode that has a location.
type Location struct {
	// Latitude and Longitude represent the point location of this blob,
	// such as the place where a photo was taken.
	//
	// Negative values represent positions south of the equator or
	// west of the prime meridian:
	// Northern latitudes are positive, southern latitudes are negative.
	// Eastern longitudes are positive, western longitudes are negative.
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`

	// TODO(tajtiattila): decide how to represent blobs with
	// no single point location such as a track file once we index them,
	// perhaps with a N/S/E/W boundary. Note that a single point location
	// is still useful for these, to represent the starting point of a
	// track log or the main entrance of an area or building.
}

type Longitude float64

// WrapTo180 returns l converted to the [-180,180] interval.
func (l Longitude) WrapTo180() float64 {
	lf := float64(l)
	if lf >= -180 && lf <= 180 {
		return lf
	}
	if lf == 0 {
		return lf
	}
	if lf > 0 {
		return math.Mod(lf+180., 360.) - 180.
	}
	return math.Mod(lf-180., 360.) + 180.
}

// LocationBounds is a location area delimited by its fields. See Location for
// the fields meanings and values.
type LocationBounds struct {
	North float64 `json:"north"`
	South float64 `json:"south"`
	West  float64 `json:"west"`
	East  float64 `json:"east"`
}

func (l *LocationBounds) isEmpty() bool {
	return l == nil ||
		l.North == 0 &&
			l.South == 0 &&
			l.West == 0 &&
			l.East == 0
}

func (l *LocationBounds) isWithinLongitude(loc Location) bool {
	if l.East < l.West {
		// l is spanning over antimeridian
		return loc.Longitude >= l.West || loc.Longitude <= l.East
	}
	return loc.Longitude >= l.West && loc.Longitude <= l.East
}

// Expand returns a new LocationBounds nb. If either of loc coordinates is
// outside of b, nb is the dimensions of b expanded as little as possible in
// order to include loc. Otherwise, nb is just a copy of b.
func (b *LocationBounds) Expand(loc Location) *LocationBounds {
	if b.isEmpty() {
		return &LocationBounds{
			North: loc.Latitude,
			South: loc.Latitude,
			West:  loc.Longitude,
			East:  loc.Longitude,
		}
	}
	nb := &LocationBounds{
		North: b.North,
		South: b.South,
		West:  b.West,
		East:  b.East,
	}
	if loc.Latitude > nb.North {
		nb.North = loc.Latitude
	} else if loc.Latitude < nb.South {
		nb.South = loc.Latitude
	}
	if nb.isWithinLongitude(loc) {
		return nb
	}
	center := nb.center()
	dToCenter := center.Longitude - loc.Longitude
	if math.Abs(dToCenter) <= 180 {
		if dToCenter > 0 {
			// expand Westwards
			nb.West = loc.Longitude
		} else {
			// expand Eastwards
			nb.East = loc.Longitude
		}
		return nb
	}
	if dToCenter > 0 {
		// expand Eastwards
		nb.East = loc.Longitude
	} else {
		// expand Westwards
		nb.West = loc.Longitude
	}
	return nb
}

func (b *LocationBounds) center() Location {
	var lat, long float64
	lat = b.South + (b.North-b.South)/2.
	if b.West < b.East {
		long = b.West + (b.East-b.West)/2.
		return Location{
			Latitude:  lat,
			Longitude: long,
		}
	}
	// b is spanning over antimeridian
	awest := math.Abs(b.West)
	aeast := math.Abs(b.East)
	if awest > aeast {
		long = b.East - (awest-aeast)/2.
	} else {
		long = b.West + (aeast-awest)/2.
	}
	return Location{
		Latitude:  lat,
		Longitude: long,
	}
}
