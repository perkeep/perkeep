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

package importer

// TODO(mpl): use these on all the importers.
const (
	// Account or user identity.

	// AcctAttrUserID is the account's internal representation, and often an account number.
	// It is usually required as an argument in API calls to the site we import from.
	// Not found on schema.org.
	// Example: "3179713".
	AcctAttrUserID = "userID"
	// AcctAttrUserName is the public identifier of the account. Commonly referred to as
	// "username", or "screen name", or "account name". Often a one word string.
	// Not found on schema.org.
	// Example: "johnSmith" from Twitter's "@johnSmith".
	AcctAttrUserName = "userName"

	// AcctAttrCompletedVersion records the importer-specific
	// "version number" that last ran to completion, doing a full
	// importer. When importers are updated with new behavior,
	// they update their version number and that triggers a full
	// import, rather than incremental imports.
	AcctAttrCompletedVersion = "completedVersion"

	// AcctAttrName is a longer or alternate public representation of the account's name.
	// It is often the full name of the person's account (family name and given name), thus
	// sometimes redundant with the combination of acctAttrFamilyName and acctAttrGivenName.
	// Found at http://schema.org/Person.
	// Example: "John Smith".
	AcctAttrName = "name"
	// http://schema.org/givenName
	// Example: "John".
	AcctAttrGivenName = "givenName"
	// http://schema.org/familyName
	// Example: "Smith".
	AcctAttrFamilyName = "familyName"

	// Generic item, object.

	// ItemAttrID is the generic identifier of an item when nothing suitable and more specific
	// was found on http://schema.org. Usually a number.
	AttrID = "ID"
	// http://schema.org/name
	AttrName = "name"
	// Free-flowing text definition of a location or place, such
	// as a city name, or a full postal address.
	AttrLocationText = "locationText"
	// AttrURL is the item's original or origin URL.
	AttrURL = "url"

	// AttrStartDate is http://schema.org/startDate: The start
	// date and time of the event or item (in ISO 8601 date
	// format)
	AttrStartDate = "startDate"

	// Image, photo.

)
