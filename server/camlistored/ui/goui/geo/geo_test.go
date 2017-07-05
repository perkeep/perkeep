/*
Copyright 2017 The Camlistore Authors.

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

package geo

import (
	"testing"
)

func TestWrapAntimeridian(t *testing.T) {
	tt := []struct {
		east, newEast, west, newWest float64
	}{
		{
			// Should wrap around +180
			east:    -160.,
			west:    50.,
			newEast: 200.,
			newWest: 50.,
		},
		{
			// Should wrap around -180
			east:    -10.,
			west:    150.,
			newEast: -10.,
			newWest: -210.,
		},
		{
			// Should do nothing
			east:    40.,
			west:    -60.,
			newEast: 40.,
			newWest: -60.,
		},
	}

	for k, v := range tt {
		eastWest := WrapAntimeridian(v.east, v.west)
		if v.newEast != eastWest.E || v.newWest != eastWest.W {
			t.Errorf("at test %d, got [%v, %v], wanted [%v, %v]", k, v.newEast, v.newWest, eastWest.E, eastWest.W)
		}
	}

}
