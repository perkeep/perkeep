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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"perkeep.org/internal/osutil"

	"go4.org/ctxutil"
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

var ErrNoGoogleKey = errors.New("geocode: geocoding is not configured; see https://perkeep.org/doc/geocoding")

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
	if err != nil {
		return nil, err
	}

	rectsi, err := sf.Do(address, func() (interface{}, error) {
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
		if err == nil {
			mu.Lock()
			cache[address] = rects
			mu.Unlock()
		}
		return rects, err
	})
	if err != nil {
		return nil, err
	}
	return rectsi.([]Rect), nil
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
