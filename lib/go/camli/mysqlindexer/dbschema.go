/*
Copyright 2011 Google Inc.

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

package mysqlindexer

import ()

const requiredSchemaVersion = 18

func SchemaVersion() int {
	return requiredSchemaVersion
}

func SQLCreateTables() []string {
	return []string{

		`CREATE TABLE blobs (
blobref VARCHAR(128) NOT NULL PRIMARY KEY,
size INTEGER NOT NULL,
type VARCHAR(100))`,

		`CREATE TABLE claims (
blobref VARCHAR(128) NOT NULL PRIMARY KEY,
signer VARCHAR(128) NOT NULL,
verifiedkeyid VARCHAR(128) NULL,
date VARCHAR(40) NOT NULL, 
  INDEX (signer, date),
  INDEX (verifiedkeyid, date),
unverified CHAR(1) NULL,
claim VARCHAR(50) NOT NULL,
permanode VARCHAR(128) NOT NULL,
  INDEX (permanode, signer, date),
attr VARCHAR(128) NULL,
value VARCHAR(128) NULL)`,

		`CREATE TABLE permanodes (
blobref VARCHAR(128) NOT NULL PRIMARY KEY,
unverified CHAR(1) NULL,
signer VARCHAR(128) NOT NULL DEFAULT '',
lastmod VARCHAR(40) NOT NULL DEFAULT '',
INDEX (signer, lastmod))`,

		`CREATE TABLE bytesfiles (
schemaref VARCHAR(128) NOT NULL,
camlitype VARCHAR(32) NOT NULL,
wholedigest VARCHAR(128) NOT NULL,
filename VARCHAR(255),
size BIGINT,
mime VARCHAR(255),
PRIMARY KEY(schemaref, wholedigest),
INDEX (wholedigest))`,

		// For index.PermanodeOfSignerAttrValue:
		// Rows are one per camliType "claim", for claimType "set-attribute" or "add-attribute",
		// for attribute values that are known (needed to be indexed, e.g. "camliNamedRoot")
		//
		// keyid is verified GPG KeyId (e.g. "2931A67C26F5ABDA")
		// attr is e.g. "camliNamedRoot"
		// value is the claim's "value" field
		// claimdate is the "claimDate" field.
		// blobref is the blobref of the claim.
		// permanode is the claim's "permaNode" field.
		`CREATE TABLE signerattrvalue (
keyid VARCHAR(128) NOT NULL,
attr VARCHAR(128) NOT NULL,
value VARCHAR(255) NOT NULL,
claimdate VARCHAR(40) NOT NULL,
INDEX (keyid, attr, value, claimdate),
blobref VARCHAR(128) NOT NULL,
PRIMARY KEY (blobref),
permanode VARCHAR(128) NOT NULL,
INDEX (permanode))`,

		// "Shadow" copy of signerattrvalue for fulltext searches.
		// Kept in sync witch signerattrvalue directly in the go code for now, not with triggers.
		// As of MySQL 5.5, fulltext search is still only available with MyISAM tables
		// (see http://dev.mysql.com/doc/refman/5.5/en/fulltext-search.html)
		`CREATE TABLE signerattrvalueft (
keyid VARCHAR(128) NOT NULL,
attr VARCHAR(128) NOT NULL,
value VARCHAR(255) NOT NULL,
claimdate VARCHAR(40) NOT NULL,
INDEX (keyid, attr, value, claimdate),
blobref VARCHAR(128) NOT NULL,
PRIMARY KEY (blobref),
permanode VARCHAR(128) NOT NULL,
INDEX (permanode),
FULLTEXT (value)) TYPE=MyISAM`,

		`CREATE TABLE meta (
metakey VARCHAR(255) NOT NULL PRIMARY KEY,
value VARCHAR(255) NOT NULL)`,

		// Map from blobref (of ASCII armored public key) to keyid
		`CREATE TABLE signerkeyid (
blobref VARCHAR(128) NOT NULL,
PRIMARY KEY (blobref),
keyid   VARCHAR(128) NOT NULL,
INDEX (keyid)
)`,

		// Bi-direction index of camliPath claims
		// active is "Y" or "N".
		`CREATE TABLE path (
claimref VARCHAR(128) NOT NULL,
PRIMARY KEY (claimref),
claimdate VARCHAR(40) NOT NULL,
keyid VARCHAR(128) NOT NULL,
baseref VARCHAR(128) NOT NULL,
suffix VARCHAR(255) NOT NULL,
targetref VARCHAR(128) NOT NULL,
active CHAR(1) NOT NULL,
INDEX (keyid, baseref, suffix),
INDEX (targetref, keyid),
INDEX (baseref, keyid)
)`,
	}
}
