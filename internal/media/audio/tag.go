/*
Copyright 2014 The Perkeep Authors.

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

// Package media provides means for querying information about audio and video data.

package audio // import "perkeep.org/internal/media/audio"

import (
	"time"

	"go4.org/readerutil"
	"perkeep.org/pkg/schema"
)

// MediaTags are the tags we extracted from the audio file
type MediaTags struct {
	Title    string
	Artist   string
	Album    string
	Genre    string
	Year     time.Time
	Track    int
	Disc     int
	Duration time.Duration
	Misc     map[string]string
}

func GetMediaTags(b *schema.Blob, r readerutil.SizeReaderAt) (MediaTags, error) {
	return getMediaTags(b, r)
}
