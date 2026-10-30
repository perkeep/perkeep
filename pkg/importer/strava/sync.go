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
	"errors"
	"strconv"
	"time"

	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/sorted"
)

const (
	syncCacheKey      = "sync"
	syncStateVersion  = 1
	syncPollInterval  = time.Hour
	syncDailyInterval = 24 * time.Hour
	syncRecentWindow  = 7 * 24 * time.Hour
)

// Disposable account-private scheduling hints. Losing these can repeat recent
// checks, but must never rewind or replace the durable checkpoint.
type syncCache struct {
	Version        int       `json:"version"`
	NextPoll       time.Time `json:"nextPoll"`
	NextDaily      time.Time `json:"nextDaily"`
	MostRecentID   int64     `json:"mostRecentID"`
	MostRecentHash string    `json:"mostRecentHash"`
}

type activitySyncer struct {
	r          *run
	activities *importer.Object
	now        func() time.Time
	cache      sorted.KeyValue
	schedule   syncCache
	progress   importProgress
	fetchedAt  map[int64]time.Time
}

func fullRefreshCacheKey(id int64) string {
	return "fullRefresh:" + strconv.FormatInt(id, 10)
}

// progress was validated before Run made any HTTP requests. Scheduling hints
// can defer routine checks, but cannot suppress backfill or schema migration.
func (r *run) syncActivities(activities *importer.Object, progress importProgress, now func() time.Time) error {
	s := &activitySyncer{
		r: r, activities: activities, now: now, progress: progress,
		fetchedAt: make(map[int64]time.Time),
	}
	var err error
	s.cache, err = r.AccountCache()
	if err != nil {
		r.Logf("sync cache unavailable; checking without scheduling hints: %v", err)
		s.cache = nil
	}
	s.loadSchedule()
	if !s.progress.backfillDone {
		latest, err := s.catchUp()
		if err != nil {
			return err
		}
		s.rememberPoll(latest)
	} else if !s.now().Before(s.schedule.NextPoll) {
		if err := s.poll(); err != nil {
			return err
		}
	}
	if !s.now().Before(s.schedule.NextDaily) {
		if err := s.reconcileRecent(); err != nil {
			return err
		}
		s.schedule.NextDaily = s.now().Add(syncDailyInterval)
		s.saveSchedule()
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	return r.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion)
}

// catchUp delegates authoritative progress to the chronological importer. It
// discovers the complete inclusive range before sorting and publishing groups;
// the list API does not promise a global timestamp order.
func (s *activitySyncer) catchUp() (*activity, error) {
	latest, err := s.r.importChronologicalActivitiesAt(s.activities, s.progress, s.now, func(a *activity) error {
		return s.importSummary(a, false)
	})
	if err != nil {
		return nil, err
	}
	s.progress, err = readImportProgress(s.r.AccountNode())
	return latest, err
}

func validateSummary(a *activity) error {
	if a.ID <= 0 || a.StartDate.IsZero() {
		return errors.New("invalid activity summary identity or start date")
	}
	return validateActivity(a)
}

func (s *activitySyncer) poll() error {
	client, err := s.r.client()
	if err != nil {
		return err
	}
	summaries, err := client.listActivities(s.r.Context(), 1, 1)
	if err != nil {
		return err
	}
	if len(summaries) > 1 {
		return errors.New("strava: most-recent probe returned too many activities")
	}
	var latest *activity
	if len(summaries) == 1 {
		latest = &summaries[0]
		if err := validateSummary(latest); err != nil {
			return err
		}
		if latest.StartDate.After(s.now()) {
			return errors.New("strava: most-recent activity starts in the future")
		}
		if s.progress.through.IsZero() || latest.StartDate.After(s.progress.through) {
			if _, err := s.catchUp(); err != nil {
				return err
			}
			if s.progress.through.Before(latest.StartDate) {
				return errors.New("strava: chronological catchup did not reach most-recent activity")
			}
		}
		changed := latest.ID != s.schedule.MostRecentID || summaryHash(latest) != s.schedule.MostRecentHash
		if changed && !latest.StartDate.Before(s.now().Add(-syncRecentWindow)) {
			if err := s.importSummary(latest, false); err != nil {
				return err
			}
		}
	}
	s.rememberPoll(latest)
	return nil
}

func (s *activitySyncer) rememberPoll(latest *activity) {
	s.schedule.NextPoll = s.now().Add(syncPollInterval)
	s.schedule.MostRecentID, s.schedule.MostRecentHash = 0, ""
	if latest != nil {
		s.schedule.MostRecentID, s.schedule.MostRecentHash = latest.ID, summaryHash(latest)
	}
	s.saveSchedule()
}

func (s *activitySyncer) reconcileRecent() error {
	summaries, err := s.r.discoverActivities(s.now().Add(-syncRecentWindow))
	if err != nil {
		return err
	}
	for i := range summaries {
		a := &summaries[i]
		if s.freshlyFetched(a.ID) {
			continue
		}
		if err := s.importSummary(a, true); err != nil {
			return err
		}
	}
	return s.r.Context().Err()
}

func (s *activitySyncer) importSummary(a *activity, force bool) error {
	fetched, err := s.r.importActivityWithForce(s.activities, a, force)
	if err != nil {
		return err
	}
	if fetched {
		// Persist before advancing the durable cursor: a checkpoint write
		// failure after successful metadata must not repeat heavy daily work.
		fetchedAt := s.now()
		if s.fetchedAt == nil {
			s.fetchedAt = make(map[int64]time.Time)
		}
		s.fetchedAt[a.ID] = fetchedAt
		s.cacheSet(fullRefreshCacheKey(a.ID), fetchedAt.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

func (s *activitySyncer) freshlyFetched(id int64) bool {
	if fetched, ok := s.fetchedAt[id]; ok {
		return !fetched.After(s.now()) && fetched.After(s.now().Add(-syncDailyInterval))
	}
	raw, ok := s.cacheGet(fullRefreshCacheKey(id))
	if !ok {
		return false
	}
	fetched, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || fetched.After(s.now()) {
		s.r.Logf("ignoring invalid cached full-refresh timestamp")
		return false
	}
	return fetched.After(s.now().Add(-syncDailyInterval))
}

func (s *activitySyncer) loadSchedule() {
	s.schedule = syncCache{Version: syncStateVersion}
	raw, ok := s.cacheGet(syncCacheKey)
	if !ok {
		return
	}
	var state syncCache
	if json.Unmarshal([]byte(raw), &state) != nil || state.Version != syncStateVersion ||
		state.NextPoll.After(s.now().Add(syncPollInterval)) ||
		state.NextDaily.After(s.now().Add(syncDailyInterval)) ||
		state.MostRecentID < 0 || (state.MostRecentID == 0) != (state.MostRecentHash == "") ||
		(state.MostRecentHash != "" && len(state.MostRecentHash) != 64) {
		s.r.Logf("ignoring invalid cached sync schedule")
		return
	}
	s.schedule = state
}

func (s *activitySyncer) saveSchedule() {
	raw, err := json.Marshal(s.schedule)
	if err != nil {
		s.r.Logf("could not encode sync schedule: %v", err)
		return
	}
	s.cacheSet(syncCacheKey, string(raw))
}

func (s *activitySyncer) cacheGet(key string) (string, bool) {
	if s.cache == nil {
		return "", false
	}
	raw, err := s.cache.Get(key)
	if errors.Is(err, sorted.ErrNotFound) {
		return "", false
	}
	if err != nil {
		s.r.Logf("ignoring unreadable sync cache: %v", err)
		return "", false
	}
	return raw, true
}

func (s *activitySyncer) cacheSet(key, value string) {
	if s.cache != nil {
		if err := s.cache.Set(key, value); err != nil {
			s.r.Logf("could not save sync cache: %v", err)
		}
	}
}
