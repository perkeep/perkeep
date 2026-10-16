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

package search

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"go4.org/types"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/test"
)

func TestFitnessPredicates(t *testing.T) {
	fitness := permWithAttr(nodeattr.Type, nodeattr.FitnessActivity)
	run := andConst(fitness, permWithAttr(nodeattr.FitnessActivityType, "run"))
	hike := andConst(fitness, permWithAttr(nodeattr.FitnessActivityType, "hike"))
	ride := andConst(fitness, permWithAttr(nodeattr.FitnessActivityType, "ride"))
	ebike := andConst(ride, orConst(
		orConst(permWithAttr("stravaSportType", "EBikeRide"), permWithAttr("stravaSportType", "EMountainBikeRide")),
		andConst(orConst(&Constraint{Permanode: &PermanodeConstraint{
			Attr:       "stravaSportType",
			NumValue:   &IntConstraint{ZeroMax: true},
			SkipHidden: true,
		}}, permWithAttr("stravaSportType", "")),
			orConst(permWithAttr("stravaActivityType", "EBikeRide"), permWithAttr("stravaActivityType", "EMountainBikeRide"))),
	))
	tests := []struct {
		expr string
		want *Constraint
	}{
		{"is:fitness", fitness},
		{"is:run", run},
		{"is:hike", hike},
		{"is:ride", ride},
		{"is:bike", ride},
		{"is:ebike", ebike},
		{"is:fitness -is:ebike", andConst(fitness, notConst(ebike))},
		{"is:run or is:hike", orConst(run, hike)},
		{"(is:run or is:hike) and -is:ride", andConst(orConst(run, hike), notConst(ride))},
		{"is:bike title:eBike", andConst(ride, &Constraint{Permanode: &PermanodeConstraint{
			Attr: nodeattr.Title, SkipHidden: true,
			ValueMatches: &StringConstraint{Contains: "eBike", CaseInsensitive: true},
		}})},
		{"is:run after:2026-01-01", andConst(run, &Constraint{Permanode: &PermanodeConstraint{
			Time: &TimeConstraint{After: types.Time3339(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))},
		}})},
		{"is:hike locrect:53,4,52,6", andConst(hike, &Constraint{Permanode: &PermanodeConstraint{
			Location: &LocationConstraint{North: 53, West: 4, South: 52, East: 6},
		}})},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := parseExpression(context.Background(), tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			want := &SearchQuery{Constraint: andConst(
				&Constraint{Permanode: &PermanodeConstraint{SkipHidden: true}}, tt.want)}
			if !reflect.DeepEqual(got, want) {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want)
				t.Errorf("parseExpression(%q) = %s; want %s", tt.expr, gotJSON, wantJSON)
			}
		})
	}
	bike, err := parseExpression(context.Background(), "is:bike")
	if err != nil {
		t.Fatal(err)
	}
	rideQuery, err := parseExpression(context.Background(), "is:ride")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bike, rideQuery) {
		t.Error("is:bike is not an exact alias of is:ride")
	}
}

func TestFitnessPredicateExactNames(t *testing.T) {
	test.TLog(t)
	for _, expr := range []string{
		"is:fitness:extra", "is:run:extra", "is:hike:extra", "is:ride:extra",
		"is:bike:extra", "is:ebike:extra", "is:Run", "is:Bike", "is:ebikeride", "is:walk",
	} {
		t.Run(expr, func(t *testing.T) {
			_, err := parseExpression(context.Background(), expr)
			if err == nil || !strings.Contains(err.Error(), "Unknown search predicate") {
				t.Errorf("parseExpression(%q) error = %v; want unknown predicate", expr, err)
			}
		})
	}
}

func TestFitnessSearchHelp(t *testing.T) {
	var help []struct{ Name, Description string }
	if err := json.Unmarshal([]byte(SearchHelp()), &help); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"is:fitness", "is:run", "is:hike", "is:ride", "is:bike", "is:ebike"} {
		count := 0
		for _, entry := range help {
			if entry.Name == name {
				count++
				if entry.Description == "" {
					t.Errorf("%s has no help description", name)
				}
				if name == "is:bike" && !strings.Contains(entry.Description, "alias for is:ride") {
					t.Errorf("is:bike help does not document the alias: %q", entry.Description)
				}
			}
		}
		if count != 1 {
			t.Errorf("help entries for %s = %d; want 1", name, count)
		}
	}
}
