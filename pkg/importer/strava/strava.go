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

// Package strava imports the authenticated athlete's activities from Strava.
// See README.md for setup instructions and API policy considerations.
package strava // import "perkeep.org/pkg/importer/strava"

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/sorted"
)

const (
	authURL              = "https://www.strava.com/oauth/authorize"
	attrRefreshToken     = "stravaRefreshToken"
	attrTokenExpiry      = "stravaTokenExpiry"
	attrTokenClientID    = "stravaTokenClientID"
	attrOAuthState       = "stravaOAuthState"
	attrOAuthStateExpiry = "stravaOAuthStateExpiry"
	attrImportVersion    = "stravaImportVersion"
	attrSummaryHash      = "stravaSummaryHash"
	attrActivityJSON     = "stravaActivityJSON"
	attrStreamsJSON      = "stravaStreamsJSON"
	maxActivitiesEnv     = "PERKEEP_STRAVA_MAX_ACTIVITIES"
	runCompleteVersion   = "2"
)

type imp struct{ importer.OAuth2 }

// Serialize nonce consumption and rotating credentials for each account.
// Fixed stripes avoid retaining locks for deleted accounts. A collision merely
// serializes unrelated accounts; no importer host/client mutex is held.
var accountLocks [64]sync.Mutex

func accountMutex(acct *importer.Object) *sync.Mutex {
	return &accountLocks[acct.PermanodeRef().Sum32()%uint32(len(accountLocks))]
}

var _ importer.Importer = imp{}
var _ importer.ImporterSetupHTMLer = imp{}

func init() { importer.Register("strava", imp{}) }

func (imp) Properties() importer.Properties {
	return importer.Properties{
		Title:               "Strava",
		Description:         "import your Strava workouts and GPS tracks",
		NeedsAPIKey:         true,
		SupportsIncremental: true,
	}
}

func (imp) AccountSetupHTML(h *importer.Host) string {
	setup := `<p>Create an application in <a href="https://www.strava.com/settings/api">Strava API settings</a>, set its Authorization Callback Domain to your Perkeep server's hostname, and enter its Client ID and Client Secret here. Authorization requests <code>activity:read_all</code>, including private workouts. Only the authenticated athlete's activities are imported.</p>`
	if limit, err := activityLimit(); err == nil && limit > 0 {
		setup += fmt.Sprintf("<p>Testing cap: each run considers only the %d most recent activities. Reaching this cap counts as a successful import.</p>", limit)
	}
	return setup
}

func (imp) IsAccountReady(acct *importer.Object) (bool, error) {
	// A refresh credential is sufficient to recover an absent access token or
	// expiry, including a partially persisted authorization-code exchange.
	return acct.Attr(importer.AcctAttrUserID) != "" &&
		acct.Attr(attrRefreshToken) != "", nil
}

func (im imp) SummarizeAccount(acct *importer.Object) string {
	if ready, _ := im.IsAccountReady(acct); !ready {
		return ""
	}
	name := acct.Attr(importer.AcctAttrName)
	if name == "" {
		name = acct.Attr(importer.AcctAttrUserID)
	}
	return "Strava athlete " + name
}

// CallbackRequestAccount extracts the routing part of the state. The random
// nonce is validated against the account's pending setup in ServeCallback.
func (imp) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	state := r.FormValue("state")
	if !strings.HasPrefix(state, "acct:") {
		return blob.Ref{}, errors.New("strava: missing OAuth state")
	}
	parts := strings.Split(strings.TrimPrefix(state, "acct:"), ":")
	if len(parts) != 2 || len(parts[1]) != 64 {
		return blob.Ref{}, errors.New("strava: invalid OAuth state")
	}
	ref, ok := blob.Parse(parts[0])
	if !ok {
		return blob.Ref{}, errors.New("strava: invalid OAuth account")
	}
	return ref, nil
}

func (im imp) ServeSetup(w http.ResponseWriter, req *http.Request, sc *importer.SetupContext) error {
	mu := accountMutex(sc.AccountNode)
	mu.Lock()
	defer mu.Unlock()
	id, _, err := sc.Credentials()
	if err != nil {
		return err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	state := "acct:" + sc.AccountNode.PermanodeRef().String() + ":" + hex.EncodeToString(nonce[:])
	if err := sc.AccountNode.SetAttrs(attrOAuthState, state, attrOAuthStateExpiry, strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)); err != nil {
		return err
	}
	params := url.Values{
		"client_id": {id}, "redirect_uri": {im.RedirectURL(im, sc)},
		"response_type": {"code"}, "scope": {"activity:read_all"},
		"approval_prompt": {"force"}, "state": {state},
	}
	http.Redirect(w, req, authURL+"?"+params.Encode(), http.StatusFound)
	return nil
}

func (im imp) ServeCallback(w http.ResponseWriter, req *http.Request, sc *importer.SetupContext) {
	fail := func(code int, err error) { http.Error(w, err.Error(), code) }
	if req.Method != http.MethodGet {
		fail(http.StatusMethodNotAllowed, errors.New("strava: expected GET"))
		return
	}
	acct, err := im.CallbackRequestAccount(req)
	if err != nil || acct != sc.AccountNode.PermanodeRef() {
		fail(http.StatusBadRequest, errors.New("strava: invalid OAuth account"))
		return
	}
	mu := accountMutex(sc.AccountNode)
	mu.Lock()
	defer mu.Unlock()
	expected, state := sc.AccountNode.Attr(attrOAuthState), req.FormValue("state")
	expiry, err := strconv.ParseInt(sc.AccountNode.Attr(attrOAuthStateExpiry), 10, 64)
	if err != nil || expiry <= time.Now().Unix() || expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(state)) != 1 {
		fail(http.StatusBadRequest, errors.New("strava: invalid or expired OAuth state"))
		return
	}
	// Consume the nonce even on denied authorization. A failed exchange requires
	// starting setup again, rather than accepting a replayed authorization code.
	if err := sc.AccountNode.DelAttr(attrOAuthState, state); err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	if req.FormValue("error") != "" {
		fail(http.StatusBadRequest, errors.New("strava: authorization was denied"))
		return
	}
	if req.FormValue("code") == "" || !hasScope(req.FormValue("scope"), "activity:read_all") {
		fail(http.StatusBadRequest, errors.New("strava: authorization requires a code and activity:read_all permission"))
		return
	}
	id, secret, err := sc.Credentials()
	if err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	tok, err := exchangeToken(req.Context(), sc.Host.HTTPClient(), url.Values{
		"client_id": {id}, "client_secret": {secret}, "code": {req.FormValue("code")}, "grant_type": {"authorization_code"},
	})
	if err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	if tok.Athlete.ID <= 0 {
		fail(http.StatusBadGateway, errors.New("strava: token exchange did not identify athlete"))
		return
	}
	uid := strconv.FormatInt(tok.Athlete.ID, 10)
	if old := sc.AccountNode.Attr(importer.AcctAttrUserID); old != "" && old != uid {
		fail(http.StatusBadRequest, errors.New("strava: cannot change an existing account's athlete; create a new importer account"))
		return
	}
	if err := sc.AccountNode.SetAttrs(
		importer.AcctAttrUserID, uid, importer.AcctAttrUserName, tok.Athlete.Username,
		importer.AcctAttrGivenName, tok.Athlete.Firstname, importer.AcctAttrFamilyName, tok.Athlete.Lastname,
		importer.AcctAttrName, strings.TrimSpace(tok.Athlete.Firstname+" "+tok.Athlete.Lastname),
	); err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	if err := storeToken(sc.AccountNode, tok, id); err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, req, sc.AccountURL(), http.StatusFound)
}

func hasScope(scopes, want string) bool {
	for _, s := range strings.Split(scopes, ",") {
		if s == want {
			return true
		}
	}
	return false
}

func storeToken(acct *importer.Object, tok *tokenResponse, clientIDs ...string) error {
	// Persist the rotating refresh token first. If a later claim fails, the next
	// run can still obtain a new access token with the new refresh credential.
	for _, kv := range [][2]string{
		{attrRefreshToken, tok.RefreshToken},
		{importer.AcctAttrAccessToken, tok.AccessToken},
		{attrTokenExpiry, strconv.FormatInt(tok.ExpiresAt, 10)},
	} {
		if err := acct.SetAttr(kv[0], kv[1]); err != nil {
			return err
		}
	}
	// Bind last: partially persisted replacement credentials must not make an
	// old access token look as though it belongs to the new application.
	if len(clientIDs) != 0 {
		return acct.SetAttr(attrTokenClientID, clientIDs[0])
	}
	return nil
}

type run struct{ *importer.RunContext }

// activityLimit is deliberately opt-in. Invalid values must not silently
// turn a bounded test import into an unbounded backfill.
func activityLimit() (int, error) {
	value := os.Getenv(maxActivitiesEnv)
	if value == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("strava: %s must be a positive integer or unset", maxActivitiesEnv)
	}
	return limit, nil
}

// Cache the application identity with its budget locally, not in permanent
// claim history. All accounts using the application share one mutable entry.
type persistedRateLimit struct {
	ClientID string          `json:"clientID"`
	State    json.RawMessage `json:"state"`
}

// Hash the Client ID to keep keys bounded and independent of user input.
func rateLimitCacheKey(id string) string {
	return fmt.Sprintf("ratelimit:%x", sha256.Sum256([]byte(id)))
}

func restoreRateLimit(cache sorted.KeyValue, id string, limiter *rateLimiter) error {
	raw, err := cache.Get(rateLimitCacheKey(id))
	if errors.Is(err, sorted.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("strava: reading rate-limit cache: %w", err)
	}
	var stored persistedRateLimit
	if json.Unmarshal([]byte(raw), &stored) != nil || stored.ClientID == "" || len(stored.State) == 0 {
		return errors.New("strava: invalid cached rate-limit state")
	}
	if stored.ClientID != id {
		return nil
	}
	return limiter.restore(string(stored.State))
}

func persistRateLimit(cache sorted.KeyValue, id string, limiter *rateLimiter) error {
	// A newer snapshot must not be persisted before an older snapshot's
	// delayed cache write. Serialize the snapshot and its write per app.
	limiter.persistMu.Lock()
	defer limiter.persistMu.Unlock()
	raw, err := json.Marshal(persistedRateLimit{ClientID: id, State: json.RawMessage(limiter.snapshot())})
	if err != nil {
		return errors.New("strava: could not encode rate-limit state")
	}
	return cache.Set(rateLimitCacheKey(id), string(raw))
}

// client checks expiry before each API call, including during long backfills.
func (r *run) client() (*apiClient, error) {
	acct := r.AccountNode()
	mu := accountMutex(acct)
	mu.Lock()
	defer mu.Unlock()
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	id, secret, err := r.Credentials()
	if err != nil {
		return nil, err
	}
	expiry, err := strconv.ParseInt(acct.Attr(attrTokenExpiry), 10, 64)
	if err != nil || acct.Attr(importer.AcctAttrAccessToken) == "" ||
		acct.Attr(attrTokenClientID) != id || time.Now().Add(time.Minute).Unix() >= expiry {
		tok, err := exchangeToken(r.Context(), r.Host.HTTPClient(), url.Values{
			"client_id": {id}, "client_secret": {secret}, "refresh_token": {acct.Attr(attrRefreshToken)}, "grant_type": {"refresh_token"},
		})
		if err != nil {
			return nil, err
		}
		if err := storeToken(acct, tok, id); err != nil {
			return nil, err
		}
	}
	return &apiClient{
		httpClient: r.Host.HTTPClient(), accessToken: acct.Attr(importer.AcctAttrAccessToken),
		limiter: rateLimiterForApp(id), logf: r.Logf,
	}, nil
}

func (im imp) Run(rc *importer.RunContext) error {
	return im.runAt(rc, time.Now)
}

// runAt keeps the sync clock local to this invocation. OAuth and quota clocks
// retain their existing behavior.
func (im imp) runAt(rc *importer.RunContext, now func() time.Time) (err error) {
	defer func() {
		if ctxErr := rc.Context().Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
			err = errors.Join(ctxErr, err)
		}
	}()
	if err := rc.Context().Err(); err != nil {
		return err
	}
	maxActivities, err := activityLimit()
	if err != nil {
		return err
	}
	// Keep progress monotonic across concurrent invocations for one account.
	// This is separate from accountMutex, which client takes to refresh OAuth.
	gate := importRunGate(rc.AccountNode())
	select {
	case <-rc.Context().Done():
		return rc.Context().Err()
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()
	// Cancellation and the token may become ready together. Return the token
	// without starting work if select acquired it for an already-cancelled run.
	if err := rc.Context().Err(); err != nil {
		return err
	}
	if ok, _ := im.IsAccountReady(rc.AccountNode()); !ok {
		return errors.New("strava: account is not configured; reconnect account")
	}
	var progress importProgress
	if maxActivities == 0 {
		progress, err = readImportProgress(rc.AccountNode())
		if err != nil {
			return err
		}
	}
	id, _, err := rc.Credentials()
	if err != nil {
		return err
	}
	limiter := rateLimiterForApp(id)
	cache, cacheErr := rc.ImporterCache()
	if cacheErr != nil {
		rc.Logf("quota cache unavailable; using in-memory budget: %v", cacheErr)
	} else {
		if restoreErr := restoreRateLimit(cache, id, limiter); restoreErr != nil {
			rc.Logf("ignoring quota cache; using in-memory budget: %v", restoreErr)
		}
		// Save consumed budgets even on rate-limit, HTTP, or import failures.
		// Scratch state only avoids rediscovering usage after a restart:
		// missing or broken caches must not fail an otherwise valid import.
		defer func() {
			if persistErr := persistRateLimit(cache, id, limiter); persistErr != nil {
				rc.Logf("could not save quota cache: %v", persistErr)
			}
		}()
	}
	r := &run{rc}
	if err := rc.RootNode().SetAttrs(nodeattr.Title, "Strava workouts", nodeattr.DefaultVisibility, "hide"); err != nil {
		return err
	}
	activities, err := rc.RootNode().ChildPathObject("activities")
	if err != nil {
		return err
	}
	if err := activities.SetAttr(nodeattr.Title, "Activities"); err != nil {
		return err
	}
	if maxActivities > 0 {
		return r.importCappedActivities(activities, maxActivities)
	}
	return r.syncActivities(activities, progress, now)
}

// importCappedActivities preserves the explicit newest-N testing mode. Its
// legacy completion marker does not establish chronological backfill progress.
func (r *run) importCappedActivities(activities *importer.Object, maxActivities int) error {
	pageSize := 100
	if maxActivities < pageSize {
		pageSize = maxActivities
	}
	considered := 0
	for page := 1; ; page++ {
		if err := r.Context().Err(); err != nil {
			return err
		}
		client, err := r.client()
		if err != nil {
			return err
		}
		summaries, err := client.listActivities(r.Context(), page, pageSize)
		if err != nil {
			return err
		}
		if len(summaries) == 0 {
			break
		}
		for _, summary := range summaries {
			if err := r.importActivity(activities, &summary); err != nil {
				return fmt.Errorf("strava: activity %d: %w", summary.ID, err)
			}
			// Already imported activities count toward the cap too. Re-running
			// a test must not advance into the next batch of older workouts.
			considered++
			if considered >= maxActivities {
				if err := r.Context().Err(); err != nil {
					return err
				}
				return r.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion)
			}
		}
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	return r.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion)
}

func summaryHash(a *activity) string {
	data, _ := json.Marshal(a) // activity contains no non-JSON fields.
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (r *run) importActivity(parent *importer.Object, summary *activity) error {
	_, err := r.importActivityWithForce(parent, summary, false)
	return err
}

// fetched reports a successful full import, not a summary-hash fast path.
// Forced reconciliation bypasses only that fast path: validation, attachment
// staging, and the atomic no-op-aware attribute publication remain shared.
func (r *run) importActivityWithForce(parent *importer.Object, summary *activity, force bool) (fetched bool, err error) {
	if err := r.Context().Err(); err != nil {
		return false, err
	}
	if summary.ID <= 0 {
		return false, errors.New("invalid activity ID")
	}
	obj, err := parent.ChildPathObject(strconv.FormatInt(summary.ID, 10))
	if err != nil {
		return false, err
	}
	hash := summaryHash(summary)
	if !force && obj.Attr(attrImportVersion) == runCompleteVersion && obj.Attr(attrSummaryHash) == hash {
		return false, nil
	}
	client, err := r.client()
	if err != nil {
		return false, err
	}
	a, rawActivity, err := client.getActivity(r.Context(), summary.ID)
	if err != nil {
		return false, err
	}
	if a.ID != summary.ID || a.StartDate.IsZero() {
		return false, errors.New("invalid detailed activity identity or start date")
	}
	if !summary.StartDate.IsZero() && !a.StartDate.Equal(summary.StartDate) {
		return false, errors.New("detailed activity start date differs from summary")
	}
	if err := validateActivity(a); err != nil {
		return false, err
	}
	client, err = r.client()
	if err != nil {
		return false, err
	}
	streams, rawStreams, err := client.getStreams(r.Context(), a.ID)
	if err != nil {
		return false, err
	}
	gpx, err := activityGPX(a, streams)
	if err != nil {
		return false, err
	}
	// Stage every attachment before publishing any activity attributes. A failed
	// refresh must leave the previous complete activity (including its hash and
	// file refs) intact so that the changed summary will be retried.
	sets, deletes := activityAttrs(a)
	for _, file := range []struct {
		key, filename string
		data          []byte
	}{
		{attrActivityJSON, "activity.json", rawActivity},
		{attrStreamsJSON, "streams.json", rawStreams},
		{nodeattr.FitnessTrack, "track.gpx", gpx},
	} {
		ref, err := r.writeAttachment(file.filename, file.data)
		if err != nil {
			return false, err
		}
		if ref.Valid() {
			sets = append(sets, file.key, ref.String())
		} else {
			deletes = append(deletes, file.key)
		}
		// Unlink the previous layout's filename paths without changing its
		// historical wrapper permanodes.
		deletes = append(deletes, "camliPath:"+file.filename)
	}
	// Completion is the last child in the same atomic claim as the data.
	sets = append(sets, attrSummaryHash, hash, attrImportVersion, runCompleteVersion)
	if err := r.Context().Err(); err != nil {
		return false, err
	}
	if err := obj.SetAttrsWithDeletes(deletes, sets...); err != nil {
		return false, err
	}
	r.Logf("Imported activity %d, %s: %q", summary.ID, summary.StartDate.Format(time.DateOnly), summary.Name)
	return true, nil
}

// writeAttachment uploads a named file schema without mutating any permanode.
func (r *run) writeAttachment(filename string, data []byte) (blob.Ref, error) {
	if len(data) == 0 {
		return blob.Ref{}, nil
	}
	return schema.WriteFileFromReader(r.Context(), r.Host.Target(), filename, bytes.NewReader(data))
}

func decimal(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Reject corrupt values before publishing generic attributes or attachments.
func validateActivity(a *activity) error {
	if !finite(a.Distance) || a.Distance < 0 || !finite(a.TotalElevationGain) || a.TotalElevationGain < 0 || a.MovingTime < 0 || a.ElapsedTime < 0 || a.MovingTime > a.ElapsedTime {
		return errors.New("invalid activity distance, elevation gain, or duration")
	}
	for _, v := range []*float64{a.AverageHeartrate, a.MaxHeartrate, a.Calories} {
		if v != nil && (!finite(*v) || *v < 0) {
			return errors.New("invalid activity heart rate or calories")
		}
	}
	if a.Calories != nil && !finite(*a.Calories*4184) {
		return errors.New("invalid activity energy")
	}
	if len(a.StartLatlng) != 0 && (len(a.StartLatlng) != 2 || !validCoordinate(a.StartLatlng[0], a.StartLatlng[1])) {
		return errors.New("invalid activity start coordinates")
	}
	return nil
}

// activityAttrs builds the modeled values and deletions without publishing them.
func activityAttrs(a *activity) (sets, deletes []string) {
	typ := a.SportType
	if typ == "" {
		typ = a.Type
	}
	sets = []string{
		nodeattr.Type, nodeattr.FitnessActivity,
		nodeattr.Title, a.Name, nodeattr.Description, a.Description,
		nodeattr.StartDate, a.StartDate.UTC().Format(time.RFC3339),
		nodeattr.URL, "https://www.strava.com/activities/" + strconv.FormatInt(a.ID, 10),
		nodeattr.FitnessSource, "strava", nodeattr.FitnessSourceID, strconv.FormatInt(a.ID, 10),
		nodeattr.FitnessDistance, decimal(a.Distance),
		nodeattr.FitnessMovingDuration, strconv.FormatInt(a.MovingTime, 10),
		nodeattr.FitnessElapsedDuration, strconv.FormatInt(a.ElapsedTime, 10),
		nodeattr.FitnessElevationGain, decimal(a.TotalElevationGain),
		"stravaSportType", a.SportType, "stravaActivityType", a.Type,
		"stravaTimezone", a.Timezone, "stravaExternalID", a.ExternalID, "stravaGearID", a.GearID,
		"stravaTrainer", strconv.FormatBool(a.Trainer), "stravaCommute", strconv.FormatBool(a.Commute),
		"stravaManual", strconv.FormatBool(a.Manual), "stravaPrivate", strconv.FormatBool(a.Private),
	}
	if typ == "" {
		deletes = append(deletes, nodeattr.FitnessActivityType)
	} else {
		sets = append(sets, nodeattr.FitnessActivityType, activityType(typ))
	}
	optional := func(key string, val *float64) {
		if val == nil {
			deletes = append(deletes, key)
		} else {
			sets = append(sets, key, decimal(*val))
		}
	}
	optional(nodeattr.FitnessAverageHeartRate, a.AverageHeartrate)
	optional(nodeattr.FitnessMaxHeartRate, a.MaxHeartrate)
	var energy, latitude, longitude *float64
	if a.Calories != nil {
		joules := *a.Calories * 4184
		energy = &joules
	}
	optional(nodeattr.FitnessEnergy, energy)
	if len(a.StartLatlng) == 2 {
		latitude, longitude = &a.StartLatlng[0], &a.StartLatlng[1]
	}
	optional(nodeattr.Latitude, latitude)
	optional(nodeattr.Longitude, longitude)
	return sets, deletes
}

// activityType deliberately groups provider-specific subtypes. The original
// sport type is preserved separately, and unknown future sports remain valid.
func activityType(s string) string {
	switch s {
	case "Run", "TrailRun", "VirtualRun":
		return "run"
	case "Walk":
		return "walk"
	case "Hike":
		return "hike"
	case "Ride", "MountainBikeRide", "GravelRide", "EBikeRide", "EMountainBikeRide", "VirtualRide", "Handcycle", "Velomobile":
		return "ride"
	case "Swim":
		return "swim"
	case "Rowing", "VirtualRow":
		return "row"
	case "AlpineSki", "BackcountrySki", "NordicSki", "RollerSki":
		return "ski"
	case "Workout", "WeightTraining", "Crossfit", "Yoga", "Pilates", "HighIntensityIntervalTraining", "Elliptical", "StairStepper":
		return "workout"
	default:
		return "other"
	}
}
