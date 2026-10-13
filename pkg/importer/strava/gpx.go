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

package strava

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

const gpxNamespace = "http://www.topografix.com/GPX/1/1"

// trackPoint and the GPX encoder are independent of Strava's activity and stream
// representations. Only activityGPX adapts those representations to a track.
type trackPoint struct {
	Latitude, Longitude float64
	Elevation           *float64
	Time                *time.Time
}

type gpxDocument struct {
	XMLName xml.Name `xml:"gpx"`
	XMLNS   string   `xml:"xmlns,attr"`
	Version string   `xml:"version,attr"`
	Creator string   `xml:"creator,attr"`
	Track   gpxTrack `xml:"trk"`
}

type gpxTrack struct {
	Name   string        `xml:"name"`
	Points []gpxXMLPoint `xml:"trkseg>trkpt"`
}

type gpxXMLPoint struct {
	Latitude  string `xml:"lat,attr"`
	Longitude string `xml:"lon,attr"`
	Elevation string `xml:"ele,omitempty"`
	Time      string `xml:"time,omitempty"`
}

func encodeGPX(name string, points []trackPoint) ([]byte, error) {
	if len(points) == 0 {
		return nil, nil
	}
	doc := gpxDocument{XMLNS: gpxNamespace, Version: "1.1", Creator: "Perkeep"}
	doc.Track.Name = name
	for _, p := range points {
		if !validCoordinate(p.Latitude, p.Longitude) {
			return nil, errors.New("strava: invalid GPX coordinate")
		}
		// GPX uses xsd:decimal, which forbids scientific notation.
		xp := gpxXMLPoint{
			Latitude:  strconv.FormatFloat(p.Latitude, 'f', -1, 64),
			Longitude: strconv.FormatFloat(p.Longitude, 'f', -1, 64),
		}
		if p.Elevation != nil {
			if !finite(*p.Elevation) {
				return nil, errors.New("strava: invalid GPX elevation")
			}
			xp.Elevation = strconv.FormatFloat(*p.Elevation, 'f', -1, 64)
		}
		if p.Time != nil {
			utc := p.Time.UTC()
			if utc.Year() < 1 || utc.Year() > 9999 {
				return nil, errors.New("strava: invalid GPX time")
			}
			xp.Time = utc.Format(time.RFC3339Nano)
		}
		doc.Track.Points = append(doc.Track.Points, xp)
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("strava: encoding GPX: %w", err)
	}
	return append([]byte(xml.Header), data...), nil
}

func validCoordinate(lat, lon float64) bool {
	return finite(lat) && finite(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon < 180
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// activityGPX produces a track only when coordinate samples exist. Neither the
// summary start coordinate nor moving/elapsed time can invent track samples or
// timestamps. Time and altitude, when present, must match the coordinate stream
// in both sampling metadata and length; unrelated measurements stay in raw JSON.
func activityGPX(a *activity, s streams) ([]byte, error) {
	coords, ok := s["latlng"]
	if !ok {
		return nil, nil
	}
	var latlng [][]*float64
	if json.Unmarshal(coords.Data, &latlng) != nil || latlng == nil {
		return nil, errors.New("strava: invalid latlng stream")
	}
	if len(latlng) == 0 {
		return nil, nil
	}
	if a == nil {
		return nil, errors.New("strava: missing activity for GPX")
	}
	count := len(latlng)
	if err := checkMetadata("latlng", coords, coords, count); err != nil {
		return nil, err
	}
	points := make([]trackPoint, count)
	for i, pair := range latlng {
		if len(pair) != 2 || pair[0] == nil || pair[1] == nil || !validCoordinate(*pair[0], *pair[1]) {
			return nil, errors.New("strava: invalid latlng sample")
		}
		points[i].Latitude, points[i].Longitude = *pair[0], *pair[1]
	}
	if ts, ok := s["time"]; ok {
		var seconds []*int64
		if json.Unmarshal(ts.Data, &seconds) != nil || seconds == nil || len(seconds) != count {
			return nil, errors.New("strava: invalid or misaligned time stream")
		}
		if err := checkMetadata("time", ts, coords, count); err != nil {
			return nil, err
		}
		if a.StartDate.IsZero() {
			return nil, errors.New("strava: time stream requires activity start date")
		}
		var previous int64
		for i, offset := range seconds {
			if offset == nil || *offset < previous || *offset > math.MaxInt64/int64(time.Second) {
				return nil, errors.New("strava: invalid time sample")
			}
			previous = *offset
			timestamp := a.StartDate.UTC().Add(time.Duration(*offset) * time.Second)
			points[i].Time = &timestamp
		}
	}
	if alt, ok := s["altitude"]; ok {
		var elevations []*float64
		if json.Unmarshal(alt.Data, &elevations) != nil || elevations == nil || len(elevations) != count {
			return nil, errors.New("strava: invalid or misaligned altitude stream")
		}
		if err := checkMetadata("altitude", alt, coords, count); err != nil {
			return nil, err
		}
		for i, elevation := range elevations {
			if elevation == nil || !finite(*elevation) {
				return nil, errors.New("strava: invalid altitude sample")
			}
			points[i].Elevation = elevation
		}
	}
	return encodeGPX(a.Name, points)
}

// Zero original_size and an omitted resolution convey no sampling information.
// Strava defines series_type as the base used when downsampling. Its label (and
// resolution) need not match if both streams demonstrably contain every sample.
// Otherwise the metadata must describe the same sampling as the coordinates.
func checkMetadata(name string, value, coords stream, count int) error {
	if value.SeriesType != "time" && value.SeriesType != "distance" {
		return fmt.Errorf("strava: invalid %s stream series type", name)
	}
	if value.OriginalSize < 0 || (value.OriginalSize > 0 && value.OriginalSize < count) ||
		(value.OriginalSize > 0 && coords.OriginalSize > 0 && value.OriginalSize != coords.OriginalSize) {
		return fmt.Errorf("strava: misaligned %s stream original size", name)
	}
	if value.Resolution != "" && value.Resolution != "low" && value.Resolution != "medium" && value.Resolution != "high" {
		return fmt.Errorf("strava: invalid %s stream resolution", name)
	}
	if value.OriginalSize == count && coords.OriginalSize == count {
		return nil
	}
	if value.SeriesType != coords.SeriesType {
		return fmt.Errorf("strava: misaligned %s stream series type", name)
	}
	if value.Resolution != "" && coords.Resolution != "" && value.Resolution != coords.Resolution {
		return fmt.Errorf("strava: misaligned %s stream resolution", name)
	}
	return nil
}
