// +build taglib,!windows

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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"time"

	"github.com/nicksellen/audiotags"
	"go4.org/readerutil"
	"perkeep.org/pkg/schema"
)

func getMediaTags(b *schema.Blob, r readerutil.SizeReaderAt) (MediaTags, error) {
	sr := io.NewSectionReader(r, 0, r.Size())

	// copy to a tempfile since the simple taglib api operates on files
	tmpFile, err := ioutil.TempFile("", fmt.Sprintf("*-%s", b.FileName()))
	if err != nil {
		return MediaTags{}, fmt.Errorf("Unable to create tempfile for indexing music: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, sr); err != nil {
		return MediaTags{}, fmt.Errorf("Unable to write tempfile (%v) for indexing music: %w", tmpFile.Name(), err)
	}

	tags, props, err := audiotags.Read(tmpFile.Name())
	if err != nil {
		return MediaTags{}, fmt.Errorf("Unable to read tempfile (%v) using taglib: %w", tmpFile.Name(), err)
	}

	mt := MediaTags{
		Duration: time.Duration(props.Length) * time.Second,
		Misc:     make(map[string]string),
	}

	for k, v := range tags {
		switch k {
		case "title":
			mt.Title = v
		case "artist":
			mt.Artist = v
		case "genre":
			mt.Genre = v
		case "date":
			mt.Year = tryParseDate(v)
		case "tracknumber":
			mt.Track = tryParseNumber(v)
		case "discnumber":
			mt.Disc = tryParseNumber(v)
		default:
			mt.Misc[k] = v
		}
	}

	return mt, nil
}

func tryParseDate(dateStr string) time.Time {
	const justYearLayout = "2006"
	year, _ := time.Parse(justYearLayout, dateStr)
	return year
}

func tryParseNumber(numStr string) int {
	// might be either a number or something like 01/12

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err == nil {
		return int(num)
	}

	var x, y int
	if _, err := fmt.Sscanf(numStr, "%02d/%02d", &x, &y); err != nil {
		return 0
	}
	return x
}
