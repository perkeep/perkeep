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

package search_test

import (
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema/nodeattr"
	. "perkeep.org/pkg/search"
)

func TestFitnessQueries(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		type activity struct {
			name, kind, sport, legacy string
			attrs                     map[string]string
		}
		activities := []activity{
			{name: "run", kind: "run", attrs: map[string]string{
				nodeattr.StartDate: "2026-02-01T12:00:00Z", nodeattr.Latitude: "52.5", nodeattr.Longitude: "5",
			}},
			{name: "hike", kind: "hike", attrs: map[string]string{nodeattr.StartDate: "2025-02-01T12:00:00Z"}},
			{name: "regular", kind: "ride", sport: "Ride", attrs: map[string]string{nodeattr.Title: "eBike sightseeing"}},
			{name: "generic", kind: "ride"},
			{name: "mountain", kind: "ride", sport: "MountainBikeRide"},
			{name: "gravel", kind: "ride", sport: "GravelRide"},
			{name: "virtual", kind: "ride", sport: "VirtualRide"},
			{name: "handcycle", kind: "ride", sport: "Handcycle"},
			{name: "velomobile", kind: "ride", sport: "Velomobile"},
			{name: "ebike", kind: "ride", sport: "EBikeRide", legacy: "Ride"},
			{name: "emountain", kind: "ride", sport: "EMountainBikeRide"},
			{name: "legacy-ebike", kind: "ride", legacy: "EBikeRide"},
			{name: "legacy-emountain", kind: "ride", legacy: "EMountainBikeRide"},
			{name: "empty-sport-ebike", kind: "ride", legacy: "EBikeRide", attrs: map[string]string{"stravaSportType": ""}},
			{name: "stale-legacy", kind: "ride", sport: "Ride", legacy: "EBikeRide"},
			{name: "stale-legacy-run", kind: "ride", sport: "Run", legacy: "EBikeRide"},
			{name: "wrong-case-sport", kind: "ride", sport: "ebikeride"},
			{name: "sport-substring", kind: "ride", sport: "EBikeRideExtra"},
			{name: "wrong-case-legacy", kind: "ride", legacy: "ebikeride"},
			{name: "walk", kind: "walk"},
			{name: "unknown", kind: "other", sport: "FutureSport"},
			{name: "gpsless"},
			{name: "unclassified-electric", sport: "EBikeRide"},
			{name: "wrong-kind", kind: "Ride", sport: "EBikeRide"},
			{name: "kind-substring", kind: "runExtra"},
			{name: "wrong-kind-case", kind: "Run"},
			{name: "electric-run", kind: "run", sport: "EBikeRide"},
		}
		refs := make(map[string]blob.Ref)
		var allFitness []blob.Ref
		for _, a := range activities {
			pn := qt.id.NewDeterministicPermanodeForTest("fitness-" + a.name)
			refs[a.name] = pn
			allFitness = append(allFitness, pn)
			qt.id.SetAttribute(pn, nodeattr.Type, nodeattr.FitnessActivity)
			if a.kind != "" {
				qt.id.SetAttribute(pn, nodeattr.FitnessActivityType, a.kind)
			}
			if a.sport != "" {
				qt.id.SetAttribute(pn, "stravaSportType", a.sport)
			}
			if a.legacy != "" {
				qt.id.SetAttribute(pn, "stravaActivityType", a.legacy)
			}
			// Classification does not depend on a source provider.
			qt.id.SetAttribute(pn, nodeattr.FitnessSource, "another-provider")
			for attr, value := range a.attrs {
				qt.id.SetAttribute(pn, attr, value)
			}
		}
		// Matching type/source attributes on non-fitness nodes are insufficient.
		for _, nodeType := range []string{"", "strava.com:activity", "fitnessActivityExtra", "FitnessActivity"} {
			pn := qt.id.NewDeterministicPermanodeForTest("non-fitness-" + nodeType)
			if nodeType != "" {
				qt.id.SetAttribute(pn, nodeattr.Type, nodeType)
			}
			qt.id.SetAttribute(pn, nodeattr.FitnessActivityType, "ride")
			qt.id.SetAttribute(pn, "stravaSportType", "EBikeRide")
			qt.id.SetAttribute(pn, "stravaActivityType", "EMountainBikeRide")
			qt.id.SetAttribute(pn, nodeattr.FitnessSource, "strava")
		}
		pick := func(names ...string) []blob.Ref {
			var result []blob.Ref
			for _, name := range names {
				result = append(result, refs[name])
			}
			return result
		}
		rides := pick("regular", "generic", "mountain", "gravel", "virtual", "handcycle", "velomobile",
			"ebike", "emountain", "legacy-ebike", "legacy-emountain", "empty-sport-ebike", "stale-legacy", "stale-legacy-run",
			"wrong-case-sport", "sport-substring", "wrong-case-legacy")
		tests := []struct {
			expr string
			want []blob.Ref
		}{
			{"is:fitness", allFitness},
			{"is:run", pick("run", "electric-run")},
			{"is:hike", pick("hike")},
			{"is:ride", rides},
			{"is:bike", rides},
			{"is:ebike", pick("ebike", "emountain", "legacy-ebike", "legacy-emountain", "empty-sport-ebike")},
			{"is:run or is:hike", pick("run", "electric-run", "hike")},
			{"is:bike -is:ebike", pick("regular", "generic", "mountain", "gravel", "virtual", "handcycle",
				"velomobile", "stale-legacy", "stale-legacy-run", "wrong-case-sport", "sport-substring", "wrong-case-legacy")},
			{"(is:run or is:hike) and -is:run", pick("hike")},
			{"is:bike title:eBike", pick("regular")},
			{"is:ebike title:eBike", nil},
			{"is:fitness -has:location -is:run -is:hike -is:bike",
				pick("walk", "unknown", "gpsless", "unclassified-electric", "wrong-kind", "kind-substring", "wrong-kind-case")},
			{"is:fitness locrect:53,4,52,6", pick("run")},
		}
		for _, tt := range tests {
			t.Logf("%s: %s", qt.itype, tt.expr)
			qt.wantRes(&SearchQuery{Expression: tt.expr, Limit: -1}, tt.want...)
		}
		// Existing permanode time constraints require the corpus matcher.
		if qt.itype != indexClassic {
			qt.wantRes(&SearchQuery{Expression: "is:run after:2026-01-01 before:2027-01-01"}, refs["run"])
		}
	})
}

func TestFitnessQueriesSkipHidden(t *testing.T) {
	// SkipHidden is currently enforced only by the corpus matcher, as for the
	// existing predicates; fitness searches must preserve those semantics.
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		visible := qt.id.NewDeterministicPermanodeForTest("visible-fitness")
		hidden := qt.id.NewDeterministicPermanodeForTest("hidden-fitness")
		for _, pn := range []blob.Ref{visible, hidden} {
			qt.id.SetAttribute(pn, nodeattr.Type, nodeattr.FitnessActivity)
			qt.id.SetAttribute(pn, nodeattr.FitnessActivityType, "ride")
			qt.id.SetAttribute(pn, "stravaSportType", "EBikeRide")
		}
		qt.id.SetAttribute(hidden, "camliDefVis", "hide")
		for _, expr := range []string{"is:fitness", "is:ride", "is:bike", "is:ebike", "is:fitness -is:run"} {
			qt.wantRes(&SearchQuery{Expression: expr}, visible)
		}
	})
}
