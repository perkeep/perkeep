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

package flickr

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/osutil"
)

var _ importer.TestDataMaker = imp{}

func (im imp) SetTestAccount(acctNode *importer.Object) error {
	return acctNode.SetAttrs(
		importer.AcctAttrAccessToken, "fakeAccessToken",
		importer.AcctAttrAccessTokenSecret, "fakeAccessSecret",
		importer.AcctAttrUserID, "fakeUserId",
		importer.AcctAttrName, "fakeName",
		importer.AcctAttrUserName, "fakeScreenName",
	)
}

func (im imp) MakeTestData() http.RoundTripper {
	const (
		nPhotosets = 5 // Arbitrary number of sets.
		perPage    = 3 // number of photos per page (both when getting sets and when getting photos).
		fakeUserId = "fakeUserId"
	)
	// Photoset N has N photos, so we've got 15 ( = 5 + 4 + 3 + 2 + 1) photos in total.
	var nPhotos int
	for i := 1; i <= nPhotosets; i++ {
		nPhotos += i
	}
	nPhotosPages := nPhotos / perPage
	if nPhotos%perPage != 0 {
		nPhotosPages++
	}

	okHeader := `HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8

`

	// TODO(mpl): this scheme does not take into account that we could have the same photo
	// in different albums. These two photos will end up with a different photoId.
	buildPhotoIds := func(nsets, perPage int) []string {
		var ids []string
		for i := 1; i <= nsets; i++ {
			photosetId := blob.RefFromString(fmt.Sprintf("Photoset %d", i)).DigestPrefix(10)
			page := 1
			// Photoset N has N photos.
			indexOnPage := 1
			for j := 1; j <= i; j++ {
				photoId := blob.RefFromString(fmt.Sprintf("Photo %d on page %d of photoset %s", indexOnPage, page, photosetId)).DigestPrefix(10)
				ids = append(ids, photoId)
				indexOnPage++
				if indexOnPage > perPage {
					page++
					indexOnPage = 1
				}
			}
		}
		return ids
	}
	photoIds := buildPhotoIds(nPhotosets, perPage)

	responses := make(map[string]func() *http.Response)
	// Initial photo sets list
	photosetsURL := fmt.Sprintf("%s?format=json&method=%s&nojsoncallback=1&user_id=%s", apiURL, photosetsAPIPath, fakeUserId)
	response := fmt.Sprintf("%s%s", okHeader, fakePhotosetsList(nPhotosets))
	responses[photosetsURL] = httputil.StaticResponder(response)

	// All the photoset calls. One call for each page of each photoset.
	// Each page as perPage photos, or maybe less if end of the photoset.
	{
		pageStart := 0
		albumEnd, pageEnd, albumNum, pages, page := 1, 1, 1, 1, 1
		photosetId := blob.RefFromString(fmt.Sprintf("Photoset %d", albumNum)).DigestPrefix(10)
		photosURL := fmt.Sprintf("%s?extras=original_format&format=json&method=%s&nojsoncallback=1&page=%d&photoset_id=%s&user_id=%s",
			apiURL, photosetAPIPath, page, photosetId, fakeUserId)
		response := fmt.Sprintf("%s%s", okHeader, fakePhotoset(photosetId, page, pages, photoIds[pageStart:pageEnd]))
		responses[photosURL] = httputil.StaticResponder(response)
		for k, _ := range photoIds {
			if k < pageEnd {
				continue
			}
			page++
			pageStart = k
			pageEnd = k + perPage
			if page > pages {
				albumNum++
				page = 1
				pages = albumNum / perPage
				if albumNum%perPage != 0 {
					pages++
				}
				albumEnd = pageStart + albumNum
				photosetId = blob.RefFromString(fmt.Sprintf("Photoset %d", albumNum)).DigestPrefix(10)
			}
			if pageEnd > albumEnd {
				pageEnd = albumEnd
			}
			photosURL := fmt.Sprintf("%s?extras=original_format&format=json&method=%s&nojsoncallback=1&page=%d&photoset_id=%s&user_id=%s",
				apiURL, photosetAPIPath, page, photosetId, fakeUserId)
			response := fmt.Sprintf("%s%s", okHeader, fakePhotoset(photosetId, page, pages, photoIds[pageStart:pageEnd]))
			responses[photosURL] = httputil.StaticResponder(response)
		}
	}

	// All the photo page calls (to get the photos info).
	// Each page has perPage photos, until end of photos.
	for i := 1; i <= nPhotosPages; i++ {
		photosURL := fmt.Sprintf("%s?extras=", apiURL) +
			url.QueryEscape("description,date_upload,date_taken,original_format,last_update,geo,tags,machine_tags,views,media,url_o") +
			fmt.Sprintf("&format=json&method=%s&nojsoncallback=1&page=%d&user_id=%s", photosAPIPath, i, fakeUserId)
		response := fmt.Sprintf("%s%s", okHeader, fakePhotosPage(i, nPhotosPages, perPage, photoIds))
		responses[photosURL] = httputil.StaticResponder(response)
	}

	// Actual photo(s) URL.
	pudgyPic := fakePicture()
	for _, v := range photoIds {
		photoURL := fmt.Sprintf("https://farm3.staticflickr.com/2897/14198397111_%s_o.jpg?user_id=%s", v, fakeUserId)
		responses[photoURL] = httputil.FileResponder(pudgyPic)
	}

	return httputil.NewFakeTransport(responses)
}

func fakePhotosetsList(sets int) string {
	var photosets []*photosetInfo
	for i := 1; i <= sets; i++ {
		title := fmt.Sprintf("Photoset %d", i)
		photosetId := blob.RefFromString(title).DigestPrefix(10)
		primaryPhotoId := blob.RefFromString(fmt.Sprintf("Photo 1 on page 1 of photoset %s", photosetId)).DigestPrefix(10)
		item := &photosetInfo{
			Id:             photosetId,
			PrimaryPhotoId: primaryPhotoId,
			Title:          contentString{Content: title},
			Description:    contentString{Content: "fakePhotosetDescription"},
		}
		photosets = append(photosets, item)
	}

	setslist := struct {
		Photosets photosetList
	}{
		Photosets: photosetList{
			Photoset: photosets,
		},
	}

	list, err := json.MarshalIndent(&setslist, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	return string(list)
}

func fakePhotoset(photosetId string, page, pages int, photoIds []string) string {
	var photos []struct {
		Id             string
		OriginalFormat string
	}
	for _, v := range photoIds {
		item := struct {
			Id             string
			OriginalFormat string
		}{
			Id:             v,
			OriginalFormat: "jpg",
		}
		photos = append(photos, item)
	}

	photoslist := struct {
		Photoset photosetItems
	}{
		Photoset: photosetItems{
			Id:    photosetId,
			Page:  jsonInt(page),
			Pages: jsonInt(pages),
			Photo: photos,
		},
	}

	list, err := json.MarshalIndent(&photoslist, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	return string(list)

}

func fakePhotosPage(page, pages, perPage int, photoIds []string) string {
	var photos []*photosSearchItem
	currentPage := 1
	indexOnPage := 1
	day := time.Hour * 24
	year := day * 365
	const dateCreatedFormat = "2006-01-02 15:04:05"

	for k, v := range photoIds {
		if indexOnPage > perPage {
			currentPage++
			indexOnPage = 1
		}
		if currentPage < page {
			indexOnPage++
			continue
		}
		created := time.Now().Add(-time.Duration(k) * year)
		published := created.Add(day)
		updated := published.Add(day)
		item := &photosSearchItem{
			Id:             v,
			Title:          fmt.Sprintf("Photo %d", k+1),
			Description:    contentString{Content: "fakePhotoDescription"},
			DateUpload:     fmt.Sprintf("%d", published.Unix()),
			DateTaken:      created.Format(dateCreatedFormat),
			LastUpdate:     fmt.Sprintf("%d", updated.Unix()),
			URL:            fmt.Sprintf("https://farm3.staticflickr.com/2897/14198397111_%s_o.jpg", v),
			OriginalFormat: "jpg",
		}
		photos = append(photos, item)
		if len(photos) >= perPage {
			break
		}
		indexOnPage++
	}

	photosPage := &photosSearch{
		Photos: struct {
			Page    jsonInt
			Pages   jsonInt
			Perpage jsonInt
			Total   jsonInt
			Photo   []*photosSearchItem
		}{
			Page:    jsonInt(page),
			Pages:   jsonInt(pages),
			Perpage: jsonInt(perPage),
			Photo:   photos,
		},
	}

	list, err := json.MarshalIndent(photosPage, "", "	")
	if err != nil {
		log.Fatalf("%v", err)
	}
	return string(list)

}

func fakePicture() string {
	camliDir, err := osutil.GoPackagePath("camlistore.org")
	if err == os.ErrNotExist {
		log.Fatal("Directory \"camlistore.org\" not found under GOPATH/src; are you not running with devcam?")
	}
	if err != nil {
		log.Fatalf("Error searching for \"camlistore.org\" under GOPATH: %v", err)
	}
	return filepath.Join(camliDir, filepath.FromSlash("third_party/glitch/npc_piggy__x1_walk_png_1354829432.png"))
}
