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
	attrRateLimitState   = "stravaRateLimitState"
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

// Persist the application identity with its budget on the shared importer
// configuration, not an account: a restart must restore the latest budget
// regardless of which account starts first.
type persistedRateLimit struct {
	ClientID string          `json:"clientID"`
	State    json.RawMessage `json:"state"`
}

func restoreRateLimit(acct *importer.Object, id string, limiter *rateLimiter) error {
	raw := acct.Attr(attrRateLimitState)
	if raw == "" {
		return nil
	}
	var stored persistedRateLimit
	if json.Unmarshal([]byte(raw), &stored) != nil || stored.ClientID == "" || len(stored.State) == 0 {
		return errors.New("strava: invalid persisted rate-limit state")
	}
	if stored.ClientID != id {
		return nil
	}
	return limiter.restore(string(stored.State))
}

func persistRateLimit(acct *importer.Object, id string, limiter *rateLimiter) error {
	// A newer snapshot must not be persisted before an older snapshot's
	// delayed claim upload. Serialize the snapshot and its write per app.
	limiter.persistMu.Lock()
	defer limiter.persistMu.Unlock()
	raw, err := json.Marshal(persistedRateLimit{ClientID: id, State: json.RawMessage(limiter.snapshot())})
	if err != nil {
		return errors.New("strava: could not encode rate-limit state")
	}
	return acct.SetAttr(attrRateLimitState, string(raw))
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
		limiter: rateLimiterForApp(id),
	}, nil
}

func (im imp) Run(rc *importer.RunContext) (err error) {
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
	if ok, _ := im.IsAccountReady(rc.AccountNode()); !ok {
		return errors.New("strava: account is not configured; reconnect account")
	}
	id, _, err := rc.Credentials()
	if err != nil {
		return err
	}
	limiter := rateLimiterForApp(id)
	rateNode, err := rc.ImporterNode()
	if err != nil {
		return err
	}
	if err := restoreRateLimit(rateNode, id, limiter); err != nil {
		return err
	}
	// Retain consumed budgets even on rate-limit, HTTP, or import failures.
	// Subsequent runs and clean server restarts must not reset the counters.
	defer func() {
		if persistErr := persistRateLimit(rateNode, id, limiter); persistErr != nil {
			err = errors.Join(err, fmt.Errorf("strava: saving rate-limit state: %w", persistErr))
		}
	}()
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
	// Offset pages can move between runs. Always restart at page one and use
	// activity IDs plus completion markers to resume without missing old items.
	pageSize := 100
	if maxActivities > 0 && maxActivities < pageSize {
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
			if maxActivities > 0 && considered >= maxActivities {
				return rc.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion)
			}
		}
	}
	return rc.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, runCompleteVersion)
}

func summaryHash(a *activity) string {
	data, _ := json.Marshal(a) // activity contains no non-JSON fields.
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (r *run) importActivity(parent *importer.Object, summary *activity) error {
	if err := r.Context().Err(); err != nil {
		return err
	}
	if summary.ID <= 0 {
		return errors.New("invalid activity ID")
	}
	obj, err := parent.ChildPathObject(strconv.FormatInt(summary.ID, 10))
	if err != nil {
		return err
	}
	hash := summaryHash(summary)
	if obj.Attr(attrImportVersion) == runCompleteVersion && obj.Attr(attrSummaryHash) == hash {
		return nil
	}
	// Clear the old completion marker before changing anything, so a failed
	// refresh cannot leave a partially rewritten activity looking complete.
	if err := clearAttr(obj, attrImportVersion); err != nil {
		return err
	}
	client, err := r.client()
	if err != nil {
		return err
	}
	a, rawActivity, err := client.getActivity(r.Context(), summary.ID)
	if err != nil {
		return err
	}
	if a.ID != summary.ID || a.StartDate.IsZero() {
		return errors.New("invalid detailed activity identity or start date")
	}
	if err := validateActivity(a); err != nil {
		return err
	}
	client, err = r.client()
	if err != nil {
		return err
	}
	streams, rawStreams, err := client.getStreams(r.Context(), a.ID)
	if err != nil {
		return err
	}
	gpx, err := activityGPX(a, streams)
	if err != nil {
		return err
	}
	if err := r.attach(obj, attrActivityJSON, "activity.json", rawActivity); err != nil {
		return err
	}
	if err := r.attach(obj, attrStreamsJSON, "streams.json", rawStreams); err != nil {
		return err
	}
	if err := r.attach(obj, nodeattr.FitnessTrack, "track.gpx", gpx); err != nil {
		return err
	}
	if err := setActivityAttrs(obj, a); err != nil {
		return err
	}
	if err := obj.SetAttr(attrSummaryHash, hash); err != nil {
		return err
	}
	return obj.SetAttr(attrImportVersion, runCompleteVersion)
}

func clearAttr(obj *importer.Object, key string) error {
	if obj.Attr(key) == "" {
		return nil
	}
	return obj.DelAttr(key, "")
}

func (r *run) attach(obj *importer.Object, key, filename string, data []byte) error {
	if len(data) == 0 {
		if err := clearAttr(obj, key); err != nil {
			return err
		}
		return clearAttr(obj, "camliPath:"+filename)
	}
	ref, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), filename, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := obj.SetAttr(key, ref.String()); err != nil {
		return err
	}
	// Older imports created attachment wrapper permanodes. Unlink their
	// filename paths on refresh; attachments need only the semantic file ref.
	return clearAttr(obj, "camliPath:"+filename)
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

func setActivityAttrs(obj *importer.Object, a *activity) error {
	typ := a.SportType
	if typ == "" {
		typ = a.Type
	}
	if err := obj.SetAttrs(
		nodeattr.Type, nodeattr.FitnessActivity,
		nodeattr.Title, a.Name, nodeattr.Description, a.Description,
		nodeattr.StartDate, a.StartDate.UTC().Format(time.RFC3339),
		nodeattr.URL, "https://www.strava.com/activities/"+strconv.FormatInt(a.ID, 10),
		nodeattr.FitnessSource, "strava", nodeattr.FitnessSourceID, strconv.FormatInt(a.ID, 10),
		nodeattr.FitnessDistance, decimal(a.Distance),
		nodeattr.FitnessMovingDuration, strconv.FormatInt(a.MovingTime, 10),
		nodeattr.FitnessElapsedDuration, strconv.FormatInt(a.ElapsedTime, 10),
		nodeattr.FitnessElevationGain, decimal(a.TotalElevationGain),
		"stravaSportType", a.SportType, "stravaActivityType", a.Type,
		"stravaTimezone", a.Timezone, "stravaExternalID", a.ExternalID, "stravaGearID", a.GearID,
		"stravaTrainer", strconv.FormatBool(a.Trainer), "stravaCommute", strconv.FormatBool(a.Commute),
		"stravaManual", strconv.FormatBool(a.Manual), "stravaPrivate", strconv.FormatBool(a.Private),
	); err != nil {
		return err
	}
	if typ == "" {
		if err := clearAttr(obj, nodeattr.FitnessActivityType); err != nil {
			return err
		}
	} else if err := obj.SetAttr(nodeattr.FitnessActivityType, activityType(typ)); err != nil {
		return err
	}
	optional := map[string]*float64{
		nodeattr.FitnessAverageHeartRate: a.AverageHeartrate, nodeattr.FitnessMaxHeartRate: a.MaxHeartrate,
	}
	if a.Calories != nil {
		joules := *a.Calories * 4184
		optional[nodeattr.FitnessEnergy] = &joules
	} else {
		optional[nodeattr.FitnessEnergy] = nil
	}
	if len(a.StartLatlng) == 2 {
		optional[nodeattr.Latitude] = &a.StartLatlng[0]
		optional[nodeattr.Longitude] = &a.StartLatlng[1]
	} else {
		optional[nodeattr.Latitude] = nil
		optional[nodeattr.Longitude] = nil
	}
	for key, val := range optional {
		if val == nil {
			if err := clearAttr(obj, key); err != nil {
				return err
			}
		} else if err := obj.SetAttr(key, decimal(*val)); err != nil {
			return err
		}
	}
	return nil
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
