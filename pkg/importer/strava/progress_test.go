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
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
)

func progressActivity(t *testing.T, api *coreAPI, id int64, start time.Time) activity {
	t.Helper()
	a := coreActivity(id)
	a.StartDate = start
	api.details[id] = coreJSON(t, a)
	return a
}

func progressDetailPath(id int64) string { return fmt.Sprintf("/activities/%d", id) }

func progressAssertAfter(t *testing.T, req *http.Request, boundary time.Time) {
	t.Helper()
	q := req.URL.Query()
	if boundary.IsZero() {
		if q.Has("after") {
			t.Errorf("initial/empty discovery has after=%q", q.Get("after"))
		}
	} else if got, want := q.Get("after"), strconv.FormatInt(boundary.Unix()-1, 10); got != want {
		t.Errorf("after = %q; want %q for inclusive boundary %s", got, want, boundary.Format(time.RFC3339Nano))
	}
}

// Emulate Strava's strict whole-second after filter before paginating. The
// requested per_page remains 100; short pages must not terminate discovery.
func progressFilteredPage(t *testing.T, req *http.Request, history []activity, pageSize int) []activity {
	t.Helper()
	var selected []activity
	var after time.Time
	if raw := req.URL.Query().Get("after"); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		after = time.Unix(seconds, 0)
	}
	for _, a := range history {
		if after.IsZero() || a.StartDate.After(after) {
			selected = append(selected, a)
		}
	}
	page, err := strconv.Atoi(req.URL.Query().Get("page"))
	if err != nil || page < 1 {
		t.Fatalf("invalid page %q", req.URL.RawQuery)
	}
	first := (page - 1) * pageSize
	if first >= len(selected) {
		return []activity{}
	}
	last := first + pageSize
	if last > len(selected) {
		last = len(selected)
	}
	return selected[first:last]
}

// Check the authoritative stored attributes as well as Object's in-memory
// view. In particular, failed multi-claims must not mutate either view.
func progressAssertAttrs(t *testing.T, f *coreFixture, want map[string]string) {
	t.Helper()
	acct := f.rc.AccountNode()
	stored := f.activityState(acct.PermanodeRef())
	for _, key := range []string{attrImportThrough, attrImportThroughVersion, attrBackfillCompletedVersion, importer.AcctAttrCompletedVersion} {
		var values []string
		if value := want[key]; value != "" {
			values = []string{value}
		}
		if got := acct.Attrs(key); !reflect.DeepEqual(got, values) {
			t.Errorf("cached %s = %v; want %v", key, got, values)
		}
		if got := stored[key]; !reflect.DeepEqual(got, values) {
			t.Errorf("stored %s = %v; want %v", key, got, values)
		}
	}
}

func progressWant(through time.Time, done bool) map[string]string {
	want := map[string]string{attrImportThroughVersion: runCompleteVersion}
	if !through.IsZero() {
		want[attrImportThrough] = through.UTC().Format(time.RFC3339Nano)
	}
	if done {
		want[attrBackfillCompletedVersion] = runCompleteVersion
		want[importer.AcctAttrCompletedVersion] = runCompleteVersion
	}
	return want
}

func progressAccountClaims(t *testing.T, f *coreFixture) []schema.Claim {
	t.Helper()
	var found []schema.Claim
	for _, claim := range f.activityClaims(t, f.rc.AccountNode().PermanodeRef()) {
		ops, err := claim.AttributeClaims()
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range ops {
			if op.Attribute == attrImportThrough || op.Attribute == attrImportThroughVersion || op.Attribute == attrBackfillCompletedVersion {
				found = append(found, claim)
				break
			}
		}
	}
	return found
}

func TestProgressDiscoversEverythingBeforeOldestFirstPopulation(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	base := coreActivity(1).StartDate
	old := progressActivity(t, api, 40, base.Add(100*time.Nanosecond))
	tieLow := progressActivity(t, api, 2, base.Add(200*time.Nanosecond))
	tieHigh := progressActivity(t, api, 9, tieLow.StartDate)
	later := progressActivity(t, api, 1, base.Add(300*time.Nanosecond))
	newest := progressActivity(t, api, 3, base.Add(400*time.Nanosecond))
	// Neither pages nor IDs encode chronological order. The tie is split
	// across pages, and an identical shifted-page duplicate is harmless.
	api.pages[1] = []activity{newest, tieHigh}
	api.pages[2] = []activity{later, old}
	api.pages[3] = []activity{tieLow, newest}
	var events []string
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(req.URL.Path, "/api/v3")
		if path == "/athlete/activities" {
			progressAssertAfter(t, req, time.Time{})
			events = append(events, "list:"+req.URL.Query().Get("page"))
		} else {
			events = append(events, path)
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	want := []string{"list:1", "list:2", "list:3", "list:4"}
	for _, a := range []activity{old, tieLow, tieHigh, later, newest} {
		path := progressDetailPath(a.ID)
		want = append(want, path, path+"/streams")
		coreAttrs(t, f.activity(t, a.ID), map[string]string{attrImportVersion: runCompleteVersion, attrSummaryHash: summaryHash(&a)})
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("request order = %v; want %v", events, want)
	}
	progressAssertAttrs(t, f, progressWant(newest.StartDate, true))
}

func TestProgressFailureFlushesOlderGroupsAndResumeIsInclusive(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	base := coreActivity(1).StartDate
	var history []activity
	for id := int64(4); id >= 1; id-- {
		history = append(history, progressActivity(t, api, id, base.Add(time.Duration(id)*24*time.Hour)))
	}
	boundary := time.Time{}
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/athlete/activities" {
			progressAssertAfter(t, req, boundary)
			page, _ := strconv.Atoi(req.URL.Query().Get("page"))
			api.pages[page] = progressFilteredPage(t, req, history, 2)
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	api.failures[progressDetailPath(3)] = http.StatusServiceUnavailable
	if err := (imp{}).Run(f.rc); err == nil || !strings.Contains(err.Error(), "activity 3") {
		t.Fatalf("Run error = %v; want activity 3 failure", err)
	}
	boundary = base.Add(2 * 24 * time.Hour)
	progressAssertAttrs(t, f, progressWant(boundary, false))
	if api.calls[progressDetailPath(4)] != 0 {
		t.Fatal("import continued after failure")
	}
	// The durable checkpoint can be reconstructed without any scratch data.
	reloaded, err := f.rc.Host.ObjectFromRef(f.rc.AccountNode().PermanodeRef())
	if err != nil {
		t.Fatal(err)
	}
	p, err := readImportProgress(reloaded)
	if err != nil || !p.through.Equal(boundary) || p.backfillDone {
		t.Fatalf("reloaded progress = %+v, %v", p, err)
	}
	delete(api.failures, progressDetailPath(3))
	api.calls = make(map[string]int)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	// The server excludes older history, but retains the exact watermark;
	// its already-complete activity marker avoids another detail request.
	if api.calls["/athlete/activities?page=1"] != 1 || api.calls["/athlete/activities?page=2"] != 1 || api.calls["/athlete/activities?page=3"] != 1 || api.calls["/athlete/activities?page=4"] != 0 {
		t.Fatalf("resume listing calls = %v; want bounded range plus empty page", api.calls)
	}
	for _, id := range []int64{1, 2} {
		if api.calls[progressDetailPath(id)] != 0 {
			t.Errorf("resume refetched older/completed activity %d", id)
		}
	}
	for _, id := range []int64{3, 4} {
		if api.calls[progressDetailPath(id)] != 1 {
			t.Errorf("resume details %d = %d; want 1", id, api.calls[progressDetailPath(id)])
		}
	}
	progressAssertAttrs(t, f, progressWant(base.Add(4*24*time.Hour), true))
}

func TestProgressFailedTimestampGroupAcrossPagesDoesNotAdvance(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	base := coreActivity(1).StartDate
	old := progressActivity(t, api, 1, base)
	tie := base.Add(time.Nanosecond)
	a2 := progressActivity(t, api, 2, tie)
	a3 := progressActivity(t, api, 3, tie)
	a4 := progressActivity(t, api, 4, tie)
	newest := progressActivity(t, api, 5, base.Add(2*time.Nanosecond))
	api.pages[1] = []activity{a4, newest}
	api.pages[2] = []activity{a2, old}
	api.pages[3] = []activity{a3}
	boundary := time.Time{}
	fail := true
	var f *coreFixture
	f = newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/athlete/activities" {
			progressAssertAfter(t, req, boundary)
		}
		if fail && req.URL.Path == "/api/v3/activities/4/streams" {
			f.storage.failNextClaim(attrImportVersion)
			fail = false
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	if err := (imp{}).Run(f.rc); !errors.Is(err, errCoreClaimFailure) || !strings.Contains(err.Error(), "activity 4") {
		t.Fatalf("Run error = %v; want failed activity claim cause", err)
	}
	progressAssertAttrs(t, f, progressWant(old.StartDate, false))
	for _, id := range []int64{2, 3} {
		coreAttrs(t, f.activity(t, id), map[string]string{attrImportVersion: runCompleteVersion})
	}
	coreAttrs(t, f.activity(t, 4), map[string]string{attrImportVersion: "", attrSummaryHash: ""})
	if api.calls[progressDetailPath(5)] != 0 {
		t.Fatal("newer timestamp imported after failed group")
	}
	boundary = old.StartDate
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 5; id++ {
		want := 1
		if id == 4 {
			want = 2
		}
		if got := api.calls[progressDetailPath(id)]; got != want {
			t.Errorf("details %d = %d; want %d (safe tie retry)", id, got, want)
		}
	}
	progressAssertAttrs(t, f, progressWant(newest.StartDate, true))
}

func TestProgressDiscoveryFailureCannotPublishActivitiesOrProgress(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	listingFailure := errors.New("injected second-page discovery failure")
	for _, failure := range []string{"transport", "http429", "local-quota", "conflicting-duplicate", "zero-date", "zero-id", "negative-id"} {
		t.Run(failure, func(t *testing.T) {
			api := newCoreAPI(t)
			high := coreActivity(1).StartDate
			a := progressActivity(t, api, 1, high.Add(time.Hour))
			api.pages[1] = []activity{a}
			f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/api/v3/athlete/activities" && req.URL.Query().Get("page") == "2" {
					switch failure {
					case "transport":
						return nil, listingFailure
					case "http429":
						return coreResponse(req, http.StatusTooManyRequests, []byte(`{"message":"rate limit"}`)), nil
					case "conflicting-duplicate":
						changed := a
						changed.Name += " changed while paging"
						api.pages[2] = []activity{changed}
					case "zero-date", "zero-id", "negative-id":
						invalid := a
						invalid.ID = 2
						if failure == "zero-date" {
							invalid.StartDate = time.Time{}
						} else if failure == "zero-id" {
							invalid.ID = 0
						} else {
							invalid.ID = -1
						}
						api.pages[2] = []activity{invalid}
					}
				}
				res, err := api.RoundTrip(req)
				if failure == "local-quota" && err == nil {
					res.Header.Set("X-ReadRateLimit-Limit", "1,1000")
					res.Header.Set("X-ReadRateLimit-Usage", "1,1")
				}
				return res, err
			}))
			f.configure(t)
			// Discovery failures must preserve an already valid steady-state
			// checkpoint too, not merely leave a fresh account blank.
			if err := f.rc.AccountNode().SetAttrs(attrImportThrough, high.Format(time.RFC3339Nano), attrImportThroughVersion, runCompleteVersion,
				attrBackfillCompletedVersion, runCompleteVersion, importer.AcctAttrCompletedVersion, runCompleteVersion); err != nil {
				t.Fatal(err)
			}
			before := f.activityState(f.rc.AccountNode().PermanodeRef())
			err := (imp{}).Run(f.rc)
			if err == nil {
				t.Fatal("incomplete/invalid discovery succeeded")
			}
			if failure == "transport" && !errors.Is(err, listingFailure) {
				t.Errorf("Run error = %v; want listing failure cause", err)
			}
			if failure == "local-quota" {
				var quota *rateLimitError
				if !errors.As(err, &quota) {
					t.Errorf("Run error = %v; want quota error", err)
				}
				if api.calls["/athlete/activities?page=2"] != 0 {
					t.Error("exhausted local quota still requested page 2")
				}
			}
			if failure == "conflicting-duplicate" && !strings.Contains(err.Error(), "changed during discovery") {
				t.Errorf("Run error = %v; want conflicting duplicate error", err)
			}
			if got := f.activityState(f.rc.AccountNode().PermanodeRef()); !reflect.DeepEqual(got, before) {
				t.Errorf("discovery mutated account: got %v; want %v", got, before)
			}
			progressAssertAttrs(t, f, progressWant(high, true))
			parent := coreChild(t, f.rc.RootNode(), "activities")
			if got := parent.Attr("camliPath:1"); got != "" {
				t.Errorf("discovery published activity path %q", got)
			}
			for path, count := range api.calls {
				if strings.HasPrefix(path, "/activities/") && count != 0 {
					t.Errorf("details/streams requested during incomplete discovery: %s (%d)", path, count)
				}
			}
		})
	}
}

func TestProgressExactResumeAndSevenDayOverlapBoundaries(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	for _, done := range []bool{false, true} {
		t.Run(fmt.Sprintf("backfill-done=%t", done), func(t *testing.T) {
			api := newCoreAPI(t)
			high := coreActivity(1).StartDate.Add(500123456 * time.Nanosecond)
			boundary := high
			if done {
				boundary = high.Add(-7 * 24 * time.Hour)
			}
			justBefore := progressActivity(t, api, 1, boundary.Add(-time.Nanosecond))
			exact := progressActivity(t, api, 2, boundary)
			fractional := progressActivity(t, api, 3, boundary.Add(time.Nanosecond))
			newest := progressActivity(t, api, 4, high.Add(time.Hour))
			// The first whole-second-filtered page is below the exact local
			// boundary. Later pages contain older and newer eligible activities.
			history := []activity{justBefore, newest, fractional, exact}
			f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/api/v3/athlete/activities" {
					progressAssertAfter(t, req, boundary)
					page, _ := strconv.Atoi(req.URL.Query().Get("page"))
					api.pages[page] = progressFilteredPage(t, req, history, 1)
				}
				return api.RoundTrip(req)
			}))
			f.configure(t)
			sets := []string{attrImportThrough, high.Format(time.RFC3339Nano), attrImportThroughVersion, runCompleteVersion}
			if done {
				sets = append(sets, attrBackfillCompletedVersion, runCompleteVersion, importer.AcctAttrCompletedVersion, runCompleteVersion)
			}
			if err := f.rc.AccountNode().SetAttrs(sets...); err != nil {
				t.Fatal(err)
			}
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			if api.calls["/athlete/activities?page=5"] != 1 {
				t.Errorf("discovery did not continue past below-boundary and short pages: %v", api.calls)
			}
			if api.calls[progressDetailPath(justBefore.ID)] != 0 {
				t.Error("activity just below fractional boundary imported")
			}
			for _, a := range []activity{exact, fractional, newest} {
				if api.calls[progressDetailPath(a.ID)] != 1 {
					t.Errorf("boundary-selected activity %d fetched %d times; want 1", a.ID, api.calls[progressDetailPath(a.ID)])
				}
			}
			progressAssertAttrs(t, f, progressWant(newest.StartDate, true))
		})
	}
}

func TestProgressSteadyOverlapFindsEditsAndBackdatedActivities(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	high := coreActivity(1).StartDate
	old := progressActivity(t, api, 1, high.Add(-8*24*time.Hour))
	recent := progressActivity(t, api, 2, high.Add(-24*time.Hour))
	newest := progressActivity(t, api, 3, high)
	history := []activity{newest, old, recent}
	boundary := time.Time{}
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/athlete/activities" {
			progressAssertAfter(t, req, boundary)
			page, _ := strconv.Atoi(req.URL.Query().Get("page"))
			api.pages[page] = progressFilteredPage(t, req, history, 2)
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	beforeRecent := f.activity(t, recent.ID).PermanodeRef()
	old.Name += " outside overlap"
	recent.Name += " edited within overlap"
	api.details[old.ID] = coreJSON(t, old)
	api.details[recent.ID] = coreJSON(t, recent)
	backdated := progressActivity(t, api, 4, high.Add(-7*24*time.Hour))
	tooOld := progressActivity(t, api, 5, high.Add(-7*24*time.Hour-time.Nanosecond))
	history = []activity{tooOld, recent, old, newest, backdated}
	boundary = high.Add(-7 * 24 * time.Hour)
	api.calls = make(map[string]int)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{old.ID, newest.ID, tooOld.ID} {
		if api.calls[progressDetailPath(id)] != 0 {
			t.Errorf("outside-window or unchanged activity %d refetched", id)
		}
	}
	for _, id := range []int64{recent.ID, backdated.ID} {
		if api.calls[progressDetailPath(id)] != 1 {
			t.Errorf("eligible changed/new activity %d not imported once", id)
		}
	}
	if got := f.activity(t, recent.ID).PermanodeRef(); got != beforeRecent {
		t.Errorf("edited activity moved from %v to %v", beforeRecent, got)
	}
	coreAttrs(t, f.activity(t, recent.ID), map[string]string{attrSummaryHash: summaryHash(&recent)})
	// Reconciliation of older groups must never lower the durable watermark.
	progressAssertAttrs(t, f, progressWant(high, true))
}

func TestProgressRejectsMalformedMetadataBeforeNetwork(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	high := coreActivity(1).StartDate.Format(time.RFC3339Nano)
	type invalidProgress struct {
		name  string
		attrs url.Values
		key   string
	}
	cases := []invalidProgress{
		{"bad-time", url.Values{attrImportThrough: {"yesterday"}, attrImportThroughVersion: {runCompleteVersion}}, attrImportThrough},
		{"zero-time", url.Values{attrImportThrough: {time.Time{}.Format(time.RFC3339Nano)}, attrImportThroughVersion: {runCompleteVersion}}, attrImportThrough},
		{"missing-watermark-binding", url.Values{attrImportThrough: {high}}, attrImportThroughVersion},
		{"missing-done-binding", url.Values{attrBackfillCompletedVersion: {runCompleteVersion}}, attrImportThroughVersion},
		{"unknown-binding-with-multivalued-done", url.Values{attrImportThroughVersion: {"future"}, attrBackfillCompletedVersion: {"1", "2"}}, attrBackfillCompletedVersion},
		{"unknown-done-with-empty-binding", url.Values{attrImportThroughVersion: {""}, attrBackfillCompletedVersion: {"99"}}, attrImportThroughVersion},
	}
	for _, key := range []string{attrImportThrough, attrImportThroughVersion, attrBackfillCompletedVersion} {
		cases = append(cases,
			invalidProgress{"empty-" + key, url.Values{key: {""}}, key},
			invalidProgress{"multivalued-" + key, url.Values{key: {"first", "second"}}, key},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCoreFixture(t, nil)
			f.configure(t)
			for key, values := range tc.attrs {
				if err := f.rc.AccountNode().SetAttrValues(key, values); err != nil {
					t.Fatal(err)
				}
			}
			before := f.storage.NumBlobs()
			if err := (imp{}).Run(f.rc); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Errorf("Run error = %v; want validation identifying %s", err, tc.key)
			}
			if got := f.storage.NumBlobs(); got != before {
				t.Errorf("invalid progress caused %d blob writes", got-before)
			}
		})
	}
}

func TestProgressUnknownVersionsRequireFullReconciliation(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	for _, tc := range []struct {
		name    string
		version string
		done    string
		through []string
	}{
		{"old-binding", "1", runCompleteVersion, nil},
		{"old-done", runCompleteVersion, "1", nil},
		{"unknown-done-without-binding", "", "99", nil},
		{"unknown-binding", "future", "", nil},
		{"opaque-time-with-unknown-binding", "future", "", []string{"opaque timestamp in a future schema"}},
		{"opaque-time-with-unknown-done", runCompleteVersion, "99", []string{"future timestamp payload"}},
		{"multivalued-opaque-watermark-matches-first-group", "future", runCompleteVersion, []string{coreActivity(1).StartDate.Format(time.RFC3339Nano), "opaque extra value"}},
		{"legacy-generic-completion-only", "", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newCoreAPI(t)
			base := coreActivity(1).StartDate
			older := progressActivity(t, api, 1, base)
			newer := progressActivity(t, api, 2, base.Add(time.Hour))
			api.pages[1] = []activity{newer, older}
			boundary := time.Time{}
			f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/api/v3/athlete/activities" {
					progressAssertAfter(t, req, boundary)
				}
				return api.RoundTrip(req)
			}))
			f.configure(t)
			sets := []string{importer.AcctAttrCompletedVersion, runCompleteVersion}
			if tc.version != "" {
				sets = append(sets, attrImportThroughVersion, tc.version)
			}
			if tc.done != "" {
				sets = append(sets, attrBackfillCompletedVersion, tc.done)
			}
			if tc.version != "" || tc.done != "" {
				// A newer obsolete checkpoint must not hide either activity.
				through := tc.through
				if through == nil {
					through = []string{base.Add(365 * 24 * time.Hour).Format(time.RFC3339Nano)}
				}
				if err := f.rc.AccountNode().SetAttrValues(attrImportThrough, through); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.rc.AccountNode().SetAttrs(sets...); err != nil {
				t.Fatal(err)
			}
			api.failures[progressDetailPath(newer.ID)] = http.StatusServiceUnavailable
			if err := (imp{}).Run(f.rc); err == nil {
				t.Fatal("injected import failure succeeded")
			}
			want := progressWant(older.StartDate, false)
			want[importer.AcctAttrCompletedVersion] = runCompleteVersion
			progressAssertAttrs(t, f, want)
			if api.calls[progressDetailPath(older.ID)] != 1 {
				t.Fatal("unknown/legacy progress skipped old history")
			}
			// The failed reconciliation establishes a current partial prefix,
			// not a current done flag derived from the old generic completion.
			delete(api.failures, progressDetailPath(newer.ID))
			boundary = older.StartDate
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			if api.calls[progressDetailPath(older.ID)] != 1 {
				t.Error("reconciliation retry refetched completed activity")
			}
			progressAssertAttrs(t, f, progressWant(newer.StartDate, true))
		})
	}
}

func TestProgressCappedCompletionDoesNotFinishPartialBackfill(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "1")
	api := newCoreAPI(t)
	api.perPage = 1
	base := coreActivity(1).StartDate
	old := progressActivity(t, api, 1, base)
	middle := progressActivity(t, api, 2, base.Add(time.Hour))
	newest := progressActivity(t, api, 3, base.Add(2*time.Hour))
	api.pages[1] = []activity{newest}
	boundary := time.Time{}
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/athlete/activities" {
			progressAssertAfter(t, req, boundary)
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	if err := f.rc.AccountNode().SetAttrs(attrImportThrough, old.StartDate.Format(time.RFC3339Nano), attrImportThroughVersion, runCompleteVersion); err != nil {
		t.Fatal(err)
	}
	before := len(progressAccountClaims(t, f))
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	want := progressWant(old.StartDate, false)
	want[importer.AcctAttrCompletedVersion] = runCompleteVersion
	progressAssertAttrs(t, f, want)
	if got := len(progressAccountClaims(t, f)); got != before {
		t.Errorf("capped run wrote %d chronological checkpoint claims", got-before)
	}
	if api.calls["/athlete/activities?page=2"] != 0 {
		t.Fatal("cap unnecessarily probed another page")
	}
	// A generic current version and a newly marked newest activity cannot
	// suppress the older unfinished prefix once the testing cap is removed.
	t.Setenv(maxActivitiesEnv, "")
	api.perPage = 0
	api.pages[1] = []activity{newest, middle, old}
	boundary = old.StartDate
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	for _, a := range []activity{old, middle, newest} {
		if got := api.calls[progressDetailPath(a.ID)]; got != 1 {
			t.Errorf("detail requests %d = %d; want 1", a.ID, got)
		}
	}
	progressAssertAttrs(t, f, progressWant(newest.StartDate, true))
}

func TestProgressCappedModeBypassesChronologicalMetadata(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "1")
	api := newCoreAPI(t)
	api.perPage = 1
	a := progressActivity(t, api, 1, coreActivity(1).StartDate)
	api.pages[1] = []activity{a}
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/athlete/activities" {
			progressAssertAfter(t, req, time.Time{})
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	if err := f.rc.AccountNode().SetAttrs(attrImportThrough, "not-a-time", attrBackfillCompletedVersion, "future"); err != nil {
		t.Fatal(err)
	}
	if err := f.rc.AccountNode().SetAttrValues(attrImportThroughVersion, []string{"1", "future"}); err != nil {
		t.Fatal(err)
	}
	before := f.activityState(f.rc.AccountNode().PermanodeRef())
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	after := f.activityState(f.rc.AccountNode().PermanodeRef())
	for _, key := range []string{attrImportThrough, attrImportThroughVersion, attrBackfillCompletedVersion} {
		if !reflect.DeepEqual(after[key], before[key]) || !reflect.DeepEqual(f.rc.AccountNode().Attrs(key), before[key]) {
			t.Errorf("capped run rewrote %s", key)
		}
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: runCompleteVersion})
}

func TestProgressEmptyHistoryHasNoInventedWatermark(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/athlete/activities" {
			progressAssertAfter(t, req, time.Time{})
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	progressAssertAttrs(t, f, progressWant(time.Time{}, true))
	before := f.storage.NumBlobs()
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	if got := f.storage.NumBlobs(); got != before {
		t.Errorf("unchanged empty-history poll wrote %d blobs", got-before)
	}
	// A newly uploaded old activity is discoverable: empty completion must
	// not manufacture a wall-clock watermark that would exclude it.
	a := progressActivity(t, api, 1, coreActivity(1).StartDate.Add(-365*24*time.Hour))
	api.pages[1] = []activity{a}
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	if api.calls[progressDetailPath(a.ID)] != 1 {
		t.Fatal("new activity after empty completion was skipped")
	}
	progressAssertAttrs(t, f, progressWant(a.StartDate, true))
}

func TestProgressFinalCheckpointFailureIsAtomicAndRetryable(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	for _, key := range []string{attrImportThrough, attrImportThroughVersion, attrBackfillCompletedVersion, importer.AcctAttrCompletedVersion} {
		t.Run(key, func(t *testing.T) {
			api := newCoreAPI(t)
			a := progressActivity(t, api, 1, coreActivity(1).StartDate)
			api.pages[1] = []activity{a}
			f := newCoreFixture(t, api)
			f.configure(t)
			old := map[string]string{
				attrImportThrough:                 a.StartDate.Add(24 * time.Hour).Format(time.RFC3339Nano),
				attrImportThroughVersion:          "1",
				attrBackfillCompletedVersion:      "1",
				importer.AcctAttrCompletedVersion: "1",
			}
			for key, value := range old {
				if err := f.rc.AccountNode().SetAttr(key, value); err != nil {
					t.Fatal(err)
				}
			}
			before := f.activityState(f.rc.AccountNode().PermanodeRef())
			claimCount := len(progressAccountClaims(t, f))
			f.storage.failNextClaim(key)
			if err := (imp{}).Run(f.rc); !errors.Is(err, errCoreClaimFailure) || !strings.Contains(err.Error(), "saving chronological progress") {
				t.Fatalf("Run error = %v; want checkpoint claim failure", err)
			}
			progressAssertAttrs(t, f, old)
			if got := f.activityState(f.rc.AccountNode().PermanodeRef()); !reflect.DeepEqual(got, before) {
				t.Errorf("failed checkpoint partially changed account: %v; want %v", got, before)
			}
			if got := len(progressAccountClaims(t, f)); got != claimCount {
				t.Errorf("failed final checkpoint was retried/published: %d new claims", got-claimCount)
			}
			coreAttrs(t, f.activity(t, a.ID), map[string]string{attrImportVersion: runCompleteVersion})
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			if got := api.calls[progressDetailPath(a.ID)]; got != 1 {
				t.Errorf("checkpoint retry refetched already committed activity %d times", got)
			}
			progressAssertAttrs(t, f, progressWant(a.StartDate, true))
		})
	}
}

func TestProgressFailureJoinsImportAndCheckpointCauses(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	base := coreActivity(1).StartDate
	older := progressActivity(t, api, 1, base)
	newer := progressActivity(t, api, 2, base.Add(time.Hour))
	api.pages[1] = []activity{newer, older}
	importFailure := errors.New("injected newer detail failure")
	fail := true
	var f *coreFixture
	f = newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if fail && req.URL.Path == "/api/v3/activities/2" {
			f.storage.failNextClaim(attrImportThroughVersion)
			return nil, importFailure
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	old := map[string]string{
		attrImportThrough:                 base.Add(24 * time.Hour).Format(time.RFC3339Nano),
		attrImportThroughVersion:          "1",
		attrBackfillCompletedVersion:      "1",
		importer.AcctAttrCompletedVersion: "1",
	}
	for key, value := range old {
		if err := f.rc.AccountNode().SetAttr(key, value); err != nil {
			t.Fatal(err)
		}
	}
	claimCount := len(progressAccountClaims(t, f))
	err := (imp{}).Run(f.rc)
	if !errors.Is(err, importFailure) || !errors.Is(err, errCoreClaimFailure) {
		t.Fatalf("Run error = %v; want joined import and failed checkpoint causes", err)
	}
	// The attempted deletion of the obsolete done flag shares the failed
	// envelope with the new binding and watermark; none may take effect.
	progressAssertAttrs(t, f, old)
	if got := len(progressAccountClaims(t, f)); got != claimCount {
		t.Errorf("failed graceful checkpoint produced %d claims", got-claimCount)
	}
	fail = false
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	if got := api.calls[progressDetailPath(older.ID)]; got != 1 {
		t.Errorf("failed checkpoint retry refetched older activity %d times", got)
	}
	progressAssertAttrs(t, f, progressWant(newer.StartDate, true))
}

func TestProgressCancellationFlushesOnlyCompletedGroups(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	base := coreActivity(1).StartDate
	for id := int64(4); id >= 1; id-- {
		api.pages[1] = append(api.pages[1], progressActivity(t, api, id, base.Add(time.Duration(id)*time.Hour)))
	}
	var f *coreFixture
	f = newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/activities/3" {
			// The fixture's RunContext has no exported cancel method; closing
			// its host cancels the parent context used by every API request.
			if err := f.rc.Host.Close(); err != nil {
				t.Fatal(err)
			}
			return nil, req.Context().Err()
		}
		return api.RoundTrip(req)
	}))
	f.configure(t)
	if err := (imp{}).Run(f.rc); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v; want context cancellation", err)
	}
	if err := f.rc.Context().Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("run context = %v; want genuinely canceled context", err)
	}
	progressAssertAttrs(t, f, progressWant(base.Add(2*time.Hour), false))
	if api.calls[progressDetailPath(4)] != 0 {
		t.Error("canceled run imported a newer group")
	}
	coreAttrs(t, f.activity(t, 3), map[string]string{attrImportVersion: "", attrSummaryHash: ""})
}

func TestProgressCheckpointsAreBatchedAcrossWholeGroupsAndPollWritesNothing(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	base := coreActivity(1).StartDate
	tie := base.Add(100 * time.Second)
	latest := base.Add(103 * time.Second)
	for id := int64(103); id >= 1; id-- {
		start := base.Add(time.Duration(id) * time.Second)
		if id >= 100 && id <= 102 {
			start = tie
		}
		page := int((103-id)/50) + 1
		api.pages[page] = append(api.pages[page], progressActivity(t, api, id, start))
	}
	boundary := time.Time{}
	poll := false
	var f *coreFixture
	f = newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v3/athlete/activities" {
			progressAssertAfter(t, req, boundary)
		} else if !poll && strings.HasPrefix(req.URL.Path, "/api/v3/activities/") {
			parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/api/v3/activities/"), "/")
			id, _ := strconv.ParseInt(parts[0], 10, 64)
			want := ""
			if id == 103 {
				want = tie.Format(time.RFC3339Nano)
			}
			if got := f.rc.AccountNode().Attr(attrImportThrough); got != want {
				t.Errorf("watermark while importing %d = %q; want %q (whole groups, batch of 100)", id, got, want)
			}
		}
		res, err := api.RoundTrip(req)
		if err == nil {
			// Let this storage/checkpoint test import more than the default
			// request budget without testing quota behavior at the same time.
			res.Header.Set("X-RateLimit-Limit", "10000,10000")
			res.Header.Set("X-ReadRateLimit-Limit", "10000,10000")
		}
		return res, err
	}))
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	progressAssertAttrs(t, f, progressWant(latest, true))
	claims := progressAccountClaims(t, f)
	if len(claims) != 2 {
		t.Fatalf("103 successful activities produced %d account progress claims; want 2, not one per timestamp group", len(claims))
	}
	watermarks := make(map[string]int)
	doneClaims := 0
	for _, claim := range claims {
		if got := claim.ClaimType(); got != schema.MultiClaim {
			t.Errorf("checkpoint claim type = %q; want atomic multi claim", got)
		}
		ops, err := claim.AttributeClaims()
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range ops {
			if op.Attribute == attrImportThrough {
				watermarks[op.Value]++
			}
			if op.Attribute == attrBackfillCompletedVersion {
				doneClaims++
			}
		}
	}
	wantWatermarks := map[string]int{tie.Format(time.RFC3339Nano): 1, latest.Format(time.RFC3339Nano): 1}
	if !reflect.DeepEqual(watermarks, wantWatermarks) || doneClaims != 1 {
		t.Errorf("checkpoint watermarks = %v, done claims = %d; want %v, 1", watermarks, doneClaims, wantWatermarks)
	}
	// Scratch contains only the disposable app quota, never an activity
	// ledger or chronological cursor. Removing it cannot lose progress.
	cache := f.rateCache(t)
	it := cache.Find("", "")
	var keys []string
	for it.Next() {
		keys = append(keys, it.Key())
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{rateLimitCacheKey(f.clientID)}; !reflect.DeepEqual(keys, want) {
		t.Errorf("importer scratch keys = %v; want quota key only %v", keys, want)
	}
	accountCache, err := f.rc.AccountCache()
	if err != nil {
		t.Fatal(err)
	}
	it = accountCache.Find("", "")
	if it.Next() {
		t.Errorf("unexpected account scratch ledger key %q", it.Key())
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Delete(rateLimitCacheKey(f.clientID)); err != nil {
		t.Fatal(err)
	}
	poll = true
	boundary = latest.Add(-7 * 24 * time.Hour)
	api.calls = make(map[string]int)
	before := f.storage.NumBlobs()
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	if got := f.storage.NumBlobs(); got != before {
		t.Errorf("unchanged successful poll wrote %d blobs, including unnecessary account checkpoints", got-before)
	}
	for path, calls := range api.calls {
		if strings.HasPrefix(path, "/activities/") && calls != 0 {
			t.Errorf("unchanged poll refetched %s", path)
		}
	}
	progressAssertAttrs(t, f, progressWant(latest, true))
}

func TestProgressFailedBatchCheckpointStopsAndDoesNotRetryPublication(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	api := newCoreAPI(t)
	base := coreActivity(1).StartDate
	for id := int64(101); id >= 1; id-- {
		page := int((101-id)/100) + 1
		api.pages[page] = append(api.pages[page], progressActivity(t, api, id, base.Add(time.Duration(id)*time.Second)))
	}
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		res, err := api.RoundTrip(req)
		if err == nil {
			res.Header.Set("X-RateLimit-Limit", "10000,10000")
			res.Header.Set("X-ReadRateLimit-Limit", "10000,10000")
		}
		return res, err
	}))
	f.configure(t)
	f.storage.failNextClaim(attrImportThroughVersion)
	if err := (imp{}).Run(f.rc); !errors.Is(err, errCoreClaimFailure) {
		t.Fatalf("Run error = %v; want failed batch checkpoint cause", err)
	}
	progressAssertAttrs(t, f, nil)
	if got := len(progressAccountClaims(t, f)); got != 0 {
		t.Errorf("failed checkpoint was republished by deferred flush: %d claims", got)
	}
	if got := api.calls[progressDetailPath(101)]; got != 0 {
		t.Errorf("failed batch checkpoint continued into newer activity: %d calls", got)
	}
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 101; id++ {
		if got := api.calls[progressDetailPath(id)]; got != 1 {
			t.Errorf("activity %d detail calls = %d; want committed activities reused on retry", id, got)
		}
	}
	if got := len(progressAccountClaims(t, f)); got != 2 {
		t.Errorf("retry published %d checkpoints; want batch plus final", got)
	}
	progressAssertAttrs(t, f, progressWant(base.Add(101*time.Second), true))
}

func TestProgressQueuedRunCancelsBeforeActiveAccountGateIsReleased(t *testing.T) {
	t.Setenv(maxActivitiesEnv, "")
	f := newCoreFixture(t, nil) // Any HTTP request is a test failure.
	f.configure(t)
	high := coreActivity(1).StartDate
	if err := f.rc.AccountNode().SetAttrs(attrImportThrough, high.Format(time.RFC3339Nano), attrImportThroughVersion, runCompleteVersion); err != nil {
		t.Fatal(err)
	}
	gate := importRunGate(f.rc.AccountNode())
	<-gate // Simulate the active run without needing a second private RunContext.
	defer func() { gate <- struct{}{} }()
	before := f.storage.NumBlobs()
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		result <- (imp{}).Run(f.rc)
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("queued Run returned while active gate held: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	// Unlike releasing the gate and checking cancellation afterward, this
	// requires the actual top-level Run's wait to be context-cancellable.
	if err := f.rc.Host.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("queued Run error = %v; want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Run remained queued behind the active account gate")
	}
	if len(gate) != 0 {
		t.Error("canceled waiter released a gate token it never acquired")
	}
	if got := f.storage.NumBlobs(); got != before {
		t.Errorf("canceled queued Run wrote %d blobs", got-before)
	}
	progressAssertAttrs(t, f, progressWant(high, false))
}
