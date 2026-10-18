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
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"perkeep.org/pkg/schema/nodeattr"
)

const (
	maxRecord      = 1 << 20
	maxColumns     = 128
	maxColumnName  = 128
	maxModeledText = 16 << 10
	maxMetadata    = 64 << 20
	maxTrack       = 64 << 20
	maxActivities  = 100000
	maxSensors     = int64(4 << 30)
)

// memberSource deliberately depends on neither HTTP nor provider API DTOs.
// importer.Archive supplies validated paths and actual-read archive budgets.
type memberSource interface {
	Open(string) (io.ReadCloser, error)
	OpenLimit(string, int64) (io.ReadCloser, error)
}

type profile struct{ UserName, Email, Name string }

func unwrapOptional(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "Optional[") && strings.HasSuffix(s, "]") {
		s = strings.TrimSpace(s[len("Optional[") : len(s)-1])
	}
	return s
}

func readProfile(src memberSource) (profile, error) {
	var p profile
	r, err := src.OpenLimit("profile.json", maxRecord)
	if err != nil {
		return p, fmt.Errorf("runkeeper: profile.json: %w", err)
	}
	defer r.Close()
	d := json.NewDecoder(r)
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return p, errors.New("runkeeper: profile must be a JSON object")
	}
	seen := make(map[string]bool)
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return p, err
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return p, errors.New("runkeeper: duplicate profile field")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return p, err
		}
		var dest *string
		switch key {
		case "username":
			dest = &p.UserName
		case "email":
			dest = &p.Email
		case "name":
			dest = &p.Name
		}
		if dest != nil {
			if err := json.Unmarshal(raw, dest); err != nil {
				return p, fmt.Errorf("runkeeper: profile %s must be a string", key)
			}
		}
	}
	if _, err := d.Token(); err != nil {
		return p, err
	}
	if _, err := d.Token(); err != io.EOF {
		return p, errors.New("runkeeper: trailing profile data")
	}
	p.UserName, p.Email, p.Name = unwrapOptional(p.UserName), unwrapOptional(p.Email), strings.TrimSpace(p.Name)
	if p.UserName == "" && p.Email == "" {
		return p, errors.New("runkeeper: profile has neither username nor email")
	}
	return p, nil
}

// boundedCSV bounds a logical record BEFORE encoding/csv can allocate it.
// Quoted embedded newlines and escaped quotes remain supported. Input bytes are
// also bounded by the member reader. Only a single record is buffered.
type boundedCSV struct{ r *bufio.Reader }

func newCSV(r io.Reader) *boundedCSV { return &boundedCSV{bufio.NewReaderSize(r, 32<<10)} }
func (c *boundedCSV) read() ([]string, error) {
	var b bytes.Buffer
	quoted := false
	for {
		line, err := c.r.ReadSlice('\n')
		if b.Len()+len(line) > maxRecord {
			return nil, errors.New("runkeeper: CSV record exceeds 1 MiB")
		}
		for _, v := range line {
			if v == '"' {
				quoted = !quoted
			}
		}
		b.Write(line)
		if err != nil && err != bufio.ErrBufferFull && err != io.EOF {
			return nil, err
		}
		if err == io.EOF && b.Len() == 0 {
			return nil, io.EOF
		}
		if err == io.EOF || (err == nil && !quoted) {
			if b.Len() == 1 && line[0] == '\n' {
				b.Reset()
				continue
			}
			r := csv.NewReader(&b)
			return r.Read()
		}
	}
}

type rawRow map[string]string

func readCSV(ctx context.Context, src memberSource, name string, limit int64, visit func(rawRow) error) error {
	r, err := src.OpenLimit(name, limit)
	if err != nil {
		return err
	}
	defer r.Close()
	c := newCSV(r)
	header, err := c.read()
	if err != nil {
		return fmt.Errorf("runkeeper: %s header: %w", name, err)
	}
	if len(header) > maxColumns {
		return fmt.Errorf("runkeeper: %s exceeds %d columns", name, maxColumns)
	}
	seen := make(map[string]bool)
	for i, h := range header {
		if len(h) > maxColumnName || !utf8.ValidString(h) {
			return fmt.Errorf("runkeeper: %s invalid or oversized column name (maximum %d bytes)", name, maxColumnName)
		}
		h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		if h == "" || seen[h] {
			return fmt.Errorf("runkeeper: %s empty or duplicate column", name)
		}
		seen[h] = true
		header[i] = h
	}

	required := []string{"Activity Id"}
	switch name {
	case "cardioActivities.csv":
		required = append(required, "Date", "Type", "Duration")
	case "photos.csv":
		required = append(required, "Image File Name")
	}
	for _, key := range required {
		if !seen[key] {
			return fmt.Errorf("runkeeper: %s missing column %q", name, key)
		}
	}
	for n := 2; ; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec, err := c.read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("runkeeper: %s row %d: %w", name, n, err)
		}
		if len(rec) != len(header) {
			return fmt.Errorf("runkeeper: %s row %d: wrong field count", name, n)
		}
		row := make(rawRow, len(header))
		for i, h := range header {
			if !utf8.ValidString(rec[i]) {
				return fmt.Errorf("runkeeper: %s row %d: invalid UTF-8", name, n)
			}
			row[h] = rec[i]
		}
		if err := visit(row); err != nil {
			return fmt.Errorf("runkeeper: %s row %d: %w", name, n, err)
		}
	}
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

type activity struct {
	ID    string
	Raw   rawRow
	Attrs map[string]string
}

func parseActivity(row rawRow) (*activity, error) {
	id := strings.ToLower(strings.TrimSpace(row["Activity Id"]))
	if !validUUID(id) {
		return nil, errors.New("invalid activity UUID")
	}
	for _, key := range []string{"Date", "Type", "Route Name", "Notes", "Friend's Tagged"} {
		if len(row[key]) > maxModeledText {
			return nil, fmt.Errorf("modeled field %q exceeds %d bytes", key, maxModeledText)
		}
	}
	a := &activity{ID: id, Raw: row, Attrs: map[string]string{
		nodeattr.Type: nodeattr.FitnessActivity, nodeattr.FitnessSource: "runkeeper", nodeattr.FitnessSourceID: id,
		"runkeeperActivityType": row["Type"], "runkeeperLocalDate": row["Date"],
	}}
	if row["Type"] != "" {
		a.Attrs[nodeattr.FitnessActivityType] = activityType(row["Type"])
	}
	title := strings.TrimSpace(row["Route Name"])
	if title == "" {
		title = strings.TrimSpace(row["Type"] + " " + row["Date"])
	}
	a.Attrs[nodeattr.Title] = title
	if row["Notes"] != "" {
		a.Attrs[nodeattr.Description] = row["Notes"]
	}
	if row["Friend's Tagged"] != "" {
		a.Attrs["runkeeperFriendsTagged"] = row["Friend's Tagged"]
	}
	for _, m := range []struct {
		key  string
		cols map[string]float64
	}{
		{nodeattr.FitnessDistance, map[string]float64{"Distance (mi)": 1609.344, "Distance (km)": 1000, "Distance (m)": 1}},
		{nodeattr.FitnessElevationGain, map[string]float64{"Climb (ft)": 0.3048, "Climb (m)": 1}},
		{nodeattr.FitnessEnergy, map[string]float64{"Calories Burned": 4184}},
		{nodeattr.FitnessAverageHeartRate, map[string]float64{"Average Heart Rate (bpm)": 1}},
	} {
		found := false
		for col, factor := range m.cols {
			v := strings.TrimSpace(row[col])
			if v == "" {
				continue
			}
			if found {
				return nil, fmt.Errorf("conflicting units for %s", m.key)
			}
			found = true
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || !finite(f) || f < 0 || !finite(f*factor) {
				return nil, fmt.Errorf("invalid %s", col)
			}
			a.Attrs[m.key] = decimal(f * factor)
		}
	}
	if s := strings.TrimSpace(row["Duration"]); s != "" {
		d, err := durationSeconds(s)
		if err != nil {
			return nil, err
		}
		a.Attrs[nodeattr.FitnessRecordedDuration] = decimal(d)
	}
	return a, nil
}
func durationSeconds(s string) (float64, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, errors.New("invalid duration")
	}
	total := float64(0)
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || !finite(v) || v < 0 || (i > 0 && v >= 60) || (i < len(parts)-1 && v != math.Trunc(v)) {
			return 0, errors.New("invalid duration")
		}
		total = total*60 + v
	}
	if !finite(total) {
		return 0, errors.New("invalid duration")
	}
	return total, nil
}
func activityType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running", "trail running":
		return "run"
	case "walking":
		return "walk"
	case "cycling", "mountain biking":
		return "ride"
	case "hiking":
		return "hike"
	case "swimming":
		return "swim"
	case "rowing":
		return "row"
	case "skiing", "cross-country skiing", "downhill skiing", "snowboarding":
		return "ski"
	case "elliptical", "strength training", "workout":
		return "workout"
	default:
		return "other"
	}
}
func finite(v float64) bool    { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func decimal(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Check cancellation between streaming reads of derived local attachments.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
