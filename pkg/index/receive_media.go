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
	"perkeep.org/internal/media/avi"
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

	// From QuickTime, MP4, 3GPP, and AVI videos: the size of the main
	// video track as displayed, after rotation, and its clockwise
	// rotation in degrees (90, 180, or 270; omitted if 0).
	MediaTagWidth    = "width"
	MediaTagHeight   = "height"
	MediaTagRotation = "rotation"
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

// indexAppleMakerNoteEXIF decodes the EXIF in r and indexes only its
// Apple MakerNote, unlike indexEXIF, which indexes all of the EXIF. As
// with indexEXIF, short reads return io.ErrUnexpectedEOF, so that
// readPrefixOrFile retries with the whole file.
func (ix *Index) indexAppleMakerNoteEXIF(wholeRef blob.Ref, r io.Reader, mm *mutationMap) (err error) {
	ex, err := exif.Decode(r)
	if err != nil && exif.IsCriticalError(err) {
		if exif.IsShortReadTagValueError(err) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	defer func() {
		// As in indexEXIF: the EXIF library can panic on invalid EXIF.
		if e := recover(); e != nil {
			err = errEXIFPanic
		}
	}()
	ix.indexAppleMakerNote(wholeRef, ex, mm)
	return nil
}

// isQuickTimeMIME reports whether files of the given MIME type may
// contain QuickTime metadata: QuickTime movies and the ISO BMFF formats
// derived from them.
func isQuickTimeMIME(mimeType string) bool {
	switch mimeType {
	case "video/quicktime", "video/mp4", "video/3gpp", "video/3gpp2":
		return true
	}
	return false
}

// isAVIMIME reports whether files of the given MIME type may be AVI
// videos.
func isAVIMIME(mimeType string) bool {
	switch mimeType {
	case "video/x-msvideo", "video/vnd.avi", "video/avi", "video/msvideo":
		return true
	}
	return false
}

// isIndexedVideoMIME reports whether indexVideo indexes files of the
// given MIME type.
func isIndexedVideoMIME(mimeType string) bool {
	return isQuickTimeMIME(mimeType) || isAVIMIME(mimeType)
}

// indexVideo indexes the media tags and location of a video file of the
// given MIME type, if it's one of a type that isIndexedVideoMIME reports.
func (ix *Index) indexVideo(r io.ReaderAt, size int64, mimeType string, wholeRef blob.Ref, mm *mutationMap) {
	switch {
	case isQuickTimeMIME(mimeType):
		ix.indexQuickTime(r, size, wholeRef, mm)
	case isAVIMIME(mimeType):
		ix.indexAVI(r, size, wholeRef, mm)
	}
}

// setVideoSize sets the media tags of a video's displayed size and
// rotation.
func setVideoSize(mm *mutationMap, wholeRef blob.Ref, width, height, rotation int) {
	if width <= 0 || height <= 0 {
		return
	}
	setMediaTag(mm, wholeRef, MediaTagWidth, strconv.Itoa(width))
	setMediaTag(mm, wholeRef, MediaTagHeight, strconv.Itoa(height))
	if rotation != 0 {
		setMediaTag(mm, wholeRef, MediaTagRotation, strconv.Itoa(rotation))
	}
}

// indexQuickTime indexes a QuickTime or MP4 movie's duration, size, and
// Apple metadata as media tags, and its location like an image's EXIF GPS
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
	setVideoSize(mm, wholeRef, md.Width, md.Height, md.Rotation)
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

// indexAVI indexes an AVI video's duration and size as media tags.
func (ix *Index) indexAVI(r io.ReaderAt, size int64, wholeRef blob.Ref, mm *mutationMap) {
	h, err := avi.ReadHeader(r, size)
	if err != nil {
		if debugEnv {
			ix.printf("index: AVI header: %v", err)
		}
		return
	}
	if d := h.Duration(); d > 0 {
		setMediaTag(mm, wholeRef, MediaTagDurationMS, strconv.FormatInt(d.Milliseconds(), 10))
	}
	setVideoSize(mm, wholeRef, h.Width, h.Height, 0)
}
