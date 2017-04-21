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

package schema

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/jsonsign"
)

func TestSigner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	ent, err := jsonsign.NewEntity()
	if err != nil {
		t.Fatal(err)
	}
	armorPub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	pubRef := blob.SHA1FromString(armorPub)
	sig, err := NewSigner(pubRef, strings.NewReader(armorPub), ent)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	pn, err := NewUnsignedPermanode().Sign(sig)
	if err != nil {
		t.Fatalf("NewPermanode: %v", err)
	}
	if !strings.Contains(pn, `,"camliSig":"`) {
		t.Errorf("Permanode doesn't look signed: %v", pn)
	}
}

// TestClaimDate makes sure that when we sign a schema, we set the claimDate to
// the time of the signature.
// It demonstrates that issue #917 is fixed.
func TestClaimDate(t *testing.T) {
	ent, err := jsonsign.NewEntity()
	if err != nil {
		t.Fatal(err)
	}
	armorPub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	pubRef := blob.SHA1FromString(armorPub)
	sig, err := NewSigner(pubRef, strings.NewReader(armorPub), ent)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	sigTime, err := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
	if err != nil {
		t.Fatal(err)
	}
	share := NewShareRef(ShareHaveRef, true).SetShareTarget(pubRef)
	signed, err := share.SignAt(sig, sigTime)
	if err != nil {
		t.Fatal(err)
	}

	ss := &superset{}
	if err := json.NewDecoder(strings.NewReader(signed)).Decode(ss); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ss.ClaimDate.String(), "2006-01-02") {
		t.Fatalf("wrong claimDate in superset: got %q, wanted %q", ss.ClaimDate, sigTime)
	}
}
