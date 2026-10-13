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

func TestFitnessDistanceQueries(t *testing.T) {
	// testQuery exercises the classic index and both corpus construction paths.
	// All data is synthesized locally; no importer or geocoder is involved.
	testQuery(t, func(qt *queryTest) {
		activities := []struct{ name, distance string }{
			{"missing", ""}, {"negative", "-1"}, {"nan", "NaN"},
			{"inf", "Inf"}, {"positive-inf", "+Inf"}, {"negative-inf", "-Inf"},
			{"garbage", "not-a-distance"}, {"overflow", "1e309"},
			{"zero", "0"}, {"fraction", "125"}, {"half-km", "500"},
			{"three-miles", "4828.032"},
			{"below-five-miles", "8046.7199"}, {"five-miles", "8046.72"},
			{"above-five-miles", "8046.7201"}, {"below-ten-km", "9999.9"},
			{"ten-km", "10000"}, {"above-ten-km", "10000.1"},
			{"twenty-km", "20000"}, {"max", "1.7976931348623157e308"},
		}
		refs := make(map[string]blob.Ref)
		for _, a := range activities {
			pn := qt.id.NewDeterministicPermanodeForTest("fitness-distance-" + a.name)
			refs[a.name] = pn
			qt.id.SetAttribute(pn, nodeattr.Type, nodeattr.FitnessActivity)
			qt.id.SetAttribute(pn, nodeattr.FitnessSource, "another-provider")
			qt.id.SetAttribute(pn, nodeattr.Title, a.name)
			if a.name != "twenty-km" {
				qt.id.SetAttribute(pn, nodeattr.FitnessActivityType, "run")
			} // The unknown/unclassified sport still has searchable distance.
			if a.distance != "" {
				qt.id.SetAttribute(pn, nodeattr.FitnessDistance, a.distance)
			}
		}
		qt.id.SetAttribute(refs["ten-km"], nodeattr.Latitude, "52.5")
		qt.id.SetAttribute(refs["ten-km"], nodeattr.Longitude, "5")
		// Neither old provider node types nor unrelated numeric claims qualify.
		for _, nodeType := range []string{"", "strava.com:activity", "fitnessActivityExtra", "FitnessActivity"} {
			pn := qt.id.NewDeterministicPermanodeForTest("non-fitness-distance-" + nodeType)
			if nodeType != "" {
				qt.id.SetAttribute(pn, nodeattr.Type, nodeType)
			}
			qt.id.SetAttribute(pn, nodeattr.FitnessDistance, "10000")
			qt.id.SetAttribute(pn, nodeattr.FitnessActivityType, "run")
			qt.id.SetAttribute(pn, nodeattr.FitnessSource, "strava")
		}
		pick := func(names ...string) []blob.Ref {
			var result []blob.Ref
			for _, name := range names {
				result = append(result, refs[name])
			}
			return result
		}
		finite := pick("zero", "fraction", "half-km", "three-miles", "below-five-miles",
			"five-miles", "above-five-miles", "below-ten-km", "ten-km", "above-ten-km", "twenty-km", "max")
		fiveMilesUp := pick("five-miles", "above-five-miles", "below-ten-km", "ten-km", "above-ten-km", "twenty-km", "max")
		tenKMUp := pick("ten-km", "above-ten-km", "twenty-km", "max")
		tests := []struct {
			expr string
			want []blob.Ref
		}{
			{"miles:>=5", fiveMilesUp},
			{"km:>=10", tenKMUp},
			{"kilometers:>=10", tenKMUp},
			{"meters:>=10000", tenKMUp},
			{"miles:3", pick("three-miles")},
			{"miles:=3", pick("three-miles")},
			{"miles:=5", pick("five-miles")},
			{"km:10", pick("ten-km")},
			{"kilometers:=10", pick("ten-km")},
			{"meters:10000", pick("ten-km")},
			{"km:0.125", pick("fraction")},
			{"km:.5", pick("half-km")},
			{"km:1e1", pick("ten-km")},
			{"km:5-10", pick("below-five-miles", "five-miles", "above-five-miles", "below-ten-km", "ten-km")},
			{"miles:3-5", pick("three-miles", "below-five-miles", "five-miles")},
			{"km:.125-.5", pick("fraction", "half-km")},
			{"miles:5-", fiveMilesUp},
			{"km:-10", pick("zero", "fraction", "half-km", "three-miles", "below-five-miles",
				"five-miles", "above-five-miles", "below-ten-km", "ten-km")},
			{"meters:0", pick("zero")},
			{"meters:=0", pick("zero")},
			{"meters:0-0", pick("zero")},
			{"meters:<=0", pick("zero")},
			{"meters:-0", pick("zero")},
			{"meters:>=0", finite},
			{"meters:0-", finite},
			{"meters:>0", pick("fraction", "half-km", "three-miles", "below-five-miles",
				"five-miles", "above-five-miles", "below-ten-km", "ten-km", "above-ten-km", "twenty-km", "max")},
			{"meters:<0", nil},
			{"miles:>5", pick("above-five-miles", "below-ten-km", "ten-km", "above-ten-km", "twenty-km", "max")},
			{"km:<10", pick("zero", "fraction", "half-km", "three-miles", "below-five-miles",
				"five-miles", "above-five-miles", "below-ten-km")},
			{"meters:>1.7976931348623157e308", nil},
			{"meters:>=1.7976931348623157e308", pick("max")},
			{"meters:<=1.7976931348623157e308", finite},
			{"km:>=10 km:<=10", pick("ten-km")},
			{"(miles:3 or kilometers:10) -meters:10000", pick("three-miles")},
			{"km:>=0 -miles:>=5", pick("zero", "fraction", "half-km", "three-miles", "below-five-miles")},
			{"is:run km:>=10", pick("ten-km", "above-ten-km", "max")},
			{"km:>=10 title:twenty", pick("twenty-km")},
			{"km:>=10 has:location", pick("ten-km")},
			{"km:>=10 -has:location", pick("above-ten-km", "twenty-km", "max")},
			{"km:>=10 locrect:53,4,52,6", pick("ten-km")},
		}
		for _, tt := range tests {
			t.Logf("%s: %s", qt.itype, tt.expr)
			qt.wantRes(&SearchQuery{Expression: tt.expr, Limit: -1}, tt.want...)
		}
	})
}

func TestFitnessDistanceQueriesSkipHidden(t *testing.T) {
	// Match existing predicates: SkipHidden is enforced by corpus matchers.
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		visible := qt.id.NewDeterministicPermanodeForTest("visible-fitness-distance")
		hidden := qt.id.NewDeterministicPermanodeForTest("hidden-fitness-distance")
		for _, pn := range []blob.Ref{visible, hidden} {
			qt.id.SetAttribute(pn, nodeattr.Type, nodeattr.FitnessActivity)
			qt.id.SetAttribute(pn, nodeattr.FitnessDistance, "10000")
		}
		qt.id.SetAttribute(hidden, "camliDefVis", "hide")
		for _, expr := range []string{"miles:>=5", "km:10", "kilometers:<=10", "meters:>0"} {
			qt.wantRes(&SearchQuery{Expression: expr}, visible)
		}
	})
}
