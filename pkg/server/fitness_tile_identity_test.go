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

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

func TestFitnessDefaultTileUserAgent(t *testing.T) {
	oldVersion, oldGitInfo := buildinfo.Version, buildinfo.GitInfo
	t.Cleanup(func() { buildinfo.Version, buildinfo.GitInfo = oldVersion, oldGitInfo })
	buildinfo.Version, buildinfo.GitInfo = "1.2", "test-build"
	owner := blob.RefFromString("synthetic signing public key")
	other := blob.RefFromString("another synthetic signing public key")
	base := "Perkeep/1.2 (+https://perkeep.org/)"
	ua := fitnessDefaultTileUserAgent(fitnessTileDefaultURL, owner)
	want := strings.TrimSuffix(base, ")") + "; operator-id=" + fitnessTileHash("osm\x00" + owner.String())[:32] + ")"
	if ua != want {
		t.Fatalf("User-Agent = %q, want %q", ua, want)
	}
	if strings.Contains(ua, owner.String()) {
		t.Fatal("User-Agent contains raw signer blobref")
	}
	if fitnessDefaultTileUserAgent(fitnessTileDefaultURL, owner) != ua {
		t.Fatal("operator ID is not stable")
	}
	if fitnessDefaultTileUserAgent(fitnessTileDefaultURL, other) == ua {
		t.Fatal("different signing keys have the same operator ID")
	}
	for _, template := range []string{"", "https://maps.invalid/{z}/{x}/{y}.png"} {
		if got := fitnessDefaultTileUserAgent(template, owner); got != base {
			t.Errorf("custom provider User-Agent = %q, want %q", got, base)
		}
	}
	if got := fitnessDefaultTileUserAgent(fitnessTileDefaultURL, blob.Ref{}); got != base {
		t.Errorf("ownerless User-Agent = %q, want %q", got, base)
	}
	buildinfo.Version = ""
	if got := fitnessDefaultTileUserAgent("", owner); got != "Perkeep/test-build (+https://perkeep.org/)" {
		t.Errorf("git version User-Agent = %q", got)
	}
	buildinfo.GitInfo = ""
	if got := fitnessDefaultTileUserAgent("", owner); got != "Perkeep/unknown (+https://perkeep.org/)" {
		t.Errorf("unversioned User-Agent = %q", got)
	}
	buildinfo.Version = "local build\r\n"
	if got := fitnessDefaultTileUserAgent("", owner); got != "Perkeep/local-build-- (+https://perkeep.org/)" {
		t.Errorf("unsafe version User-Agent = %q", got)
	}
}

func TestFitnessTileOperatorIdentity(t *testing.T) {
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", t.TempDir())
	t.Setenv("PERKEEP_FITNESS_TILE_USER_AGENT", "")
	t.Setenv("PERKEEP_FITNESS_TILE_URL", "")
	owner := blob.RefFromString("synthetic signing public key")
	other := blob.RefFromString("another synthetic signing public key")
	source, err := fitnessTileSourceFromEnv(owner)
	if err != nil {
		t.Fatal(err)
	}
	otherSource, err := fitnessTileSourceFromEnv(other)
	if err != nil {
		t.Fatal(err)
	}
	if source == otherSource || source.profile() == otherSource.profile() {
		t.Fatal("different OSM operators share a User-Agent/cache profile")
	}
	t.Setenv("PERKEEP_FITNESS_TILE_URL", fitnessTileDefaultURL)
	explicit, err := fitnessTileSourceFromEnv(owner)
	if err != nil || explicit != source {
		t.Fatalf("explicit OSM default changed source: %v", err)
	}

	// Exercise the actual outgoing header without contacting OSM.
	headers := make(chan http.Header, 1)
	data := fitnessTileTestPNG(t, 256, 256)
	source.client.Transport = fitnessTileTestTransport(func(r *http.Request) (*http.Response, error) {
		headers <- r.Header.Clone()
		return fitnessTileTestResponse(200, http.Header{"Cache-Control": {"max-age=3600"}}, data), nil
	})
	if _, err := source.tile(context.Background(), 0, 0, 0, "", true); err != nil {
		t.Fatal(err)
	}
	if got, want := (<-headers).Get("User-Agent"), fitnessDefaultTileUserAgent(fitnessTileDefaultURL, owner); got != want {
		t.Fatalf("outgoing User-Agent = %q, want %q", got, want)
	}

	t.Setenv("PERKEEP_FITNESS_TILE_URL", "https://maps.invalid/{z}/{x}/{y}.png")
	custom, err := fitnessTileSourceFromEnv(owner)
	if err != nil {
		t.Fatal(err)
	}
	otherCustom, err := fitnessTileSourceFromEnv(other)
	if err != nil {
		t.Fatal(err)
	}
	if custom != otherCustom || strings.Contains(custom.userAgent, "operator-id") {
		t.Fatal("custom provider received a signer-derived operator ID")
	}

	t.Setenv("PERKEEP_FITNESS_TILE_URL", "")
	override := "Perkeep/operator (+https://operator.invalid/contact)"
	t.Setenv("PERKEEP_FITNESS_TILE_USER_AGENT", override)
	overridden, err := fitnessTileSourceFromEnv(owner)
	if err != nil {
		t.Fatal(err)
	}
	if overridden.userAgent != override {
		t.Fatalf("explicit User-Agent override = %q, want %q", overridden.userAgent, override)
	}
	otherOverridden, err := fitnessTileSourceFromEnv(other)
	if err != nil || otherOverridden != overridden {
		t.Fatalf("override still depends on signer: %v", err)
	}
}

func TestFitnessTileOperatorIdentityIntegration(t *testing.T) {
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", t.TempDir())
	t.Setenv("PERKEEP_FITNESS_TILE_USER_AGENT", "")
	t.Setenv("PERKEEP_FITNESS_TILE_URL", "")
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	defer id.Index.Close()
	searchHandler := search.NewHandler(id.Index, index.NewOwner(indextest.KeyID, id.SignerBlobRef))
	pn := id.NewPermanode()
	id.SetAttribute(pn, nodeattr.Type, nodeattr.FitnessActivity)
	track, _ := id.UploadFile("synthetic.gpx", fitnessTestGPX, time.Time{})
	id.SetAttribute(pn, nodeattr.FitnessTrack, track.String())

	source, err := fitnessTileSourceFromEnv(id.SignerBlobRef)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	data := fitnessTileTestPNG(t, 256, 256)
	wantUA := fitnessDefaultTileUserAgent(fitnessTileDefaultURL, id.SignerBlobRef)
	source.client.Transport = fitnessTileTestTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if got := r.Header.Get("User-Agent"); got != wantUA {
			t.Errorf("thumbnail User-Agent = %q, want %q", got, wantUA)
		}
		return fitnessTileTestResponse(200, http.Header{"Cache-Control": {"max-age=3600"}}, data), nil
	})
	source.limiter = &fitnessTileLimiter{slots: make(chan struct{}, 16)}

	ui := &UIHandler{search: searchHandler}
	if got := ui.discovery().FitnessMapProfile; got != fitnessMapProfile(source) {
		t.Fatalf("UI profile = %q, want thumbnail profile %q", got, fitnessMapProfile(source))
	}
	ih := &ImageHandler{Search: searchHandler, Fetcher: id.BlobSource, MaxWidth: 320, MaxHeight: 180}
	rec := httptest.NewRecorder()
	ih.ServeHTTP(rec, httptest.NewRequest("GET", "/ui/thumbnail/unused/track.png", nil), pn)
	if rec.Code != 200 || calls.Load() == 0 {
		t.Fatalf("thumbnail status = %d, tile requests = %d", rec.Code, calls.Load())
	}
}
