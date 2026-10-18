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

package runkeeper

import (
	"encoding/xml"
	"io"
	"strings"
	"time"
)

const (
	attrStartDateSource          = "runkeeperStartDateSource"
	startDateSourceGPX           = "gpx"
	startDateSourceCSV           = "csv"
	startDateSourceCSVAssumedUTC = "csv-assumed-utc"
	maxStartDateScan             = 256 << 10
)

// csvStartDate uses UTC as a deterministic sorting placeholder for an unzoned
// wall-clock date, not as an assertion about its original timezone. The source
// marker distinguishes this approximation from an explicitly zoned timestamp.
func csvStartDate(value string) (start, source string) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		if start := formatStartDate(t); start != "" {
			return start, startDateSourceCSV
		}
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		// time.Parse uses UTC, never the server's local timezone.
		if t, err := time.Parse(layout, value); err == nil {
			return t.Format(time.RFC3339Nano), startDateSourceCSVAssumedUTC
		}
	}
	return "", ""
}

func formatStartDate(t time.Time) string {
	t = t.UTC()
	// A valid offset timestamp near a year boundary can normalize outside
	// RFC3339's four-digit year range. Don't publish an unsortable startDate.
	if t.Year() < 0 || t.Year() > 9999 {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

// recoverTrackStart reads only a bounded GPX prefix. A complete, zoned track or
// point timestamp can survive a bad coordinate, an unclosed segment, or another
// route validation error. This does not repair the XML or approve its route.
func recoverTrackStart(r io.Reader) string {
	d := xml.NewDecoder(io.LimitReader(r, maxStartDateScan))
	var path []xml.Name
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(path) >= 64 {
				return ""
			}
			if len(path) == 0 {
				if t.Name != (xml.Name{Space: gpxNamespace, Local: "gpx"}) {
					return ""
				}
				version := ""
				for _, a := range t.Attr {
					if a.Name.Space == "" && a.Name.Local == "version" {
						version = a.Value
					}
				}
				if version != "1.1" {
					return ""
				}
			}
			path = append(path, t.Name)
			if !trackTimePath(path) {
				continue
			}
			text, err := readGPXTime(d)
			if err != nil {
				return ""
			}
			path = path[:len(path)-1] // readGPXTime consumed the end element
			if instant, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text)); err == nil {
				if start := formatStartDate(instant); start != "" {
					return start
				}
			}
		case xml.EndElement:
			if len(path) == 0 {
				return ""
			}
			path = path[:len(path)-1]
			if len(path) == 0 {
				return "" // don't recover a date from an appended document
			}
		case xml.CharData:
			if len(path) == 0 && strings.TrimSpace(string(t)) != "" {
				return ""
			}
		}
	}
}

func trackTimePath(path []xml.Name) bool {
	if len(path) < 3 || path[0].Local != "gpx" || path[1].Local != "trk" || path[len(path)-1].Local != "time" {
		return false
	}
	for _, name := range path {
		if name.Space != gpxNamespace {
			return false
		}
	}
	if len(path) == 3 { // gpx/trk/time
		return true
	}
	if len(path) < 5 || path[len(path)-2].Local != "trkpt" {
		return false
	}
	// Accept a point's time even if the exporter forgot to close a preceding
	// segment. Only date extraction tolerates this; route validation does not.
	for _, name := range path[2 : len(path)-2] {
		if name.Local != "trkseg" {
			return false
		}
	}
	return true
}
