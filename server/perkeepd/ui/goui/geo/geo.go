/*
Copyright 2017 The Perkeep Authors.

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

// Package geo provides utilities helping with geographic coordinates in the map
// aspect of the Perkeep web UI.
package geo

import (
	"fmt"
	"strconv"
	"strings"

	"perkeep.org/pkg/types/camtypes"
)

const (
	LocPredicatePrefix     = "loc"
	LocAreaPredicatePrefix = "locrect"
	LocMapPredicatePrefix  = "map"
)

// IsLocMapPredicate returns whether predicate is a map location predicate.
func IsLocMapPredicate(predicate string) bool {
	if _, err := rectangleFromPredicate(predicate, LocMapPredicatePrefix); err != nil {
		return false
	}
	return true
}

// rectangleFromPredicate, if predicate is a valid location predicate of the given kind
// and returns the corresponding rectangular area.
func rectangleFromPredicate(predicate, kind string) (*camtypes.LocationBounds, error) {
	errNotARectangle := fmt.Errorf("not a valid %v predicate", kind)
	if !strings.HasPrefix(predicate, kind+":") {
		return nil, errNotARectangle
	}
	loc := strings.TrimPrefix(predicate, kind+":")
	coords := strings.Split(loc, ",")
	if len(coords) != 4 {
		return nil, errNotARectangle
	}
	var coord [4]float64
	for k, v := range coords {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, errNotARectangle
		}
		coord[k] = f
	}
	return &camtypes.LocationBounds{
		North: coord[0],
		South: coord[2],
		East:  coord[3],
		West:  coord[1],
	}, nil
}
