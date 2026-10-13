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
	"encoding/json"
	"strings"
	"testing"

	"perkeep.org/pkg/types/camtypes"
)

func TestFitnessMapDiscovery(t *testing.T) {
	t.Setenv("PERKEEP_FITNESS_TILE_URL", "")
	discovery := new(camtypes.UIDiscovery)
	addFitnessMapDiscovery(discovery)
	if discovery.FitnessMapEnabled || discovery.FitnessMapProfile != "" {
		t.Fatal("unconfigured basemap advertised as enabled")
	}

	// A non-routable synthetic origin verifies that discovery is local-only.
	template := "https://maps.invalid/{z}/{x}/{y}.png?key=private-test-key"
	t.Setenv("PERKEEP_FITNESS_TILE_URL", template)
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", t.TempDir())
	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION", "")
	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION_URL", "")
	addFitnessMapDiscovery(discovery)
	if !discovery.FitnessMapEnabled || discovery.FitnessMapProfile == "" {
		t.Fatal("configured basemap missing from discovery")
	}
	if discovery.FitnessMapAttribution != "© OpenStreetMap contributors" ||
		discovery.FitnessMapAttributionURL != "https://www.openstreetmap.org/copyright" {
		t.Fatal("missing default licence information")
	}
	raw, err := json.Marshal(discovery)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-test-key") || strings.Contains(string(raw), "maps.invalid") {
		t.Fatal("provider URL or credential leaked through discovery")
	}
	profile := discovery.FitnessMapProfile

	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION", "Map data: OpenStreetMap; style: Example")
	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION_URL", "https://example.invalid/licence")
	addFitnessMapDiscovery(discovery)
	if discovery.FitnessMapProfile == profile || discovery.FitnessMapAttributionURL != "https://example.invalid/licence" {
		t.Fatal("provider credit did not update discovery/cache profile")
	}

	for _, unsafe := range []string{"javascript:alert(1)", "https://user:secret@example.invalid/", "//example.invalid/"} {
		t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION_URL", unsafe)
		addFitnessMapDiscovery(discovery)
		if discovery.FitnessMapAttributionURL != "https://www.openstreetmap.org/copyright" {
			t.Fatal("unsafe attribution link advertised")
		}
	}
}
