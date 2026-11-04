/*
Copyright 2026 The Perkeep Authors

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

package googletakeout

import (
	"context"
	"path"

	"perkeep.org/internal/takeout"
	"perkeep.org/internal/takeoutimport"
)

// This file is the import of Google Photos: its photos and videos, with
// their metadata, and its albums.

// photosDir is Google Photos' product directory in an export.
const photosDir = "Google Photos"

// A photosOverview is what Google Photos has in an export, and what of it
// the server has.
type photosOverview struct {
	Files      int  // photos and videos, to import
	Edited     int  // edited copies of them
	WithEdited bool // whether Files includes Edited
	Albums     int
	Distinct   int // distinct contents of Files
	Bytes      int64

	Have, Missing           int // of Distinct, which the server has or not
	HaveBytes, MissingBytes int64
}

// A photosResult is what the import of Google Photos did.
type photosResult struct {
	Uploaded      int // photos and videos uploaded
	UploadedBytes int64
	Meta          takeoutimport.MetaStats
}

// MetaString describes the result's metadata sync.
func (r *photosResult) MetaString() string { return r.Meta.String() }

// photos returns the export's photos and videos to import, by their
// contents, and how many edited copies of them it has.
func (r *run) photos() (cs []*takeout.Content, ents []takeout.Entry, edited int) {
	withEdited := r.acct.Attr(attrEdited) == "true"
	for _, e := range r.exp.Entries() {
		if !takeout.IsPhotosMedia(e.File.Name) {
			continue
		}
		if takeout.IsEdited(e.File.Name) {
			edited++
			if !withEdited {
				continue
			}
		}
		ents = append(ents, e)
	}
	return takeout.Contents(ents), ents, edited
}

// scanPhotos finds what Google Photos has in the export, and which of its
// photos and videos the server has.
func (r *run) scanPhotos(ctx context.Context) (*photosOverview, error) {
	cs, ents, edited := r.photos()
	r.rc.Logf("Asking the server about %d distinct contents of %d Google Photos files...", len(cs), len(ents))
	have, missing, err := r.im.Check(ctx, cs)
	if err != nil {
		return nil, err
	}
	ov := &photosOverview{
		Files:        len(ents),
		Edited:       edited,
		WithEdited:   r.acct.Attr(attrEdited) == "true",
		Distinct:     len(cs),
		Bytes:        takeoutimport.ContentsSize(cs),
		Have:         len(have),
		HaveBytes:    takeoutimport.ContentsSize(have),
		Missing:      len(missing),
		MissingBytes: takeoutimport.ContentsSize(missing),
	}
	for _, e := range r.exp.Entries() {
		if dir, ok := productDir(e.File.Name); ok && dir == photosDir && takeout.IsAlbumMetadata(path.Base(e.File.Name)) {
			ov.Albums++
		}
	}
	r.rc.Logf("The server has %d of the %d Google Photos photos and videos, and is missing %d (%s).",
		ov.Have, ov.Distinct, ov.Missing, takeoutimport.FormatBytes(ov.MissingBytes))
	return ov, nil
}

// importPhotos imports Google Photos' photos and videos that the server
// doesn't have, with their metadata, archives the whole export, and then
// updates the metadata of the photos the server had, and the albums.
func (r *run) importPhotos(ctx context.Context) (*photosResult, error) {
	cs, ents, _ := r.photos()
	r.rc.Logf("Asking the server about %d distinct contents of %d Google Photos files...", len(cs), len(ents))
	_, missing, err := r.im.Check(ctx, cs)
	if err != nil {
		return nil, err
	}
	r.rc.Logf("Uploading %d photos and videos (%s)...", len(missing), takeoutimport.FormatBytes(takeoutimport.ContentsSize(missing)))
	stats, err := r.im.Import(ctx, r.exp, cs, missing, takeoutimport.Options{Permanodes: true})
	if err != nil {
		return nil, err
	}
	res := &photosResult{
		Uploaded:      len(missing),
		UploadedBytes: takeoutimport.ContentsSize(missing),
		Meta:          *stats,
	}
	r.rc.Logf("Imported Google Photos: uploaded %d photos and videos (%s); %s", res.Uploaded, takeoutimport.FormatBytes(res.UploadedBytes), stats)
	return res, nil
}
