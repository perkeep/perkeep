/*
Copyright 2013 The Perkeep Authors

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

// Package geocode handles mapping user-entered locations into lat/long polygons.
package geocode // import "perkeep.org/internal/geocode"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/buildinfo"

	"go4.org/ctxutil"
	"go4.org/legal"
	"go4.org/syncutil/singleflight"
	"go4.org/wkfs"
	"golang.org/x/net/context/ctxhttp"
)

type LatLong struct {
	Lat  float64 `json:"lat"`
	Long float64 `json:"lng"`
}

type Rect struct {
	NorthEast LatLong `json:"northeast"`
	SouthWest LatLong `json:"southwest"`
}

// AltLookupFn provides alternative geocode lookup in tests.
//
// If AltLookupFn is not nil, Lookup returns the results of
// the AltLookupFn call.
//
// Lookup performs its standard lookup using its cache
// and the Google geocoding service if AltLookupFn is nil,
// or it returns (nil, nil) for the address being looked up.
//
// It's up to the caller to change AltLookupFn only
// when Lookup is not being called.
var AltLookupFn func(ctx context.Context, address string) ([]Rect, error)

const (
	apiKeyName = "google-geocode.key"
)

var (
	mu     sync.RWMutex
	cache  = map[string][]Rect{}
	apiKey string

	sf singleflight.Group
)

// GetAPIKeyPath returns the file path to the Google geocoding API key.
func GetAPIKeyPath() (string, error) {
	dir, err := osutil.PerkeepConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not get config dir: %v", err)
	}
	return filepath.Join(dir, apiKeyName), nil
}

// GetAPIKey returns the Google geocoding API key stored in the Perkeep
// configuration directory as google-geocode.key.
func GetAPIKey() (string, error) {
	mu.RLock()
	key := apiKey
	mu.RUnlock()
	if apiKey != "" {
		return key, nil
	}
	mu.Lock()
	defer mu.Unlock()

	dir, err := osutil.PerkeepConfigDir()
	if err != nil {
		return "", err
	}
	slurp, err := wkfs.ReadFile(filepath.Join(dir, apiKeyName))
	if os.IsNotExist(err) {
		return "", ErrNoGoogleKey
	}
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(string(slurp))
	if key == "" {
		return "", ErrNoGoogleKey
	}
	apiKey = key
	return key, nil
}

var ErrNoGoogleKey = errors.New("geocode: Google API key not configured, using OpenStreetMap; see https://perkeep.org/doc/geocoding")

// Lookup returns rectangles for the given address. Currently the only
// implementation is the Google geocoding service.
func Lookup(ctx context.Context, address string) ([]Rect, error) {
	if AltLookupFn != nil {
		return AltLookupFn(ctx, address)
	}

	mu.RLock()
	rects, ok := cache[address]
	mu.RUnlock()
	if ok {
		return rects, nil
	}

	key, err := GetAPIKey()
	if err != nil && err != ErrNoGoogleKey {
		return nil, err
	}

	rectsi, err := sf.Do(address, func() (any, error) {
		if key != "" {
			return lookupGoogle(ctx, address, key)
		} else {
			return lookupOpenStreetMap(ctx, address)
		}
	})
	if err != nil {
		return nil, err
	}
	rects = rectsi.([]Rect)

	mu.Lock()
	cache[address] = rects
	mu.Unlock()
	return rects, nil
}

func lookupGoogle(ctx context.Context, address string, key string) ([]Rect, error) {
	// TODO: static data files from OpenStreetMap, Wikipedia, etc?
	urlStr := "https://maps.googleapis.com/maps/api/geocode/json?address=" + url.QueryEscape(address) + "&sensor=false&key=" + url.QueryEscape(key)
	res, err := ctxhttp.Get(ctx, ctxutil.Client(ctx), urlStr)
	if err != nil {
		log.Printf("geocode: HTTP error doing Google lookup: %v", err)
		return nil, err
	}
	defer res.Body.Close()
	rects, err := decodeGoogleResponse(res.Body)
	if err != nil {
		log.Printf("geocode: error decoding Google geocode response for %q: %v", address, err)
	} else {
		log.Printf("geocode: Google lookup (%q) = %#v", address, rects)
	}
	return rects, err
}

type googleResTop struct {
	Results []*googleResult `json:"results"`
}

type googleResult struct {
	Geometry *googleGeometry `json:"geometry"`
}

type googleGeometry struct {
	Bounds   *Rect `json:"bounds"`
	Viewport *Rect `json:"viewport"`
}

func decodeGoogleResponse(r io.Reader) (rects []Rect, err error) {
	var resTop googleResTop
	if err := json.NewDecoder(r).Decode(&resTop); err != nil {
		return nil, err
	}
	for _, res := range resTop.Results {
		if res.Geometry != nil {
			if r := res.Geometry.Bounds; r != nil && !(r.NorthEast.Lat == 90 && r.NorthEast.Long == 180 &&
				r.SouthWest.Lat == -90 && r.SouthWest.Long == -180) {
				// Google sometimes returns a "whole world" rect for large addresses (like "USA"), so we only
				// use the Bounds when they exist and make sense. Otherwise we use the Viewport if available.
				rects = append(rects, *r)
			} else if res.Geometry.Viewport != nil {
				rects = append(rects, *res.Geometry.Viewport)
			}
		}
	}
	return
}

var openstreetmapUserAgent = fmt.Sprintf("perkeep/%v", buildinfo.Summary())

func lookupOpenStreetMap(ctx context.Context, address string) ([]Rect, error) {
	// TODO: static data files from OpenStreetMap, Wikipedia, etc?
	urlStr := "https://nominatim.openstreetmap.org/search?format=json&limit=1&q=" + url.QueryEscape(address)
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		log.Printf("geocode: HTTP error doing OpenStreetMap lookup: %v", err)
		return nil, err
	}
	// Nominatim Usage Policy requires a user agent (https://operations.osmfoundation.org/policies/nominatim/)
	req.Header.Set("User-Agent", openstreetmapUserAgent)
	res, err := ctxhttp.Do(ctx, ctxutil.Client(ctx), req)
	if err != nil {
		log.Printf("geocode: HTTP error doing OpenStreetMap lookup: %v", err)
		return nil, err
	}
	defer res.Body.Close()
	rects, err := decodeOpenStreetMapResponse(res.Body)
	if err != nil {
		log.Printf("geocode: error decoding OpenStreetMap geocode response for %q: %v", address, err)
	} else {
		log.Printf("geocode: OpenStreetMap lookup (%q) = %#v", address, rects)
	}
	return rects, err
}

type openstreetmapResult struct {
	// BoundingBox is encoded as four floats (encoded as strings) in order: SW Lat, NE Lat, SW Long, NE Long
	BoundingBox []string `json:"boundingbox"`
}

func decodeOpenStreetMapResponse(r io.Reader) (rects []Rect, err error) {
	var osmResults []*openstreetmapResult
	if err := json.NewDecoder(r).Decode(&osmResults); err != nil {
		return nil, err
	}
	for _, res := range osmResults {
		if len(res.BoundingBox) == 4 {
			var coords []float64
			for _, b := range res.BoundingBox {
				f, err := strconv.ParseFloat(b, 64)
				if err != nil {
					return nil, err
				}
				coords = append(coords, f)
			}
			rect := Rect{
				NorthEast: LatLong{Lat: coords[1], Long: coords[3]},
				SouthWest: LatLong{Lat: coords[0], Long: coords[2]},
			}
			rects = append(rects, rect)
		}
	}

	return
}

func init() {
	legal.RegisterLicense(`
Mapping data and services copyright OpenStreetMap contributors, ODbL 1.0. 
https://osm.org/copyright.`)
}
