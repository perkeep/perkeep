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

// Types for Foursquare's JSON API.

package foursquare

type user struct {
	Id        string
	FirstName string
	LastName  string
}

type userInfo struct {
	Response struct {
		User user
	}
}

type checkinsList struct {
	Response struct {
		Checkins struct {
			Items []*checkinItem
		}
	}
}

type checkinItem struct {
	Id             string
	CreatedAt      int64  // unix time in seconds from 4sq
	TimeZoneOffset int    // offset in minutes. positive is east.
	Shout          string // "Message from check-in, if present and visible to the acting user."
	Venue          venueItem
}

type venueItem struct {
	Id         string // eg 42474900f964a52087201fe3 from 4sq
	Name       string
	Location   *venueLocationItem
	Categories []*venueCategory
}

type photosList struct {
	Response struct {
		Photos struct {
			Items []*photoItem
		}
	}
}

type photoItem struct {
	Id     string
	Prefix string
	Suffix string
	Width  int
	Height int
}

func (vi *venueItem) primaryCategory() *venueCategory {
	for _, c := range vi.Categories {
		if c.Primary {
			return c
		}
	}
	return nil
}

func (vi *venueItem) icon() string {
	c := vi.primaryCategory()
	if c == nil || c.Icon == nil || c.Icon.Prefix == "" {
		return ""
	}
	return c.Icon.Prefix + "bg_88" + c.Icon.Suffix
}

type venueLocationItem struct {
	Address    string
	City       string
	PostalCode string
	State      string
	Country    string // 4sq provides "US"
	Lat        float64
	Lng        float64
}

type venueCategory struct {
	Primary bool
	Name    string
	Icon    *categoryIcon
}

type categoryIcon struct {
	Prefix string
	Suffix string
}
