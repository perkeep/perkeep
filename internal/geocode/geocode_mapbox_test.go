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

package geocode

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeMapboxResponse(t *testing.T) {
	tests := []struct {
		name string
		res  string
		want []Rect
	}{
		{
			name: "moscow",
			res:  mapboxMoscow,
			want: []Rect{
				{
					NorthEast: LatLong{pf("56.0200519998407"), pf("37.9672888618296")},
					SouthWest: LatLong{pf("55.1424098289334"), pf("36.8030478778")},
				},
				{
					NorthEast: LatLong{pf("56.020768"), pf("37.962469")},
					SouthWest: LatLong{pf("55.490567"), pf("37.129646")},
				},
				{
					NorthEast: LatLong{pf("56.961685"), pf("40.205582")},
					SouthWest: LatLong{pf("54.255257"), pf("35.143634")},
				},
				{
					NorthEast: LatLong{pf("46.8304461192348"), pf("-116.789356995108")},
					SouthWest: LatLong{pf("46.6039941176436"), pf("-117.03995202")},
				},
				{
					NorthEast: LatLong{pf("41.4276180166518"), pf("-75.3873106153993")},
					SouthWest: LatLong{pf("41.2288640409993"), pf("-75.6943059894169")},
				},
			},
		},
		{
			name: "usa",
			res:  mapboxUSA,
			want: []Rect{
				{
					NorthEast: LatLong{pf("71.540724"), pf("-66.885444")},
					SouthWest: LatLong{pf("18.765563"), pf("-179.9")},
				},
				{
					NorthEast: LatLong{pf("38.902772"), pf("29.631972")},
					SouthWest: LatLong{pf("38.476608"), pf("28.828607")},
				},
				{
					NorthEast: LatLong{pf("38.885095"), pf("29.88813")},
					SouthWest: LatLong{pf("38.212164"), pf("28.838478")},
				},
			},
		},
	}
	for _, tt := range tests {
		rects, err := decodeMapboxResponse(strings.NewReader(tt.res))
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

var mapboxMoscow = `
{
   "type": "FeatureCollection",
   "query": [
       "moscow"
   ],
   "features": [
       {
           "id": "place.9707587740083070",
           "type": "Feature",
           "place_type": [
               "place"
           ],
           "relevance": 1,
           "properties": {
               "wikidata": "Q649"
           },
           "text": "Москва",
           "place_name": "Москва, Город Москва, Russia",
           "matching_text": "Moscow",
           "matching_place_name": "Moscow, Город Москва, Russia",
           "bbox": [
               36.8030478778,
               55.1424098289334,
               37.9672888618296,
               56.0200519998407
           ],
           "center": [
               37.61778,
               55.75583
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   37.61778,
                   55.75583
               ]
           },
           "context": [
               {
                   "id": "region.9211611335557580",
                   "short_code": "RU-MOW",
                   "wikidata": "Q649",
                   "text": "Город Москва"
               },
               {
                   "id": "country.10008046970720960",
                   "short_code": "ru",
                   "wikidata": "Q159",
                   "text": "Russia"
               }
           ]
       },
       {
           "id": "region.9211611335557580",
           "type": "Feature",
           "place_type": [
               "region"
           ],
           "relevance": 1,
           "properties": {
               "short_code": "RU-MOW",
               "wikidata": "Q649"
           },
           "text": "Город Москва",
           "place_name": "Город Москва, Russia",
           "matching_text": "Moscow",
           "matching_place_name": "Moscow, Russia",
           "bbox": [
               37.129646,
               55.490567,
               37.962469,
               56.020768
           ],
           "center": [
               37.61778,
               55.75583
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   37.61778,
                   55.75583
               ]
           },
           "context": [
               {
                   "id": "country.10008046970720960",
                   "short_code": "ru",
                   "wikidata": "Q159",
                   "text": "Russia"
               }
           ]
       },
       {
           "id": "region.4921716942413970",
           "type": "Feature",
           "place_type": [
               "region"
           ],
           "relevance": 1,
           "properties": {
               "short_code": "RU-MOS",
               "wikidata": "Q1697"
           },
           "text": "Московская область",
           "place_name": "Московская область, Russia",
           "matching_text": "Moscow Oblast",
           "matching_place_name": "Moscow Oblast, Russia",
           "bbox": [
               35.143634,
               54.255257,
               40.205582,
               56.961685
           ],
           "center": [
               37.738,
               55.628
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   37.738,
                   55.628
               ]
           },
           "context": [
               {
                   "id": "country.10008046970720960",
                   "short_code": "ru",
                   "wikidata": "Q159",
                   "text": "Russia"
               }
           ]
       },
       {
           "id": "place.19031168784618380",
           "type": "Feature",
           "place_type": [
               "place"
           ],
           "relevance": 1,
           "properties": {
               "wikidata": "Q499927"
           },
           "text": "Moscow",
           "place_name": "Moscow, Idaho, United States",
           "bbox": [
               -117.03995202,
               46.6039941176436,
               -116.789356995108,
               46.8304461192348
           ],
           "center": [
               -117.0002,
               46.7324
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   -117.0002,
                   46.7324
               ]
           },
           "context": [
               {
                   "id": "region.8636888917605220",
                   "short_code": "US-ID",
                   "wikidata": "Q1221",
                   "text": "Idaho"
               },
               {
                   "id": "country.9053006287256050",
                   "short_code": "us",
                   "wikidata": "Q30",
                   "text": "United States"
               }
           ]
       },
       {
           "id": "place.14816276511618380",
           "type": "Feature",
           "place_type": [
               "place"
           ],
           "relevance": 1,
           "properties": {
               "wikidata": "Q1186607"
           },
           "text": "Moscow",
           "place_name": "Moscow, Pennsylvania, United States",
           "bbox": [
               -75.6943059894169,
               41.2288640409993,
               -75.3873106153993,
               41.4276180166518
           ],
           "center": [
               -75.5185,
               41.3367
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   -75.5185,
                   41.3367
               ]
           },
           "context": [
               {
                   "id": "region.2157738952111630",
                   "short_code": "US-PA",
                   "wikidata": "Q1400",
                   "text": "Pennsylvania"
               },
               {
                   "id": "country.9053006287256050",
                   "short_code": "us",
                   "wikidata": "Q30",
                   "text": "United States"
               }
           ]
       }
   ],
   "attribution": "NOTICE: © 2019 Mapbox and its suppliers. All rights reserved. Use of this data is subject to the Mapbox Terms of Service (https://www.mapbox.com/about/maps/). This response and the information it contains may not be retained. POI(s) provided by Foursquare."
}
`

// Response for "usa".
// Note the geometry bounds covering the whole world. In this case, use the viewport instead.
var mapboxUSA = `
{
   "type": "FeatureCollection",
   "query": [
       "usa"
   ],
   "features": [
       {
           "id": "country.9053006287256050",
           "type": "Feature",
           "place_type": [
               "country"
           ],
           "relevance": 1,
           "properties": {
               "short_code": "us",
               "wikidata": "Q30"
           },
           "text": "United States",
           "place_name": "United States",
           "matching_text": "USA",
           "matching_place_name": "USA",
           "bbox": [
               -179.9,
               18.765563,
               -66.885444,
               71.540724
           ],
           "center": [
               -100,
               40
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   -100,
                   40
               ]
           }
       },
       {
           "id": "place.5201278862470614",
           "type": "Feature",
           "place_type": [
               "place"
           ],
           "relevance": 1,
           "properties": {
               "wikidata": "Q189134"
           },
           "text": "Usak",
           "place_name": "Usak, Usak, Turkey",
           "bbox": [
               28.828607,
               38.476608,
               29.631972,
               38.902772
           ],
           "center": [
               29.4,
               38.68333
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   29.4,
                   38.68333
               ]
           },
           "context": [
               {
                   "id": "region.12729197259802190",
                   "short_code": "TR-64",
                   "wikidata": "Q483078",
                   "text": "Usak"
               },
               {
                   "id": "country.4833608844176230",
                   "short_code": "tr",
                   "wikidata": "Q43",
                   "text": "Turkey"
               }
           ]
       },
       {
           "id": "region.12729197259802190",
           "type": "Feature",
           "place_type": [
               "region"
           ],
           "relevance": 1,
           "properties": {
               "short_code": "TR-64",
               "wikidata": "Q483078"
           },
           "text": "Usak",
           "place_name": "Usak, Turkey",
           "bbox": [
               28.838478,
               38.212164,
               29.88813,
               38.885095
           ],
           "center": [
               29.34194,
               38.52389
           ],
           "geometry": {
               "type": "Point",
               "coordinates": [
                   29.34194,
                   38.52389
               ]
           },
           "context": [
               {
                   "id": "country.4833608844176230",
                   "short_code": "tr",
                   "wikidata": "Q43",
                   "text": "Turkey"
               }
           ]
       },
       {
           "id": "poi.558345750034",
           "type": "Feature",
           "place_type": [
               "poi"
           ],
           "relevance": 1,
           "properties": {
               "wikidata": "Q1433143",
               "address": "Uşak - Afyon Yolu 7. Km",
               "landmark": true,
               "category": "airport",
               "maki": "airport"
           },
           "text": "Uşak Havalimanı (USQ)",
           "place_name": "Uşak Havalimanı (USQ), Uşak - Afyon Yolu 7. Km, Usak, Usak, Turkey",
           "center": [
               29.481602000000002,
               38.6794835
           ],
           "geometry": {
               "coordinates": [
                   29.481602000000002,
                   38.6794835
               ],
               "type": "Point"
           },
           "context": [
               {
                   "id": "place.5201278862470614",
                   "wikidata": "Q189134",
                   "text": "Usak"
               },
               {
                   "id": "region.12729197259802190",
                   "short_code": "TR-64",
                   "wikidata": "Q483078",
                   "text": "Usak"
               },
               {
                   "id": "country.4833608844176230",
                   "short_code": "tr",
                   "wikidata": "Q43",
                   "text": "Turkey"
               }
           ]
       },
       {
           "id": "poi.438086664817",
           "type": "Feature",
           "place_type": [
               "poi"
           ],
           "relevance": 1,
           "properties": {
               "landmark": true,
               "wikidata": "Q1655287",
               "address": "9000 Airport Boulevard Northwest",
               "category": "airport",
               "maki": "airport"
           },
           "text": "Concord Regional Airport",
           "place_name": "Concord Regional Airport, 9000 Airport Boulevard Northwest, Concord, North Carolina 28027, United States",
           "matching_text": "USA",
           "matching_place_name": "USA, 9000 Airport Boulevard Northwest, Concord, North Carolina 28027, United States",
           "center": [
               -80.7115985,
               35.3841395
           ],
           "geometry": {
               "coordinates": [
                   -80.7115985,
                   35.3841395
               ],
               "type": "Point"
           },
           "context": [
               {
                   "id": "neighborhood.287495",
                   "text": "Favoni Corporate Center"
               },
               {
                   "id": "postcode.9893405202347250",
                   "text": "28027"
               },
               {
                   "id": "place.9994527680580390",
                   "wikidata": "Q1030184",
                   "text": "Concord"
               },
               {
                   "id": "region.2248353445854480",
                   "short_code": "US-NC",
                   "wikidata": "Q1454",
                   "text": "North Carolina"
               },
               {
                   "id": "country.9053006287256050",
                   "short_code": "us",
                   "wikidata": "Q30",
                   "text": "United States"
               }
           ]
       }
   ],
   "attribution": "NOTICE: © 2019 Mapbox and its suppliers. All rights reserved. Use of this data is subject to the Mapbox Terms of Service (https://www.mapbox.com/about/maps/). This response and the information it contains may not be retained. POI(s) provided by Foursquare."
}
`
