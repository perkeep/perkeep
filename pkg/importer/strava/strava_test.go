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
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
)

// coreStorage is a synchronous, minimal claim index for importer tests. The
// real importer writes signed schema blobs into memory.Storage; Describe
// returns fresh copies of the attributes reconstructed from those claims.
// No external Perkeep server, host config, or network access is needed.
type coreStorage struct {
	memory.Storage
	mu        sync.Mutex
	attrs     map[blob.Ref]url.Values
	failClaim string // fail the next set-attribute claim for this key
}

var errCoreClaimFailure = errors.New("injected claim storage failure")

func (s *coreStorage) ReceiveBlob(ctx context.Context, ref blob.Ref, src io.Reader) (blob.SizedRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return blob.SizedRef{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var claim struct {
		CamliType string   `json:"camliType"`
		Permanode blob.Ref `json:"permaNode"`
		Type      string   `json:"claimType"`
		Attribute string   `json:"attribute"`
		Value     string   `json:"value"`
	}
	validJSON := json.Unmarshal(data, &claim) == nil
	if claim.CamliType == "claim" && claim.Type == "set-attribute" && claim.Attribute == s.failClaim {
		s.failClaim = ""
		return blob.SizedRef{}, errCoreClaimFailure
	}
	_, alreadyStored := s.Storage.BlobContents(ref)
	sb, err := s.Storage.ReceiveBlob(ctx, ref, bytes.NewReader(data))
	if err != nil || alreadyStored {
		return sb, err
	}
	if !validJSON {
		return sb, nil // raw file content is not necessarily JSON
	}
	if s.attrs == nil {
		s.attrs = make(map[blob.Ref]url.Values)
	}
	if claim.CamliType == "permanode" {
		s.attrs[ref] = make(url.Values)
	}
	if claim.CamliType != "claim" {
		return sb, nil
	}
	attrs := s.attrs[claim.Permanode]
	if attrs == nil {
		return blob.SizedRef{}, fmt.Errorf("claim for unknown permanode %v", claim.Permanode)
	}
	switch claim.Type {
	case "set-attribute":
		attrs.Set(claim.Attribute, claim.Value)
	case "del-attribute":
		if claim.Value == "" {
			attrs.Del(claim.Attribute)
		} else {
			values := attrs[claim.Attribute]
			kept := make([]string, 0, len(values))
			for _, value := range values {
				if value != claim.Value {
					kept = append(kept, value)
				}
			}
			attrs[claim.Attribute] = kept
		}
	default:
		return blob.SizedRef{}, fmt.Errorf("unexpected claim type %q", claim.Type)
	}
	return sb, nil
}

func (s *coreStorage) failNextClaim(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failClaim = key
}

func (s *coreStorage) Describe(_ context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attrs, ok := s.attrs[req.BlobRef]
	if !ok {
		return nil, fmt.Errorf("unknown permanode %v", req.BlobRef)
	}
	copyAttrs := make(url.Values, len(attrs))
	for key, values := range attrs {
		copyAttrs[key] = append([]string(nil), values...)
	}
	return &search.DescribeResponse{Meta: search.MetaMap{
		req.BlobRef.String(): {
			BlobRef: req.BlobRef,
			Permanode: &search.DescribedPermanode{
				Attr: copyAttrs,
			},
		},
	}}, nil
}

func (s *coreStorage) Query(_ context.Context, req *search.SearchQuery) (*search.SearchResult, error) {
	if req.Expression != "attr:camliNodeType:importer attr:importerType:strava" {
		return nil, errors.New("unexpected search query in importer core test")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res := &search.SearchResult{}
	for ref, attrs := range s.attrs {
		if attrs.Get("camliNodeType") == "importer" && attrs.Get("importerType") == "strava" {
			res.Blobs = append(res.Blobs, &search.SearchResultBlob{Blob: ref})
		}
	}
	return res, nil
}

type coreTransport func(*http.Request) (*http.Response, error)

func (f coreTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// coreRateCache can fail cache operations independently of blob uploads.
type coreRateCache struct {
	sorted.KeyValue
	getErr error
	setErr error
}

func (c *coreRateCache) Get(key string) (string, error) {
	if c.getErr != nil {
		return "", c.getErr
	}
	return c.KeyValue.Get(key)
}

func (c *coreRateCache) Set(key, value string) error {
	if c.setErr != nil {
		return c.setErr
	}
	return c.KeyValue.Set(key, value)
}

type coreFixture struct {
	rc         *importer.RunContext
	storage    *coreStorage
	handler    http.Handler
	clientID   string
	hostConfig importer.HostConfig
}

var coreFixtureClientCounter uint64

func newCoreFixture(t *testing.T, transport http.RoundTripper, caches ...sorted.KeyValue) *coreFixture {
	t.Helper()
	var config importer.HostConfig
	if len(caches) > 0 {
		config.Cache = caches[0]
	}
	return newCoreFixtureWithConfig(t, transport, config)
}

func newCoreFixtureWithConfig(t *testing.T, transport http.RoundTripper, config importer.HostConfig) *coreFixture {
	t.Helper()
	// App-wide rate limits must not leak between fixtures or repeated test runs.
	clientID := fmt.Sprintf("test-client-id:%s:%d", t.Name(), atomic.AddUint64(&coreFixtureClientCounter, 1))
	// Reuse a checked-in private key instead of generating an expensive key
	// for each test. It is used only to sign these in-memory test blobs.
	entity, err := jsonsign.EntityFromSecring("26F5ABDA", "../../jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		t.Fatal(err)
	}
	pubRef := blob.RefFromString(pub)
	signer, err := schema.NewSigner(pubRef, strings.NewReader(pub), entity)
	if err != nil {
		t.Fatal(err)
	}
	storage := new(coreStorage)
	if _, err := storage.ReceiveBlob(context.Background(), pubRef, strings.NewReader(pub)); err != nil {
		t.Fatal(err)
	}
	if transport == nil {
		transport = coreTransport(func(req *http.Request) (*http.Response, error) {
			t.Errorf("unexpected HTTP request: %s %s", req.Method, req.URL.Path)
			return nil, errors.New("unexpected HTTP request")
		})
	}
	hostConfig := importer.HostConfig{
		BaseURL:      "https://perkeep.example",
		Prefix:       "/importer/",
		Target:       storage,
		BlobSource:   storage,
		Signer:       signer,
		Search:       storage,
		ClientId:     map[string]string{"strava": clientID},
		ClientSecret: map[string]string{"strava": "test-client-secret"},
		HTTPClient:   &http.Client{Transport: transport},
		Cache:        config.Cache,
		CacheRoot:    config.CacheRoot,
		CacheType:    config.CacheType,
	}
	host, err := importer.NewHost(hostConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	})
	rc, err := importer.CreateAccount(host, "strava")
	if err != nil {
		t.Fatal(err)
	}
	return &coreFixture{
		rc: rc, storage: storage,
		handler:    &httputil.PrefixHandler{Prefix: "/importer/", Handler: host},
		clientID:   clientID,
		hostConfig: hostConfig,
	}
}

func (f *coreFixture) configure(t *testing.T) {
	t.Helper()
	if err := f.rc.AccountNode().SetAttrs(
		importer.AcctAttrUserID, "7",
		importer.AcctAttrAccessToken, "test-access",
		attrRefreshToken, "test-refresh",
		attrTokenExpiry, strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
		attrTokenClientID, f.clientID,
	); err != nil {
		t.Fatal(err)
	}
}

func (f *coreFixture) rateCache(t *testing.T) sorted.KeyValue {
	t.Helper()
	cache, err := f.rc.ImporterCache()
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func (f *coreFixture) rateState(t *testing.T) string {
	t.Helper()
	raw, err := f.rateCache(t).Get(rateLimitCacheKey(f.clientID))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *coreFixture) permanodes(t *testing.T, want int) {
	t.Helper()
	f.storage.mu.Lock()
	defer f.storage.mu.Unlock()
	if got := len(f.storage.attrs); got != want {
		t.Errorf("permanode count = %d; want %d", got, want)
	}
}

func (f *coreFixture) activity(t *testing.T, id int64) *importer.Object {
	t.Helper()
	parent := coreChild(t, f.rc.RootNode(), "activities")
	return coreChild(t, parent, strconv.FormatInt(id, 10))
}

func coreChild(t *testing.T, parent *importer.Object, path string) *importer.Object {
	t.Helper()
	obj, err := parent.ChildPathObjectOrFunc(path, func() (*importer.Object, error) {
		return nil, fmt.Errorf("missing child %q", path)
	})
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func coreAttrs(t *testing.T, obj *importer.Object, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got := obj.Attr(key); got != value {
			t.Errorf("%s = %q; want %q", key, got, value)
		}
	}
}

func coreJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func coreResponse(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}
}

// coreAPI serves only the authenticated athlete's list, details, and streams.
// Its call counts distinguish re-listing the backfill from re-fetching content.
type coreAPI struct {
	t        *testing.T
	perPage  int // zero means the default 100
	pages    map[int][]activity
	details  map[int64][]byte
	streams  map[int64][]byte // absent entry means the API has no streams
	failures map[string]int
	calls    map[string]int
}

func newCoreAPI(t *testing.T) *coreAPI {
	return &coreAPI{
		t: t, pages: make(map[int][]activity), details: make(map[int64][]byte),
		streams: make(map[int64][]byte), failures: make(map[string]int), calls: make(map[string]int),
	}
}

func (api *coreAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	api.t.Helper()
	if req.Method != http.MethodGet || req.URL.Host != "www.strava.com" {
		api.t.Errorf("unexpected request %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-access" {
		api.t.Errorf("Authorization = %q; want test bearer token", got)
	}
	path := strings.TrimPrefix(req.URL.Path, "/api/v3")
	key := path
	if path == "/athlete/activities" {
		key += "?page=" + req.URL.Query().Get("page")
	}
	api.calls[key]++
	if status := api.failures[path]; status != 0 {
		return coreResponse(req, status, []byte(`{"message":"test failure"}`)), nil
	}
	if path == "/athlete/activities" {
		perPage := api.perPage
		if perPage == 0 {
			perPage = 100
		}
		if got := req.URL.Query().Get("per_page"); got != strconv.Itoa(perPage) {
			api.t.Errorf("per_page = %q; want %d", got, perPage)
		}
		page, err := strconv.Atoi(req.URL.Query().Get("page"))
		if err != nil || page < 1 {
			api.t.Errorf("invalid page query %q", req.URL.RawQuery)
		}
		activities := api.pages[page]
		if activities == nil {
			activities = []activity{}
		}
		return coreResponse(req, http.StatusOK, coreJSON(api.t, activities)), nil
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "activities" {
		api.t.Errorf("unexpected API path %q", path)
		return coreResponse(req, http.StatusNotFound, nil), nil
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		api.t.Errorf("invalid activity path %q", path)
	}
	if len(parts) == 2 {
		body, ok := api.details[id]
		if !ok {
			api.t.Errorf("unexpected activity details request %d", id)
		}
		return coreResponse(req, http.StatusOK, body), nil
	}
	if len(parts) == 3 && parts[2] == "streams" {
		if got := req.URL.Query().Get("key_by_type"); got != "true" {
			api.t.Errorf("key_by_type = %q; want true", got)
		}
		body, ok := api.streams[id]
		if !ok {
			return coreResponse(req, http.StatusNotFound, nil), nil
		}
		return coreResponse(req, http.StatusOK, body), nil
	}
	api.t.Errorf("unexpected API path %q", path)
	return coreResponse(req, http.StatusNotFound, nil), nil
}

func coreActivity(id int64) activity {
	calories, average, maximum := 100.5, 142.5, 181.0
	return activity{
		ID: id, Name: "Morning <ride> & coffee", Description: "A workout description",
		Type: "Ride", SportType: "GravelRide",
		StartDate: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
		Distance:  12345.5, MovingTime: 1800, ElapsedTime: 1900, TotalElevationGain: 246.5,
		StartLatlng: []float64{37.5, -122.25}, Calories: &calories,
		AverageHeartrate: &average, MaxHeartrate: &maximum,
		Timezone: "(GMT-08:00) America/Los_Angeles", ExternalID: "workout.fit", GearID: "b123",
	}
}

const coreTrackStreams = `{
  "latlng":{"data":[[37.5,-122.25],[37.6,-122.3]],"series_type":"time","original_size":2,"resolution":"high"},
  "time":{"data":[0,60],"series_type":"time","original_size":2,"resolution":"high"},
  "altitude":{"data":[10,12.5],"series_type":"time","original_size":2,"resolution":"high"},
  "heartrate":{"data":[142,150],"series_type":"time","original_size":2,"resolution":"high"},
  "provider_future_field":{"data":[1,2],"series_type":"time","original_size":2,"resolution":"high"}
}`

func (f *coreFixture) file(t *testing.T, obj *importer.Object, attr, filename string) []byte {
	t.Helper()
	ref, ok := blob.Parse(obj.Attr(attr))
	if !ok {
		t.Fatalf("%s is not a file schema ref: %q", attr, obj.Attr(attr))
	}
	if got := obj.Attr("camliPath:" + filename); got != "" {
		t.Errorf("camliPath:%s = %q; attachments must be direct file-schema attrs only", filename, got)
	}
	reader, err := schema.NewFileReader(context.Background(), f.storage, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if got := reader.FileName(); got != filename {
		t.Errorf("file schema name = %q; want %q", got, filename)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestImporterAccountReadiness(t *testing.T) {
	f := newCoreFixture(t, nil)
	f.configure(t)
	acct := f.rc.AccountNode()
	if ok, err := (imp{}).IsAccountReady(acct); err != nil || !ok {
		t.Fatalf("configured account ready = %v, %v", ok, err)
	}
	if got := (imp{}).SummarizeAccount(acct); got != "Strava athlete 7" {
		t.Errorf("summary = %q", got)
	}
	if err := acct.SetAttr(importer.AcctAttrName, "Test Athlete"); err != nil {
		t.Fatal(err)
	}
	if got := (imp{}).SummarizeAccount(acct); got != "Strava athlete Test Athlete" {
		t.Errorf("named summary = %q", got)
	}
	for _, key := range []string{importer.AcctAttrUserID, importer.AcctAttrAccessToken, attrRefreshToken, attrTokenExpiry} {
		t.Run("missing-"+key, func(t *testing.T) {
			old := acct.Attr(key)
			if err := acct.DelAttr(key, ""); err != nil {
				t.Fatal(err)
			}
			wantReady := key == importer.AcctAttrAccessToken || key == attrTokenExpiry
			if ok, err := (imp{}).IsAccountReady(acct); err != nil || ok != wantReady {
				t.Errorf("ready with missing %s = %v, %v; want %v", key, ok, err, wantReady)
			}
			wantSummary := ""
			if wantReady {
				wantSummary = "Strava athlete Test Athlete"
			}
			if got := (imp{}).SummarizeAccount(acct); got != wantSummary {
				t.Errorf("account summary = %q; want %q", got, wantSummary)
			}
			if err := acct.SetAttr(key, old); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, expiry := range []string{"not-an-integer", "0", "-1", "999999999999999999999"} {
		if err := acct.SetAttr(attrTokenExpiry, expiry); err != nil {
			t.Fatal(err)
		}
		if ok, err := (imp{}).IsAccountReady(acct); err != nil || !ok {
			t.Errorf("refreshable account with expiry %q ready = %v, %v", expiry, ok, err)
		}
	}
	// An expired token is still configured: the run should refresh it.
	if err := acct.SetAttr(attrTokenExpiry, "1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := (imp{}).IsAccountReady(acct); err != nil || !ok {
		t.Errorf("expired but refreshable account ready = %v, %v", ok, err)
	}
}

func TestImporterRunUnconfiguredDoesNotRequestHTTP(t *testing.T) {
	f := newCoreFixture(t, nil)
	if err := (imp{}).Run(f.rc); err == nil {
		t.Fatal("Run of unconfigured account succeeded")
	}
	if got := f.rc.AccountNode().Attr(importer.AcctAttrCompletedVersion); got != "" {
		t.Errorf("failed run marked complete: %q", got)
	}
}

func TestImporterRootDefaultVisibility(t *testing.T) {
	f := newCoreFixture(t, newCoreAPI(t))
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	rootRef, ok := blob.Parse(f.rc.AccountNode().Attr("importRoot"))
	if !ok {
		t.Fatal("account has no import-root reference")
	}
	root, err := f.rc.Host.ObjectFromRef(rootRef)
	if err != nil {
		t.Fatal(err)
	}
	coreAttrs(t, root, map[string]string{nodeattr.Title: "Strava workouts", nodeattr.DefaultVisibility: "hide"})
	activities := coreChild(t, root, "activities")
	coreAttrs(t, activities, map[string]string{nodeattr.Title: "Activities", nodeattr.DefaultVisibility: ""})
	coreAttrs(t, f.rc.AccountNode(), map[string]string{nodeattr.DefaultVisibility: ""})
	f.permanodes(t, 3) // cache access must not create an importer configuration node
}

func TestImporterBackfillRepeatedRunsAndEdits(t *testing.T) {
	api := newCoreAPI(t)
	first, second := coreActivity(101), coreActivity(202)
	api.pages[1], api.pages[2] = []activity{first}, []activity{second}
	// Include a field unknown to the modeled activity: raw archives must
	// preserve the provider's original response, not marshal the Go struct.
	firstJSON := coreJSON(t, first)
	api.details[first.ID] = append(firstJSON[:len(firstJSON)-1], []byte(`,"future_field":{"nested":true}}`)...)
	api.details[second.ID] = coreJSON(t, second)
	api.streams[first.ID], api.streams[second.ID] = []byte(coreTrackStreams), []byte(coreTrackStreams)
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	coreAttrs(t, f.rc.RootNode(), map[string]string{nodeattr.Title: "Strava workouts", nodeattr.DefaultVisibility: "hide"})
	parent := coreChild(t, f.rc.RootNode(), "activities")
	coreAttrs(t, parent, map[string]string{nodeattr.Title: "Activities", nodeattr.DefaultVisibility: ""})
	f.permanodes(t, 3+2) // account, root, collection, and two activities
	firstObj, secondObj := f.activity(t, first.ID), f.activity(t, second.ID)
	firstRef, secondRef := firstObj.PermanodeRef(), secondObj.PermanodeRef()
	fileRefs := map[string]string{}
	for _, attr := range []string{attrActivityJSON, attrStreamsJSON, nodeattr.FitnessTrack} {
		fileRefs[attr] = firstObj.Attr(attr)
	}
	coreAttrs(t, firstObj, map[string]string{
		nodeattr.Type: nodeattr.FitnessActivity, nodeattr.Title: first.Name,
		nodeattr.Description: first.Description, nodeattr.StartDate: "2025-01-02T03:04:05Z",
		nodeattr.FitnessSource: "strava", nodeattr.FitnessSourceID: "101", nodeattr.FitnessActivityType: "ride",
		nodeattr.FitnessDistance: "12345.5", nodeattr.FitnessMovingDuration: "1800",
		nodeattr.FitnessElapsedDuration: "1900", nodeattr.FitnessElevationGain: "246.5",
		nodeattr.FitnessAverageHeartRate: "142.5", nodeattr.FitnessMaxHeartRate: "181",
		nodeattr.FitnessEnergy: "420492", nodeattr.Latitude: "37.5", nodeattr.Longitude: "-122.25",
		nodeattr.URL: "https://www.strava.com/activities/101", "stravaSportType": "GravelRide",
		"stravaActivityType": "Ride", "stravaTimezone": first.Timezone,
		"stravaExternalID": "workout.fit", "stravaGearID": "b123",
		attrImportVersion: runCompleteVersion, attrSummaryHash: summaryHash(&first),
		nodeattr.DefaultVisibility: "",
	})
	for _, file := range []struct {
		attr, name string
		want       []byte
	}{
		{attrActivityJSON, "activity.json", api.details[first.ID]},
		{attrStreamsJSON, "streams.json", []byte(coreTrackStreams)},
	} {
		got := f.file(t, firstObj, file.attr, file.name)
		if !json.Valid(got) || !bytes.Equal(got, file.want) {
			t.Errorf("%s archive does not preserve original valid JSON: %s", file.name, got)
		}
	}
	gpx := f.file(t, firstObj, nodeattr.FitnessTrack, "track.gpx")
	var doc struct {
		XMLName xml.Name
		Points  []struct {
			Time string `xml:"time"`
		} `xml:"trk>trkseg>trkpt"`
	}
	if err := xml.Unmarshal(gpx, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.XMLName.Local != "gpx" || len(doc.Points) != 2 || doc.Points[1].Time != "2025-01-02T03:05:05Z" {
		t.Errorf("unexpected archived GPX: %s", gpx)
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{
		importer.AcctAttrCompletedVersion: runCompleteVersion,
		"importRoot":                      f.rc.RootNode().PermanodeRef().String(),
	})
	wantCalls := map[string]int{
		"/athlete/activities?page=1": 1, "/athlete/activities?page=2": 1, "/athlete/activities?page=3": 1,
		"/activities/101": 1, "/activities/101/streams": 1, "/activities/202": 1, "/activities/202/streams": 1,
	}
	if !reflect.DeepEqual(api.calls, wantCalls) {
		t.Fatalf("first run requests = %v; want %v", api.calls, wantCalls)
	}
	before := f.storage.NumBlobs()
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 3; page++ {
		wantCalls[fmt.Sprintf("/athlete/activities?page=%d", page)]++
	}
	if !reflect.DeepEqual(api.calls, wantCalls) {
		t.Errorf("unchanged run re-fetched details/streams: %v; want %v", api.calls, wantCalls)
	}
	// Polling unchanged activities updates only the mutable local cache.
	if got := f.storage.NumBlobs(); got != before {
		t.Errorf("unchanged run wrote %d blobs; want none", got-before)
	}
	if f.activity(t, first.ID).PermanodeRef() != firstRef || f.activity(t, second.ID).PermanodeRef() != secondRef {
		t.Error("unchanged run replaced activity permanodes")
	}
	f.permanodes(t, 3+2)
	for attr, ref := range fileRefs {
		if got := f.activity(t, first.ID).Attr(attr); got != ref {
			t.Errorf("unchanged run replaced %s file ref: %q; want %q", attr, got, ref)
		}
	}
	// An edited summary invalidates just that activity. Its stable ID path
	// must retain the existing permanode, not create a second activity.
	// Distance affects the details JSON but not the generated GPX.
	first.Distance++
	api.pages[1] = []activity{first}
	api.details[first.ID] = coreJSON(t, first)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 3; page++ {
		wantCalls[fmt.Sprintf("/athlete/activities?page=%d", page)]++
	}
	wantCalls["/activities/101"]++
	wantCalls["/activities/101/streams"]++
	if !reflect.DeepEqual(api.calls, wantCalls) {
		t.Errorf("edited run requests = %v; want %v", api.calls, wantCalls)
	}
	firstObj, secondObj = f.activity(t, first.ID), f.activity(t, second.ID)
	if firstObj.PermanodeRef() != firstRef || secondObj.PermanodeRef() != secondRef {
		t.Error("activity permanode changed across runs")
	}
	f.permanodes(t, 3+2)
	coreAttrs(t, firstObj, map[string]string{
		nodeattr.Title: first.Name, nodeattr.FitnessDistance: decimal(first.Distance), attrSummaryHash: summaryHash(&first),
	})
	if got := firstObj.Attr(attrActivityJSON); got == fileRefs[attrActivityJSON] {
		t.Error("edited activity retained the old details file ref")
	}
	for _, attr := range []string{attrStreamsJSON, nodeattr.FitnessTrack} {
		if got := firstObj.Attr(attr); got != fileRefs[attr] {
			t.Errorf("%s file ref changed with unchanged content: %q; want %q", attr, got, fileRefs[attr])
		}
	}
	if got := f.file(t, firstObj, attrActivityJSON, "activity.json"); !bytes.Equal(got, api.details[first.ID]) {
		t.Error("edited activity JSON did not update")
	}
	if got := f.file(t, firstObj, attrStreamsJSON, "streams.json"); !bytes.Equal(got, api.streams[first.ID]) {
		t.Error("unchanged streams JSON changed after edit")
	}
	if got := f.file(t, firstObj, nodeattr.FitnessTrack, "track.gpx"); !bytes.Equal(got, gpx) {
		t.Error("unchanged GPX changed after edit")
	}
}

func TestImporterRefreshRemovesLegacyAttachmentPaths(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wrappers  bool
		noStreams bool
	}{
		{name: "wrapper-permanodes", wrappers: true},
		{name: "direct-file-paths"},
		{name: "removed-wrapper-attachments", wrappers: true, noStreams: true},
		{name: "removed-direct-file-attachments", noStreams: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newCoreAPI(t)
			a := coreActivity(101)
			api.pages[1], api.details[a.ID], api.streams[a.ID] = []activity{a}, coreJSON(t, a), []byte(coreTrackStreams)
			f := newCoreFixture(t, api)
			f.configure(t)
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			obj := f.activity(t, a.ID)
			activityRef := obj.PermanodeRef()
			// Reconstruct the old layout and completion markers while keeping
			// the same summary. Version 2 must refresh even an unchanged item.
			type oldAttachment struct {
				obj           *importer.Object
				filename, ref string
			}
			var oldAttachments []oldAttachment
			for _, file := range []struct{ attr, filename string }{
				{attrActivityJSON, "activity.json"},
				{attrStreamsJSON, "streams.json"},
				{nodeattr.FitnessTrack, "track.gpx"},
			} {
				ref := obj.Attr(file.attr)
				if tc.wrappers {
					child, err := obj.ChildPathObject(file.filename)
					if err != nil {
						t.Fatal(err)
					}
					if err := child.SetAttrs(nodeattr.Title, file.filename, nodeattr.CamliContent, ref); err != nil {
						t.Fatal(err)
					}
					oldAttachments = append(oldAttachments, oldAttachment{child, file.filename, ref})
				} else if err := obj.SetAttr("camliPath:"+file.filename, ref); err != nil {
					t.Fatal(err)
				}
			}
			if err := obj.SetAttr(attrImportVersion, "1"); err != nil {
				t.Fatal(err)
			}
			if err := f.rc.AccountNode().SetAttr(importer.AcctAttrCompletedVersion, "1"); err != nil {
				t.Fatal(err)
			}
			wantPermanodes := 3 + 1 + len(oldAttachments)
			f.permanodes(t, wantPermanodes)
			if tc.noStreams {
				delete(api.streams, a.ID)
			}
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			obj = f.activity(t, a.ID)
			if obj.PermanodeRef() != activityRef {
				t.Error("layout migration replaced the activity permanode")
			}
			f.permanodes(t, wantPermanodes) // refresh must not create any new child
			coreAttrs(t, obj, map[string]string{
				attrImportVersion: "2", attrSummaryHash: summaryHash(&a),
				"camliPath:activity.json": "", "camliPath:streams.json": "", "camliPath:track.gpx": "",
			})
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: "2"})
			if got := f.file(t, obj, attrActivityJSON, "activity.json"); !bytes.Equal(got, api.details[a.ID]) {
				t.Error("migrated details file did not preserve raw JSON")
			}
			if tc.noStreams {
				coreAttrs(t, obj, map[string]string{attrStreamsJSON: "", nodeattr.FitnessTrack: ""})
			} else {
				if got := f.file(t, obj, attrStreamsJSON, "streams.json"); !bytes.Equal(got, api.streams[a.ID]) {
					t.Error("migrated streams file did not preserve raw JSON")
				}
				f.file(t, obj, nodeattr.FitnessTrack, "track.gpx")
			}
			for _, old := range oldAttachments {
				// Legacy wrappers may remain as historical nodes, but imports
				// neither rewrite nor hide them; only their paths are unlinked.
				child, err := f.rc.Host.ObjectFromRef(old.obj.PermanodeRef())
				if err != nil {
					t.Fatal(err)
				}
				coreAttrs(t, child, map[string]string{
					nodeattr.Title: old.filename, nodeattr.CamliContent: old.ref, nodeattr.DefaultVisibility: "",
				})
			}
			if api.calls["/activities/101"] != 2 || api.calls["/activities/101/streams"] != 2 {
				t.Errorf("old version with unchanged summary was not refreshed: %v", api.calls)
			}
		})
	}
}

func TestAttachEmptyClearsSemanticAttrAndLegacyPath(t *testing.T) {
	for _, file := range []struct{ attr, filename string }{
		{attrActivityJSON, "activity.json"},
		{attrStreamsJSON, "streams.json"},
		{nodeattr.FitnessTrack, "track.gpx"},
	} {
		t.Run(file.filename, func(t *testing.T) {
			f := newCoreFixture(t, nil)
			obj := f.rc.RootNode()
			r := &run{f.rc}
			if err := r.attach(obj, file.attr, file.filename, []byte("old attachment")); err != nil {
				t.Fatal(err)
			}
			ref := obj.Attr(file.attr)
			if err := obj.SetAttr("camliPath:"+file.filename, ref); err != nil {
				t.Fatal(err)
			}
			if err := r.attach(obj, file.attr, file.filename, nil); err != nil {
				t.Fatal(err)
			}
			coreAttrs(t, obj, map[string]string{file.attr: "", "camliPath:" + file.filename: ""})
			before := f.storage.NumBlobs()
			if err := r.attach(obj, file.attr, file.filename, []byte{}); err != nil {
				t.Fatal(err)
			}
			if got := f.storage.NumBlobs(); got != before {
				t.Errorf("clearing absent attachments wrote %d blobs", got-before)
			}
		})
	}
}

func TestImporterFailedActivityResumes(t *testing.T) {
	for _, failure := range []string{"detail-http", "streams-http", "invalid-start", "invalid-track", "invalid-distance", "invalid-duration", "invalid-coordinates", "invalid-calories"} {
		t.Run(failure, func(t *testing.T) {
			api := newCoreAPI(t)
			first, second := coreActivity(101), coreActivity(202)
			api.pages[1] = []activity{first, second}
			api.details[first.ID], api.details[second.ID] = coreJSON(t, first), coreJSON(t, second)
			api.streams[first.ID], api.streams[second.ID] = []byte(coreTrackStreams), []byte(coreTrackStreams)
			switch failure {
			case "detail-http":
				api.failures["/activities/202"] = http.StatusBadGateway
			case "streams-http":
				// Generic failures can resume immediately; 429 intentionally
				// retains an app-wide cooldown and is covered by limiter tests.
				api.failures["/activities/202/streams"] = http.StatusBadGateway
			case "invalid-start":
				broken := second
				broken.StartDate = time.Time{}
				api.details[second.ID] = coreJSON(t, broken)
			case "invalid-track":
				api.streams[second.ID] = []byte(`{"latlng":{"data":[[200,0]],"series_type":"time"}}`)
			case "invalid-distance", "invalid-duration", "invalid-coordinates", "invalid-calories":
				broken := second
				switch failure {
				case "invalid-distance":
					broken.Distance = -1
				case "invalid-duration":
					broken.MovingTime = broken.ElapsedTime + 1
				case "invalid-coordinates":
					broken.StartLatlng = []float64{91, 0}
				case "invalid-calories":
					calories := -1.0
					broken.Calories = &calories
				}
				api.details[second.ID] = coreJSON(t, broken)
			}
			f := newCoreFixture(t, api)
			f.configure(t)
			if err := (imp{}).Run(f.rc); err == nil {
				t.Fatal("Run with failing activity succeeded")
			}
			firstObj, secondObj := f.activity(t, first.ID), f.activity(t, second.ID)
			ref := secondObj.PermanodeRef()
			coreAttrs(t, firstObj, map[string]string{attrImportVersion: runCompleteVersion})
			coreAttrs(t, secondObj, map[string]string{
				attrImportVersion: "", attrSummaryHash: "", nodeattr.Type: "", attrActivityJSON: "", attrStreamsJSON: "",
				nodeattr.FitnessTrack: "", "camliPath:activity.json": "", "camliPath:streams.json": "", "camliPath:track.gpx": "",
			})
			if strings.HasPrefix(failure, "invalid-") && failure != "invalid-track" && api.calls["/activities/202/streams"] != 0 {
				t.Error("invalid detailed metrics were not rejected before fetching streams")
			}
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
			delete(api.failures, "/activities/202")
			delete(api.failures, "/activities/202/streams")
			api.details[second.ID], api.streams[second.ID] = coreJSON(t, second), []byte(coreTrackStreams)
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			secondObj = f.activity(t, second.ID)
			if secondObj.PermanodeRef() != ref {
				t.Error("resume created a different permanode")
			}
			if api.calls["/activities/101"] != 1 || api.calls["/activities/101/streams"] != 1 {
				t.Error("resume re-fetched previously completed activity")
			}
			if api.calls["/activities/202"] != 2 {
				t.Errorf("failed activity detail requests = %d; want 2", api.calls["/activities/202"])
			}
			coreAttrs(t, secondObj, map[string]string{attrImportVersion: runCompleteVersion, attrSummaryHash: summaryHash(&second)})
			coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: runCompleteVersion})
		})
	}
}

func TestImporterFailedEditClearsCompletionAndStaleOptionalAttrs(t *testing.T) {
	api := newCoreAPI(t)
	a := coreActivity(101)
	api.pages[1], api.details[a.ID], api.streams[a.ID] = []activity{a}, coreJSON(t, a), []byte(coreTrackStreams)
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	ref := f.activity(t, a.ID).PermanodeRef()
	a.Name = "Private indoor manual workout"
	a.SportType, a.Private, a.Manual, a.Trainer = "Workout", true, true, true
	a.Calories, a.AverageHeartrate, a.MaxHeartrate, a.StartLatlng = nil, nil, nil, nil
	api.pages[1], api.details[a.ID] = []activity{a}, coreJSON(t, a)
	api.failures["/activities/101/streams"] = http.StatusBadGateway
	if err := (imp{}).Run(f.rc); err == nil {
		t.Fatal("failed edit succeeded")
	}
	coreAttrs(t, f.activity(t, a.ID), map[string]string{attrImportVersion: ""})
	delete(api.failures, "/activities/101/streams")
	delete(api.streams, a.ID) // manual activity returns HTTP 404 for streams
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	obj := f.activity(t, a.ID)
	if obj.PermanodeRef() != ref {
		t.Error("editing activity replaced its permanode")
	}
	coreAttrs(t, obj, map[string]string{
		nodeattr.Title: a.Name, nodeattr.FitnessActivityType: "workout",
		"stravaPrivate": "true", "stravaManual": "true", "stravaTrainer": "true",
		nodeattr.FitnessEnergy: "", nodeattr.FitnessAverageHeartRate: "", nodeattr.FitnessMaxHeartRate: "",
		nodeattr.Latitude: "", nodeattr.Longitude: "", nodeattr.FitnessTrack: "",
		attrStreamsJSON: "", "camliPath:track.gpx": "", "camliPath:streams.json": "",
		attrSummaryHash: summaryHash(&a), attrImportVersion: runCompleteVersion,
	})
	if got := f.file(t, obj, attrActivityJSON, "activity.json"); !bytes.Equal(got, api.details[a.ID]) {
		t.Error("activity JSON was not refreshed")
	}
	if api.calls["/activities/101"] != 3 || api.calls["/activities/101/streams"] != 3 {
		t.Errorf("failed edit was not retried: %v", api.calls)
	}
}

func TestImporterPrivateAndManualWithoutGPS(t *testing.T) {
	api := newCoreAPI(t)
	private, manual := coreActivity(101), coreActivity(202)
	private.Private, manual.Manual = true, true
	private.StartLatlng, manual.StartLatlng = nil, nil
	api.pages[1] = []activity{private, manual}
	api.details[private.ID], api.details[manual.ID] = coreJSON(t, private), coreJSON(t, manual)
	api.streams[private.ID] = []byte(`{"heartrate":{"data":[142,150],"series_type":"time"}}`)
	f := newCoreFixture(t, api)
	f.configure(t)
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	for _, a := range []activity{private, manual} {
		obj := f.activity(t, a.ID)
		coreAttrs(t, obj, map[string]string{
			attrImportVersion: runCompleteVersion, nodeattr.FitnessTrack: "", "camliPath:track.gpx": "",
			"stravaPrivate": strconv.FormatBool(a.Private), "stravaManual": strconv.FormatBool(a.Manual),
		})
		if !json.Valid(f.file(t, obj, attrActivityJSON, "activity.json")) {
			t.Error("no-GPS activity JSON is invalid")
		}
	}
	if got := f.file(t, f.activity(t, private.ID), attrStreamsJSON, "streams.json"); !bytes.Equal(got, api.streams[private.ID]) {
		t.Error("non-GPS stream data was not archived")
	}
	coreAttrs(t, f.activity(t, manual.ID), map[string]string{attrStreamsJSON: "", "camliPath:streams.json": ""})
}

func TestImporterRotatingRefreshTokenPersistence(t *testing.T) {
	var f *coreFixture
	var calls []string
	expiry := time.Now().Add(2 * time.Hour).Unix()
	transport := coreTransport(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.Path)
		switch req.URL.Path {
		case "/oauth/token":
			if req.Method != http.MethodPost {
				t.Errorf("refresh method = %s", req.Method)
			}
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			want := url.Values{
				"client_id": {f.clientID}, "client_secret": {"test-client-secret"},
				"refresh_token": {"test-refresh"}, "grant_type": {"refresh_token"},
			}
			if !reflect.DeepEqual(req.PostForm, want) {
				t.Errorf("refresh form = %v; want %v", req.PostForm, want)
			}
			return coreResponse(req, http.StatusOK, coreJSON(t, tokenResponse{
				AccessToken: "rotated-access", RefreshToken: "rotated-refresh", ExpiresAt: expiry,
			})), nil
		case "/api/v3/athlete/activities":
			// Check persistence before the first use of the new access token.
			coreAttrs(t, f.rc.AccountNode(), map[string]string{
				attrRefreshToken: "rotated-refresh", importer.AcctAttrAccessToken: "rotated-access",
				attrTokenExpiry: strconv.FormatInt(expiry, 10),
			})
			reloaded, err := f.rc.Host.ObjectFromRef(f.rc.AccountNode().PermanodeRef())
			if err != nil {
				t.Fatal(err)
			}
			coreAttrs(t, reloaded, map[string]string{attrRefreshToken: "rotated-refresh"})
			if got := req.Header.Get("Authorization"); got != "Bearer rotated-access" {
				t.Errorf("post-refresh authorization = %q", got)
			}
			return coreResponse(req, http.StatusOK, []byte("[]")), nil
		default:
			t.Errorf("unexpected refresh-test request %s", req.URL.Path)
			return coreResponse(req, http.StatusNotFound, nil), nil
		}
	})
	f = newCoreFixture(t, transport)
	f.configure(t)
	if err := f.rc.AccountNode().SetAttr(attrTokenExpiry, strconv.FormatInt(time.Now().Add(30*time.Second).Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	if err := (imp{}).Run(f.rc); err != nil {
		t.Fatal(err)
	}
	want := []string{"/oauth/token", "/api/v3/athlete/activities", "/api/v3/athlete/activities"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("refresh requests = %v; want %v", calls, want)
	}
}

func TestImporterHTTPContextCancellationDoesNotComplete(t *testing.T) {
	calls := 0
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Context() == nil {
			t.Error("API request has no context")
		}
		return nil, context.Canceled
	}))
	f.configure(t)
	if err := (imp{}).Run(f.rc); !errors.Is(err, context.Canceled) {
		t.Errorf("Run cancellation error = %v; want errors.Is(context.Canceled)", err)
	}
	if calls != 1 {
		t.Errorf("canceled run made %d HTTP requests; want 1", calls)
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{importer.AcctAttrCompletedVersion: ""})
}

func TestImporterCallbackRequestAccount(t *testing.T) {
	ref := blob.RefFromString("test-account")
	valid := "acct:" + ref.String() + ":" + strings.Repeat("a", 64)
	for _, state := range []string{"", ref.String(), "acct:" + ref.String(), "acct:invalid:" + strings.Repeat("a", 64), valid + ":extra", strings.TrimSuffix(valid, "a")} {
		req := httptest.NewRequest(http.MethodGet, "/callback?"+url.Values{"state": {state}}.Encode(), nil)
		if got, err := (imp{}).CallbackRequestAccount(req); err == nil || got.Valid() {
			t.Errorf("CallbackRequestAccount(%q) = %v, %v; want error", state, got, err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/callback?"+url.Values{"state": {valid}}.Encode(), nil)
	if got, err := (imp{}).CallbackRequestAccount(req); err != nil || got != ref {
		t.Errorf("valid CallbackRequestAccount = %v, %v; want %v", got, err, ref)
	}
}

func TestImporterCallbackRejectsInvalidStateAndPermission(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		change  func(url.Values, *importer.Object) error
		status  int
		consume bool
	}{
		{name: "wrong-method", method: http.MethodPost, status: http.StatusMethodNotAllowed},
		{name: "missing-state", change: func(v url.Values, _ *importer.Object) error { v.Del("state"); return nil }, status: http.StatusBadRequest},
		{name: "wrong-account", change: func(v url.Values, _ *importer.Object) error {
			v.Set("state", "acct:"+blob.RefFromString("another account").String()+":"+strings.Repeat("a", 64))
			return nil
		}, status: http.StatusBadRequest},
		{name: "wrong-nonce", change: func(v url.Values, a *importer.Object) error {
			v.Set("state", "acct:"+a.PermanodeRef().String()+":"+strings.Repeat("b", 64))
			return nil
		}, status: http.StatusBadRequest},
		{name: "expired", change: func(_ url.Values, a *importer.Object) error {
			return a.SetAttr(attrOAuthStateExpiry, strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10))
		}, status: http.StatusBadRequest},
		{name: "missing-expiry", change: func(_ url.Values, a *importer.Object) error {
			return a.DelAttr(attrOAuthStateExpiry, "")
		}, status: http.StatusBadRequest},
		{name: "missing-pending-state", change: func(_ url.Values, a *importer.Object) error {
			return a.DelAttr(attrOAuthState, "")
		}, status: http.StatusBadRequest},
		{name: "denied", change: func(v url.Values, _ *importer.Object) error { v.Set("error", "access_denied"); return nil }, status: http.StatusBadRequest, consume: true},
		{name: "missing-code", change: func(v url.Values, _ *importer.Object) error { v.Del("code"); return nil }, status: http.StatusBadRequest, consume: true},
		{name: "missing-scope", change: func(v url.Values, _ *importer.Object) error { v.Del("scope"); return nil }, status: http.StatusBadRequest, consume: true},
		{name: "insufficient-scope", change: func(v url.Values, _ *importer.Object) error { v.Set("scope", "read,activity:read"); return nil }, status: http.StatusBadRequest, consume: true},
		{name: "scope-substring", change: func(v url.Values, _ *importer.Object) error { v.Set("scope", "not_activity:read_all"); return nil }, status: http.StatusBadRequest, consume: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCoreFixture(t, nil) // any attempted token exchange fails the test
			acct := f.rc.AccountNode()
			state := "acct:" + acct.PermanodeRef().String() + ":" + strings.Repeat("a", 64)
			if err := acct.SetAttrs(attrOAuthState, state, attrOAuthStateExpiry, strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)); err != nil {
				t.Fatal(err)
			}
			params := url.Values{"state": {state}, "code": {"test-code"}, "scope": {"read,activity:read_all"}}
			if tt.change != nil {
				if err := tt.change(params, acct); err != nil {
					t.Fatal(err)
				}
			}
			before := acct.Attr(attrOAuthState)
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/callback?"+params.Encode(), nil)
			(imp{}).ServeCallback(w, req, &importer.SetupContext{Context: context.Background(), Host: f.rc.Host, AccountNode: acct})
			if w.Code != tt.status {
				t.Errorf("callback status = %d; want %d: %s", w.Code, tt.status, w.Body.String())
			}
			wantState := before
			if tt.consume {
				wantState = ""
			}
			coreAttrs(t, acct, map[string]string{attrOAuthState: wantState, importer.AcctAttrAccessToken: "", attrRefreshToken: ""})
		})
	}
}

func (f *coreFixture) setup(t *testing.T) string {
	t.Helper()
	acct := f.rc.AccountNode()
	body := strings.NewReader("mode=login")
	req := httptest.NewRequest(http.MethodPost, "/importer/strava/"+acct.PermanodeRef().String(), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("setup status = %d: %s", w.Code, w.Body.String())
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := location.Scheme + "://" + location.Host + location.Path; got != authURL {
		t.Errorf("authorization location = %q; want %q", got, authURL)
	}
	params := location.Query()
	for key, want := range map[string]string{
		"client_id": f.clientID, "response_type": "code", "scope": "activity:read_all",
		"redirect_uri": "https://perkeep.example/importer/strava/callback", "approval_prompt": "force",
	} {
		if got := params.Get(key); got != want {
			t.Errorf("authorization %s = %q; want %q", key, got, want)
		}
	}
	state := params.Get("state")
	prefix := "acct:" + acct.PermanodeRef().String() + ":"
	if !strings.HasPrefix(state, prefix) {
		t.Fatalf("authorization state %q has wrong account", state)
	}
	nonce, err := hex.DecodeString(strings.TrimPrefix(state, prefix))
	if err != nil || len(nonce) != 32 {
		t.Errorf("authorization nonce = %q; want 32 random bytes", state)
	}
	coreAttrs(t, acct, map[string]string{attrOAuthState: state})
	expiry, err := strconv.ParseInt(acct.Attr(attrOAuthStateExpiry), 10, 64)
	remaining := time.Until(time.Unix(expiry, 0))
	if err != nil || remaining < 9*time.Minute || remaining > 10*time.Minute {
		t.Errorf("pending state expiry = %q (%v remaining)", acct.Attr(attrOAuthStateExpiry), remaining)
	}
	return state
}

func (f *coreFixture) callback(state string) *httptest.ResponseRecorder {
	params := url.Values{"state": {state}, "code": {"test-code"}, "scope": {"read,activity:read_all"}}
	req := httptest.NewRequest(http.MethodGet, "/importer/strava/callback?"+params.Encode(), nil)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func TestImporterOAuthSetupCallbackAndReplay(t *testing.T) {
	exchanges := 0
	expiry := time.Now().Add(time.Hour).Unix()
	var f *coreFixture
	f = newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		exchanges++
		if req.URL.String() != tokenURL || req.Method != http.MethodPost {
			t.Errorf("token exchange request = %s %s", req.Method, req.URL)
		}
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		want := url.Values{
			"client_id": {f.clientID}, "client_secret": {"test-client-secret"},
			"code": {"test-code"}, "grant_type": {"authorization_code"},
		}
		if !reflect.DeepEqual(req.PostForm, want) {
			t.Errorf("code exchange form = %v; want %v", req.PostForm, want)
		}
		return coreResponse(req, http.StatusOK, coreJSON(t, tokenResponse{
			AccessToken: "oauth-access", RefreshToken: "oauth-refresh", ExpiresAt: expiry,
			Athlete: athlete{ID: 7, Username: "test-athlete", Firstname: "Test", Lastname: "Athlete"},
		})), nil
	}))
	firstState := f.setup(t)
	state := f.setup(t)
	if state == firstState {
		t.Error("repeated setup reused its nonce")
	}
	// A prior setup's state cannot complete a later setup.
	if w := f.callback(firstState); w.Code != http.StatusBadRequest {
		t.Errorf("superseded callback status = %d; want 400", w.Code)
	}
	w := f.callback(state)
	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d: %s", w.Code, w.Body.String())
	}
	wantURL := "https://perkeep.example/importer/strava/" + f.rc.AccountNode().PermanodeRef().String()
	if got := w.Header().Get("Location"); got != wantURL {
		t.Errorf("callback redirect = %q; want %q", got, wantURL)
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{
		importer.AcctAttrUserID: "7", importer.AcctAttrUserName: "test-athlete",
		importer.AcctAttrGivenName: "Test", importer.AcctAttrFamilyName: "Athlete",
		importer.AcctAttrName: "Test Athlete", importer.AcctAttrAccessToken: "oauth-access",
		attrRefreshToken: "oauth-refresh", attrTokenExpiry: strconv.FormatInt(expiry, 10), attrOAuthState: "",
	})
	if ready, err := (imp{}).IsAccountReady(f.rc.AccountNode()); err != nil || !ready {
		t.Errorf("account not ready after callback: %v, %v", ready, err)
	}
	if w := f.callback(state); w.Code != http.StatusBadRequest {
		t.Errorf("replayed callback status = %d; want 400", w.Code)
	}
	if exchanges != 1 {
		t.Errorf("authorization exchanges = %d; want 1", exchanges)
	}
}

func TestImporterCallbackCannotSwitchAthlete(t *testing.T) {
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		return coreResponse(req, http.StatusOK, coreJSON(t, tokenResponse{
			AccessToken: "other-access", RefreshToken: "other-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(),
			Athlete: athlete{ID: 8, Firstname: "Another"},
		})), nil
	}))
	f.configure(t)
	state := f.setup(t)
	w := f.callback(state)
	if w.Code != http.StatusBadRequest {
		t.Errorf("switch athlete status = %d; want 400: %s", w.Code, w.Body.String())
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{
		importer.AcctAttrUserID: "7", importer.AcctAttrAccessToken: "test-access",
		attrRefreshToken: "test-refresh", attrOAuthState: "",
	})
}

func TestImporterConcurrentCallbacksExchangeStateOnlyOnce(t *testing.T) {
	var exchanges atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		exchanges.Add(1)
		<-release
		return coreResponse(req, http.StatusOK, coreJSON(t, tokenResponse{
			AccessToken: "oauth-access", RefreshToken: "oauth-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(),
			Athlete: athlete{ID: 7},
		})), nil
	}))
	state := f.setup(t)
	const requests = 8
	results := make(chan *httptest.ResponseRecorder, requests)
	for range requests {
		go func() {
			entered <- struct{}{}
			results <- f.callback(state)
		}()
	}
	// Hold the first exchange while the competing callbacks are launched.
	for range requests {
		<-entered
	}
	close(release)
	successes := 0
	for range requests {
		select {
		case w := <-results:
			switch w.Code {
			case http.StatusFound:
				successes++
			case http.StatusBadRequest:
			default:
				t.Errorf("concurrent callback status = %d: %s", w.Code, w.Body.String())
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent callbacks did not finish")
		}
	}
	if successes != 1 || exchanges.Load() != 1 {
		t.Errorf("concurrent callback successes = %d, token exchanges = %d; want 1 each", successes, exchanges.Load())
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{attrOAuthState: "", attrRefreshToken: "oauth-refresh"})
}

func TestImporterConcurrentRefreshOnlyRotatesOnce(t *testing.T) {
	var refreshes atomic.Int32
	f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != tokenURL {
			t.Errorf("unexpected concurrent refresh request: %s", req.URL)
		}
		refreshes.Add(1)
		return coreResponse(req, http.StatusOK, coreJSON(t, tokenResponse{
			AccessToken: "rotated-access", RefreshToken: "rotated-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(),
		})), nil
	}))
	f.configure(t)
	if err := f.rc.AccountNode().SetAttr(attrTokenExpiry, "1"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		client *apiClient
		err    error
	}
	const requests = 8
	start := make(chan struct{})
	results := make(chan result, requests)
	for range requests {
		go func() {
			<-start
			client, err := (&run{f.rc}).client()
			results <- result{client, err}
		}()
	}
	close(start)
	for range requests {
		select {
		case result := <-results:
			if result.err != nil {
				t.Errorf("concurrent refresh: %v", result.err)
			} else if result.client.accessToken != "rotated-access" {
				t.Errorf("concurrent client access token = %q", result.client.accessToken)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent refresh did not finish")
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("concurrent refresh exchanges = %d; want 1", got)
	}
	coreAttrs(t, f.rc.AccountNode(), map[string]string{attrRefreshToken: "rotated-refresh"})
}

func TestImporterOptionalZeroAndAbsentValues(t *testing.T) {
	f := newCoreFixture(t, nil)
	obj, err := f.rc.Host.NewObject()
	if err != nil {
		t.Fatal(err)
	}
	a := coreActivity(101)
	if err := setActivityAttrs(obj, &a); err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	a.AverageHeartrate, a.MaxHeartrate, a.Calories = &zero, &zero, &zero
	a.StartLatlng = []float64{0, 0}
	a.SportType, a.Type, a.Description = "", "", ""
	if err := setActivityAttrs(obj, &a); err != nil {
		t.Fatal(err)
	}
	coreAttrs(t, obj, map[string]string{
		nodeattr.FitnessEnergy: "0", nodeattr.FitnessAverageHeartRate: "0", nodeattr.FitnessMaxHeartRate: "0",
		nodeattr.Latitude: "0", nodeattr.Longitude: "0", nodeattr.FitnessActivityType: "", nodeattr.Description: "",
	})
	a.AverageHeartrate, a.MaxHeartrate, a.Calories, a.StartLatlng = nil, nil, nil, nil
	if err := setActivityAttrs(obj, &a); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{nodeattr.FitnessEnergy, nodeattr.FitnessAverageHeartRate, nodeattr.FitnessMaxHeartRate, nodeattr.Latitude, nodeattr.Longitude, nodeattr.FitnessActivityType} {
		if got := obj.Attrs(key); len(got) != 0 {
			t.Errorf("absent %s retains attribute values: %v", key, got)
		}
	}
	// The provider's legacy type is still a useful fallback.
	a.Type = "Walk"
	if err := setActivityAttrs(obj, &a); err != nil {
		t.Fatal(err)
	}
	coreAttrs(t, obj, map[string]string{nodeattr.FitnessActivityType: "walk"})
	a.SportType = "FutureSport"
	if err := setActivityAttrs(obj, &a); err != nil {
		t.Fatal(err)
	}
	coreAttrs(t, obj, map[string]string{nodeattr.FitnessActivityType: "other", "stravaSportType": "FutureSport"})
}

func TestImporterValidateActivity(t *testing.T) {
	tests := []struct {
		name   string
		change func(*activity)
	}{
		{"negative-distance", func(a *activity) { a.Distance = -1 }},
		{"nan-distance", func(a *activity) { a.Distance = math.NaN() }},
		{"infinite-distance", func(a *activity) { a.Distance = math.Inf(1) }},
		{"negative-elevation", func(a *activity) { a.TotalElevationGain = -1 }},
		{"nan-elevation", func(a *activity) { a.TotalElevationGain = math.NaN() }},
		{"negative-moving-time", func(a *activity) { a.MovingTime = -1 }},
		{"negative-elapsed-time", func(a *activity) { a.ElapsedTime = -1 }},
		{"moving-exceeds-elapsed", func(a *activity) { a.MovingTime = a.ElapsedTime + 1 }},
		{"negative-average-heart-rate", func(a *activity) { *a.AverageHeartrate = -1 }},
		{"infinite-max-heart-rate", func(a *activity) { *a.MaxHeartrate = math.Inf(1) }},
		{"negative-calories", func(a *activity) { *a.Calories = -1 }},
		{"nan-calories", func(a *activity) { *a.Calories = math.NaN() }},
		{"energy-overflow", func(a *activity) { *a.Calories = math.MaxFloat64 }},
		{"short-coordinate", func(a *activity) { a.StartLatlng = []float64{0} }},
		{"long-coordinate", func(a *activity) { a.StartLatlng = []float64{0, 0, 0} }},
		{"latitude-out-of-range", func(a *activity) { a.StartLatlng = []float64{91, 0} }},
		{"longitude-out-of-range", func(a *activity) { a.StartLatlng = []float64{0, 180} }},
		{"nonfinite-coordinate", func(a *activity) { a.StartLatlng = []float64{0, math.NaN()} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := coreActivity(101)
			tt.change(&a)
			if err := validateActivity(&a); err == nil {
				t.Error("invalid detailed activity was accepted")
			}
		})
	}
	a := coreActivity(101)
	if err := validateActivity(&a); err != nil {
		t.Errorf("valid detailed activity rejected: %v", err)
	}
	zero := 0.0
	a.Distance, a.TotalElevationGain, a.MovingTime, a.ElapsedTime = 0, 0, 0, 0
	a.AverageHeartrate, a.MaxHeartrate, a.Calories = &zero, &zero, &zero
	a.StartLatlng = []float64{0, 0}
	if err := validateActivity(&a); err != nil {
		t.Errorf("valid zero metrics rejected: %v", err)
	}
	a.AverageHeartrate, a.MaxHeartrate, a.Calories, a.StartLatlng = nil, nil, nil, nil
	if err := validateActivity(&a); err != nil {
		t.Errorf("absent optional metrics rejected: %v", err)
	}
}

func TestImporterRefreshOnlyAndPartialTokenWritesRecover(t *testing.T) {
	for _, scenario := range []string{"refresh-only", "invalid-expiry", "missing-access", "access-claim-fails", "expiry-claim-fails"} {
		t.Run(scenario, func(t *testing.T) {
			var f *coreFixture
			var calls []string
			expiry := time.Now().Add(time.Hour).Unix()
			f = newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.URL.Path)
				switch req.URL.Path {
				case "/oauth/token":
					if err := req.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if got := req.PostForm.Get("refresh_token"); got != "initial-refresh" {
						t.Errorf("recovery refresh credential = %q; want latest persisted token", got)
					}
					if got := req.PostForm.Get("grant_type"); got != "refresh_token" {
						t.Errorf("recovery grant_type = %q", got)
					}
					return coreResponse(req, http.StatusOK, coreJSON(t, tokenResponse{
						AccessToken: "recovered-access", RefreshToken: "recovered-refresh", ExpiresAt: expiry,
					})), nil
				case "/api/v3/athlete/activities":
					if got := req.Header.Get("Authorization"); got != "Bearer recovered-access" {
						t.Errorf("recovery used stale access credential: %q", got)
					}
					coreAttrs(t, f.rc.AccountNode(), map[string]string{
						attrRefreshToken: "recovered-refresh", importer.AcctAttrAccessToken: "recovered-access",
						attrTokenExpiry: strconv.FormatInt(expiry, 10),
					})
					return coreResponse(req, http.StatusOK, []byte("[]")), nil
				default:
					t.Errorf("unexpected recovery request: %s", req.URL.Path)
					return coreResponse(req, http.StatusNotFound, nil), nil
				}
			}))
			acct := f.rc.AccountNode()
			if err := acct.SetAttr(importer.AcctAttrUserID, "7"); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "access-claim-fails", "expiry-claim-fails":
				field := importer.AcctAttrAccessToken
				if scenario == "expiry-claim-fails" {
					field = attrTokenExpiry
				}
				f.storage.failNextClaim(field)
				err := storeToken(acct, &tokenResponse{
					AccessToken: "initial-access", RefreshToken: "initial-refresh", ExpiresAt: expiry,
				})
				if !errors.Is(err, errCoreClaimFailure) {
					t.Fatalf("partial storeToken error = %v; want injected failure", err)
				}
				coreAttrs(t, acct, map[string]string{attrRefreshToken: "initial-refresh", field: ""})
			default:
				if err := acct.SetAttr(attrRefreshToken, "initial-refresh"); err != nil {
					t.Fatal(err)
				}
				if scenario == "invalid-expiry" {
					if err := acct.SetAttrs(importer.AcctAttrAccessToken, "initial-access", attrTokenExpiry, "invalid"); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "missing-access" {
					if err := acct.SetAttr(attrTokenExpiry, strconv.FormatInt(expiry, 10)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if ready, err := (imp{}).IsAccountReady(acct); err != nil || !ready {
				t.Fatalf("partially persisted account not refreshable: %v, %v", ready, err)
			}
			if err := (imp{}).Run(f.rc); err != nil {
				t.Fatal(err)
			}
			want := []string{"/oauth/token", "/api/v3/athlete/activities"}
			if !reflect.DeepEqual(calls, want) {
				t.Errorf("recovery requests = %v; want %v", calls, want)
			}
			reloaded, err := f.rc.Host.ObjectFromRef(acct.PermanodeRef())
			if err != nil {
				t.Fatal(err)
			}
			coreAttrs(t, reloaded, map[string]string{
				attrRefreshToken: "recovered-refresh", importer.AcctAttrAccessToken: "recovered-access",
				attrTokenExpiry: strconv.FormatInt(expiry, 10), importer.AcctAttrCompletedVersion: runCompleteVersion,
			})
		})
	}
}

func TestImporterRefreshClaimFailureDoesNotPublishAccessToken(t *testing.T) {
	f := newCoreFixture(t, nil)
	acct := f.rc.AccountNode()
	if err := acct.SetAttr(importer.AcctAttrUserID, "7"); err != nil {
		t.Fatal(err)
	}
	f.storage.failNextClaim(attrRefreshToken)
	err := storeToken(acct, &tokenResponse{
		AccessToken: "initial-access", RefreshToken: "initial-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if !errors.Is(err, errCoreClaimFailure) {
		t.Fatalf("storeToken error = %v; want refresh claim failure", err)
	}
	coreAttrs(t, acct, map[string]string{attrRefreshToken: "", importer.AcctAttrAccessToken: "", attrTokenExpiry: ""})
	if ready, err := (imp{}).IsAccountReady(acct); err != nil || ready {
		t.Errorf("account without persisted refresh token ready = %v, %v", ready, err)
	}
	if err := (imp{}).Run(f.rc); err == nil {
		t.Error("account without persisted refresh credential unexpectedly ran")
	}
}
