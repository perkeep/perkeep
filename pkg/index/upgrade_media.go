/*
Copyright 2026 The Perkeep Authors.

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

package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go4.org/strutil"
	"perkeep.org/internal/images"
	"perkeep.org/internal/magic"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

// isMediaTagsUpgrade reports whether an index of schemaVersion can be
// upgraded to the required version by addMediaTags, without a reindex.
func isMediaTagsUpgrade(schemaVersion int) bool {
	return schemaVersion >= 7 && schemaVersion <= 9 && requiredSchemaVersion == 10
}

var errMissingMediaTags = errors.New("index: missing the rows of index schema versions 8 to 10")

// mediaTagFile is a file that may have rows added in index schema
// versions 8 to 10.
type mediaTagFile struct {
	fileRef  blob.Ref // for videos and Apple images, any of the files with wholeRef
	wholeRef blob.Ref
	mimeType string
	video    bool // a video that indexVideo indexes
	// heic is whether it's a HEIC file that version 9 indexes took to be
	// some other type, which populateFile reindexes whole.
	heic bool
	// Otherwise, it's an Apple image.
}

// mediaTagBatchSize is how many files' rows addMediaTags commits at once.
const mediaTagBatchSize = 500

// addMediaTags upgrades a version 7, 8, or 9 index to version 10. Version
// 8 added media tags from Apple MakerNotes and QuickTime metadata, and
// video locations; version 9 adds video sizes, and indexes 3GPP and AVI
// videos too; version 10 recognizes HEIC files by any of their HEVC
// brands, such as those of photos that iOS saves after editing, which
// earlier versions indexed as image/heif, by their names, without their
// image rows. Otherwise they index the same rows as version 7. Only
// videos, images whose EXIF Make is Apple, and such HEIC files can have
// the new rows, so rather than reindexing everything, addMediaTags reads
// just those files with the same code as populateFile, then sets the
// schema version. It's idempotent, so if it's interrupted, the next start
// runs it again.
func (x *Index) addMediaTags(ctx context.Context, fetcher blob.Fetcher) (err error) {
	from := x.schemaVersion()
	// Like fixMissingWholeRef, guard against misuse.
	if !isMediaTagsUpgrade(from) {
		panic("addMediaTags should only be used when upgrading from v7, v8, or v9 to v10 of the index schema")
	}
	start := time.Now()
	x.logf("upgrading index schema from version %d to %d by adding media rows...", from, requiredSchemaVersion)
	defer func() {
		if err != nil {
			x.logf("adding media rows failed: %v", err)
		}
	}()

	files, err := x.mediaTagFiles(from)
	if err != nil {
		return err
	}
	var videos, heics int
	for _, f := range files {
		switch {
		case f.video:
			videos++
		case f.heic:
			heics++
		}
	}
	x.logf("adding media rows of %d videos, %d Apple images, and %d HEIC files...", videos, len(files)-videos-heics, heics)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan mediaTagFile)
	results := make(chan *mutationMap)
	var failed atomic.Int64
	var wg sync.WaitGroup
	for range max(1, ReindexMaxProcs()) {
		wg.Go(func() {
			for f := range work {
				mm := new(mutationMap)
				if err := x.addFileMediaTags(ctx, fetcher, f, mm); err != nil {
					// Like a failed reindex of the file, which
					// wouldn't have these rows either.
					if failed.Add(1) <= 10 {
						x.logf("adding media rows of %v: %v", f.fileRef, err)
					}
				}
				select {
				case results <- mm:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() {
		defer close(work)
		for _, f := range files {
			select {
			case work <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var batch []*mutationMap
	var done int
	commit := func() error {
		bm := x.s.BeginBatch()
		for _, mm := range batch {
			for k, v := range mm.kv {
				bm.Set(k, v)
			}
		}
		batch = batch[:0]
		return x.s.CommitBatch(bm)
	}
	for mm := range results {
		done++
		if len(mm.kv) > 0 {
			batch = append(batch, mm)
		}
		if len(batch) >= mediaTagBatchSize {
			if err := commit(); err != nil {
				return err
			}
		}
		select {
		case <-ticker.C:
			x.logf("added media rows of %d of %d files", done, len(files))
		default:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := commit(); err != nil {
		return err
	}
	if err := x.s.Set(keySchemaVersion.name, fmt.Sprint(requiredSchemaVersion)); err != nil {
		return fmt.Errorf("could not write index schema version %d: %w", requiredSchemaVersion, err)
	}
	x.logf("upgraded index schema to version %d in %v; %d files failed", requiredSchemaVersion, time.Since(start).Round(time.Millisecond), failed.Load())
	return nil
}

// appleMakeKeySuffix ends the keyEXIFTag keys of images' IFD0 Make tags.
var appleMakeKeySuffix = fmt.Sprintf("|%d|%04x", exifIFD0, 0x010f)

// mediaTagFiles returns the files that can have rows added in upgrading an
// index from schema version from: videos, and images whose EXIF Make is
// Apple, one file per content, for indexes before versions 9 and 8; and
// every file that version 9 indexes didn't take to be HEIC but may be:
// those indexed as image/heif, by their names, or named as HEIC.
func (x *Index) mediaTagFiles(from int) (files []mediaTagFile, err error) {
	// Version 8 indexes already have the Apple images' media tags, and
	// version 9 ones the videos' too.
	withApple, withVideos := from < 8, from < 9
	var vals []string

	// Apple images, by their indexed EXIF Make.
	apple := make(map[blob.Ref]bool)
	it := x.queryPrefix(keyEXIFTag)
	for withApple && it.Next() {
		// exiftag|wholeRef|dir|tag = type|n|vals
		k := it.Key()
		if !strings.HasSuffix(k, appleMakeKeySuffix) {
			continue
		}
		vals = strutil.AppendSplitN(vals[:0], it.Value(), "|", 3)
		if len(vals) != 3 || vals[0] != "string" || strings.TrimSpace(urld(vals[2])) != "Apple" {
			continue
		}
		wholeRef, ok := blob.Parse(strings.TrimSuffix(strings.TrimPrefix(k, keyEXIFTag.name+"|"), appleMakeKeySuffix))
		if ok {
			apple[wholeRef] = true
		}
	}
	if err := it.Close(); err != nil {
		return nil, err
	}

	// The files with those contents, videos, and possible HEIC files.
	seen := make(map[blob.Ref]bool)
	it = x.queryPrefix(keyFileInfo)
	defer closeIterator(it, &err)
	for it.Next() {
		// fileinfo|fileRef = size|fileName|mimeType|wholeRef
		fileRef, ok := blob.Parse(strings.TrimPrefix(it.Key(), keyFileInfo.name+"|"))
		if !ok {
			continue
		}
		vals = strutil.AppendSplitN(vals[:0], it.Value(), "|", 4)
		if len(vals) != 4 {
			continue
		}
		wholeRef, ok := blob.Parse(urld(vals[3]))
		if !ok {
			continue
		}
		mimeType := urld(vals[2])
		if maybeMissedHEIC(urld(vals[1]), mimeType) {
			// Each file's own rows, such as this fileinfo one,
			// need reindexing, not just its contents'.
			files = append(files, mediaTagFile{fileRef: fileRef, wholeRef: wholeRef, mimeType: mimeType, heic: true})
			continue
		}
		if seen[wholeRef] {
			continue
		}
		video := withVideos && isIndexedVideoMIME(mimeType)
		if !video && !apple[wholeRef] {
			continue
		}
		seen[wholeRef] = true
		files = append(files, mediaTagFile{fileRef: fileRef, wholeRef: wholeRef, mimeType: mimeType, video: video})
	}
	return files, nil
}

// maybeMissedHEIC reports whether a file indexed by a version 9 index with
// fileName and mimeType may be a HEIC file that it didn't recognize: one
// indexed as image/heif, which files named ".heic" or ".heif" that aren't
// sniffed as HEIC are, or otherwise named as HEIC but not indexed so.
func maybeMissedHEIC(fileName, mimeType string) bool {
	if mimeType == "image/heif" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	return (ext == ".heic" || ext == ".heif") && mimeType != "image/heic"
}

// addFileMediaTags adds to mm the rows of f that populateFile indexes for
// schema versions 8 to 10: for HEIC files, all of them, and otherwise the
// media tags and location of f's contents.
func (x *Index) addFileMediaTags(ctx context.Context, fetcher blob.Fetcher, f mediaTagFile, mm *mutationMap) error {
	rc, _, err := fetcher.Fetch(ctx, f.fileRef)
	if err != nil {
		return err
	}
	b, err := schema.BlobFromReader(f.fileRef, rc)
	rc.Close()
	if err != nil {
		return err
	}
	if f.heic {
		return x.populateFile(ctx, &missTrackFetcher{fetcher: fetcher}, b, mm)
	}
	fr, err := b.NewFileReader(fetcher)
	if err != nil {
		return err
	}
	defer fr.Close()
	if f.video {
		x.indexVideo(fr, fr.Size(), f.mimeType, f.wholeRef, mm)
		return nil
	}

	prefix := make([]byte, min(imagePrefixSize, fr.Size()))
	if _, err := io.ReadFull(fr, prefix); err != nil {
		return err
	}
	var conf images.Config
	if magic.MIMEType(prefix) == "image/heic" {
		// Only HEIC images need their config, for their EXIF item.
		conf, _ = decodeImageConfig(prefix, fetcher, b)
	}
	return readPrefixOrFile(imageEXIFData(prefix, conf), fetcher, b, func(r filePrefixReader) error {
		return x.indexAppleMakerNoteEXIF(f.wholeRef, r, mm)
	})
}
