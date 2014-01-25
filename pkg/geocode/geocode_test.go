/*
Copyright 2013 The Camlistore Authors

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

package geocode

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeGoogleResponse(t *testing.T) {
	tests := []struct {
		name string
		res  string
		want []Rect
	}{
		{
			name: "moscow",
			res:  googleMoscow,
			want: []Rect{
				Rect{
					NorthEast: LatLong{pf("56.009657"), pf("37.945661")},
					SouthWest: LatLong{pf("55.48992699999999"), pf("37.319329")},
				},
				Rect{
					NorthEast: LatLong{pf("46.758882"), pf("-116.962068")},
					SouthWest: LatLong{pf("46.710912"), pf("-117.039698")},
				},
			},
		},
		{
			name: "usa",
			res:  googleUSA,
			want: []Rect{
				Rect{
					NorthEast: LatLong{pf("49.38"), pf("-66.94")},
					SouthWest: LatLong{pf("25.82"), pf("-124.39")},
				},
			},
		},
	}
	for _, tt := range tests {
		rects, err := decodeGoogleResponse(strings.NewReader(tt.res))
		if err != nil {
			t.Errorf("Decoding %s: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(rects, tt.want) {
			t.Errorf("Test %s: wrong rects\n Got %#v\nWant %#v", tt.name, rects, tt.want)
		}
	}
}

// parseFloat64
func pf(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return v
}

var googleMoscow = `
{
   "results" : [
      {
         "address_components" : [
            {
               "long_name" : "Moscow",
               "short_name" : "Moscow",
               "types" : [ "locality", "political" ]
            },
            {
               "long_name" : "gorod Moskva",
               "short_name" : "g. Moskva",
               "types" : [ "administrative_area_level_2", "political" ]
            },
            {
               "long_name" : "Moscow",
               "short_name" : "Moscow",
               "types" : [ "administrative_area_level_1", "political" ]
            },
            {
               "long_name" : "Russia",
               "short_name" : "RU",
               "types" : [ "country", "political" ]
            }
         ],
         "formatted_address" : "Moscow, Russia",
         "geometry" : {
            "bounds" : {
               "northeast" : {
                  "lat" : 56.009657,
                  "lng" : 37.945661
               },
               "southwest" : {
                  "lat" : 55.48992699999999,
                  "lng" : 37.319329
               }
            },
            "location" : {
               "lat" : 55.755826,
               "lng" : 37.6173
            },
            "location_type" : "APPROXIMATE",
            "viewport" : {
               "northeast" : {
                  "lat" : 56.009657,
                  "lng" : 37.945661
               },
               "southwest" : {
                  "lat" : 55.48992699999999,
                  "lng" : 37.319329
               }
            }
         },
         "types" : [ "locality", "political" ]
      },
      {
         "address_components" : [
            {
               "long_name" : "Moscow",
               "short_name" : "Moscow",
               "types" : [ "locality", "political" ]
            },
            {
               "long_name" : "Latah",
               "short_name" : "Latah",
               "types" : [ "administrative_area_level_2", "political" ]
            },
            {
               "long_name" : "Idaho",
               "short_name" : "ID",
               "types" : [ "administrative_area_level_1", "political" ]
            },
            {
               "long_name" : "United States",
               "short_name" : "US",
               "types" : [ "country", "political" ]
            }
         ],
         "formatted_address" : "Moscow, ID, USA",
         "geometry" : {
            "bounds" : {
               "northeast" : {
                  "lat" : 46.758882,
                  "lng" : -116.962068
               },
               "southwest" : {
                  "lat" : 46.710912,
                  "lng" : -117.039698
               }
            },
            "location" : {
               "lat" : 46.73238749999999,
               "lng" : -117.0001651
            },
            "location_type" : "APPROXIMATE",
            "viewport" : {
               "northeast" : {
                  "lat" : 46.758882,
                  "lng" : -116.962068
               },
               "southwest" : {
                  "lat" : 46.710912,
                  "lng" : -117.039698
               }
            }
         },
         "types" : [ "locality", "political" ]
      }
   ],
   "status" : "OK"
}
`

// Response for "usa".
// Note the geometry bounds covering the whole world. In this case, use the viewport instead.
var googleUSA = `
{
   "results" : [
      {
         "address_components" : [
            {
               "long_name" : "United States",
               "short_name" : "US",
               "types" : [ "country", "political" ]
            }
         ],
         "formatted_address" : "United States",
         "geometry" : {
            "bounds" : {
               "northeast" : {
                  "lat" : 90,
                  "lng" : 180
               },
               "southwest" : {
                  "lat" : -90,
                  "lng" : -180
               }
            },
            "location" : {
               "lat" : 37.09024,
               "lng" : -95.712891
            },
            "location_type" : "APPROXIMATE",
            "viewport" : {
               "northeast" : {
                  "lat" : 49.38,
                  "lng" : -66.94
               },
               "southwest" : {
                  "lat" : 25.82,
                  "lng" : -124.39
               }
            }
         },
         "types" : [ "country", "political" ]
      }
   ],
   "status" : "OK"
}
`
