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

// Package nodeattr contains constants for permanode attribute names.
//
// For all date values in RFC 3339 format, Camlistore additionally
// treats the special timezone offset -00:01 (one minute west of UTC)
// as meaning that the local time was known, but the location or
// timezone was not. Usually this is from EXIF files.
package nodeattr

const (
	// Type is the Camlistore permanode type ("camliNodeType").
	// Importer-specific ones are of the form "domain.com:objecttype".
	// Well-defined ones are documented in doc/schema/claims/attributes.txt.
	Type = "camliNodeType"

	// CamliContent is "camliContent", the blobref of the permanode's content.
	// For files or images, the camliContent is fileref (the blobref of
	// the "file" schema blob).
	CamliContent = "camliContent"

	// DateCreated is http://schema.org/dateCreated in RFC 3339
	// format.
	DateCreated = "dateCreated"

	// StartDate is http://schema.org/startDate, the start date
	// and time of the event or item, in RFC 3339 format.
	StartDate = "startDate"

	// DateModified is http://schema.org/dateModified, in RFC 3339
	// format.
	DateModified = "dateModified"

	// DatePublished is http://schema.org/datePublished in RFC
	// 3339 format.
	DatePublished = "datePublished"

	// Title is http://schema.org/title
	Title = "title"

	// Description is http://schema.org/description
	// Value is plain text, no HTML, newlines are newlines.
	Description = "description"

	// Content is "content", used e.g. for the content of a tweet.
	// TODO: define this more
	Content = "content"

	// URL is the item's original or origin URL.
	URL = "url"

	// LocationText is free-flowing text definition of a location or place, such
	// as a city name, or a full postal address.
	LocationText = "locationText"

	Latitude  = "latitude"
	Longitude = "longitude"
)
