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
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/rwcarlsen/goexif/exif"
	"perkeep.org/internal/media/applemakernote"
	"perkeep.org/internal/media/quicktime"
	"perkeep.org/pkg/blob"
)

// Media tags indexed from Apple MakerNotes and QuickTime metadata, in
// addition to the audio tags of indexMusic. Note: if you add to these,
// please update internal/searchtypes/query.go's MediaTagConstraint Tag
// docs.
const (
	// MediaTagContentIdentifier is the Apple content identifier, which
	// links the still image and the video of a Live Photo.
	MediaTagContentIdentifier = "contentidentifier"

	// From Apple MakerNotes.
	MediaTagBurstUUID       = "burstuuid"
	MediaTagPhotoIdentifier = "photoidentifier"

	// From QuickTime metadata. Audio files have durations too.
	MediaTagDurationMS             = "durationms"
	MediaTagMake                   = "make"
	MediaTagModel                  = "model"
	MediaTagSoftware               = "software"
	MediaTagCreationDate           = "creationdate"
	MediaTagLivePhotoAuto          = "livephotoauto"
	MediaTagLivePhotoVitalityScore = "livephotovitalityscore"
)

func setMediaTag(mm *mutationMap, wholeRef blob.Ref, tag, value string) {
	if value != "" {
		mm.Set(keyMediaTag.Key(wholeRef, tag), keyMediaTag.Val(value))
	}
}

// indexAppleMakerNote indexes the Apple MakerNote in ex, if any, as
// media tags.
func (ix *Index) indexAppleMakerNote(wholeRef blob.Ref, ex *exif.Exif, mm *mutationMap) {
	tag, err := ex.Get(exif.MakerNote)
	if err != nil {
		return
	}
	mn, err := applemakernote.Parse(tag.Val)
	if err != nil {
		if !errors.Is(err, applemakernote.ErrNotApple) {
			ix.printf("index: Apple MakerNote: %v", err)
		}
		return
	}
	setMediaTag(mm, wholeRef, MediaTagContentIdentifier, mn.ContentIdentifier)
	setMediaTag(mm, wholeRef, MediaTagBurstUUID, mn.BurstUUID)
	setMediaTag(mm, wholeRef, MediaTagPhotoIdentifier, mn.PhotoIdentifier)
}

// isQuickTimeMIME reports whether files of the given MIME type may
// contain QuickTime metadata.
func isQuickTimeMIME(mimeType string) bool {
	return mimeType == "video/quicktime" || mimeType == "video/mp4"
}

// indexQuickTime indexes a QuickTime or MP4 movie's duration and Apple
// metadata as media tags, and its location like an image's EXIF GPS
// location.
func (ix *Index) indexQuickTime(r io.ReaderAt, size int64, wholeRef blob.Ref, mm *mutationMap) {
	md, err := quicktime.ReadMetadata(r, size)
	if err != nil {
		if debugEnv {
			ix.printf("index: QuickTime metadata: %v", err)
		}
		return
	}
	if md.Duration > 0 {
		setMediaTag(mm, wholeRef, MediaTagDurationMS, strconv.FormatInt(md.Duration.Milliseconds(), 10))
	}
	for tag, key := range map[string]string{
		MediaTagContentIdentifier:      quicktime.KeyContentIdentifier,
		MediaTagMake:                   quicktime.KeyMake,
		MediaTagModel:                  quicktime.KeyModel,
		MediaTagSoftware:               quicktime.KeySoftware,
		MediaTagCreationDate:           quicktime.KeyCreationDate,
		MediaTagLivePhotoAuto:          quicktime.KeyLivePhotoAuto,
		MediaTagLivePhotoVitalityScore: quicktime.KeyLivePhotoVitality,
	} {
		setMediaTag(mm, wholeRef, tag, md.Items[key])
	}
	if lat, long, ok := quicktime.ParseISO6709(md.Items[quicktime.KeyLocationISO6709]); ok {
		// Same 7-place precision as EXIF GPS locations.
		mm.Set(keyEXIFGPS.Key(wholeRef), keyEXIFGPS.Val(fmt.Sprintf("%.7f", lat), fmt.Sprintf("%.7f", long)))
	}
}
