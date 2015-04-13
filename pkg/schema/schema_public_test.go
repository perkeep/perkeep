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

package schema_test

import (
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
)

func TestShareSearchSerialization(t *testing.T) {
	signer := blob.MustParse("yyy-5678")

	q := &search.SearchQuery{
		Expression: "is:image",
		Limit:      42,
	}
	bb := schema.NewShareRef(schema.ShareHaveRef, true)
	bb.SetShareSearch(q)
	bb = bb.SetSigner(signer)
	bb = bb.SetClaimDate(time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC))
	s := bb.Blob().JSON()

	want := `{"camliVersion": 1,
  "authType": "haveref",
  "camliSigner": "yyy-5678",
  "camliType": "claim",
  "claimDate": "2009-11-10T23:00:00Z",
  "claimType": "share",
  "search": {
    "expression": "is:image",
    "limit": 42,
    "around": null
  },
  "transitive": true
}`
	if want != s {
		t.Errorf("Incorrect serialization of shared search. Wanted:\n %s\nGot:\n%s\n", want, s)
	}
}
