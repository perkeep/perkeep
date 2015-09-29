/*
Copyright 2015 The Camlistore Authors

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

package flickr

import (
	"encoding/json"
	"testing"
)

func TestParseJSONFloat(t *testing.T) {
	var ps photosSearch
	if err := json.Unmarshal([]byte(photosSearchData1), &ps); err != nil {
		t.Errorf("unmarshal error: %v", err)
	}
	for i, want := range []jsonFloat{-37.899166, -37.899166, 0} {
		if ps.Photos.Photo[i].Latitude != want {
			t.Errorf("%d. want latitude=%f got %f", i+1, want, ps.Photos.Photo[i].Latitude)
		}
	}
}
func TestParseJSONInt(t *testing.T) {
	var ps photosSearch
	if err := json.Unmarshal([]byte(photosSearchData2), &ps); err != nil {
		t.Errorf("unmarshal error: %v", err)
	}
	for i, want := range []jsonInt{1, 1} {
		if ps.Photos.Photo[i].IsPublic != want {
			t.Errorf("%d. want ispublic=%d, got %d", i+1, want, ps.Photos.Photo[i].IsPublic)
		}
	}
}

const (
	photosSearchData2 = `{
  "photos": {
    "page": 63,
    "pages": 63,
    "perpage": 100,
    "total": 6236,
    "photo": [
      {
        "id": "0000084",
        "owner": "00002737@N00",
        "secret": "0000f03945",
        "server": "3",
        "farm": 1,
        "title": "Machinery in Brickmakers' park",
        "ispublic": 1,
        "isfriend": 0,
        "isfamily": 0,
        "description": {
          "_content": ""
        },
        "dateupload": "1106791534",
        "lastupdate": "1251702196",
        "datetaken": "2005-01-26 07:47:33",
        "datetakengranularity": "0",
        "datetakenunknown": 0,
        "views": "145",
        "tags": "australia melbourne victoria machinery oakleigh pc3166 auspctagged 3166 geo:country=australia geo:zip=3166 brickmakerspark",
        "machine_tags": "geo:zip=3166 geo:country=australia",
        "originalsecret": "0000f03945",
        "originalformat": "jpg",
        "latitude": "-37.895717",
        "longitude": "145.099933",
        "accuracy": "15",
        "context": 0,
        "place_id": "0000fRpQU7qUoVfD",
        "woeid": "0000751",
        "geo_is_family": 0,
        "geo_is_friend": 0,
        "geo_is_contact": 0,
        "geo_is_public": 1,
        "media": "photo",
        "media_status": "ready",
        "url_o": "https://farm1.staticflickr.com/3/3850184_87b1f03945_o.jpg",
        "height_o": "640",
        "width_o": "480"
      },
      {
        "id": "3850183",
        "owner": "47322737@N00",
        "secret": "492b7f19de",
        "server": "3",
        "farm": 1,
        "title": "Machinery in Brickmakers' park",
        "ispublic": "1",
        "isfriend": 0,
        "isfamily": 0,
        "description": {
          "_content": ""
        },
        "dateupload": "1106791534",
        "lastupdate": "1251702196",
        "datetaken": "2005-01-26 07:50:58",
        "datetakengranularity": "0",
        "datetakenunknown": 0,
        "views": "204",
        "tags": "australia melbourne victoria machinery oakleigh pc3166 auspctagged 3166 geo:country=australia geo:zip=3166 brickmakerspark",
        "machine_tags": "geo:zip=3166 geo:country=australia",
        "originalsecret": "492b7f19de",
        "originalformat": "jpg",
        "latitude": "-37.895717",
        "longitude": "145.099933",
        "accuracy": "15",
        "context": 0,
        "place_id": "SrpyfRpQU7qUoVfD",
        "woeid": "1104751",
        "geo_is_family": 0,
        "geo_is_friend": 0,
        "geo_is_contact": 0,
        "geo_is_public": 1,
        "media": "photo",
        "media_status": "ready",
        "url_o": "https://farm1.staticflickr.com/3/3850183_492b7f19de_o.jpg",
        "height_o": "480",
        "width_o": "640"
      }
    ]
  },
  "stat": "ok"
}`

	photosSearchData1 = `{
  "photos": {
    "page": 1,
    "pages": 63,
    "perpage": 100,
    "total": "6226",
    "photo": [
      {
        "id": "00007283018",
        "owner": "00002737@N00",
        "secret": "00000fa7ec",
        "server": "331",
        "farm": 1,
        "title": "The mysterious masked man waits for his #milkshake",
        "ispublic": 1,
        "isfriend": 0,
        "isfamily": 0,
        "description": {
          "_content": ""
        },
        "dateupload": "1435974606",
        "lastupdate": "1435974611",
        "datetaken": "2015-07-04 11:50:06",
        "datetakengranularity": 0,
        "datetakenunknown": "1",
        "views": "0",
        "tags": "square squareformat juno iphoneography instagramapp uploaded:by=instagram",
        "machine_tags": "uploaded:by=instagram",
        "originalsecret": "0000958ab8",
        "originalformat": "jpg",
        "latitude": "-37.899166",
        "longitude": "145.090277",
        "accuracy": "16",
        "context": 0,
        "place_id": "0000fRpQU7qUoVfD",
        "woeid": "0000751",
        "geo_is_family": 0,
        "geo_is_friend": 0,
        "geo_is_contact": 0,
        "geo_is_public": 1,
        "media": "photo",
        "media_status": "ready",
        "url_o": "https://farm1.staticflickr.com/331/00007283018_0000958ab8_o.jpg",
        "height_o": "1080",
        "width_o": "1080"
      },
      {
        "id": "00001743956",
        "owner": "00002737@N00",
        "secret": "aa00088ef7",
        "server": "380",
        "farm": 1,
        "title": "A #LEGO #maze",
        "ispublic": 1,
        "isfriend": 0,
        "isfamily": 0,
        "description": {
          "_content": ""
        },
        "dateupload": "1435481921",
        "lastupdate": "1435481924",
        "datetaken": "2015-06-28 18:58:41",
        "datetakengranularity": 0,
        "datetakenunknown": "1",
        "views": "33",
        "tags": "square squareformat lark iphoneography instagramapp uploaded:by=instagram",
        "machine_tags": "uploaded:by=instagram",
        "originalsecret": "000df6239a",
        "originalformat": "jpg",
        "latitude": -37.899166,
        "longitude": "0",
        "accuracy": 0,
        "context": 0,
        "media": "photo",
        "media_status": "ready",
        "url_o": "https://farm1.staticflickr.com/380/00001743956_0000f6239a_o.jpg",
        "height_o": "640",
        "width_o": "640"
      },
      {
        "id": "00001743956",
        "owner": "00002737@N00",
        "secret": "aa00088ef7",
        "server": "380",
        "farm": 1,
        "title": "A #LEGO #maze",
        "ispublic": 1,
        "isfriend": 0,
        "isfamily": 0,
        "description": {
          "_content": ""
        },
        "dateupload": "1435481921",
        "lastupdate": "1435481924",
        "datetaken": "2015-06-28 18:58:41",
        "datetakengranularity": 0,
        "datetakenunknown": "1",
        "views": "33",
        "tags": "square squareformat lark iphoneography instagramapp uploaded:by=instagram",
        "machine_tags": "uploaded:by=instagram",
        "originalsecret": "000df6239a",
        "originalformat": "jpg",
        "latitude": 0,
        "longitude": 0,
        "accuracy": 0,
        "context": 0,
        "media": "photo",
        "media_status": "ready",
        "url_o": "https://farm1.staticflickr.com/380/00001743956_0000f6239a_o.jpg",
        "height_o": "640",
        "width_o": "640"
      }
	]
  },
  "stat": "ok"
}`
)
