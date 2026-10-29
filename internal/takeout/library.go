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

package takeout

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
)

// A Library is the Google Photos photos and albums of an export, with
// their metadata.
type Library struct {
	Photos []*Photo // in the order of the contents given to NewLibrary
	Albums []*Album // sorted by title
}

// A Photo is a photo or video in a Library: one content, maybe in more
// than one directory.
type Photo struct {
	Content *Content
	Meta    *PhotoMetadata // or nil, if it has no sidecar
}

// An Album is a Google Photos album in a Library: the photos in an
// album's directory, and the album's metadata. If more than one album has
// the same title, Takeout puts their photos in the same directory, so
// they're one Album, with each one's metadata.
type Album struct {
	Dir    string
	Meta   []*AlbumMetadata // one per album with the directory's name
	Photos []*Photo         // in name order, without repeats
}

// Title returns the album's title.
func (a *Album) Title() string {
	for _, m := range a.Meta {
		if m.Title != "" {
			return m.Title
		}
	}
	return a.Dir
}

// Description returns the album's description, if any.
func (a *Album) Description() string {
	var descs []string
	for _, m := range a.Meta {
		if m.Description != "" && !slices.Contains(descs, m.Description) {
			descs = append(descs, m.Description)
		}
	}
	return strings.Join(descs, "\n\n")
}

// NewLibrary returns the Library of the photos and videos cs, the
// contents of entries of e that IsPhotosMedia, reading the sidecars and
// album metadata files in their directories, up to workers at once.
func NewLibrary(ctx context.Context, e *Export, cs []*Content, workers int) (*Library, error) {
	type dir struct {
		media    map[string]*Content // by base name
		sidecars map[string]Entry    // by base name
		albums   []Entry             // metadata.json files
	}
	dirs := make(map[string]*dir)
	getDir := func(name string) *dir {
		d, ok := dirs[name]
		if !ok {
			d = &dir{media: make(map[string]*Content), sidecars: make(map[string]Entry)}
			dirs[name] = d
		}
		return d
	}
	for _, c := range cs {
		for _, ent := range c.Entries {
			dirName, base := path.Split(ent.File.Name)
			getDir(dirName).media[base] = c
		}
	}
	var toRead []Entry
	for _, ent := range e.Entries() {
		dirName, base := path.Split(ent.File.Name)
		if !strings.HasPrefix(dirName, photosPrefix) || !strings.EqualFold(path.Ext(base), ".json") {
			continue
		}
		d, ok := dirs[dirName]
		if !ok {
			continue // no photos of interest in it
		}
		if IsAlbumMetadata(base) {
			if IsAlbumDir(path.Base(dirName)) {
				d.albums = append(d.albums, ent)
				toRead = append(toRead, ent)
			}
			continue
		}
		d.sidecars[base] = ent
		toRead = append(toRead, ent)
	}

	var mu sync.Mutex
	contents := make(map[string][]byte) // by entry name
	err := forEach(ctx, toRead, workers, func(ent Entry) error {
		data, err := ent.ReadAll(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		contents[ent.File.Name] = data
		return nil
	})
	if err != nil {
		return nil, err
	}

	lib := new(Library)
	photos := make(map[*Content]*Photo, len(cs))
	for _, c := range cs {
		p := &Photo{Content: c}
		photos[c] = p
		lib.Photos = append(lib.Photos, p)
	}
	for _, dirName := range slices.Sorted(maps.Keys(dirs)) {
		d := dirs[dirName]
		parsed := make(map[string]*PhotoMetadata)
		parse := func(sc string) *PhotoMetadata {
			if m, ok := parsed[sc]; ok {
				return m
			}
			m, err := ParsePhotoMetadata(contents[d.sidecars[sc].File.Name])
			if err != nil {
				m = nil // a bad sidecar is like a missing one
			}
			parsed[sc] = m
			return m
		}
		media := slices.Sorted(maps.Keys(d.media))
		sidecars := slices.Sorted(maps.Keys(d.sidecars))
		matched := MatchSidecars(media, sidecars, func(sc string) string {
			if m := parse(sc); m != nil {
				return m.Title
			}
			return ""
		})
		for _, name := range media {
			p := photos[d.media[name]]
			if sc, ok := matched[name]; ok && p.Meta == nil {
				p.Meta = parse(sc)
			}
		}
		if len(d.albums) == 0 {
			continue
		}
		a := &Album{Dir: path.Base(dirName)}
		slices.SortFunc(d.albums, func(x, y Entry) int {
			nx, _ := albumMetadataIndex(path.Base(x.File.Name))
			ny, _ := albumMetadataIndex(path.Base(y.File.Name))
			return nx - ny
		})
		for _, ent := range d.albums {
			m, err := ParseAlbumMetadata(contents[ent.File.Name])
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", ent.File.Name, err)
			}
			a.Meta = append(a.Meta, m)
		}
		seen := make(map[*Photo]bool)
		for _, name := range media {
			if p := photos[d.media[name]]; !seen[p] {
				seen[p] = true
				a.Photos = append(a.Photos, p)
			}
		}
		lib.Albums = append(lib.Albums, a)
	}
	slices.SortStableFunc(lib.Albums, func(a, b *Album) int { return strings.Compare(a.Title(), b.Title()) })
	return lib, nil
}
