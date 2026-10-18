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
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	gpxNamespace = "http://www.topografix.com/GPX/1/1"
	// Keep usable tracks within the generic thumbnail renderer budgets. Larger
	// originals remain source attachments, not misleading fitnessTrack refs.
	maxRenderTrack  = 16 << 20
	maxRenderPoints = 200000
)

type trackInfo struct {
	Hash, Start, Latitude, Longitude string
	Usable                           bool
	Warning                          string
	RepairVersion, RepairedHash      string
	RepairCount                      int
}

func hashMember(src memberSource, name string, limit int64) (string, error) {
	r, err := src.OpenLimit(name, limit)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func inspectTrack(src memberSource, name string) (trackInfo, error) {
	var info trackInfo
	var err error
	// Hash the entire source independently of XML validity: even edits after a
	// malformed segment must invalidate the activity's completion fingerprint.
	raw, err := src.OpenLimit(name, maxTrack)
	if err != nil {
		return info, err
	}
	h := sha256.New()
	size, err := io.Copy(h, raw)
	raw.Close()
	if err != nil {
		return info, err
	}
	info.Hash = hex.EncodeToString(h.Sum(nil))
	if size > maxRenderTrack {
		info.Warning = boundedWarning(fmt.Sprintf("unusable GPX %s: exceeds thumbnail byte limit", name))
		return recoverTrackDate(src, name, info)
	}
	r, err := src.OpenLimit(name, maxRenderTrack)
	if err != nil {
		return info, err
	}
	defer r.Close()
	info, err = parseTrack(r, info.Hash)
	if err != nil {
		if errors.Is(err, errNestedTrackSegment) {
			f, repaired, repairErr := repairedTrackFile(src, name, info.Hash)
			if repairErr == nil {
				removeTrackFile(f)
				if info.Start != "" {
					repaired.Start = info.Start
				}
				return repaired, nil
			}
			if !errors.Is(repairErr, errGPXRepairRejected) {
				return info, repairErr
			}
		}
		info = trackInfo{
			Hash:    info.Hash,
			Start:   info.Start,
			Warning: boundedWarning(fmt.Sprintf("unusable GPX %s: %v", name, err)),
		}
		return recoverTrackDate(src, name, info)
	}
	return info, nil
}

func recoverTrackDate(src memberSource, name string, info trackInfo) (trackInfo, error) {
	if info.Start != "" {
		return info, nil
	}
	r, err := src.OpenLimit(name, maxTrack)
	if err != nil {
		return info, err
	}
	defer r.Close()
	info.Start = recoverTrackStart(r)
	return info, nil
}

func parseTrack(r io.Reader, hash string) (trackInfo, error) {
	info := trackInfo{Hash: hash}
	d := xml.NewDecoder(r)
	var path []string
	roots, points := 0, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return info, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(path) >= 64 {
				return info, errors.New("XML nesting too deep")
			}
			if len(path) == 0 {
				roots++
				if roots != 1 || t.Name.Local != "gpx" || t.Name.Space != gpxNamespace {
					return info, errors.New("not GPX 1.1")
				}
				version := ""
				for _, a := range t.Attr {
					if a.Name.Space == "" && a.Name.Local == "version" {
						version = a.Value
					}
				}
				if version != "1.1" {
					return info, errors.New("not GPX 1.1")
				}
			}
			path = append(path, t.Name.Local)
			loc := strings.Join(path, "/")
			switch loc {
			case "gpx/trk", "gpx/trk/trkseg", "gpx/trk/trkseg/trkpt", "gpx/trk/time", "gpx/trk/trkseg/trkpt/time":
				if t.Name.Space != gpxNamespace {
					return info, errors.New("unexpected GPX element namespace")
				}
			}
			if t.Name.Space == gpxNamespace && t.Name.Local == "trkseg" && loc != "gpx/trk/trkseg" {
				return info, errNestedTrackSegment
			}
			if t.Name.Space == gpxNamespace && t.Name.Local == "trkpt" && loc != "gpx/trk/trkseg/trkpt" {
				return info, errors.New("misplaced track point")
			}
			if loc == "gpx/trk/trkseg/trkpt" {
				var lat, lon string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "lat":
						lat = a.Value
					case "lon":
						lon = a.Value
					}
				}
				la, e1 := strconv.ParseFloat(lat, 64)
				lo, e2 := strconv.ParseFloat(lon, 64)
				if e1 != nil || e2 != nil || !finite(la) || !finite(lo) || la < -90 || la > 90 || lo < -180 || lo > 180 {
					return info, errors.New("invalid track coordinate")
				}
				if points == 0 {
					info.Latitude, info.Longitude = decimal(la), decimal(lo)
				}
				points++
				if points > maxRenderPoints {
					return info, errors.New("exceeds thumbnail point limit")
				}
			}
			if loc == "gpx/trk/time" || loc == "gpx/trk/trkseg/trkpt/time" {
				text, err := readGPXTime(d)
				if err != nil {
					return info, err
				}
				instant, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text))
				start := formatStartDate(instant)
				if err != nil || start == "" {
					return info, errors.New("invalid or unzoned GPX time")
				}
				if info.Start == "" {
					info.Start = start
				}
				path = path[:len(path)-1]
			}
		case xml.EndElement:
			if len(path) == 0 {
				return info, errors.New("unmatched XML end")
			}
			path = path[:len(path)-1]
		case xml.CharData:
			if len(path) == 0 && strings.TrimSpace(string(t)) != "" {
				return info, errors.New("data outside GPX document")
			}
		}
	}
	if roots != 1 || len(path) != 0 || points == 0 {
		return info, errors.New("GPX has no usable track points")
	}
	info.Usable = true
	return info, nil
}

// Reject nested time markup and bound accumulated text, instead of decoding an
// arbitrarily long element into one string. The enclosing GPX is also bounded.
func readGPXTime(d *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			if b.Len()+len(t) > 128 {
				return "", errors.New("GPX time too long")
			}
			b.Write(t)
		case xml.EndElement:
			return b.String(), nil
		default:
			return "", errors.New("invalid GPX time markup")
		}
	}
}
