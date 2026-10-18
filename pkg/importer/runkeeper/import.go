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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

const (
	importVersion    = "3"
	attrVersion      = "runkeeperImportVersion"
	attrHash         = "runkeeperSourceHash"
	attrActivityJSON = "runkeeperActivityJSON"
	attrSourceGPX    = "runkeeperSourceGPX"
	attrPhotosJSON   = "runkeeperPhotosJSON"
	attrSteps        = "runkeeperStepsJSONL"
	attrHeartRate    = "runkeeperHeartRateJSONL"
	maxPhotoManifest = 4 << 20
	maxPhotoMetadata = 32 << 20
	photoRefOverhead = 256
)

var activityKeys = []string{
	nodeattr.Type, nodeattr.Title, nodeattr.Description, nodeattr.FitnessSource, nodeattr.FitnessSourceID,
	nodeattr.FitnessActivityType, nodeattr.FitnessDistance, nodeattr.FitnessElevationGain, nodeattr.FitnessEnergy,
	nodeattr.FitnessAverageHeartRate, nodeattr.FitnessRecordedDuration, nodeattr.FitnessElapsedDuration, nodeattr.FitnessTrack,
	"startDate", "latitude", "longitude", "runkeeperActivityType", "runkeeperLocalDate", "runkeeperFriendsTagged",
	attrActivityJSON, attrSourceGPX, attrPhotosJSON, attrSteps, attrHeartRate, attrStartDateSource,
	attrGPXRepairVersion, attrGPXRepairCount,
}

type importProgress struct {
	Total, Done, Unchanged, WarningCount int
	Warnings                             []string
}
type archiveRun struct {
	rc       *importer.RunContext
	src      memberSource
	names    map[string]bool
	spool    *sensorSpool
	tracks   map[string]trackInfo
	hashes   map[string]string
	progress importProgress
	report   func(importProgress) error
}

func (r *archiveRun) notify() error {
	if r.report != nil {
		return r.report(r.progress)
	}
	return nil
}
func boundedWarning(s string) string {
	if len(s) > 1024 {
		s = s[:1024]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s = strings.Clone(s) // Do not retain the original possibly long ZIP name.
	}
	return s
}
func (r *archiveRun) warn(s string) {
	s = boundedWarning(s)
	r.progress.WarningCount++
	if len(r.progress.Warnings) < 100 {
		r.progress.Warnings = append(r.progress.Warnings, s)
	}
}

func importArchive(rc *importer.RunContext, src memberSource, names map[string]bool, report func(importProgress) error) error {
	r := &archiveRun{rc: rc, src: src, names: names, tracks: make(map[string]trackInfo), hashes: make(map[string]string), report: report}
	ctx := rc.Context()
	var activities []*activity
	ids := make(map[string]bool)
	if err := readCSV(ctx, src, "cardioActivities.csv", maxMetadata, func(row rawRow) error {
		if len(activities) >= maxActivities {
			return fmt.Errorf("activity count exceeds %d", maxActivities)
		}
		a, err := parseActivity(row)
		if err != nil {
			return err
		}
		if ids[a.ID] {
			return fmt.Errorf("duplicate activity UUID %s", a.ID)
		}
		ids[a.ID] = true
		activities = append(activities, a)
		return nil
	}); err != nil {
		return err
	}
	// Source local wall times provide a stable oldest-first presentation, not an
	// authoritative UTC checkpoint. Identity never depends on a date or filename.
	sort.Slice(activities, func(i, j int) bool {
		if activities[i].Raw["Date"] != activities[j].Raw["Date"] {
			return activities[i].Raw["Date"] < activities[j].Raw["Date"]
		}
		return activities[i].ID < activities[j].ID
	})
	r.progress.Total = len(activities)
	if err := r.notify(); err != nil {
		return err
	}
	photos := make(map[string][]rawRow)
	if names["photos.csv"] {
		count := 0
		encodedPhotos := int64(0)
		activityPhotoBytes := make(map[string]int64)
		if err := readCSV(ctx, src, "photos.csv", maxMetadata, func(row rawRow) error {
			count++
			if count > maxActivities {
				return fmt.Errorf("too many photo records")
			}
			id := strings.ToLower(strings.TrimSpace(row["Activity Id"]))
			if ids[id] {
				// Budget the repeated encoded keys/values, not merely the CSV input.
				// Reserve the maximum fixed hash/file-ref additions before accumulation.
				encoded, err := json.Marshal(photoInput{Raw: row})
				if err != nil {
					return err
				}
				size := int64(len(encoded) + photoRefOverhead + 1)
				if size > maxPhotoMetadata-encodedPhotos || size > maxPhotoManifest-2-activityPhotoBytes[id] {
					return fmt.Errorf("encoded photo metadata byte limit exceeded")
				}
				encodedPhotos += size
				activityPhotoBytes[id] += size
				photos[id] = append(photos[id], row)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	var err error
	r.spool, err = newSpool()
	if err != nil {
		return err
	}
	defer r.spool.close()
	for _, m := range []struct{ name, kind string }{{"steps.csv", "steps"}, {"heartRate.csv", "heartRate"}} {
		if names[m.name] {
			if err := r.spool.read(ctx, src, m.name, m.kind, ids); err != nil {
				return err
			}
		}
	}
	if err := r.spool.flush(); err != nil {
		return err
	}
	if err := rc.RootNode().SetAttrs(nodeattr.Title, "Runkeeper workouts", nodeattr.DefaultVisibility, "hide"); err != nil {
		return err
	}
	collection, err := rc.RootNode().ChildPathObject("activities")
	if err != nil {
		return err
	}
	if err := collection.SetAttr(nodeattr.Title, "Activities"); err != nil {
		return err
	}
	if err := r.attachAccountSources(); err != nil {
		return err
	}
	for _, a := range activities {
		if err := ctx.Err(); err != nil {
			return err
		}
		changed, err := r.importActivity(collection, a, photos[a.ID])
		if err != nil {
			return fmt.Errorf("runkeeper activity %s: %w", a.ID, err)
		}
		r.progress.Done++
		if !changed {
			r.progress.Unchanged++
		}
		if r.progress.Done%100 == 0 {
			if err := r.notify(); err != nil {
				return err
			}
		}
	}
	if err := r.notify(); err != nil {
		return err
	}
	return rc.AccountNode().SetAttr("completedVersion", importVersion)
}

func (r *archiveRun) writeMember(name string, limit int64) (blob.Ref, error) {
	src, err := r.src.OpenLimit(name, limit)
	if err != nil {
		return blob.Ref{}, err
	}
	defer src.Close()
	return schema.WriteFileFromReader(r.rc.Context(), r.rc.Host.Target(), path.Base(name), src)
}
func (r *archiveRun) writeJSON(name string, v any) (blob.Ref, error) {
	// Only bounded metadata (one activity/photo manifest), never tracks or sensors.
	data, err := json.Marshal(v)
	if err != nil {
		return blob.Ref{}, err
	}
	if name == "photos.json" && len(data) > maxPhotoManifest {
		return blob.Ref{}, fmt.Errorf("photo manifest exceeds byte limit")
	}
	return schema.WriteFileFromReader(r.rc.Context(), r.rc.Host.Target(), name, bytes.NewReader(data))
}
func (r *archiveRun) attachAccountSources() error {
	var sets, deletes []string
	for _, s := range []struct {
		name, attr string
		limit      int64
	}{
		{"profile.json", "runkeeperProfileJSON", maxRecord},
		{"cardioActivities.csv", "runkeeperCardioCSV", maxMetadata},
		{"photos.csv", "runkeeperPhotosCSV", maxMetadata},
		{"measurements.csv", "runkeeperMeasurementsCSV", maxMetadata},
		{"steps.csv", "runkeeperStepsCSV", maxSensors},
		{"heartRate.csv", "runkeeperHeartRateCSV", maxSensors},
	} {
		if !r.names[s.name] {
			deletes = append(deletes, s.attr)
			continue
		}
		ref, err := r.writeMember(s.name, s.limit)
		if err != nil {
			return err
		}
		sets = append(sets, s.attr, ref.String())
	}
	return r.rc.AccountNode().SetAttrsWithDeletes(deletes, sets...)
}

type photoInput struct {
	Raw  rawRow `json:"source"`
	Hash string `json:"contentHash,omitempty"`
	Ref  string `json:"fileRef,omitempty"`
}

func (r *archiveRun) importActivity(parent *importer.Object, a *activity, rows []rawRow) (bool, error) {
	var track trackInfo
	trackName := strings.TrimSpace(a.Raw["GPX File"])
	if trackName != "" {
		if !r.names[trackName] {
			r.warn(a.ID + ": missing GPX " + trackName)
		} else {
			var ok bool
			track, ok = r.tracks[trackName]
			if !ok {
				var err error
				track, err = inspectTrack(r.src, trackName)
				if err != nil {
					return false, err
				}
				r.tracks[trackName] = track
			}
			if track.Warning != "" {
				r.warn(a.ID + ": " + track.Warning)
			}
		}
	}
	if track.Start == "" {
		switch a.Attrs[attrStartDateSource] {
		case startDateSourceCSVAssumedUTC:
			r.warn(a.ID + ": no zoned activity time; using CSV wall-clock date as UTC for startDate (timezone unknown)")
		case "":
			r.warn(a.ID + ": no usable activity date in GPX or CSV")
		}
	}
	var photos []photoInput
	for _, row := range rows {
		p := photoInput{Raw: row}
		name := strings.TrimSpace(row["Image File Name"])
		if name != "" && r.names[name] {
			var ok bool
			p.Hash, ok = r.hashes[name]
			if !ok {
				var err error
				p.Hash, err = hashMember(r.src, name, maxTrack)
				if err != nil {
					return false, err
				}
				r.hashes[name] = p.Hash
			}
		} else {
			r.warn(a.ID + ": missing photo " + name)
		}
		photos = append(photos, p)
	}
	slices.SortFunc(photos, func(a, b photoInput) int {
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		return strings.Compare(string(aa), string(bb))
	})
	steps, err := r.spool.hash(r.rc.Context(), a.ID+"-steps")
	if err != nil {
		return false, err
	}
	heart, err := r.spool.hash(r.rc.Context(), a.ID+"-heartRate")
	if err != nil {
		return false, err
	}
	h := sha256.New()
	if err := json.NewEncoder(h).Encode(struct {
		Version                           string
		Row                               rawRow
		Track                             string
		TrackRepairVersion, RepairedTrack string
		Photos                            []photoInput
		Steps, HeartRate                  string
	}{importVersion, a.Raw, track.Hash, track.RepairVersion, track.RepairedHash, photos, steps, heart}); err != nil {
		return false, err
	}
	fingerprint := hex.EncodeToString(h.Sum(nil))
	obj, err := parent.ChildPathObject(a.ID)
	if err != nil {
		return false, err
	}
	if obj.Attr(attrVersion) == importVersion && obj.Attr(attrHash) == fingerprint {
		return false, nil
	}
	attrs := make(map[string]string, len(a.Attrs)+8)
	for k, v := range a.Attrs {
		attrs[k] = v
	}
	if track.Start != "" {
		attrs[nodeattr.StartDate] = track.Start
		attrs[attrStartDateSource] = startDateSourceGPX
	}
	ref, err := r.writeJSON("activity.json", a.Raw)
	if err != nil {
		return false, err
	}
	attrs[attrActivityJSON] = ref.String()
	if track.Hash != "" {
		ref, err := r.writeMember(trackName, maxTrack)
		if err != nil {
			return false, err
		}
		attrs[attrSourceGPX] = ref.String()
		if track.Usable {
			if track.RepairVersion != "" {
				ref, err = r.writeRepairedTrack(trackName, track)
				if err != nil {
					return false, err
				}
				attrs[attrGPXRepairVersion] = track.RepairVersion
				attrs[attrGPXRepairCount] = strconv.Itoa(track.RepairCount)
			}
			attrs[nodeattr.FitnessTrack] = ref.String()
			attrs["latitude"], attrs["longitude"] = track.Latitude, track.Longitude
		}
	}
	for i := range photos {
		if photos[i].Hash == "" {
			continue
		}
		ref, err := r.writeMember(strings.TrimSpace(photos[i].Raw["Image File Name"]), maxTrack)
		if err != nil {
			return false, err
		}
		photos[i].Ref = ref.String()
	}
	if len(photos) > 0 {
		ref, err := r.writeJSON("photos.json", photos)
		if err != nil {
			return false, err
		}
		attrs[attrPhotosJSON] = ref.String()
	}
	for _, s := range []struct{ kind, attr string }{{"steps", attrSteps}, {"heartRate", attrHeartRate}} {
		name := r.spool.files[a.ID+"-"+s.kind]
		if name == "" {
			continue
		}
		ref, err := r.writeSensor(name, s.kind+".jsonl")
		if err != nil {
			return false, err
		}
		attrs[s.attr] = ref.String()
	}
	var sets, deletes []string
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sets = append(sets, k, attrs[k])
	}
	for _, k := range activityKeys {
		if _, ok := attrs[k]; !ok {
			deletes = append(deletes, k)
		}
	}
	// All files succeed before the sole atomic publication, including completion.
	sets = append(sets, attrHash, fingerprint, attrVersion, importVersion)
	if err := r.rc.Context().Err(); err != nil {
		return false, err
	}
	if err := obj.SetAttrsWithDeletes(deletes, sets...); err != nil {
		return false, err
	}
	return true, nil
}

func (r *archiveRun) writeRepairedTrack(name string, expected trackInfo) (blob.Ref, error) {
	f, info, err := repairedTrackFile(r.src, name, expected.Hash)
	if err != nil {
		return blob.Ref{}, err
	}
	defer removeTrackFile(f)
	if info.RepairVersion != expected.RepairVersion || info.RepairCount != expected.RepairCount || info.RepairedHash != expected.RepairedHash {
		return blob.Ref{}, errors.New("runkeeper: repaired GPX changed during import")
	}
	filename := "runkeeper-repaired-v" + info.RepairVersion + ".gpx"
	return schema.WriteFileFromReader(r.rc.Context(), r.rc.Host.Target(), filename, contextReader{r.rc.Context(), f})
}

func (r *archiveRun) writeSensor(name, filename string) (blob.Ref, error) {
	f, err := os.Open(name)
	if err != nil {
		return blob.Ref{}, err
	}
	defer f.Close()
	return schema.WriteFileFromReader(r.rc.Context(), r.rc.Host.Target(), filename, contextReader{r.rc.Context(), f})
}
