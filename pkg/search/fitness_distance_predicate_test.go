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
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"perkeep.org/pkg/test"
)

func TestFitnessDistancePredicates(t *testing.T) {
	distance := func(fc *FloatConstraint) *Constraint {
		return andConst(fitnessActivityConstraint(""), distanceFloatConstraint(fc))
	}
	exact := func(v float64) *Constraint {
		return distance(&FloatConstraint{Min: v, Max: v, ZeroMin: v == 0, ZeroMax: v == 0})
	}
	atLeastFiveMiles := distance(&FloatConstraint{Min: 8046.72})
	atLeastTenKM := distance(&FloatConstraint{Min: 10000})
	tests := []struct {
		expr string
		want *Constraint
	}{
		{"miles:>=5", atLeastFiveMiles},
		{"km:>=10", atLeastTenKM},
		{"kilometers:>=10", atLeastTenKM},
		{"meters:>=10000", atLeastTenKM},
		{"miles:3", exact(4828.032)},
		{"miles:=3", exact(4828.032)},
		{"km:0.125", exact(125)},
		{"km:.5", exact(500)},
		{"meters:5.", exact(5)},
		{"km:1e1", exact(10000)},
		{"km:1e-1", exact(100)},
		{"km:5e-1-1e+1", distance(&FloatConstraint{Min: 500, Max: 10000})},
		{"km:5-10", distance(&FloatConstraint{Min: 5000, Max: 10000})},
		{"km:0.5-1.5", distance(&FloatConstraint{Min: 500, Max: 1500})},
		{"miles:5-", atLeastFiveMiles},
		{"km:-10", distance(&FloatConstraint{Max: 10000, ZeroMin: true})},
		{"km:<=10", distance(&FloatConstraint{Max: 10000, ZeroMin: true})},
		{"meters:0", exact(0)},
		{"meters:=0", exact(0)},
		{"meters:0-0", exact(0)},
		{"meters:<=0", exact(0)},
		{"meters:-0", exact(0)},
		{"meters:>=0", distance(&FloatConstraint{ZeroMin: true})},
		{"meters:0-", distance(&FloatConstraint{ZeroMin: true})},
		{"km:>10", andConst(fitnessActivityConstraint(""), andConst(
			distanceFloatConstraint(&FloatConstraint{ZeroMin: true}),
			notConst(distanceFloatConstraint(&FloatConstraint{Max: 10000}))))},
		{"km:<10", andConst(fitnessActivityConstraint(""), andConst(
			distanceFloatConstraint(&FloatConstraint{ZeroMin: true}),
			notConst(distanceFloatConstraint(&FloatConstraint{Min: 10000}))))},
		{`miles:">= 5"`, atLeastFiveMiles},
		{`km:" 5 - 10 "`, distance(&FloatConstraint{Min: 5000, Max: 10000})},
		{"is:run miles:>=5", andConst(fitnessActivityConstraint("run"), atLeastFiveMiles)},
		{"miles:>=5 or km:>=10", orConst(atLeastFiveMiles, atLeastTenKM)},
		{"is:fitness -miles:>=5", andConst(fitnessActivityConstraint(""), notConst(atLeastFiveMiles))},
		{"km:>=10 locrect:53,4,52,6", andConst(atLeastTenKM, &Constraint{
			Permanode: &PermanodeConstraint{Location: &LocationConstraint{North: 53, West: 4, South: 52, East: 6}},
		})},
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
				t.Errorf("got %s; want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestFitnessDistanceInvalid(t *testing.T) {
	test.TLog(t)
	for _, expr := range []string{
		"miles:", "km:-", "km:>", "km:<=", "km:=", "km:=>5", "km:==5",
		"km:1-0", "km:1.00000000000000001-1", "km:1--2", "km:5:10",
		"meters:=-1", "meters:>=-1", "meters:<=-1", "meters:=-0",
		"meters:+1", "meters:NaN", "meters:Inf", "meters:+Inf", "meters:-Inf",
		"meters:1/2", "meters:0x10", "km:10abc", "km:1e", "km:1e401",
		"km:1e-401", "km:1e-10000000", "meters:1e309", "miles:1e308",
		"meters:1e-324", "km:1e-400", "km:1e308-", "km:-1e308",
		"km:" + strings.Repeat("1", 129),
		"mi:5", "mile:5", "Km:5", "duration:>=5",
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := parseExpression(context.Background(), expr); err == nil {
				t.Errorf("parseExpression(%q) succeeded", expr)
			}
		})
	}
}

func TestFitnessDistanceNormalization(t *testing.T) {
	k := newFitnessDistance("miles", 1609344, 1000).(fitnessDistance)
	got, _, err := k.meters("3")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := strconv.ParseFloat("4828.032", 64)
	if got != want {
		t.Errorf("miles:3 normalized to %.17g; want %.17g", got, want)
	}
	for _, expr := range []string{
		"meters:>0", "meters:<0", "meters:>1.7976931348623157e308",
		"meters:<1.7976931348623157e308", "km:1e-325",
	} {
		if _, err := parseExpression(context.Background(), expr); err != nil {
			t.Errorf("%s: %v", expr, err)
		}
	}
}

func TestFitnessDistanceSearchHelp(t *testing.T) {
	var help []struct{ Name, Description string }
	if err := json.Unmarshal([]byte(SearchHelp()), &help); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"miles", "km", "kilometers", "meters"} {
		count := 0
		for _, entry := range help {
			if entry.Name != name {
				continue
			}
			count++
			if !strings.Contains(entry.Description, "distance") || !strings.Contains(entry.Description, "5-10") {
				t.Errorf("%s help missing distance/range documentation: %q", name, entry.Description)
			}
			if name == "kilometers" && !strings.Contains(entry.Description, "alias for km") {
				t.Errorf("kilometers alias not documented: %q", entry.Description)
			}
		}
		if count != 1 {
			t.Errorf("%s help count = %d; want 1", name, count)
		}
	}
}

func TestFloatConstraintNonfinite(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, fc := range []*FloatConstraint{{}, {ZeroMin: true}, {Min: 1, Max: 2}} {
			if fc.floatMatches(v) {
				t.Errorf("%+v matches nonfinite value %v", fc, v)
			}
		}
		for _, fc := range []*FloatConstraint{{Min: v}, {Max: v}} {
			if err := fc.checkValid(); err == nil {
				t.Errorf("%+v accepts a nonfinite bound", fc)
			}
		}
	}
	if !(&FloatConstraint{}).floatMatches(math.MaxFloat64) {
		t.Error("unbounded FloatConstraint does not match MaxFloat64")
	}
}
