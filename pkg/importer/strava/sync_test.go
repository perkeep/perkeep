/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

func syncTestTime() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }

func syncRun(t *testing.T, f *coreFixture, now time.Time) {
	t.Helper()
	if err := (imp{}).runAt(f.rc, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
}

// syncActivities deliberately does not correlate IDs and dates. The provider's
// default and after-filtered listings are newest-first. Discovery must finish
// before sorting globally, not sort by an ID or reverse pages independently.
func syncActivities(t *testing.T, api *coreAPI, count int, start time.Time) []activity {
	t.Helper()
	api.highQuota = true
	api.dateFiltered = true
	activities := make([]activity, count)
	for i := range activities {
		a := coreActivity(int64(900000 + (i*73)%997))
		a.StartDate = start.Add(time.Duration(i) * time.Minute)
		activities[i] = a
		api.details[a.ID], api.streams[a.ID] = coreJSON(t, a), []byte(coreTrackStreams)
	}
	syncPages(api, activities)
	return activities
}

func syncPages(api *coreAPI, ascending []activity) {
	api.pages = make(map[int][]activity)
	newest := append([]activity(nil), ascending...)
	slices.Reverse(newest)
	for i := 0; i < len(newest); i += 100 {
		api.pages[i/100+1] = append([]activity(nil), newest[i:min(i+100, len(newest))]...)
	}
}

func syncReset(api *coreAPI) {
	api.calls = make(map[string]int)
	api.queries, api.order = nil, nil
}

func syncProgressAt(t *testing.T, f *coreFixture) importProgress {
	t.Helper()
	// Read from signed claims, not only the live account Object's cached attrs.
	acct, err := f.rc.Host.ObjectFromRef(f.rc.AccountNode().PermanodeRef())
	if err != nil {
		t.Fatal(err)
	}
	p, err := readImportProgress(acct)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func syncWantProgress(t *testing.T, f *coreFixture, a activity, complete bool) {
	t.Helper()
	p := syncProgressAt(t, f)
	if !p.through.Equal(a.StartDate) || p.backfillDone != complete {
		t.Errorf("durable progress = %+v; want exact time %s, complete=%t", p, a.StartDate.Format(time.RFC3339Nano), complete)
	}
	progressAssertAttrs(t, f, progressWant(a.StartDate, complete))
}

func syncWantHeavy(t *testing.T, api *coreAPI, ids []int64) {
	t.Helper()
	if !slices.Equal(api.order, ids) {
		t.Fatalf("detail request order = %v; want %v", api.order, ids)
	}
	for _, id := range ids {
		path := "/activities/" + strconv.FormatInt(id, 10)
		if api.calls[path] != 1 || api.calls[path+"/streams"] != 1 {
			t.Errorf("heavy requests for %d = %d/%d; want 1/1", id, api.calls[path], api.calls[path+"/streams"])
		}
	}
}

func syncIDs(activities []activity) []int64 {
	ids := make([]int64, len(activities))
	for i, a := range activities {
		ids[i] = a.ID
	}
	return ids
}

func syncAccountCache(t *testing.T, f *coreFixture) sorted.KeyValue {
	t.Helper()
	cache, err := f.rc.AccountCache()
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

// After backfill, the only unbounded request is the one-summary probe. Every
// full-range discovery must retain its explicit activity-date lower bound.
func syncWantDiscoveryBounds(t *testing.T, api *coreAPI, boundaries ...time.Time) {
	t.Helper()
	wanted := make(map[string]bool)
	for _, boundary := range boundaries {
		wanted[strconv.FormatInt(boundary.Unix()-1, 10)] = false
	}
	for _, q := range api.queries {
		if q.Get("per_page") == "1" && !q.Has("after") {
			continue
		}
		after := q.Get("after")
		if _, ok := wanted[after]; !ok || q.Get("per_page") != "100" {
			t.Errorf("unexpected discovery range %v; want inclusive bounds %v", q, boundaries)
			continue
		}
		wanted[after] = true
	}
	for after, seen := range wanted {
		if !seen {
			t.Errorf("missing bounded discovery after=%s in %v", after, api.queries)
		}
	}
}

func TestSyncInitialBackfillOldestFirstAcrossPages(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 205, now.Add(-30*24*time.Hour))
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities))
	syncWantProgress(t, f, activities[len(activities)-1], true)
	if len(api.queries) < 4 {
		t.Fatalf("listing queries = %v; want multiple pages and final empty page", api.queries)
	}
	for _, q := range api.queries {
		if q.Has("after") && q.Get("after") != strconv.FormatInt(now.Add(-syncRecentWindow).Unix()-1, 10) || q.Get("per_page") != "100" {
			t.Errorf("initial traversal query = %v; want full discovery or daily activity-date window, per_page=100", q)
		}
	}
	for _, a := range activities {
		coreAttrs(t, f.activity(t, a.ID), map[string]string{attrImportVersion: "2"})
	}
}

func TestSyncInterruptedPrefixFlushAndColdResume(t *testing.T) {
	test.TLog(t)
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 8, now.Add(-30*24*time.Hour))
	failPath := "/activities/" + strconv.FormatInt(activities[5].ID, 10)
	api.failures[failPath] = http.StatusBadGateway
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).runAt(f.rc, func() time.Time { return now }); err == nil {
		t.Fatal("failing backfill succeeded")
	}
	syncWantProgress(t, f, activities[4], false)
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
	delete(api.failures, failPath)
	syncReset(api)
	// Reconstruct the Host/account from signed claims, not a RunContext's
	// in-memory Object attrs or its disposable scheduling hints.
	if err := f.rc.Host.Close(); err != nil {
		t.Fatal(err)
	}
	host, err := importer.NewHost(f.hostConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	accountRef := f.rc.AccountNode().PermanodeRef()
	account, err := host.ObjectFromRef(accountRef)
	if err != nil {
		t.Fatal(err)
	}
	if account.Attr(attrImportThrough) != f.activityState(accountRef).Get(attrImportThrough) {
		t.Fatal("new Host did not reload the durable checkpoint")
	}
	if err := host.RunImporterAccount("strava", accountRef); err != nil {
		t.Fatal(err)
	}
	syncWantHeavy(t, api, syncIDs(activities[5:]))
	// The managed run may use its own account Object; reload signed claims.
	account, err = host.ObjectFromRef(accountRef)
	if err != nil {
		t.Fatal(err)
	}
	p, err := readImportProgress(account)
	if err != nil || !p.through.Equal(activities[7].StartDate) || !p.backfillDone {
		t.Fatalf("cold Host durable progress = %+v, %v", p, err)
	}
	if account.Attr(attrImportThroughVersion) != runCompleteVersion || account.Attr(importer.AcctAttrCompletedVersion) != runCompleteVersion {
		t.Error("cold Host run did not persist completion binding")
	}
	if len(api.queries) == 0 || api.queries[0].Get("after") != strconv.FormatInt(activities[4].StartDate.Unix()-1, 10) {
		t.Errorf("resume queries = %v; want durable high-watermark overlap", api.queries)
	}
}

func TestSyncShiftedPagesDeduplicateBeforePopulation(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 5, now.Add(-30*24*time.Hour))
	api.dateFiltered = false
	// A repeated ID may shift across pages, and provider pages need not be
	// chronological. Every selected page is discovered before publication.
	api.pages = map[int][]activity{1: {activities[4], activities[1]}, 2: {activities[0], activities[4]}, 3: {activities[3], activities[2]}}
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities))
	syncWantProgress(t, f, activities[4], true)
	for _, a := range activities {
		f.assertNewActivityClaim(t, f.activity(t, a.ID), &a)
	}
}

func TestSyncHardKillGapSkipsAlreadyCompleteActivityDetails(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 8, now.Add(-30*24*time.Hour))
	path := "/activities/" + strconv.FormatInt(activities[5].ID, 10)
	api.failures[path] = http.StatusBadGateway
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).runAt(f.rc, func() time.Time { return now }); err == nil {
		t.Fatal("failing backfill succeeded")
	}
	// A hard kill loses the in-memory cursor but not activity completion claims.
	// Recreate that durable state with a checkpoint lagging three activities.
	lagging := importProgress{through: activities[1].StartDate}
	if err := writeImportProgress(f.rc.AccountNode(), lagging, false); err != nil {
		t.Fatal(err)
	}
	delete(api.failures, path)
	syncReset(api)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities[5:]))
	syncWantProgress(t, f, activities[7], true)
	for _, a := range activities[:5] {
		f.assertNewActivityClaim(t, f.activity(t, a.ID), &a)
	}
}

func TestSyncSameSecondTiesAcrossPagesAndInterruptedCheckpoint(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 225, now.Add(-30*24*time.Hour))
	for i := range activities {
		activities[i].StartDate = activities[0].StartDate
		api.details[activities[i].ID] = coreJSON(t, activities[i])
	}
	syncPages(api, activities)
	// Master breaks exact-time ties by stable numeric source ID.
	expected := append([]activity(nil), activities...)
	slices.SortFunc(expected, func(a, b activity) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	failed := expected[137]
	path := "/activities/" + strconv.FormatInt(failed.ID, 10)
	api.failures[path] = http.StatusBadGateway
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).runAt(f.rc, func() time.Time { return now }); err == nil {
		t.Fatal("failing tie cluster succeeded")
	}
	p := syncProgressAt(t, f)
	if !p.through.IsZero() || p.backfillDone {
		t.Fatalf("partial exact-time group advanced timestamp-only progress: %+v", p)
	}
	if got := len(progressAccountClaims(t, f)); got != 0 {
		t.Fatalf("partial exact-time group published %d progress claims", got)
	}
	for _, a := range expected[:137] {
		coreAttrs(t, f.activity(t, a.ID), map[string]string{attrImportVersion: runCompleteVersion, attrSummaryHash: summaryHash(&a)})
		f.assertNewActivityClaim(t, f.activity(t, a.ID), &a)
	}
	delete(api.failures, path)
	syncReset(api)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(expected[137:]))
	syncWantProgress(t, f, activities[0], true)
	if got := len(progressAccountClaims(t, f)); got != 1 {
		t.Errorf("completed dense group produced %d progress claims; want one constant-size timestamp checkpoint", got)
	}
	for _, a := range activities {
		coreAttrs(t, f.activity(t, a.ID), map[string]string{attrImportVersion: "2"})
	}
}

func TestSyncPeriodicCheckpointCountAndElapsedTime(t *testing.T) {
	for _, elapsed := range []bool{false, true} {
		t.Run(fmt.Sprintf("elapsed=%t", elapsed), func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "")
			api := newCoreAPI(t)
			now := syncTestTime()
			count := 53
			if elapsed {
				count = 3
			}
			activities := syncActivities(t, api, count, now.Add(-30*24*time.Hour))
			f := newCoreFixture(t, api)
			f.configure(t)
			observed := make(map[int64]importProgress)
			api.before = func(req *http.Request) error {
				path := strings.TrimPrefix(req.URL.Path, "/api/v3/activities/")
				if id, err := strconv.ParseInt(path, 10, 64); err == nil {
					observed[id] = syncProgressAt(t, f)
				}
				if elapsed && strings.HasSuffix(req.URL.Path, "/"+strconv.FormatInt(activities[0].ID, 10)+"/streams") {
					now = now.Add(31 * time.Second)
				}
				return nil
			}
			syncRunClock := func() error { return (imp{}).runAt(f.rc, func() time.Time { return now }) }
			if err := syncRunClock(); err != nil {
				t.Fatal(err)
			}
			checkpointIndex, nextIndex := 49, 50
			if elapsed {
				checkpointIndex, nextIndex = 0, 1
			}
			cp := observed[activities[nextIndex].ID]
			if !cp.through.Equal(activities[checkpointIndex].StartDate) {
				t.Errorf("checkpoint before activity %d = %+v; want completed activity %d", nextIndex+1, cp, checkpointIndex+1)
			}
			if !elapsed {
				if cp := observed[activities[49].ID]; !cp.through.IsZero() {
					t.Errorf("checkpoint prematurely persisted before activity 50: %+v", cp)
				}
			}
			syncWantProgress(t, f, activities[len(activities)-1], true)
		})
	}
}

func TestSyncFailedProgressClaimDoesNotAdvanceDurableMarker(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 52, now.Add(-30*24*time.Hour))
	f := newCoreFixture(t, api)
	f.configure(t)
	// A single injected failure must not be retried by deferred flushing.
	f.storage.failNextClaim(attrImportThroughVersion)
	if err := (imp{}).runAt(f.rc, func() time.Time { return now }); !errors.Is(err, errCoreClaimFailure) {
		t.Fatalf("progress claim failure = %v; want injected failure", err)
	}
	cp := syncProgressAt(t, f)
	if !cp.through.IsZero() || cp.backfillDone {
		t.Errorf("failed progress claim advanced marker: %+v", cp)
	}
	if slices.Contains(api.order, activities[50].ID) {
		t.Error("continued importing past failed periodic checkpoint")
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
	if f.storage.failClaim != "" {
		t.Fatal("injected progress failure was not attempted")
	}
	if got := len(progressAccountClaims(t, f)); got != 0 {
		t.Fatalf("failed progress write was retried: %d claims", got)
	}
	syncReset(api)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities[50:]))
	syncWantProgress(t, f, activities[51], true)
}

func TestSyncCancellationAndPaginationFailureCannotPublishPrefix(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancel), func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "")
			api := newCoreAPI(t)
			now := syncTestTime()
			activities := syncActivities(t, api, 102, now.Add(-30*24*time.Hour))
			api.before = func(req *http.Request) error {
				if req.URL.Query().Get("page") == "2" {
					if cancel {
						return context.Canceled
					}
					return errors.New("injected second page failure")
				}
				return nil
			}
			f := newCoreFixture(t, api)
			f.configure(t)
			err := (imp{}).runAt(f.rc, func() time.Time { return now })
			if err == nil || cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("interrupted traversal = %v", err)
			}
			if p := syncProgressAt(t, f); !p.through.IsZero() || p.backfillDone {
				t.Fatalf("incomplete discovery published progress: %+v", p)
			}
			if len(api.order) != 0 {
				t.Errorf("incomplete discovery requested activity details: %v", api.order)
			}
			api.before = nil
			syncReset(api)
			syncRun(t, f, now)
			syncWantHeavy(t, api, syncIDs(activities))
			syncWantProgress(t, f, activities[101], true)
		})
	}
}

func TestSyncInvalidAndConflictingDiscoveryFailsBeforePopulation(t *testing.T) {
	for _, kind := range []string{"zero-date", "zero-id", "negative-id", "conflicting-duplicate"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "")
			api := newCoreAPI(t)
			now := syncTestTime()
			activities := syncActivities(t, api, 3, now.Add(-30*24*time.Hour))
			api.dateFiltered = false
			broken := activities[1]
			switch kind {
			case "zero-date":
				broken.StartDate = time.Time{}
			case "zero-id":
				broken.ID = 0
			case "negative-id":
				broken.ID = -1
			case "conflicting-duplicate":
				broken = activities[0]
				broken.Name += " changed while paging"
			}
			api.pages = map[int][]activity{1: {activities[0]}, 2: {broken, activities[2]}}
			f := newCoreFixture(t, api)
			f.configure(t)
			err := (imp{}).runAt(f.rc, func() time.Time { return now })
			if err == nil {
				t.Fatal("invalid/conflicting discovery succeeded")
			}
			if kind == "conflicting-duplicate" && !strings.Contains(err.Error(), "changed during discovery") {
				t.Errorf("discovery error=%v", err)
			}
			if p := syncProgressAt(t, f); !p.through.IsZero() || p.backfillDone {
				t.Errorf("invalid discovery advanced progress: %+v", p)
			}
			if len(api.order) != 0 || len(progressAccountClaims(t, f)) != 0 {
				t.Errorf("invalid discovery populated activities or progress: %v", api.order)
			}
		})
	}
}

func TestSyncHourlyGateCheapPollNewActivityAndLongDowntime(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 3, now.Add(-30*24*time.Hour))
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	before := f.storage.NumBlobs()
	syncReset(api)
	syncRun(t, f, now.Add(59*time.Minute))
	if len(api.calls) != 0 {
		t.Errorf("early hourly gate made requests: %v", api.calls)
	}
	syncRun(t, f, now.Add(time.Hour))
	if len(api.order) != 0 || len(api.queries) != 1 || api.queries[0].Has("after") {
		t.Errorf("unchanged hourly probe did more than one newest-summary request: %v, details %v", api.queries, api.order)
	}
	if f.storage.NumBlobs() != before {
		t.Error("unchanged hourly poll archived blobs")
	}
	syncReset(api)
	newer := coreActivity(11)
	newer.StartDate = now.Add(90 * time.Minute)
	activities = append(activities, newer)
	api.details[newer.ID], api.streams[newer.ID] = coreJSON(t, newer), []byte(coreTrackStreams)
	syncPages(api, activities)
	syncRun(t, f, now.Add(2*time.Hour))
	syncWantHeavy(t, api, []int64{newer.ID})
	syncWantProgress(t, f, newer, true)
	syncWantDiscoveryBounds(t, api, activities[2].StartDate)
	// More than one provider page, and older than the daily reconciliation
	// window, must still be found after a long offline interval.
	for i := 0; i < 105; i++ {
		a := coreActivity(int64(100 + i))
		a.StartDate = now.Add(time.Duration(i+3) * time.Hour)
		activities = append(activities, a)
		api.details[a.ID], api.streams[a.ID] = coreJSON(t, a), []byte(coreTrackStreams)
	}
	syncPages(api, activities)
	syncReset(api)
	syncRun(t, f, now.Add(40*24*time.Hour))
	syncWantHeavy(t, api, syncIDs(activities[4:]))
	syncWantProgress(t, f, activities[len(activities)-1], true)
	// Catchup starts at the exact durable prefix, not the daily window.
	// Daily reconciliation independently uses wall-clock now minus seven days.
	syncWantDiscoveryBounds(t, api, newer.StartDate, now.Add(40*24*time.Hour).Add(-syncRecentWindow))
}

func TestSyncDailyDescriptionStreamsAndFrozenHistory(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 3, now.Add(-2*24*time.Hour))
	activities[0].StartDate = now.Add(-30 * 24 * time.Hour)
	api.details[activities[0].ID] = coreJSON(t, activities[0])
	syncPages(api, activities)
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	oldObject := f.activity(t, activities[0].ID)
	oldState := f.activityState(oldObject.PermanodeRef())
	recent := f.activity(t, activities[1].ID)
	oldTrack := recent.Attr(nodeattr.FitnessTrack)
	oldHash := recent.Attr(attrSummaryHash)
	// Details and streams change with precisely the same listed summaries.
	for _, a := range activities {
		a.Description = "description-only edit"
		api.details[a.ID] = coreJSON(t, a)
		api.streams[a.ID] = []byte(strings.ReplaceAll(coreTrackStreams, "12.5", "22.5"))
	}
	syncReset(api)
	syncRun(t, f, now.Add(24*time.Hour))
	got := append([]int64(nil), api.order...)
	slices.Sort(got)
	want := syncIDs(activities[1:])
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("daily heavy requests = %v; want recent %v only", api.order, want)
	}
	recent = f.activity(t, activities[1].ID)
	coreAttrs(t, recent, map[string]string{nodeattr.Description: "description-only edit", attrSummaryHash: oldHash})
	if recent.Attr(nodeattr.FitnessTrack) == oldTrack {
		t.Error("same-summary streams edit did not update GPX")
	}
	if !reflect.DeepEqual(f.activityState(oldObject.PermanodeRef()), oldState) {
		t.Error("daily sweep changed frozen old activity")
	}
	// An unchanged forced refresh may spend requests, but writes no signed
	// activity claims, files, schemas, or other immutable blobs.
	before := f.storage.NumBlobs()
	claims := len(f.activityClaims(t, recent.PermanodeRef()))
	syncReset(api)
	syncRun(t, f, now.Add(48*time.Hour))
	if len(api.order) != 2 {
		t.Fatalf("next daily sweep did not force recent content: %v", api.order)
	}
	if f.storage.NumBlobs() != before || len(f.activityClaims(t, recent.PermanodeRef())) != claims {
		t.Error("unchanged forced refresh wrote immutable blobs or activity claims")
	}
}

func TestSyncFailedDailyResumesSuccessfulPerActivityRefreshes(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	syncActivities(t, api, 4, now.Add(-24*time.Hour))
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	// Whichever direction the daily window uses, fail the second attempted
	// refresh. The first successful refresh must not be fetched on retry.
	syncReset(api)
	daily := now.Add(24 * time.Hour)
	var succeededID, failedID int64
	api.before = func(req *http.Request) error {
		path := strings.TrimPrefix(req.URL.Path, "/api/v3/activities/")
		if id, err := strconv.ParseInt(path, 10, 64); err == nil {
			if succeededID == 0 {
				succeededID = id
			} else if id != succeededID {
				failedID = id
				return errors.New("injected daily detail failure")
			}
		}
		return nil
	}
	if err := (imp{}).runAt(f.rc, func() time.Time { return daily }); err == nil || succeededID == 0 || failedID == 0 {
		t.Fatalf("failed daily run = %v, first/failed = %d/%d", err, succeededID, failedID)
	}
	cache := syncAccountCache(t, f)
	raw, err := cache.Get(fullRefreshCacheKey(succeededID))
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || !stamp.Equal(daily) {
		t.Errorf("successful per-activity refresh checkpoint = %q, %v; want %s", raw, err, daily)
	}
	api.before = nil
	syncReset(api)
	syncRun(t, f, daily.Add(time.Minute))
	if slices.Contains(api.order, succeededID) {
		t.Errorf("daily retry re-fetched successfully checkpointed %d: %v", succeededID, api.order)
	}
	if !slices.Contains(api.order, failedID) {
		t.Errorf("daily retry lost failed activity %d: %v", failedID, api.order)
	}
}

func TestSyncCacheLossCorruptionAndErrorsDoNotRewindHistory(t *testing.T) {
	test.TLog(t)
	for _, kind := range []string{"missing", "invalid-json", "future-schedule", "read-error", "write-error"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "")
			api := newCoreAPI(t)
			now := syncTestTime()
			activities := syncActivities(t, api, 3, now.Add(-30*24*time.Hour))
			backing := &coreRateCache{KeyValue: sorted.NewMemoryKeyValue()}
			t.Cleanup(func() { backing.Close() })
			f := newCoreFixture(t, api, backing)
			f.configure(t)
			syncRun(t, f, now)
			durable := f.rc.AccountNode().Attr(attrImportThrough)
			cache := syncAccountCache(t, f)
			switch kind {
			case "missing":
				if err := cache.Delete(syncCacheKey); err != nil {
					t.Fatal(err)
				}
			case "invalid-json":
				if err := cache.Set(syncCacheKey, "not-json"); err != nil {
					t.Fatal(err)
				}
			case "future-schedule":
				if err := cache.Set(syncCacheKey, string(coreJSON(t, syncCache{Version: 1, NextPoll: now.Add(365 * 24 * time.Hour), NextDaily: now.Add(365 * 24 * time.Hour)}))); err != nil {
					t.Fatal(err)
				}
			case "read-error":
				backing.getErr = errors.New("injected sync cache read failure")
			case "write-error":
				backing.setErr = errors.New("injected sync cache write failure")
			}
			newActivity := coreActivity(7)
			newActivity.StartDate = now.Add(time.Hour)
			activities = append(activities, newActivity)
			api.details[newActivity.ID], api.streams[newActivity.ID] = coreJSON(t, newActivity), []byte(coreTrackStreams)
			syncPages(api, activities)
			syncReset(api)
			syncRun(t, f, now.Add(2*time.Hour))
			// On cache write failure, successful catchup still populates the
			// per-run fetchedAt map, preventing the due daily sweep from
			// fetching this recent activity a second time.
			syncWantHeavy(t, api, []int64{newActivity.ID})
			syncWantProgress(t, f, newActivity, true)
			for _, q := range api.queries {
				if q.Has("after") {
					lower, _ := strconv.ParseInt(q.Get("after"), 10, 64)
					if lower < activities[2].StartDate.Unix()-1 {
						t.Errorf("cache failure rewound history: %v (prior %s)", q, durable)
					}
				}
			}
		})
	}
}

func TestSyncAccountCacheIsolation(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 2, now.Add(-24*time.Hour))
	backing := sorted.NewMemoryKeyValue()
	t.Cleanup(func() { backing.Close() })
	f := newCoreFixture(t, api, backing)
	f.configure(t)
	syncRun(t, f, now)
	second := f.reopenForAnotherAccount(t)
	syncReset(api)
	syncRun(t, second, now)
	syncWantHeavy(t, api, syncIDs(activities))
	if first, err := syncAccountCache(t, f).Get(syncCacheKey); err != nil || first == "" {
		t.Errorf("first account sync cache disappeared: %q, %v", first, err)
	}
	// The second account's recent-refresh stamps cannot suppress the first
	// account's due refresh, or vice versa.
	syncReset(api)
	syncRun(t, second, now.Add(24*time.Hour))
	if len(api.order) != len(activities) {
		t.Errorf("second account daily refresh = %v", api.order)
	}
	syncReset(api)
	syncRun(t, f, now.Add(24*time.Hour))
	if len(api.order) != len(activities) {
		t.Errorf("sibling account cache suppressed first account refresh: %v", api.order)
	}
}

// Keep query parsing honest independently of the production implementation:
// IDs, before/after exclusivity, and offset pages are part of the fake's API.
func TestCoreAPIDateFilteredPagination(t *testing.T) {
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 5, now)
	q := url.Values{"after": {strconv.FormatInt(now.Unix()-1, 10)}, "before": {strconv.FormatInt(activities[4].StartDate.Unix(), 10)}, "page": {"2"}, "per_page": {"2"}}
	req, err := http.NewRequest(http.MethodGet, "https://www.strava.com/api/v3/athlete/activities?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-access")
	res, err := api.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got []activity
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(syncIDs(got), []int64{activities[1].ID, activities[0].ID}) {
		t.Errorf("filtered newest-first page = %v; want %v", syncIDs(got), []int64{activities[1].ID, activities[0].ID})
	}
}

func TestSyncUnavailableCachePreservesDurableBackfillPrefix(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 5, now.Add(-30*24*time.Hour))
	root := filepath.Join(t.TempDir(), "blocked-cache")
	if err := os.WriteFile(root, []byte("not a cache directory"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newCoreFixtureWithConfig(t, api, importer.HostConfig{CacheRoot: root})
	f.configure(t)
	p := importProgress{through: activities[2].StartDate}
	if err := writeImportProgress(f.rc.AccountNode(), p, false); err != nil {
		t.Fatal(err)
	}
	parent, err := f.rc.RootNode().ChildPathObject("activities")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&run{f.rc}).importActivity(parent, &activities[2]); err != nil {
		t.Fatal(err)
	}
	syncReset(api)
	logs := coreCaptureLog(t)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities[3:]))
	syncWantProgress(t, f, activities[4], true)
	if !strings.Contains(logs.String(), "cache") {
		t.Errorf("unavailable cache not diagnosed: %s", logs)
	}
}

func TestSyncCappedRunsNeverAdvanceUnlimitedBackfillCheckpoint(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "")
			api := newCoreAPI(t)
			now := syncTestTime()
			activities := syncActivities(t, api, 6, now.Add(-30*24*time.Hour))
			f := newCoreFixture(t, api)
			f.configure(t)
			if partial {
				failedPath := "/activities/" + strconv.FormatInt(activities[2].ID, 10)
				api.failures[failedPath] = http.StatusBadGateway
				if err := (imp{}).runAt(f.rc, func() time.Time { return now }); err == nil {
					t.Fatal("interrupted initial run succeeded")
				}
				delete(api.failures, failedPath)
				syncWantProgress(t, f, activities[1], false)
			}
			beforeProgress := syncProgressAt(t, f)
			t.Setenv(coreMaxActivitiesEnv, "2")
			api.perPage = 2
			syncReset(api)
			syncRun(t, f, now)
			if got := syncProgressAt(t, f); got != beforeProgress {
				t.Errorf("capped run changed unlimited progress: %+v -> %+v", beforeProgress, got)
			}
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
			syncWantHeavy(t, api, []int64{activities[5].ID, activities[4].ID})
			t.Setenv(coreMaxActivitiesEnv, "")
			api.perPage = 0
			syncReset(api)
			syncRun(t, f, now)
			expected := activities[:4]
			if partial {
				expected = activities[2:4]
			}
			syncWantHeavy(t, api, syncIDs(expected))
			syncWantProgress(t, f, activities[5], true)
			for _, a := range activities {
				coreAttrs(t, f.activity(t, a.ID), map[string]string{attrImportVersion: "2"})
			}
		})
	}
}

func TestSyncCorruptPerActivityRefreshCacheCannotSuppressWork(t *testing.T) {
	test.TLog(t)
	for _, kind := range []string{"missing", "malformed", "future"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "")
			api := newCoreAPI(t)
			now := syncTestTime()
			activities := syncActivities(t, api, 2, now.Add(-24*time.Hour))
			f := newCoreFixture(t, api)
			f.configure(t)
			syncRun(t, f, now)
			cache := syncAccountCache(t, f)
			var schedule syncCache
			raw, err := cache.Get(syncCacheKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw), &schedule); err != nil {
				t.Fatal(err)
			}
			schedule.NextDaily = time.Time{}
			if err := cache.Set(syncCacheKey, string(coreJSON(t, schedule))); err != nil {
				t.Fatal(err)
			}
			key := fullRefreshCacheKey(activities[0].ID)
			switch kind {
			case "missing":
				err = cache.Delete(key)
			case "malformed":
				err = cache.Set(key, "not a timestamp")
			case "future":
				err = cache.Set(key, now.Add(365*24*time.Hour).Format(time.RFC3339Nano))
			}
			if err != nil {
				t.Fatal(err)
			}
			syncReset(api)
			syncRun(t, f, now.Add(time.Hour))
			syncWantHeavy(t, api, []int64{activities[0].ID})
		})
	}
}

func TestSyncManagedCancellationFlushesDurablePrefix(t *testing.T) {
	test.TLog(t)
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 3, now.Add(-30*24*time.Hour))
	f := newCoreFixture(t, api)
	f.configure(t)
	closed := make(chan error, 1)
	cancellationStarted := false
	api.before = func(req *http.Request) error {
		if req.URL.Path == "/api/v3/activities/"+strconv.FormatInt(activities[2].ID, 10) {
			cancellationStarted = true
			// Close cancels the managed RunContext and waits until its deferred
			// checkpoint flush returns, before disposing the local cache.
			go func() { closed <- f.rc.Host.Close() }()
			<-req.Context().Done()
			return req.Context().Err()
		}
		return nil
	}
	err := f.rc.Host.RunImporterAccount("strava", f.rc.AccountNode().PermanodeRef())
	if !errors.Is(err, context.Canceled) || !cancellationStarted {
		t.Fatalf("managed cancellation = %v; cancellation started %t", err, cancellationStarted)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	syncWantProgress(t, f, activities[1], false)
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
}

func TestSyncNewestFirstEqualTimestampPageStillFindsOlderHistory(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 101, now.Add(-30*24*time.Hour))
	for i := 1; i < len(activities); i++ {
		activities[i].StartDate = activities[0].StartDate.Add(time.Hour)
		api.details[activities[i].ID] = coreJSON(t, activities[i])
	}
	syncPages(api, activities)
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	expected := append([]activity(nil), activities[1:]...)
	slices.SortFunc(expected, func(a, b activity) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	syncWantHeavy(t, api, append([]int64{activities[0].ID}, syncIDs(expected)...))
	syncWantProgress(t, f, activities[100], true)
	if api.calls["/athlete/activities?page=2"] != 1 || api.calls["/athlete/activities?page=3"] != 1 {
		t.Errorf("newest-first full-range discovery did not enumerate older page and EOF: %v", api.calls)
	}
}

func TestSyncMalformedCurrentProgressFailsBeforeScheduling(t *testing.T) {
	for _, attrs := range []map[string]string{
		{attrImportThrough: "not a timestamp", attrImportThroughVersion: runCompleteVersion},
		{attrImportThrough: time.Time{}.Format(time.RFC3339Nano), attrImportThroughVersion: runCompleteVersion},
		{attrImportThrough: syncTestTime().Format(time.RFC3339Nano)},
		{attrBackfillCompletedVersion: runCompleteVersion},
	} {
		t.Run(fmt.Sprint(attrs), func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "")
			api := newCoreAPI(t)
			f := newCoreFixture(t, api)
			f.configure(t)
			for key, value := range attrs {
				if err := f.rc.AccountNode().SetAttr(key, value); err != nil {
					t.Fatal(err)
				}
			}
			before := f.activityState(f.rc.AccountNode().PermanodeRef())
			if err := (imp{}).runAt(f.rc, syncTestTime); err == nil {
				t.Fatal("invalid authoritative progress silently accepted")
			}
			if len(api.calls) != 0 {
				t.Errorf("invalid progress triggered requests: %v", api.calls)
			}
			if got := f.activityState(f.rc.AccountNode().PermanodeRef()); !reflect.DeepEqual(got, before) {
				t.Errorf("invalid progress overwritten: %v -> %v", before, got)
			}
		})
	}
}

func TestSyncUnknownProgressVersionDiscardsOpaquePayload(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 2, now.Add(-30*24*time.Hour))
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := f.rc.AccountNode().SetAttrs(attrImportThroughVersion, "future", attrImportThrough, "opaque future schema", attrBackfillCompletedVersion, "future"); err != nil {
		t.Fatal(err)
	}
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities))
	syncWantProgress(t, f, activities[1], true)
	if api.queries[0].Has("after") {
		t.Errorf("unknown version skipped history: %v", api.queries[0])
	}
}

func TestSyncSameSecondUploadMissedByHourlyProbeIsRecoveredDaily(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 1, now.Add(-time.Hour))
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	added := activities[0]
	added.ID = 77
	api.details[added.ID], api.streams[added.ID] = coreJSON(t, added), []byte(coreTrackStreams)
	// Strava's tie order is unspecified: keep the already known ID first.
	// The one-summary hourly probe is allowed to miss the newly uploaded
	// equal-time ID until the next daily recent-window reconciliation.
	api.pages[1] = []activity{activities[0], added}
	syncReset(api)
	syncRun(t, f, now.Add(time.Hour))
	if len(api.queries) != 1 || len(api.order) != 0 {
		t.Errorf("unchanged equal-time hourly probe was not cheap: %v, details %v", api.queries, api.order)
	}
	parent := coreChild(t, f.rc.RootNode(), "activities")
	if parent.Attr("camliPath:77") != "" {
		t.Fatal("fixture unexpectedly exposed the new ID to the one-summary probe")
	}
	syncReset(api)
	syncRun(t, f, now.Add(24*time.Hour))
	if !slices.Contains(api.order, added.ID) {
		t.Fatalf("daily reconciliation failed to recover same-second upload: %v", api.order)
	}
	coreAttrs(t, f.activity(t, added.ID), map[string]string{attrImportVersion: "2", attrSummaryHash: summaryHash(&added)})
}

func TestSyncShortNonfinalPagesDoNotCompleteBackfill(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 5, now.Add(-30*24*time.Hour))
	api.dateFiltered = false
	api.pages = make(map[int][]activity)
	for i, a := range activities {
		api.pages[i+1] = []activity{a}
	}
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities))
	syncWantProgress(t, f, activities[4], true)
	if api.calls["/athlete/activities?page=6"] == 0 {
		t.Error("short nonfinal discovery page was treated as EOF")
	}
}

func TestSyncEmptyBackfillCanDiscoverFirstActivity(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	f := newCoreFixture(t, api)
	f.configure(t)
	syncRun(t, f, now)
	cp := syncProgressAt(t, f)
	if !cp.backfillDone || !cp.through.IsZero() {
		t.Fatalf("empty backfill checkpoint = %+v", cp)
	}
	if _, err := readImportProgress(f.rc.AccountNode()); err != nil {
		t.Fatalf("empty checkpoint cannot be reloaded: %v", err)
	}
	activities := syncActivities(t, api, 1, now.Add(30*time.Minute))
	syncReset(api)
	syncRun(t, f, now.Add(time.Hour))
	syncWantHeavy(t, api, syncIDs(activities))
	syncWantProgress(t, f, activities[0], true)
}

func TestSyncDetailDateMismatchDoesNotAdvanceCheckpoint(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 2, now.Add(-30*24*time.Hour))
	wrong := activities[1]
	wrong.StartDate = wrong.StartDate.Add(time.Hour)
	api.details[wrong.ID] = coreJSON(t, wrong)
	f := newCoreFixture(t, api)
	f.configure(t)
	err := (imp{}).runAt(f.rc, func() time.Time { return now })
	if err == nil || !strings.Contains(err.Error(), "start date differs") {
		t.Fatalf("detail timestamp mismatch error = %v", err)
	}
	syncWantProgress(t, f, activities[0], false)
	if api.calls[fmt.Sprintf("/activities/%d/streams", wrong.ID)] != 0 {
		t.Fatal("mismatched detail timestamp reached streams request")
	}
	obj := f.activity(t, wrong.ID)
	coreAttrs(t, obj, map[string]string{attrImportVersion: "", attrSummaryHash: "", nodeattr.Type: ""})
	api.details[wrong.ID] = coreJSON(t, activities[1])
	syncReset(api)
	syncRun(t, f, now)
	syncWantHeavy(t, api, syncIDs(activities[1:]))
	syncWantProgress(t, f, activities[1], true)
}

func TestSyncCountAndElapsedCheckpointWaitForCompleteTimestampGroup(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "")
	api := newCoreAPI(t)
	now := syncTestTime()
	activities := syncActivities(t, api, 53, now.Add(-30*24*time.Hour))
	tie := activities[0].StartDate.Add(123 * time.Nanosecond)
	for i := 0; i < 52; i++ {
		activities[i].StartDate = tie
		api.details[activities[i].ID] = coreJSON(t, activities[i])
	}
	syncPages(api, activities)
	f := newCoreFixture(t, api)
	f.configure(t)
	var seen int
	api.before = func(req *http.Request) error {
		path := strings.TrimPrefix(req.URL.Path, "/api/v3/activities/")
		if id, err := strconv.ParseInt(path, 10, 64); err == nil && id != activities[52].ID {
			seen++
			if p := syncProgressAt(t, f); !p.through.IsZero() {
				t.Errorf("checkpoint advanced inside exact-time group after %d details: %+v", seen, p)
			}
			if seen == 2 {
				now = now.Add(31 * time.Second)
			}
		}
		if req.URL.Path == "/api/v3/activities/"+strconv.FormatInt(activities[52].ID, 10) {
			if p := syncProgressAt(t, f); !p.through.Equal(tie) || p.backfillDone {
				t.Errorf("whole-group checkpoint before newer activity = %+v; want %s", p, tie.Format(time.RFC3339Nano))
			}
			return errors.New("injected next group failure")
		}
		return nil
	}
	if err := (imp{}).runAt(f.rc, func() time.Time { return now }); err == nil {
		t.Fatal("injected newer activity failure succeeded")
	}
	if seen != 52 {
		t.Fatalf("completed tie details=%d;want 52", seen)
	}
	syncWantProgress(t, f, activities[0], false)
	if got := len(progressAccountClaims(t, f)); got != 1 {
		t.Errorf("exact-time group produced %d progress claims; want one", got)
	}
}
