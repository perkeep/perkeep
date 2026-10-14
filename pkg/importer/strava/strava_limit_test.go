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
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go4.org/jsonconfig"

	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/sorted"
)

const coreMaxActivitiesEnv = "PERKEEP_STRAVA_MAX_ACTIVITIES"

// Return genuinely newest-first summaries, including details and streams so
// an accidental request for an older activity is visible in the call counts.
func coreLimitActivities(t *testing.T, api *coreAPI, count int) []activity {
	t.Helper()
	summaries := make([]activity, count)
	for i := range summaries {
		a := coreActivity(int64(1000 - i))
		a.StartDate = a.StartDate.Add(-time.Duration(i) * 24 * time.Hour)
		summaries[i] = a
		api.details[a.ID] = coreJSON(t, a)
		api.streams[a.ID] = []byte(coreTrackStreams)
	}
	return summaries
}

func coreLimitCalls(t *testing.T, api *coreAPI, want map[string]int) {
	t.Helper()
	if !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("HTTP calls = %v; want %v", api.calls, want)
	}
}

func coreLimitPaths(t *testing.T, f *coreFixture, summaries []activity, imported int) {
	t.Helper()
	parent := coreChild(t, f.rc.RootNode(), "activities")
	for i, a := range summaries {
		path := "camliPath:" + strconv.FormatInt(a.ID, 10)
		if i >= imported {
			if got := parent.Attr(path); got != "" {
				t.Errorf("older activity %d has a path %q outside the cap", a.ID, got)
			}
			continue
		}
		coreAttrs(t, f.activity(t, a.ID), map[string]string{
			attrImportVersion: runCompleteVersion, attrSummaryHash: summaryHash(&a),
		})
	}
}

func TestImporterActivityLimitNewestTenAndRepeat(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "10")
	api := newCoreAPI(t)
	api.perPage = 10
	// Deliberately over-deliver: the importer must inspect exactly the first
	// ten summaries even if the provider ignores per_page.
	summaries := coreLimitActivities(t, api, 15)
	api.pages[1] = summaries
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"/athlete/activities?page=1": 1}
	for _, a := range summaries[:10] {
		path := "/activities/" + strconv.FormatInt(a.ID, 10)
		want[path], want[path+"/streams"] = 1, 1
	}
	coreLimitCalls(t, api, want) // 1 listing + 10 details + 10 streams = 21
	coreLimitPaths(t, f, summaries, 10)
	f.permanodes(t, 3+10) // account, root, collection, not 30 attachment wrappers
	for _, a := range summaries[:10] {
		obj := f.activity(t, a.ID)
		coreAttrs(t, obj, map[string]string{nodeattr.DefaultVisibility: ""})
		if got := f.file(t, obj, attrActivityJSON, "activity.json"); !bytes.Equal(got, api.details[a.ID]) {
			t.Errorf("activity %d details file did not preserve raw JSON", a.ID)
		}
		if got := f.file(t, obj, attrStreamsJSON, "streams.json"); !bytes.Equal(got, api.streams[a.ID]) {
			t.Errorf("activity %d streams file did not preserve raw JSON", a.ID)
		}
		f.file(t, obj, nodeattr.FitnessTrack, "track.gpx")
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})

	// Completed, unchanged summaries still consume the cap. Restart at the
	// newest page rather than importing the next ten previously unseen IDs.
	api.calls = make(map[string]int)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	coreLimitCalls(t, api, map[string]int{"/athlete/activities?page=1": 1})
	coreLimitPaths(t, f, summaries, 10)
	f.permanodes(t, 3+10)
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
}

func TestImporterActivityLimitOne(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "1")
	api := newCoreAPI(t)
	api.perPage = 1
	summaries := coreLimitActivities(t, api, 2)
	api.pages[1] = summaries
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	coreLimitCalls(t, api, map[string]int{
		"/athlete/activities?page=1": 1, "/activities/1000": 1, "/activities/1000/streams": 1,
	})
	coreLimitPaths(t, f, summaries, 1)
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
}

func TestImporterActivityLimitRetainsOlderStoredNodes(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "1")
	api := newCoreAPI(t)
	api.perPage = 1
	summaries := coreLimitActivities(t, api, 2)
	api.pages[1] = summaries
	f := newCoreFixture(t, api)
	f.configure(t)
	parent, err := f.rc.RootNode().ChildPathObject("activities")
	if err != nil {
		t.Fatal(err)
	}
	older, err := parent.ChildPathObject("999")
	if err != nil {
		t.Fatal(err)
	}
	if err := older.SetAttrs(attrImportVersion, runCompleteVersion, attrSummaryHash, summaryHash(&summaries[1])); err != nil {
		t.Fatal(err)
	}
	ref := older.PermanodeRef()
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	coreLimitCalls(t, api, map[string]int{
		"/athlete/activities?page=1": 1, "/activities/1000": 1, "/activities/1000/streams": 1,
	})
	older = f.activity(t, 999)
	if older.PermanodeRef() != ref {
		t.Error("capped run replaced older stored activity")
	}
	coreAttrs(t, older, map[string]string{attrImportVersion: "2", attrSummaryHash: summaryHash(&summaries[1])})
}

func TestImporterActivityLimitFixedPageSize(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "3")
	api := newCoreAPI(t)
	api.perPage = 3
	summaries := coreLimitActivities(t, api, 4)
	// A nonempty short page does not end this API's pagination. The second
	// request must keep per_page=3, not change to the one remaining summary.
	api.pages[1], api.pages[2] = summaries[:2], summaries[2:]
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"/athlete/activities?page=1": 1, "/athlete/activities?page=2": 1}
	for _, a := range summaries[:3] {
		path := "/activities/" + strconv.FormatInt(a.ID, 10)
		want[path], want[path+"/streams"] = 1, 1
	}
	coreLimitCalls(t, api, want)
	coreLimitPaths(t, f, summaries, 3)
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
}

func TestImporterActivityLimitOverOneHundred(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "101")
	api := newCoreAPI(t) // per_page must stay 100 on both pages
	summaries := coreLimitActivities(t, api, 102)
	api.pages[1], api.pages[2] = summaries[:100], summaries[100:]
	f := newCoreFixture(t, api)
	f.configure(t)
	parent, err := f.rc.RootNode().ChildPathObject("activities")
	if err != nil {
		t.Fatal(err)
	}
	// Precomplete the in-window nodes without writing 101 sets of archived
	// files or spending the fallback REST quota on their details and streams.
	for _, a := range summaries[:101] {
		obj, err := parent.ChildPathObject(strconv.FormatInt(a.ID, 10))
		if err != nil {
			t.Fatal(err)
		}
		if err := obj.SetAttrs(attrImportVersion, runCompleteVersion, attrSummaryHash, summaryHash(&a)); err != nil {
			t.Fatal(err)
		}
	}
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	coreLimitCalls(t, api, map[string]int{"/athlete/activities?page=1": 1, "/athlete/activities?page=2": 1})
	coreLimitPaths(t, f, summaries, 101)
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
}

func TestImporterActivityLimitFewerAvailable(t *testing.T) {
	for _, available := range []int{0, 2} {
		t.Run(strconv.Itoa(available), func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "10")
			api := newCoreAPI(t)
			api.perPage = 10
			summaries := coreLimitActivities(t, api, available)
			api.pages[1] = summaries
			f := newCoreFixture(t, api)
			f.configure(t)
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			want := map[string]int{"/athlete/activities?page=1": 1}
			if available != 0 {
				want["/athlete/activities?page=2"] = 1
			}
			for _, a := range summaries {
				path := "/activities/" + strconv.FormatInt(a.ID, 10)
				want[path], want[path+"/streams"] = 1, 1
			}
			coreLimitCalls(t, api, want)
			coreLimitPaths(t, f, summaries, available)
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
		})
	}
}

func TestImporterActivityLimitUnsetOrEmptyIsUnlimited(t *testing.T) {
	for _, unset := range []bool{true, false} {
		name := "empty"
		if unset {
			name = "unset"
		}
		t.Run(name, func(t *testing.T) {
			// Setenv registers restoration of the original value even when we
			// subsequently remove it to exercise an actually absent variable.
			t.Setenv(coreMaxActivitiesEnv, "")
			if unset {
				if err := os.Unsetenv(coreMaxActivitiesEnv); err != nil {
					t.Fatal(err)
				}
			}
			api := newCoreAPI(t)
			summaries := coreLimitActivities(t, api, 11)
			api.pages[1], api.pages[2] = summaries[:10], summaries[10:]
			f := newCoreFixture(t, api)
			f.configure(t)
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			want := map[string]int{
				"/athlete/activities?page=1": 1, "/athlete/activities?page=2": 1,
				"/athlete/activities?page=3": 1,
			}
			for _, a := range summaries {
				path := "/activities/" + strconv.FormatInt(a.ID, 10)
				want[path], want[path+"/streams"] = 1, 1
			}
			coreLimitCalls(t, api, want)
			coreLimitPaths(t, f, summaries, len(summaries))
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
		})
	}
}

func TestImporterActivityLimitInvalidBeforeNetwork(t *testing.T) {
	for _, value := range []string{"not-an-integer", "0", "-1", "1.5", "1e2", " ", " 10", "10 ", "999999999999999999999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, value)
			f := newCoreFixture(t, nil) // any HTTP request fails the test
			f.configure(t)
			// A ready account with an expired token would refresh before its
			// first listing if configuration validation happened too late.
			if err := f.rc.AccountNode().SetAttr(attrTokenExpiry, "1"); err != nil {
				t.Fatal(err)
			}
			err := (imp{}).Run(f.rc)
			if err == nil || !strings.Contains(err.Error(), coreMaxActivitiesEnv) {
				t.Fatalf("Run with cap %q = %v; want cap configuration error", value, err)
			}
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
		})
	}
}

func TestImporterActivityLimitFailureBeforeNthDoesNotAdvanceCompletion(t *testing.T) {
	for _, completed := range []string{"", "older-version"} {
		t.Run("previous-"+completed, func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "3")
			api := newCoreAPI(t)
			api.perPage = 3
			summaries := coreLimitActivities(t, api, 4)
			api.pages[1] = summaries
			api.failures["/activities/999"] = http.StatusBadGateway
			f := newCoreFixture(t, api)
			f.configure(t)
			if completed != "" {
				if err := f.rc.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, completed); err != nil {
					t.Fatal(err)
				}
			}
			if err := (imp{}).Run(f.rc); err == nil {
				t.Fatal("failed second activity unexpectedly completed capped run")
			}
			coreLimitCalls(t, api, map[string]int{
				"/athlete/activities?page=1": 1, "/activities/1000": 1,
				"/activities/1000/streams": 1, "/activities/999": 1,
			})
			coreAttrs(t, f.activity(t, 1000), map[string]string{attrImportVersion: "2"})
			coreAttrs(t, f.activity(t, 999), map[string]string{attrImportVersion: ""})
			parent := coreChild(t, f.rc.RootNode(), "activities")
			for _, id := range []string{"998", "997"} {
				if got := parent.Attr("camliPath:" + id); got != "" {
					t.Errorf("uninspected activity %s has path %q", id, got)
				}
			}
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: completed})
		})
	}
}

func TestImporterActivityLimitReadBudgetStopsBeforeStreams(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "10")
	api := newCoreAPI(t)
	api.perPage = 10
	api.pages[1] = coreLimitActivities(t, api, 1)
	requests := 0
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		res, err := api.RoundTrip(req)
		if err == nil {
			res.Header.Set("X-ReadRateLimit-Limit", "2,100")
			res.Header.Set("X-ReadRateLimit-Usage", strconv.Itoa(requests)+","+strconv.Itoa(requests))
		}
		return res, err
	}))
	f.configure(t)
	// Listing consumes the first permit; detailed activity consumes the
	// second. Even with a 200 detail response, streams must never reach HTTP.
	if err := (imp{}).Run(f.rc); err == nil {
		t.Fatal("run completed despite exhausted read budget")
	}
	coreLimitCalls(t, api, map[string]int{"/athlete/activities?page=1": 1, "/activities/1000": 1})
	coreAttrs(t, f.activity(t, 1000), map[string]string{attrImportVersion: "", attrSummaryHash: ""})
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
}

func TestImporterInvalidSavedRateStateBeforeNetwork(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "10")
	for _, state := range []string{
		"not JSON", `{}`, `{"clientID":"other-app"}`,
	} {
		t.Run(state, func(t *testing.T) {
			f := newCoreFixture(t, nil)
			f.configure(t)
			if err := f.rc.Host.Cache().Set(rateLimitCacheKey(f.clientID), state); err != nil {
				t.Fatal(err)
			}
			err := (imp{}).Run(f.rc)
			if err == nil || !strings.Contains(err.Error(), "persisted rate-limit state") {
				t.Fatalf("Run with invalid saved state = %v; want validation error", err)
			}
			if got := f.rateState(t); got != state {
				t.Errorf("invalid cache entry changed: %q", got)
			}
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
		})
	}
}

func TestImporterSavedRateStateForAnotherAppIgnored(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "10")
	api := newCoreAPI(t)
	api.perPage = 10
	f := newCoreFixture(t, api)
	f.configure(t)
	// Its envelope is valid, but the other app's state cannot be restored by
	// this limiter. Ignore it before attempting to validate the inner state.
	if err := f.rc.Host.Cache().Set(rateLimitCacheKey(f.clientID), `{"clientID":"another-app","state":{"version":999}}`); err != nil {
		t.Fatal(err)
	}
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	coreLimitCalls(t, api, map[string]int{"/athlete/activities?page=1": 1})
	var stored persistedRateLimit
	if err := json.Unmarshal([]byte(f.rateState(t)), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ClientID != f.clientID || len(stored.State) == 0 {
		t.Errorf("new snapshot belongs to %q; want %q with state", stored.ClientID, f.clientID)
	}
}

func TestImporterFailedRunPersistsRateState(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "10")
	api := newCoreAPI(t)
	api.perPage = 10
	api.failures["/athlete/activities"] = http.StatusBadGateway
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).Run(f.rc); err == nil {
		t.Fatal("listing failure succeeded")
	}
	coreLimitCalls(t, api, map[string]int{"/athlete/activities?page=1": 1})
	var stored persistedRateLimit
	if err := json.Unmarshal([]byte(f.rateState(t)), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ClientID != f.clientID {
		t.Errorf("saved snapshot client ID = %q; want %q", stored.ClientID, f.clientID)
	}
	var state rateLimitState
	if err := json.Unmarshal(stored.State, &state); err != nil {
		t.Fatal(err)
	}
	if state.Short.ReadUsage != 1 || state.Daily.ReadUsage != 1 {
		t.Errorf("failed request not saved in quota state: %+v", state)
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
}

func TestImporterRateStateCacheFailureReported(t *testing.T) {
	t.Setenv(coreMaxActivitiesEnv, "10")
	api := newCoreAPI(t)
	api.perPage = 10
	api.pages[1] = coreLimitActivities(t, api, 1)
	requests := 0
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		res, err := api.RoundTrip(req)
		if err == nil {
			res.Header.Set("X-ReadRateLimit-Limit", "2,100")
			res.Header.Set("X-ReadRateLimit-Usage", strconv.Itoa(requests)+","+strconv.Itoa(requests))
		}
		return res, err
	}))
	f.configure(t)
	cacheErr := errors.New("rate cache write failed")
	f.rc.Host.Cache().(*coreRateCache).setErr = cacheErr
	err := (imp{}).Run(f.rc)
	if !errors.Is(err, cacheErr) {
		t.Errorf("snapshot failure cause lost: %v", err)
	}
	var quotaErr *rateLimitError
	if !errors.As(err, &quotaErr) {
		t.Errorf("quota failure cause lost when saving snapshot: %v", err)
	}
	coreLimitCalls(t, api, map[string]int{"/athlete/activities?page=1": 1, "/activities/1000": 1})
	if _, err := f.rc.Host.Cache().Get(rateLimitCacheKey(f.clientID)); !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("failed write left cache entry: %v", err)
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
}

func TestImporterSharedRateStateSurvivesRestartForAnotherAccount(t *testing.T) {
	for _, cooldown := range []bool{false, true} {
		name := "exhausted-read-budget"
		if cooldown {
			name = "429-cooldown"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "10")
			cacheConfig := jsonconfig.Obj{
				"type": sorted.DefaultKVFileType,
				"file": filepath.Join(t.TempDir(), "importer-cache"),
			}
			cache, err := sorted.NewKeyValue(cacheConfig)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cache.Close() })
			requests := 0
			f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.URL.Path != "/api/v3/athlete/activities" {
					t.Errorf("unexpected request %s", req.URL.Path)
				}
				status := http.StatusOK
				if cooldown {
					status = http.StatusTooManyRequests
				}
				res := coreResponse(req, status, []byte("[]"))
				if cooldown {
					res.Header.Set("Retry-After", "3600")
				} else {
					// Exhaust the daily budget too, so a quarter-hour boundary
					// during this test cannot accidentally reopen the quota.
					res.Header.Set("X-ReadRateLimit-Limit", "1,1")
					res.Header.Set("X-ReadRateLimit-Usage", "1,1")
				}
				return res, nil
			}), cache)
			f.configure(t)
			err = (imp{}).Run(f.rc)
			if cooldown && err == nil {
				t.Fatal("429 run succeeded")
			}
			if !cooldown && err != nil {
				t.Fatal(err)
			}
			if f.rateState(t) == "" {
				t.Fatal("shared importer cache has no persisted quota")
			}

			// Simulate a clean process restart: discard the app's in-memory
			// budget, close and reopen the file cache, then create a fresh host.
			appRateLimiters.Lock()
			delete(appRateLimiters.byID, f.clientID)
			appRateLimiters.Unlock()
			if err := cache.Close(); err != nil {
				t.Fatal(err)
			}
			cache, err = sorted.NewKeyValue(cacheConfig)
			if err != nil {
				t.Fatal(err)
			}
			f.hostConfig.Cache = cache
			host, err := importer.NewHost(f.hostConfig)
			if err != nil {
				t.Fatal(err)
			}
			rc, err := importer.CreateAccount(host, "strava")
			if err != nil {
				t.Fatal(err)
			}
			second := &coreFixture{rc: rc, storage: f.storage, clientID: f.clientID}
			second.configure(t)
			err = (imp{}).Run(second.rc)
			var quotaErr *rateLimitError
			if !errors.As(err, &quotaErr) {
				t.Fatalf("second account after restart = %v; want restored quota block", err)
			}
			if requests != 1 {
				t.Errorf("restart opened quota for sibling account: %d requests; want 1 total", requests)
			}
			coreAttrs(t, second.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
		})
	}
}

func TestImporterTokenBoundToOldAppMustRefreshBeforeREST(t *testing.T) {
	for _, success := range []bool{false, true} {
		name := "refresh-rejected"
		if success {
			name = "refresh-rebinds-token"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(coreMaxActivitiesEnv, "10")
			var f *coreFixture
			var calls []string
			expiry := time.Now().Add(time.Hour).Unix()
			f = newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.URL.Path)
				switch req.URL.Path {
				case "/oauth/token":
					if req.Method != http.MethodPost {
						t.Errorf("refresh method = %s", req.Method)
					}
					if err := req.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if got := req.PostForm.Get("client_id"); got != f.clientID {
						t.Errorf("refresh app = %q; want configured %q", got, f.clientID)
					}
					if !success {
						return coreResponse(req, http.StatusUnauthorized, []byte(`{"message":"test refresh rejected"}`)), nil
					}
					return coreResponse(req, http.StatusOK, coreJSON(t, tokenResponse{
						AccessToken: "new-app-access", RefreshToken: "new-app-refresh", ExpiresAt: expiry,
					})), nil
				case "/api/v3/athlete/activities":
					coreAttrs(t, f.rc.AccountNode(), map[string]string{
						attrTokenClientID: f.clientID, importer.AcctAttrAccessToken: "new-app-access",
						attrRefreshToken: "new-app-refresh",
					})
					if got := req.Header.Get("Authorization"); got != "Bearer new-app-access" {
						t.Errorf("REST used old app's token: %q", got)
					}
					return coreResponse(req, http.StatusOK, []byte("[]")), nil
				default:
					t.Errorf("unexpected app change request %s", req.URL.Path)
					return coreResponse(req, http.StatusNotFound, nil), nil
				}
			}))
			f.configure(t) // its existing token is valid well into the future
			if err := f.rc.AccountNode().SetAttr(attrTokenClientID, "previous-app"); err != nil {
				t.Fatal(err)
			}
			err := (imp{}).Run(f.rc)
			want := []string{"/oauth/token"}
			if success {
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, "/api/v3/athlete/activities")
				coreAttrs(t, f.rc.AccountNode(), map[string]string{
					attrTokenClientID: f.clientID, importer.AcctAttrCompletedVersion: "2",
				})
			} else {
				if err == nil {
					t.Fatal("rejected cross-app refresh succeeded")
				}
				coreAttrs(t, f.rc.AccountNode(), map[string]string{
					attrTokenClientID: "previous-app", importer.AcctAttrCompletedVersion: "",
				})
			}
			if !reflect.DeepEqual(calls, want) {
				t.Errorf("app change requests = %v; want %v", calls, want)
			}
			var saved persistedRateLimit
			if err := json.Unmarshal([]byte(f.rateState(t)), &saved); err != nil {
				t.Fatal(err)
			}
			var state rateLimitState
			if err := json.Unmarshal(saved.State, &state); err != nil {
				t.Fatal(err)
			}
			wantUsage := int64(0)
			if success {
				wantUsage = 1
			}
			if saved.ClientID != f.clientID || state.Daily.ReadUsage != wantUsage {
				t.Errorf("app change saved quota = %+v, read usage %d; want current app with usage %d", saved, state.Daily.ReadUsage, wantUsage)
			}
		})
	}
}

func TestImporterRateCacheReadFailureBeforeNetwork(t *testing.T) {
	f := newCoreFixture(t, nil)
	f.configure(t)
	want := errors.New("rate cache unavailable")
	f.rc.Host.Cache().(*coreRateCache).getErr = want
	if err := (imp{}).Run(f.rc); !errors.Is(err, want) {
		t.Fatalf("Run = %v; want cache read failure", err)
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
}

func TestRateCacheSeparatesApplications(t *testing.T) {
	cache := sorted.NewMemoryKeyValue()
	defer cache.Close()
	first := newRateLimiter(rateTestTime)
	first.state.Daily.ReadUsage = 1000
	second := newRateLimiter(rateTestTime)
	second.state.Daily.ReadUsage = 7
	// Long IDs must still produce bounded keys, without exposing the ID.
	firstID := strings.Repeat("first-app/", 1000)
	secondID := "second-app"
	if key := rateLimitCacheKey(firstID); len(key) > sorted.MaxKeySize || strings.Contains(key, "first-app") {
		t.Fatalf("unbounded or unescaped cache key: %q", key)
	}
	for id, limiter := range map[string]*rateLimiter{firstID: first, secondID: second} {
		if err := persistRateLimit(cache, id, limiter); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]int64{firstID: 1000, secondID: 7, "missing-app": 0} {
		restored := newRateLimiter(rateTestTime)
		if err := restoreRateLimit(cache, id, restored); err != nil {
			t.Fatal(err)
		}
		if got := restored.state.Daily.ReadUsage; got != want {
			t.Errorf("restored %q usage = %d; want %d", id, got, want)
		}
	}
}

func TestImporterIgnoresLegacyRateClaim(t *testing.T) {
	api := newCoreAPI(t)
	f := newCoreFixture(t, api)
	f.configure(t)
	const legacyAttr = "stravaRateLimitState"
	// Cache upgrades must not read or mutate old, permanently archived state.
	node := f.rateNode(t)
	if err := node.SetAttr(legacyAttr, "obsolete quota state"); err != nil {
		t.Fatal(err)
	}
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	before := f.storage.NumBlobs()
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	if got := f.storage.NumBlobs(); got != before {
		t.Errorf("unchanged poll wrote %d blobs; want none", got-before)
	}
	coreAttrs(t, node, map[string]string{legacyAttr: "obsolete quota state"})
	if f.rateState(t) == "" {
		t.Fatal("poll did not update the local rate cache")
	}
}
