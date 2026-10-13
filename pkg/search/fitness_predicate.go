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

	"perkeep.org/pkg/schema/nodeattr"
)

type isFitness struct {
	matchEqual
	activityType string
}

func newIsFitness(name, activityType string) keyword {
	return isFitness{matchEqual("is:" + name), activityType}
}

func (k isFitness) Description() string {
	switch k.Name() {
	case "is:fitness":
		return "matches all fitnessActivity permanodes, including unknown sports and activities without GPS."
	case "is:bike":
		return "alias for is:ride; includes all cycling subtypes, including electric bikes."
	case "is:ride":
		return "matches fitness activities classified as ride, including mountain, gravel, virtual, and electric cycling."
	default:
		return "matches fitness activities classified as " + k.activityType + "."
	}
}

func fitnessActivityConstraint(activityType string) *Constraint {
	c := permWithAttr(nodeattr.Type, nodeattr.FitnessActivity)
	if activityType != "" {
		c = andConst(c, permWithAttr(nodeattr.FitnessActivityType, activityType))
	}
	return c
}

func (k isFitness) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return fitnessActivityConstraint(k.activityType), nil
}

type isEBike struct {
	matchEqual
}

func newIsEBike() keyword {
	return isEBike{"is:ebike"}
}

func (k isEBike) Description() string {
	return "matches fitness rides whose Strava sport is EBikeRide or EMountainBikeRide; uses legacy stravaActivityType only when stravaSportType is absent or empty."
}

func (k isEBike) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	electric := func(attr string) *Constraint {
		return orConst(permWithAttr(attr, "EBikeRide"), permWithAttr(attr, "EMountainBikeRide"))
	}
	// Prefer the modern sport classification over a possibly stale legacy type.
	noSportType := &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:       "stravaSportType",
			NumValue:   &IntConstraint{ZeroMax: true},
			SkipHidden: true,
		},
	}
	return andConst(fitnessActivityConstraint("ride"),
		orConst(electric("stravaSportType"),
			andConst(orConst(noSportType, permWithAttr("stravaSportType", "")),
				electric("stravaActivityType")))), nil
}
