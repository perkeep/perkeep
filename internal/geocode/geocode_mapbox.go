// +build mapbox

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
// and the Mapbox geocoding service if AltLookupFn is nil,
// or it returns (nil, nil) for the address being looked up.
//
// It's up to the caller to change AltLookupFn only
// when Lookup is not being called.
var AltLookupFn func(ctx context.Context, address string) ([]Rect, error)

const (
	apiKeyName = "mapbox-geocode.key"
)

var (
	mu     sync.RWMutex
	cache  = map[string][]Rect{}
	apiKey string

	sf singleflight.Group
)

// GetAPIKeyPath returns the file path to the Mapbox geocoding API key.
func GetAPIKeyPath() (string, error) {
	dir, err := osutil.PerkeepConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not get config dir: %v", err)
	}
	return filepath.Join(dir, apiKeyName), nil
}

// GetAPIKey returns the Mapbox geocoding API key stored in the Perkeep
// configuration directory as mapbox-geocode.key.
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
	log.Println(dir)
	slurp, err := wkfs.ReadFile(filepath.Join(dir, apiKeyName))
	if os.IsNotExist(err) {
		return "", ErrNoMapboxKey
	}
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(string(slurp))
	if key == "" {
		return "", ErrNoMapboxKey
	}
	apiKey = key
	return key, nil
}

var ErrNoMapboxKey = errors.New("geocode: geocoding is not configured; see https://perkeep.org/doc/geocoding")

// Lookup returns rectangles for the given address. Currently the only
// implementation is the Mapbox geocoding service.
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
		urlStr := fmt.Sprintf("https://api.mapbox.com/geocoding/v5/mapbox.places/%s.json?access_token=%s", url.QueryEscape(address), url.QueryEscape(key))
		res, err := ctxhttp.Get(ctx, ctxutil.Client(ctx), urlStr)
		if err != nil {
			log.Printf("geocode: HTTP error doing Mapbox lookup: %v", err)
			return nil, err
		}
		defer res.Body.Close()
		rects, err := decodeMapboxResponse(res.Body)
		if err != nil {
			log.Printf("geocode: error decoding Mapbox geocode response for %q: %v", address, err)
		} else {
			log.Printf("geocode: Mapbox lookup (%q) = %#v", address, rects)
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

type mapboxResTop struct {
	Features []mapboxFeature `json:"features"`
}

type mapboxFeature struct {
	BBox []float64 `json:"bbox",omitempty`
}

func decodeMapboxResponse(r io.Reader) (rects []Rect, err error) {
	var resTop mapboxResTop
	if err := json.NewDecoder(r).Decode(&resTop); err != nil {
		return nil, err
	}
	for _, feature := range resTop.Features {
		if feature.BBox != nil {
			r := Rect{
				NorthEast: LatLong{
					Lat:  feature.BBox[3],
					Long: feature.BBox[2],
				},
				SouthWest: LatLong{
					Lat:  feature.BBox[1],
					Long: feature.BBox[0],
				},
			}
			rects = append(rects, r)
		}
	}
	return
}
