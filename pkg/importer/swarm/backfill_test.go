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

package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema/nodeattr"
)

// fakeFoursquare is the parts of Foursquare's API that runs use: the
// user's checkins, venues' photos, and the photos themselves.
type fakeFoursquare struct {
	t        *testing.T
	checkins []*checkinItem

	mu           sync.Mutex
	photoLookups map[string]int // venue ID to paid photo lookups
}

func (f *fakeFoursquare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Host == "api.foursquare.com" && r.URL.Path == "/v2/"+checkinsAPIPath:
		var resp checkinsList
		if r.FormValue("offset") == "0" {
			resp.Response.Checkins.Items = f.checkins
		}
		json.NewEncoder(w).Encode(resp)
	case r.Host == "api.foursquare.com" && strings.HasPrefix(r.URL.Path, "/v2/venues/") && strings.HasSuffix(r.URL.Path, "/photos"):
		if r.FormValue("client_secret") == "" {
			f.t.Errorf("photo lookup %s without the client's credentials", r.URL.Path)
		}
		venue := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/venues/"), "/photos")
		f.photoLookups[venue]++
		var resp photosList
		resp.Response.Photos.Items = []*photoItem{{
			Id: "photo-of-" + venue, Prefix: "https://photos.foursquare.example/img/", Suffix: "/" + venue + ".jpg",
		}}
		json.NewEncoder(w).Encode(resp)
	case r.Host == "photos.foursquare.example":
		fmt.Fprintf(w, "jpeg of %s", r.URL.Path)
	default:
		f.t.Errorf("unexpected request %s", r.URL)
		http.NotFound(w, r)
	}
}

func (f *fakeFoursquare) lookups() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[string]int{}
	for k, v := range f.photoLookups {
		m[k] = v
	}
	return m
}

// TestPaidAPIBackfill checks that a run with the paid API, after one
// without it, gets the photos of the venues of recent checkins, which
// were imported already, and that the setting is kept on the account.
func TestPaidAPIBackfill(t *testing.T) {
	now := time.Now()
	f := &fakeFoursquare{t: t, photoLookups: map[string]int{}}
	for i, ago := range []time.Duration{24 * time.Hour, 48 * time.Hour, 60 * 24 * time.Hour} {
		f.checkins = append(f.checkins, &checkinItem{
			Id:        fmt.Sprintf("checkin%d", i),
			CreatedAt: now.Add(-ago).Unix(),
			Venue:     venueItem{Id: fmt.Sprintf("venue%d", i), Name: fmt.Sprintf("Venue %d", i)},
		})
	}
	ts := httptest.NewTestServer(t, f)
	h := imptest.NewMemoryHost(t, importer.HostConfig{
		HTTPClient:   ts.Client(),
		ClientId:     map[string]string{"swarm": "client-id"},
		ClientSecret: map[string]string{"swarm": "client-secret"},
	})
	rc, err := importer.CreateAccount(h, "swarm")
	if err != nil {
		t.Fatal(err)
	}
	acct := rc.AccountNode()
	if err := acct.SetAttrs(acctAttrUserId, "1", acctAttrAccessToken, "token"); err != nil {
		t.Fatal(err)
	}
	im := importer.All()["swarm"]
	run := func() {
		t.Helper()
		if err := im.Run(rc); err != nil {
			t.Fatal(err)
		}
	}

	run()
	if got := f.lookups(); len(got) != 0 {
		t.Fatalf("run without the paid API looked up photos: %v", got)
	}

	// Turn on the paid API on the account's page.
	req := httptest.NewRequest("POST", "/importer/swarm/"+acct.PermanodeRef().String(),
		strings.NewReader(url.Values{"mode": {"togglepaidapi"}, "usePaidAPI": {"on"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(httputil.PathSuffixHeader, "swarm/"+acct.PermanodeRef().String())
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !rc.UsePaidAPI() {
		t.Fatal("UsePaidAPI = false after turning it on")
	}
	res, err := h.Searcher().Describe(context.Background(), &search.DescribeRequest{BlobRef: acct.PermanodeRef()})
	if err != nil {
		t.Fatal(err)
	}
	if m := res.Meta.Get(acct.PermanodeRef()); m == nil || m.Permanode == nil || m.Permanode.Attr.Get(attrUsePaidAPIForTest) != "true" {
		t.Fatalf("account permanode doesn't keep the setting: %+v", m)
	}

	run()
	if got, want := f.lookups(), map[string]int{"venue0": 1, "venue1": 1}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("paid run's photo lookups = %v; want %v (only the recent checkins' venues)", got, want)
	}
	for _, v := range []string{"venue0", "venue1"} {
		photos, err := imptest.GetRequiredChildPathObj(placeNode(t, rc, v), "photos")
		if err != nil {
			t.Fatal(err)
		}
		if photos.Attr("camliPath:photo-of-"+v+".jpg") == "" {
			t.Errorf("%s has no photo: %v", v, photos.Attr(nodeattr.Title))
		}
	}

	// The venues have photos now, so another run doesn't look again.
	run()
	if got := f.lookups(); got["venue0"] != 1 || got["venue1"] != 1 || got["venue2"] != 0 {
		t.Errorf("photo lookups after another run = %v; want no more", got)
	}
}

// attrUsePaidAPIForTest is the account attribute where the importer host
// keeps whether to use the paid API.
const attrUsePaidAPIForTest = "importUsePaidAPI"

func placeNode(t *testing.T, rc *importer.RunContext, venue string) *importer.Object {
	t.Helper()
	places, err := imptest.GetRequiredChildPathObj(rc.RootNode(), "places")
	if err != nil {
		t.Fatal(err)
	}
	p, err := imptest.GetRequiredChildPathObj(places, venue)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
