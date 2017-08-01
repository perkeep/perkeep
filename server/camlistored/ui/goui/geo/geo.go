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

// Package geo provides utilities helping with geographic coordinates in the map
// aspect of the Camlistore web UI.
package geo

import (
	"context"
	"errors"
	"log"
	"math"
	"strconv"
	"strings"

	"camlistore.org/pkg/geocode"
	"camlistore.org/pkg/types/camtypes"
)

const (
	LocPredicatePrefix     = "loc"
	LocAreaPredicatePrefix = "locrect"
)

// HandleLocAreaPredicate checks whether predicate is a location area predicate
// (locrect). If so, it runs asynchronously handleCoordinatesFound on the given
// coordinates, and returns true. Otherwise, it returns false.
func HandleLocAreaPredicate(predicate string, handleCoordinatesFound func(*camtypes.LocationBounds)) bool {
	r, err := RectangleFromPredicate(predicate)
	if err != nil {
		return false
	}
	go handleCoordinatesFound(r)
	return true
}

var errNotARectangle = errors.New("not a valid locrect predicate")

// RectangleFromPredicate, if predicate is a valid "locrect:" search predicate,
// returns the corresponding rectangular area.
func RectangleFromPredicate(predicate string) (*camtypes.LocationBounds, error) {
	if !strings.HasPrefix(predicate, LocAreaPredicatePrefix+":") {
		return nil, errNotARectangle
	}
	loc := strings.TrimPrefix(predicate, LocAreaPredicatePrefix+":")
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

// IsLocPredicate returns whether the given predicate is a simple (as in, not
// composed) location predicate, such as the one supported by the Camlistore search
// handler (e.g. "loc:seattle").
func IsLocPredicate(predicate string) bool {
	if !strings.HasPrefix(predicate, LocPredicatePrefix+":") {
		return false
	}
	loc := strings.TrimPrefix(predicate, LocPredicatePrefix+":")
	if !strings.HasPrefix(loc, `"`) {
		if len(strings.Fields(loc)) > 1 {
			// Not a simple location query, but a logical one. Deal with it in another CL.
			// TODO(mpl): accept more complex queries.
			return false
		}
		return true
	}
	// we have a quoted location
	if !strings.HasSuffix(loc, `"`) {
		// the quoted location ends before the end of the query, or the quote never closes. either way, refuse that.
		return false
	}
	if strings.Count(loc, `"`) != 2 {
		// refuse anything that is not just one quoted location
		return false
	}
	return true
}

// Lookup searches for the coordinates of the given location, and passes the
// found zone (a rectangle), if any, to handleCoordinatesFound.
func Lookup(location string, handleCoordinatesFound func(*camtypes.LocationBounds)) {
	go func() {
		rect, err := geocode.Lookup(context.Background(), location)
		if err != nil {
			log.Printf("geocode lookup error: %v", err)
			return
		}
		if len(rect) == 0 {
			log.Printf("no coordinates found for %v", location)
			return
		}
		handleCoordinatesFound(&camtypes.LocationBounds{
			North: rect[0].NorthEast.Lat,
			South: rect[0].SouthWest.Lat,
			East:  rect[0].NorthEast.Long,
			West:  rect[0].SouthWest.Long,
		})
	}()
}

// Location is a geographical coordinate, specified by its latitude and its longitude.
type Location struct {
	Lat  float64 // -90 (south) to 90 (north)
	Long float64 // -180 (west) to 180 (east)
}

// TODO(mpl): write tests for LocationCenter, if we end up keeping it. not
// needed anymore for now, but might soon very well be. Otherwise remove.

// LocationCenter returns the center of the rectangle defined by the given
// coordinates.
func LocationCenter(north, south, west, east float64) Location {
	var lat, long float64
	if west < east {
		long = west + (east-west)/2.
	} else {
		// rectangle spanning longitude ±180°
		awest := math.Abs(west)
		aeast := math.Abs(east)
		if awest > aeast {
			long = east - (awest-aeast)/2.
		} else {
			long = west + (aeast-awest)/2.
		}
	}
	// TODO(mpl): are there tricky cases at ±90?
	lat = south + (north-south)/2.
	return Location{
		Lat:  lat,
		Long: long,
	}
}

// EastWest is returned by WrapAntimeridian. It exists only because there's no
// multi-valued returns with javascript functions, so we need WrapAntimeridian to
// return some sort of struct, that gets converted to a javascript object by
// gopherjs.
type EastWest struct {
	E float64
	W float64
}

// WrapAntimeridian determines if the shortest geodesic between east and west
// goes over the antimeridian. If yes, it converts one of the two to the closest
// equivalent value out of the [-180, 180] range. The choice of which of the two to
// convert is such as to maximize the part of the geodesic that stays in the
// [-180, 180] range.
// The reason for that function is that leaflet.js cannot handle drawing areas that
// cross the antimeridian if both corner are in the [-180, 180] range.
// https://github.com/Leaflet/Leaflet/issues/82
func WrapAntimeridian(east, west float64) EastWest {
	if west < east {
		return EastWest{
			E: east,
			W: west,
		}
	}
	lc := LocationCenter(50, -50, west, east)
	if lc.Long > 0 {
		// wrap around the +180 antimeridian.
		newEast := 180 + (180 - math.Abs(east))
		return EastWest{
			E: newEast,
			W: west,
		}
	}
	// else wrap around the -180 antimeridian
	newWest := -180 - (180 - west)
	return EastWest{
		E: east,
		W: newWest,
	}
}
