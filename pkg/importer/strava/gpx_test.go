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
	"math"
	"strings"
	"testing"
	"time"
)

func gpxTestStream(data string) stream {
	return stream{Data: json.RawMessage(data), SeriesType: "time", OriginalSize: 3, Resolution: "high"}
}

func gpxTestInput() (*activity, streams) {
	return &activity{
		ID: 42, Name: `Morning <ride> & "friends"`,
		StartDate: time.Date(2026, 1, 2, 12, 30, 0, 0, time.FixedZone("offset", 2*60*60)),
	}, streams{
		"latlng":   gpxTestStream(`[[48.1,2.2],[48.2,2.3],[48.3,2.4]]`),
		"time":     gpxTestStream(`[0,7,15]`),
		"altitude": gpxTestStream(`[100.25,0,-3.5]`),
	}
}

func gpxTestParse(t *testing.T, raw []byte) gpxDocument {
	t.Helper()
	if !strings.HasPrefix(string(raw), xml.Header) {
		t.Fatalf("missing XML header: %s", raw)
	}
	var doc gpxDocument
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, raw)
	}
	if doc.XMLName.Space != gpxNamespace || doc.Version != "1.1" || doc.Creator == "" {
		t.Fatalf("invalid GPX envelope: %+v", doc)
	}
	return doc
}

func TestActivityGPX(t *testing.T) {
	a, s := gpxTestInput()
	raw, err := activityGPX(a, s)
	if err != nil {
		t.Fatal(err)
	}
	doc := gpxTestParse(t, raw)
	if doc.Track.Name != a.Name || len(doc.Track.Points) != 3 {
		t.Fatalf("track = %+v", doc.Track)
	}
	want := []gpxXMLPoint{
		{Latitude: "48.1", Longitude: "2.2", Elevation: "100.25", Time: "2026-01-02T10:30:00Z"},
		{Latitude: "48.2", Longitude: "2.3", Elevation: "0", Time: "2026-01-02T10:30:07Z"},
		{Latitude: "48.3", Longitude: "2.4", Elevation: "-3.5", Time: "2026-01-02T10:30:15Z"},
	}
	for i, p := range doc.Track.Points {
		if p != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, p, want[i])
		}
	}
	if strings.Contains(string(raw), "<ride>") || strings.Contains(string(raw), "extensions") {
		t.Errorf("unescaped name or nonstandard extensions: %s", raw)
	}
	// The GPX child order is significant in the GPX 1.1 schema.
	var tokens []xml.Name
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Space != gpxNamespace {
				t.Errorf("element outside GPX namespace: %+v", start.Name)
			}
			tokens = append(tokens, start.Name)
		}
	}
	wantOrder := []string{"gpx", "trk", "name", "trkseg", "trkpt", "ele", "time", "trkpt", "ele", "time", "trkpt", "ele", "time"}
	if len(tokens) != len(wantOrder) {
		t.Fatalf("element order = %v", tokens)
	}
	for i, name := range tokens {
		if name.Local != wantOrder[i] {
			t.Errorf("element %d = %s, want %s", i, name.Local, wantOrder[i])
		}
	}
}

func TestActivityGPXGPSless(t *testing.T) {
	for _, s := range []streams{nil, {}, {"time": gpxTestStream(`[0,1,2]`)}, {"latlng": gpxTestStream(`[]`)}} {
		a := &activity{Name: "Manual ride", Manual: true, Trainer: true, StartLatlng: []float64{40, -80}}
		raw, err := activityGPX(a, s)
		if err != nil || raw != nil {
			t.Errorf("GPSless streams=%v: raw=%q, err=%v", s, raw, err)
		}
	}
	if raw, err := activityGPX(nil, nil); err != nil || raw != nil {
		t.Errorf("nil GPSless = %q, %v", raw, err)
	}
}

func TestActivityGPXOptionalStreams(t *testing.T) {
	for _, tc := range []struct {
		name          string
		omitTime      bool
		omitElevation bool
	}{
		{"coordinates only", true, true},
		{"without elevation", false, true},
		{"without times", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s := gpxTestInput()
			if tc.omitTime {
				delete(s, "time")
				a.StartDate = time.Time{}
				a.ElapsedTime, a.MovingTime = 99, 88
			}
			if tc.omitElevation {
				delete(s, "altitude")
			}
			raw, err := activityGPX(a, s)
			if err != nil {
				t.Fatal(err)
			}
			doc := gpxTestParse(t, raw)
			for _, p := range doc.Track.Points {
				if (p.Time == "") != tc.omitTime || (p.Elevation == "") != tc.omitElevation {
					t.Errorf("optional values = %+v", p)
				}
			}
		})
	}
}

func TestActivityGPXTimeOffsets(t *testing.T) {
	a, s := gpxTestInput()
	// A missing sample at activity start is not replaced by a fabricated point.
	s["time"] = gpxTestStream(`[5,5,9]`)
	a.StartDate = time.Date(2026, 1, 2, 23, 59, 58, 123000000, time.UTC)
	raw, err := activityGPX(a, s)
	if err != nil {
		t.Fatal(err)
	}
	doc := gpxTestParse(t, raw)
	want := []string{"2026-01-03T00:00:03.123Z", "2026-01-03T00:00:03.123Z", "2026-01-03T00:00:07.123Z"}
	for i, p := range doc.Track.Points {
		if p.Time != want[i] {
			t.Errorf("time %d = %s, want %s", i, p.Time, want[i])
		}
	}
}

func TestActivityGPXRejectsMalformedAndMisalignedStreams(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*activity, streams)
	}{
		{"coordinates malformed", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[1,2]`) }},
		{"coordinates null", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`null`) }},
		{"coordinates missing data", func(a *activity, s streams) { s["latlng"] = gpxTestStream(``) }},
		{"coordinate null", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[null,0]]`) }},
		{"coordinate tuple null", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[null]`) }},
		{"coordinate too short", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[1]]`) }},
		{"coordinate too long", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[1,2,3]]`) }},
		{"coordinate string", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[["1",2]]`) }},
		{"coordinate latitude high", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[90.01,0]]`) }},
		{"coordinate latitude low", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[-90.01,0]]`) }},
		{"coordinate longitude high", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[0,180]]`) }},
		{"coordinate longitude low", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[0,-180.01]]`) }},
		{"coordinate nonfinite", func(a *activity, s streams) { s["latlng"] = gpxTestStream(`[[1e999,0]]`) }},
		{"time partial", func(a *activity, s streams) { s["time"] = gpxTestStream(`[0,7]`) }},
		{"time extra", func(a *activity, s streams) { s["time"] = gpxTestStream(`[0,7,15,20]`) }},
		{"time empty", func(a *activity, s streams) { s["time"] = gpxTestStream(`[]`) }},
		{"time null", func(a *activity, s streams) { s["time"] = gpxTestStream(`null`) }},
		{"time null sample", func(a *activity, s streams) { s["time"] = gpxTestStream(`[0,null,15]`) }},
		{"time negative", func(a *activity, s streams) { s["time"] = gpxTestStream(`[-1,7,15]`) }},
		{"time decreasing", func(a *activity, s streams) { s["time"] = gpxTestStream(`[0,15,7]`) }},
		{"time fractional", func(a *activity, s streams) { s["time"] = gpxTestStream(`[0,7.5,15]`) }},
		{"time overflow", func(a *activity, s streams) { s["time"] = gpxTestStream(`[0,7,9223372036854775807]`) }},
		{"time missing start", func(a *activity, s streams) { a.StartDate = time.Time{} }},
		{"time unrepresentable year", func(a *activity, s streams) { a.StartDate = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC) }},
		{"altitude partial", func(a *activity, s streams) { s["altitude"] = gpxTestStream(`[1,2]`) }},
		{"altitude extra", func(a *activity, s streams) { s["altitude"] = gpxTestStream(`[1,2,3,4]`) }},
		{"altitude empty", func(a *activity, s streams) { s["altitude"] = gpxTestStream(`[]`) }},
		{"altitude null", func(a *activity, s streams) { s["altitude"] = gpxTestStream(`null`) }},
		{"altitude null sample", func(a *activity, s streams) { s["altitude"] = gpxTestStream(`[1,null,3]`) }},
		{"altitude string", func(a *activity, s streams) { s["altitude"] = gpxTestStream(`[1,"2",3]`) }},
		{"altitude nonfinite", func(a *activity, s streams) { s["altitude"] = gpxTestStream(`[1,1e999,3]`) }},
		{"unknown coordinate series", func(a *activity, s streams) { v := s["latlng"]; v.SeriesType = "unknown"; s["latlng"] = v }},
		{"missing coordinate series", func(a *activity, s streams) { v := s["latlng"]; v.SeriesType = ""; s["latlng"] = v }},
		{"mixed time series", func(a *activity, s streams) { v := s["time"]; v.SeriesType = "distance"; s["time"] = v }},
		{"mixed altitude series", func(a *activity, s streams) { v := s["altitude"]; v.SeriesType = "distance"; s["altitude"] = v }},
		{"unknown altitude series", func(a *activity, s streams) { v := s["altitude"]; v.SeriesType = "unknown"; s["altitude"] = v }},
		{"small original size", func(a *activity, s streams) { v := s["latlng"]; v.OriginalSize = 2; s["latlng"] = v }},
		{"negative original size", func(a *activity, s streams) { v := s["latlng"]; v.OriginalSize = -1; s["latlng"] = v }},
		{"mixed original size", func(a *activity, s streams) { v := s["altitude"]; v.OriginalSize = 4; s["altitude"] = v }},
		{"mixed resolution", func(a *activity, s streams) { v := s["time"]; v.Resolution = "low"; s["time"] = v }},
		{"unknown resolution", func(a *activity, s streams) { v := s["latlng"]; v.Resolution = "unknown"; s["latlng"] = v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s := gpxTestInput()
			// Sampled streams need matching bases even when lengths match.
			for name, value := range s {
				value.OriginalSize = 50
				s[name] = value
			}
			tc.change(a, s)
			if raw, err := activityGPX(a, s); err == nil || raw != nil {
				t.Errorf("malformed streams accepted: raw=%q err=%v", raw, err)
			}
		})
	}
	_, s := gpxTestInput()
	if raw, err := activityGPX(nil, s); err == nil || raw != nil {
		t.Errorf("nil activity = %q, %v", raw, err)
	}
}

func TestActivityGPXAlignedDownsampling(t *testing.T) {
	for _, series := range []string{"time", "distance"} {
		for _, resolution := range []string{"", "low", "medium", "high"} {
			a, s := gpxTestInput()
			for key, value := range s {
				value.SeriesType, value.Resolution, value.OriginalSize = series, resolution, 50
				s[key] = value
			}
			if _, err := activityGPX(a, s); err != nil {
				t.Errorf("aligned %s/%s streams: %v", series, resolution, err)
			}
		}
	}
	a, s := gpxTestInput()
	for key, value := range s {
		value.OriginalSize = 0
		s[key] = value
	}
	if _, err := activityGPX(a, s); err != nil {
		t.Errorf("unspecified original sizes: %v", err)
	}
}

func TestActivityGPXFullStreamsWithDifferentSamplingLabels(t *testing.T) {
	a, s := gpxTestInput()
	coords := s["latlng"]
	coords.SeriesType = "distance"
	s["latlng"] = coords
	altitude := s["altitude"]
	altitude.Resolution = "medium"
	s["altitude"] = altitude
	if _, err := activityGPX(a, s); err != nil {
		t.Fatalf("full, unsampled streams rejected: %v", err)
	}
	// Equal lengths alone are not proof of alignment without original_size.
	for name, value := range s {
		value.OriginalSize = 0
		s[name] = value
	}
	if raw, err := activityGPX(a, s); err == nil || raw != nil {
		t.Errorf("different sampling bases without original sizes: %q, %v", raw, err)
	}
}

func TestGPXEncoderReusableAndDecimalCoordinates(t *testing.T) {
	zero, tiny := 0.0, -0.00000001
	points := []trackPoint{
		{Latitude: 90, Longitude: -180, Elevation: &zero},
		{Latitude: -90, Longitude: 179.999999, Elevation: &tiny},
		{Latitude: 0.00000001, Longitude: -0.00000001},
	}
	raw, err := encodeGPX("Generic track", points)
	if err != nil {
		t.Fatal(err)
	}
	doc := gpxTestParse(t, raw)
	if doc.Track.Name != "Generic track" || len(doc.Track.Points) != 3 {
		t.Fatalf("generic track = %+v", doc.Track)
	}
	if doc.Track.Points[2].Latitude != "0.00000001" || doc.Track.Points[2].Longitude != "-0.00000001" || doc.Track.Points[1].Elevation != "-0.00000001" {
		t.Errorf("decimal serialization = %+v", doc.Track.Points)
	}
	for _, p := range doc.Track.Points {
		if p.Time != "" || strings.ContainsAny(p.Latitude+p.Longitude+p.Elevation, "eE") {
			t.Errorf("invented time or nondecimal coordinates: %+v", p)
		}
	}
	if raw, err := encodeGPX("empty", nil); err != nil || raw != nil {
		t.Errorf("empty track = %q, %v", raw, err)
	}
}

func TestGPXEncoderRejectsInvalidValues(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, p := range []trackPoint{{Latitude: value}, {Longitude: value}, {Elevation: &value}} {
			if raw, err := encodeGPX("invalid", []trackPoint{p}); err == nil || raw != nil {
				t.Errorf("invalid point = %q, %v", raw, err)
			}
		}
	}
	for _, timestamp := range []time.Time{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if raw, err := encodeGPX("invalid", []trackPoint{{Time: &timestamp}}); err == nil || raw != nil {
			t.Errorf("invalid time = %q, %v", raw, err)
		}
	}
}
