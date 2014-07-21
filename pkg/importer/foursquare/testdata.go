/*
Copyright 2014 The Camlistore Authors

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

package foursquare

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/osutil"
)

var _ importer.TestDataMaker = (*imp)(nil)

func (im *imp) SetTestAccount(acctNode *importer.Object) error {
	// TODO(mpl): refactor with twitter
	return acctNode.SetAttrs(
		importer.AcctAttrAccessToken, "fakeAccessToken",
		importer.AcctAttrAccessTokenSecret, "fakeAccessSecret",
		importer.AcctAttrUserID, "fakeUserID",
		importer.AcctAttrName, "fakeName",
		importer.AcctAttrUserName, "fakeScreenName",
	)
}

func (im *imp) MakeTestData() http.RoundTripper {

	const nCheckins = 150 // Arbitrary number of checkins generated.

	// if you add another venue, make sure the venueCounter reset
	// in fakeCheckinsList allows for that case to happen.
	// We could use global vars instead, but don't want to pollute the
	// fousquare pkg namespace.
	towns := map[int]*venueLocationItem{
		0: {
			Address:    "Baker street",
			City:       "Dublin",
			PostalCode: "0",
			State:      "none",
			Country:    "Ireland",
			Lat:        53.4053427,
			Lng:        -8.3320801,
		},
		1: {
			Address:    "Fish&Ships street",
			City:       "London",
			PostalCode: "1",
			State:      "none",
			Country:    "England",
			Lat:        55.3617609,
			Lng:        -3.4433238,
		},
		2: {
			Address:    "Haggis street",
			City:       "Glasgow",
			PostalCode: "2",
			State:      "none",
			Country:    "Scotland",
			Lat:        57.7394571,
			Lng:        -4.686997,
		},
		3: {
			Address:    "rue du croissant",
			City:       "Grenoble",
			PostalCode: "38000",
			State:      "none",
			Country:    "France",
			Lat:        45.1841655,
			Lng:        5.7155424,
		},
		4: {
			Address:    "burrito street",
			City:       "San Francisco",
			PostalCode: "94114",
			State:      "CA",
			Country:    "US",
			Lat:        37.7593625,
			Lng:        -122.4266995,
		},
	}

	// We need to compute the venueIds in advance, because the venue id is used as a parameter
	// in some of the requests we need to register.
	var venueIds []string
	for _, v := range towns {
		venueIds = append(venueIds, blob.RefFromString(v.City).DigestPrefix(10))
	}

	checkinsURL := apiURL + checkinsAPIPath
	checkinsListCached := make(map[int]string)
	okHeader := `HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8

`

	responses := make(map[string]func() *http.Response)

	// register all the checkins calls; offset varies.
	for i := 0; i < nCheckins; i += checkinsRequestLimit {
		url := fmt.Sprintf("%s?limit=%d&oauth_token=fakeAccessToken&offset=%d&v=%s",
			checkinsURL, checkinsRequestLimit, i, apiVersion)
		response := okHeader + fakeCheckinsList(i, nCheckins, towns, checkinsListCached)
		responses[url] = httputil.StaticResponder(response)
	}

	// register all the venue photos calls (venueId varies)
	photosURL := apiURL + "venues"
	photosResponse := okHeader + fakePhotosList()
	for _, id := range venueIds {
		url := fmt.Sprintf("%s/%s/photos?limit=%d&oauth_token=fakeAccessToken&v=%s",
			photosURL, id, photosRequestLimit, apiVersion)
		responses[url] = httputil.StaticResponder(photosResponse)
	}

	// register the photoitem calls
	pudgyPic := fakePhoto()
	photoURL := "https://camlistore.org/pic/pudgy.png"
	originalPhotoURL := "https://camlistore.org/original/pic/pudgy.png"
	iconURL := "https://camlistore.org/bg_88/pic/pudgy.png"
	responses[photoURL] = httputil.FileResponder(pudgyPic)
	responses[originalPhotoURL] = httputil.FileResponder(pudgyPic)
	responses[iconURL] = httputil.FileResponder(pudgyPic)

	return httputil.NewFakeTransport(responses)
}

// fakeCheckinsList returns a JSON checkins list of checkinsRequestLimit checkin
// items, starting at offset. It stops before checkinsRequestLimit if maxCheckin is
// reached. It uses towns to populate the venues. The returned list is saved in
// cached.
func fakeCheckinsList(offset, maxCheckin int, towns map[int]*venueLocationItem, cached map[int]string) string {
	if cl, ok := cached[offset]; ok {
		return cl
	}
	max := offset + checkinsRequestLimit
	if max > maxCheckin {
		max = maxCheckin
	}
	var items []*checkinItem
	tzCounter := 0
	venueCounter := 0
	for i := offset; i < max; i++ {
		shout := fmt.Sprintf("fakeShout %d", i)
		item := &checkinItem{
			Id:             blob.RefFromString(shout).DigestPrefix(10),
			Shout:          shout,
			CreatedAt:      time.Now().Unix(),
			TimeZoneOffset: tzCounter * 60,
			Venue:          fakeVenue(venueCounter, towns),
		}
		items = append(items, item)
		tzCounter++
		venueCounter++
		if tzCounter == 24 {
			tzCounter = 0
		}
		if venueCounter == 5 {
			venueCounter = 0
		}
	}

	response := struct {
		Checkins struct {
			Items []*checkinItem
		}
	}{
		Checkins: struct {
			Items []*checkinItem
		}{
			Items: items,
		},
	}
	list, err := json.MarshalIndent(checkinsList{Response: response}, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	cached[offset] = string(list)
	return cached[offset]
}

func fakeVenue(counter int, towns map[int]*venueLocationItem) venueItem {
	prefix := "https://camlistore.org/"
	suffix := "/pic/pudgy.png"
	// TODO: add more.
	categories := []*venueCategory{
		{
			Primary: true,
			Name:    "town",
			Icon: &categoryIcon{
				Prefix: prefix,
				Suffix: suffix,
			},
		},
	}

	return venueItem{
		Id:         blob.RefFromString(towns[counter].City).DigestPrefix(10),
		Name:       towns[counter].City,
		Location:   towns[counter],
		Categories: categories,
	}
}

func fakePhotosList() string {
	items := []*photoItem{
		fakePhotoItem(),
	}
	response := struct {
		Photos struct {
			Items []*photoItem
		}
	}{
		Photos: struct {
			Items []*photoItem
		}{
			Items: items,
		},
	}
	list, err := json.MarshalIndent(photosList{Response: response}, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	return string(list)
}

func fakePhotoItem() *photoItem {
	prefix := "https://camlistore.org/"
	suffix := "/pic/pudgy.png"
	return &photoItem{
		Id:     blob.RefFromString(prefix + suffix).DigestPrefix(10),
		Prefix: prefix,
		Suffix: suffix,
		Width:  704,
		Height: 186,
	}
}

// TODO(mpl): refactor with twitter
func fakePhoto() string {
	camliDir, err := osutil.GoPackagePath("camlistore.org")
	if err == os.ErrNotExist {
		log.Fatal("Directory \"camlistore.org\" not found under GOPATH/src; are you not running with devcam?")
	}
	if err != nil {
		log.Fatalf("Error searching for \"camlistore.org\" under GOPATH: %v", err)
	}
	return filepath.Join(camliDir, filepath.FromSlash("third_party/glitch/npc_piggy__x1_walk_png_1354829432.png"))
}
