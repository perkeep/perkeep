/*
Copyright 2026 The Perkeep Authors

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
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/jsonsign"
)

const (
	multiTestPN1 = "sha1-1111111111111111111111111111111111111111"
	multiTestPN2 = "sha1-2222222222222222222222222222222222222222"
)

func readAttributeTestClaim(t *testing.T, claimType, fields string) Claim {
	t.Helper()
	raw := `{"camliVersion":1,"camliType":"claim","claimType":` + fmt.Sprintf("%q", claimType) +
		`,"claimDate":"2026-10-14T12:00:00Z","camliSigner":"` + multiTestPN2 +
		`","camliSig":"test-signature"` + fields + `}`
	b, err := BlobFromReader(blob.RefFromString(raw), strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b.JSON() != raw {
		t.Fatal("BlobFromReader did not preserve original JSON")
	}
	claim, ok := b.AsClaim()
	if !ok {
		t.Fatal("blob is not a claim")
	}
	return claim
}

func TestMultiClaimAttributeClaims(t *testing.T) {
	pn1, pn2 := blob.MustParse(multiTestPN1), blob.MustParse(multiTestPN2)
	tests := []struct {
		name, fields string
		want         []AttributeClaim
	}{
		{
			name: "ordered operations and delete forms",
			fields: `,"permaNode":"` + multiTestPN1 + `","claims":[
				{"claimType":"set-attribute","attribute":"tag","value":"first"},
				{"claimType":"add-attribute","attribute":"tag","value":"second"},
				{"claimType":"del-attribute","attribute":"tag","value":"first"},
				{"claimType":"del-attribute","attribute":"tag"},
				{"claimType":"del-attribute","attribute":"tag","value":""}]`,
			want: []AttributeClaim{
				{SetAttributeClaim, pn1, "tag", "first"},
				{AddAttributeClaim, pn1, "tag", "second"},
				{DelAttributeClaim, pn1, "tag", "first"},
				{DelAttributeClaim, pn1, "tag", ""},
				{DelAttributeClaim, pn1, "tag", ""},
			},
		},
		{
			name: "default absent empty and explicit override",
			fields: `,"permaNode":"` + multiTestPN1 + `","claims":[
				{"claimType":"set-attribute","attribute":"title","value":"default"},
				{"claimType":"set-attribute","permaNode":"","attribute":"title","value":"empty"},
				{"claimType":"set-attribute","permaNode":"` + multiTestPN2 + `","attribute":"title","value":"override"},
				{"claimType":"set-attribute","permaNode":"` + multiTestPN1 + `","attribute":"title","value":"explicit"}]`,
			want: []AttributeClaim{
				{SetAttributeClaim, pn1, "title", "default"},
				{SetAttributeClaim, pn1, "title", "empty"},
				{SetAttributeClaim, pn2, "title", "override"},
				{SetAttributeClaim, pn1, "title", "explicit"},
			},
		},
		{
			name: "multiple explicit targets without default",
			fields: `,"claims":[
				{"claimType":"add-attribute","permaNode":"` + multiTestPN2 + `","attribute":"tag","value":"two"},
				{"claimType":"add-attribute","permaNode":"` + multiTestPN1 + `","attribute":"tag","value":"one"}]`,
			want: []AttributeClaim{
				{AddAttributeClaim, pn2, "tag", "two"},
				{AddAttributeClaim, pn1, "tag", "one"},
			},
		},
		{
			name: "explicit targets with empty enclosing target",
			fields: `,"permaNode":"","claims":[
				{"claimType":"set-attribute","permaNode":"` + multiTestPN1 + `","attribute":"title","value":"one"},
				{"claimType":"add-attribute","permaNode":"` + multiTestPN2 + `","attribute":"tag","value":"two"}]`,
			want: []AttributeClaim{
				{SetAttributeClaim, pn1, "title", "one"},
				{AddAttributeClaim, pn2, "tag", "two"},
			},
		},
		{
			name: "empty strings allowed as values",
			fields: `,"permaNode":"` + multiTestPN1 + `","claims":[
				{"claimType":"set-attribute","attribute":"title","value":""},
				{"claimType":"add-attribute","attribute":"tag","value":""}]`,
			want: []AttributeClaim{
				{SetAttributeClaim, pn1, "title", ""},
				{AddAttributeClaim, pn1, "tag", ""},
			},
		},
		{
			name: "opaque attribute values and unknown fields",
			fields: `,"permaNode":"` + multiTestPN1 + `","claims":[
				{"claimType":"set-attribute","attribute":"camliContent","value":"not-a-blob-ref","extension":{"x":null}},
				{"claimType":"set-attribute","attribute":"camliMember","value":"` + multiTestPN2 + `"},
				{"claimType":"set-attribute","attribute":" ","value":"nonempty attribute"}]`,
			want: []AttributeClaim{
				{SetAttributeClaim, pn1, "camliContent", "not-a-blob-ref"},
				{SetAttributeClaim, pn1, "camliMember", multiTestPN2},
				{SetAttributeClaim, pn1, " ", "nonempty attribute"},
			},
		},
		{name: "empty no-op", fields: `,"claims":[]`, want: []AttributeClaim{}},
		{name: "empty no-op with empty enclosing target", fields: `,"permaNode":"","claims":[]`, want: []AttributeClaim{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := readAttributeTestClaim(t, string(MultiClaim), tt.fields)
			before := claim.Blob().JSON()
			got, err := claim.AttributeClaims()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AttributeClaims() = %#v; want %#v", got, tt.want)
			}
			if claim.Blob().JSON() != before {
				t.Error("AttributeClaims changed original JSON")
			}
			if len(got) > 0 {
				got[0].Attribute = "changed"
				again, err := claim.AttributeClaims()
				if err != nil || !reflect.DeepEqual(again, tt.want) {
					t.Errorf("caller mutation affected claim: %#v, %v", again, err)
				}
			}
		})
	}
}

func TestMultiClaimRejectsMalformedArrays(t *testing.T) {
	for _, field := range []string{
		"", `,"claims":null`, `,"claims":{}`, `,"claims":"[]"`,
		`,"claims":true`, `,"claims":false`, `,"claims":0`,
	} {
		t.Run(field, func(t *testing.T) {
			claim := readAttributeTestClaim(t, string(MultiClaim), field)
			before := claim.Blob().JSON()
			got, err := claim.AttributeClaims()
			if err == nil || got != nil {
				t.Errorf("AttributeClaims() = %#v, %v; want nil, error", got, err)
			}
			if claim.Blob().JSON() != before {
				t.Error("validation changed original JSON")
			}
		})
	}
}

func TestMultiClaimRejectsMalformedChildrenAtomically(t *testing.T) {
	validChild := func() map[string]any {
		return map[string]any{
			"claimType": string(SetAttributeClaim),
			"permaNode": multiTestPN1,
			"attribute": "tag",
			"value":     "good",
		}
	}
	withField := func(field string, value any) map[string]any {
		m := validChild()
		m[field] = value
		return m
	}
	withoutField := func(field string) map[string]any {
		m := validChild()
		delete(m, field)
		return m
	}
	type invalidChild struct {
		name  string
		child any
	}
	tests := []invalidChild{
		{"null", nil},
		{"array", []any{}},
		{"string", "set-attribute"},
		{"true", true},
		{"false", false},
		{"number", 0},
		{"missing claimType", withoutField("claimType")},
		{"missing attribute", withoutField("attribute")},
		{"empty attribute", withField("attribute", "")},
		{"malformed permanode", withField("permaNode", "not-a-ref")},
		{"malformed known digest", withField("permaNode", "sha1-1234")},
	}
	for _, claimType := range []string{"", "share", "delete", "multi", "unknown"} {
		tests = append(tests, invalidChild{"unsupported " + claimType, withField("claimType", claimType)})
	}
	// Null is not an omitted string, and malformed fields must not fall back to
	// the parent or become empty strings.
	for _, field := range []string{"claimType", "permaNode", "attribute", "value"} {
		for _, value := range []any{nil, true, false, 42, []any{}, map[string]any{}} {
			tests = append(tests, invalidChild{fmt.Sprintf("%s %T %v", field, value, value), withField(field, value)})
		}
	}
	for _, claimType := range []ClaimType{AddAttributeClaim, SetAttributeClaim, DelAttributeClaim} {
		for _, value := range []any{nil, true, 0, []any{}, map[string]any{}} {
			child := withField("claimType", string(claimType))
			child["value"] = value
			tests = append(tests, invalidChild{fmt.Sprintf("%s value %T", claimType, value), child})
		}
		if claimType != DelAttributeClaim {
			child := withField("claimType", string(claimType))
			delete(child, "value")
			tests = append(tests, invalidChild{string(claimType) + " missing value", child})
		}
	}
	for _, field := range []string{"claimDate", "camliSigner", "camliSig"} {
		for _, value := range []any{nil, "", "metadata", 0, map[string]any{}} {
			tests = append(tests, invalidChild{fmt.Sprintf("prohibited %s %T", field, value), withField(field, value)})
		}
	}
	for _, tt := range tests {
		for badIndex := 0; badIndex < 3; badIndex++ {
			t.Run(fmt.Sprintf("%s/index=%d", tt.name, badIndex), func(t *testing.T) {
				children := []any{validChild(), validChild(), validChild()}
				children[badIndex] = tt.child
				raw, err := json.Marshal(children)
				if err != nil {
					t.Fatal(err)
				}
				// A valid default must not rescue a malformed child target.
				claim := readAttributeTestClaim(t, string(MultiClaim),
					`,"permaNode":"`+multiTestPN2+`","claims":`+string(raw))
				before := claim.Blob().JSON()
				got, err := claim.AttributeClaims()
				if err == nil || got != nil {
					t.Fatalf("AttributeClaims() = %#v, %v; want nil, error", got, err)
				}
				if !strings.Contains(err.Error(), fmt.Sprintf("claims[%d]", badIndex)) {
					t.Errorf("error %q does not identify invalid child %d", err, badIndex)
				}
				if claim.Blob().JSON() != before {
					t.Error("validation changed original JSON")
				}
			})
		}
	}
}

func TestMultiClaimRequiresResolvedPermanodes(t *testing.T) {
	for _, parent := range []string{"", `,"permaNode":""`} {
		for _, target := range []string{"", `,"permaNode":""`} {
			for badIndex := 0; badIndex < 2; badIndex++ {
				t.Run(fmt.Sprintf("parent=%s/child=%s/index=%d", parent, target, badIndex), func(t *testing.T) {
					children := []string{
						`{"claimType":"set-attribute","permaNode":"` + multiTestPN1 + `","attribute":"title","value":"valid"}`,
						`{"claimType":"set-attribute","permaNode":"` + multiTestPN2 + `","attribute":"title","value":"valid"}`,
					}
					children[badIndex] = `{"claimType":"set-attribute","attribute":"title","value":"x"` + target + `}`
					claim := readAttributeTestClaim(t, string(MultiClaim),
						parent+`,"claims":[`+strings.Join(children, ",")+`]`)
					got, err := claim.AttributeClaims()
					if err == nil || got != nil {
						t.Errorf("unresolved target: got %#v, %v; want nil, error", got, err)
					}
				})
			}
		}
	}
	claim := readAttributeTestClaim(t, string(MultiClaim), `,"permaNode":null,"claims":[]`)
	if got, err := claim.AttributeClaims(); err == nil || got != nil {
		t.Errorf("null default permanode: got %#v, %v; want nil, error", got, err)
	}
}

func TestSupersetPermanodeDecoding(t *testing.T) {
	for _, camliType := range []CamliType{TypeClaim, TypePermanode} {
		for _, claimType := range []ClaimType{MultiClaim, SetAttributeClaim, AddAttributeClaim, DelAttributeClaim} {
			for _, target := range []string{`""`, `"invalid"`, `"sha1-1234"`, `0`, `true`, `{}`, `[]`, `"` + multiTestPN1 + `"`} {
				t.Run(fmt.Sprintf("%s/%s/%s", camliType, claimType, target), func(t *testing.T) {
					// Put permaNode before the claim type to exercise delayed
					// decoding regardless of JSON field order and whitespace.
					raw := fmt.Sprintf(`{"camliVersion":1,"permaNode": %s, "camliType":%q,"claimType":%q}`,
						target, camliType, claimType)
					allowEmpty := camliType == TypeClaim && claimType == MultiClaim && target == `""`
					validRef := target == `"`+multiTestPN1+`"`
					b, err := BlobFromReader(blob.RefFromString(raw), strings.NewReader(raw))
					if !allowEmpty && !validRef {
						if err == nil {
							t.Error("malformed non-empty or non-multi target was accepted")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if b.JSON() != raw {
						t.Error("typed decoding changed original JSON")
					}
					if b.ss.Permanode.Valid() != validRef {
						t.Errorf("decoded permanode = %v; valid = %v, want %v", b.ss.Permanode, b.ss.Permanode.Valid(), validRef)
					}
				})
			}
		}
	}
}

func TestNewMultiClaim(t *testing.T) {
	pn1, pn2 := blob.MustParse(multiTestPN1), blob.MustParse(multiTestPN2)
	date := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	set := NewSetAttributeClaim(blob.Ref{}, "title", "hello").
		SetSigner(pn1).SetRawStringField("camliSig", "old signature")
	add := NewAddAttributeClaim(pn2, "tag", "first")
	del := NewDelAttributeClaim(blob.Ref{}, "tag", "")
	children := []*Builder{set, add, del}
	before := make([]string, len(children))
	for i, child := range children {
		var err error
		before[i], err = child.JSON()
		if err != nil {
			t.Fatal(err)
		}
	}
	bb, err := NewMultiClaim(children...)
	if err != nil {
		t.Fatal(err)
	}
	if bb.Type() != TypeClaim || bb.ClaimType() != MultiClaim {
		t.Fatalf("unexpected type %q, claimType %q", bb.Type(), bb.ClaimType())
	}
	// The constructor and setting a default must not mutate the inputs.
	if bb.SetMultiClaimPermanode(pn1) != bb {
		t.Error("setter did not return the builder")
	}
	for i, child := range children {
		got, err := child.JSON()
		if err != nil || got != before[i] {
			t.Errorf("input child %d was changed: %s, %v", i, got, err)
		}
	}
	bb.SetClaimDate(date).SetSigner(pn2).SetRawStringField("camliSig", "envelope signature")
	got, err := bb.JSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"camliVersion": 1,
  "camliSig": "envelope signature",
  "camliSigner": "` + multiTestPN2 + `",
  "camliType": "claim",
  "claimDate": "2026-10-14T12:00:00Z",
  "claimType": "multi",
  "claims": [
    {
      "attribute": "title",
      "claimType": "set-attribute",
      "value": "hello"
    },
    {
      "attribute": "tag",
      "claimType": "add-attribute",
      "permaNode": "` + multiTestPN2 + `",
      "value": "first"
    },
    {
      "attribute": "tag",
      "claimType": "del-attribute"
    }
  ],
  "permaNode": "` + multiTestPN1 + `"
}`
	if got != want {
		t.Errorf("JSON =\n%s\nwant:\n%s", got, want)
	}
	// Copies must be independent of subsequent source mutations.
	set.SetRawStringField("value", "changed")
	clone := bb.Builder()
	clone.SetMultiClaimPermanode(pn2)
	c, ok := bb.Blob().AsClaim()
	if !ok {
		t.Fatal("built blob is not a claim")
	}
	operations, err := c.AttributeClaims()
	expected := []AttributeClaim{
		{SetAttributeClaim, pn1, "title", "hello"},
		{AddAttributeClaim, pn2, "tag", "first"},
		{DelAttributeClaim, pn1, "tag", ""},
	}
	if err != nil || !reflect.DeepEqual(operations, expected) {
		t.Errorf("AttributeClaims() = %#v, %v; want %#v", operations, err, expected)
	}
	rebuilt := c.Blob().Builder().Blob()
	if rebuilt.JSON() != got || rebuilt.BlobRef() != c.Blob().BlobRef() {
		t.Error("blob builder round trip changed JSON or blobref")
	}
	bb.SetMultiClaimPermanode(blob.Ref{})
	if _, present := bb.m["permaNode"]; present {
		t.Error("zero default was not removed")
	}
	c, ok = bb.Blob().AsClaim()
	if !ok {
		t.Fatal("blob without default is not a claim")
	}
	if operations, err := c.AttributeClaims(); err == nil || operations != nil {
		t.Errorf("missing default: got %#v, %v; want nil, error", operations, err)
	}
}

func TestNewMultiClaimEmptyAndSingle(t *testing.T) {
	for _, count := range []int{0, 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var children []*Builder
			if count == 1 {
				children = append(children, NewSetAttributeClaim(blob.MustParse(multiTestPN1), "title", "one"))
			}
			bb, err := NewMultiClaim(children...)
			if err != nil {
				t.Fatal(err)
			}
			bb.SetSigner(blob.MustParse(multiTestPN2)).SetRawStringField("camliSig", "test")
			c, ok := bb.Blob().AsClaim()
			if !ok || c.ClaimType() != MultiClaim {
				t.Fatal("constructor did not produce a multi claim")
			}
			got, err := c.AttributeClaims()
			if err != nil || len(got) != count {
				t.Errorf("AttributeClaims() = %#v, %v; want %d operations", got, err, count)
			}
			if count == 0 && !strings.Contains(bb.Blob().JSON(), `"claims": []`) {
				t.Error("empty batch was not encoded as an empty array")
			}
		})
	}
}

func TestNewMultiClaimOmitsExplicitEmptyChildTarget(t *testing.T) {
	child := NewSetAttributeClaim(blob.Ref{}, "title", "inherited").SetRawStringField("permaNode", "")
	bb, err := NewMultiClaim(child)
	if err != nil {
		t.Fatal(err)
	}
	operation := bb.m["claims"].([]any)[0].(map[string]any)
	if _, present := operation["permaNode"]; present {
		t.Error("constructor did not omit explicitly empty child target")
	}
	if value, present := child.m["permaNode"]; !present || value != "" {
		t.Error("constructor modified source builder")
	}
}

func TestNewMultiClaimRejectsInvalidBuilders(t *testing.T) {
	valid := func() *Builder {
		return NewSetAttributeClaim(blob.MustParse(multiTestPN1), "title", "value")
	}
	empty, err := NewMultiClaim()
	if err != nil {
		t.Fatal(err)
	}
	malformed := valid()
	malformed.m["permaNode"] = nil
	noValue := valid()
	delete(noValue.m, "value")
	noTarget := valid()
	delete(noTarget.m, "permaNode")
	if _, err := NewMultiClaim(noTarget); err != nil {
		t.Errorf("absent child target should allow a later default: %v", err)
	}
	for name, child := range map[string]*Builder{
		"nil":               nil,
		"zero":              {},
		"permanode":         NewUnsignedPermanode(),
		"share":             NewShareRef(ShareHaveRef, true),
		"delete":            NewDeleteClaim(blob.MustParse(multiTestPN1)),
		"nested multi":      empty,
		"unknown operation": valid().SetRawStringField("claimType", "unknown"),
		"empty attribute":   valid().SetRawStringField("attribute", ""),
		"invalid ref":       valid().SetRawStringField("permaNode", "invalid"),
		"null ref":          malformed,
		"missing value":     noValue,
	} {
		t.Run(name, func(t *testing.T) {
			bb, err := NewMultiClaim(valid(), child)
			if err == nil || bb != nil {
				t.Errorf("NewMultiClaim() = %#v, %v; want nil, error", bb, err)
			}
		})
	}
}

func TestMultiClaimSignAt(t *testing.T) {
	ent, err := jsonsign.NewEntity()
	if err != nil {
		t.Fatal(err)
	}
	armorPub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(blob.RefFromString(armorPub), strings.NewReader(armorPub), ent)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := NewMultiClaim(
		NewSetAttributeClaim(blob.Ref{}, "title", "signed value"),
		NewAddAttributeClaim(blob.MustParse(multiTestPN2), "tag", "other target"),
	)
	if err != nil {
		t.Fatal(err)
	}
	bb.SetMultiClaimPermanode(blob.MustParse(multiTestPN1))
	date := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	signed, err := bb.SignAt(ctxbg, signer, date)
	if err != nil {
		t.Fatal(err)
	}
	verification := jsonsign.NewVerificationRequest(signed, signer.baseSigReq.Fetcher)
	if _, err := verification.Verify(ctxbg); err != nil {
		t.Fatalf("multi claim signature verification: %v", err)
	}
	b, err := BlobFromReader(blob.RefFromString(signed), strings.NewReader(signed))
	if err != nil {
		t.Fatal(err)
	}
	claim, ok := b.AsClaim()
	if !ok {
		t.Fatal("signed blob is not a claim")
	}
	got, err := claim.AttributeClaims()
	want := []AttributeClaim{
		{SetAttributeClaim, blob.MustParse(multiTestPN1), "title", "signed value"},
		{AddAttributeClaim, blob.MustParse(multiTestPN2), "tag", "other target"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("signed AttributeClaims() = %#v, %v; want %#v", got, err, want)
	}
	claimDate, err := b.ClaimDate()
	if err != nil || !claimDate.Equal(date) || claim.Signer() != signer.pubref || claim.Signature() == "" {
		t.Errorf("invalid envelope metadata: date %v, signer %v, error %v", claimDate, claim.Signer(), err)
	}
	// Both the child array and the default target belong to the signature.
	for _, tampered := range []string{
		strings.Replace(signed, "signed value", "changed value", 1),
		strings.Replace(signed, multiTestPN1, multiTestPN2, 1),
	} {
		if tampered == signed {
			t.Fatal("tampering did not change signed JSON")
		}
		verification := jsonsign.NewVerificationRequest(tampered, signer.baseSigReq.Fetcher)
		if _, err := verification.Verify(ctxbg); err == nil {
			t.Error("signature verified after tampering with multi claim")
		}
	}
	t.Run("empty default with explicit target", func(t *testing.T) {
		bb, err := NewMultiClaim(NewSetAttributeClaim(blob.MustParse(multiTestPN1), "title", "signed value"))
		if err != nil {
			t.Fatal(err)
		}
		bb.SetRawStringField("permaNode", "")
		signed, err := bb.SignAt(ctxbg, signer, date)
		if err != nil {
			t.Fatal(err)
		}
		b, err := BlobFromReader(blob.RefFromString(signed), strings.NewReader(signed))
		if err != nil {
			t.Fatal(err)
		}
		claim, ok := b.AsClaim()
		if !ok {
			t.Fatal("signed blob with empty default is not a claim")
		}
		got, err := claim.AttributeClaims()
		if err != nil || !reflect.DeepEqual(got, want[:1]) {
			t.Errorf("empty-default AttributeClaims() = %#v, %v; want %#v", got, err, want[:1])
		}
		if b.JSON() != signed {
			t.Error("empty default decoding changed signed JSON")
		}
		verification := jsonsign.NewVerificationRequest(b.JSON(), signer.baseSigReq.Fetcher)
		if _, err := verification.Verify(ctxbg); err != nil {
			t.Errorf("empty-default signature verification: %v", err)
		}
	})
}

func TestSetMultiClaimPermanodePanicsOnOtherBuilders(t *testing.T) {
	for _, bb := range []*Builder{NewBuilder(), NewUnsignedPermanode(), NewSetAttributeClaim(blob.MustParse(multiTestPN1), "title", "x")} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("SetMultiClaimPermanode did not panic on non-multi builder")
				}
			}()
			bb.SetMultiClaimPermanode(blob.MustParse(multiTestPN1))
		}()
	}
}

func TestAttributeClaimsSingle(t *testing.T) {
	pn := blob.MustParse(multiTestPN1)
	for _, claimType := range []ClaimType{AddAttributeClaim, SetAttributeClaim, DelAttributeClaim} {
		claim := readAttributeTestClaim(t, string(claimType),
			`,"permaNode":"`+multiTestPN1+`","attribute":"tag","value":"value"`)
		got, err := claim.AttributeClaims()
		want := []AttributeClaim{{claimType, pn, "tag", "value"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, %v; want %#v", claimType, got, err, want)
		}
		if claim.ModifiedPermanode() != pn || claim.Attribute() != "tag" || claim.Value() != "value" {
			t.Error("single-claim accessors changed")
		}
	}
	claim := readAttributeTestClaim(t, string(DelAttributeClaim),
		`,"permaNode":"`+multiTestPN1+`","attribute":"tag"`)
	if got, err := claim.AttributeClaims(); err != nil || len(got) != 1 || got[0].Value != "" {
		t.Errorf("delete all: got %#v, %v", got, err)
	}
	for _, claimType := range []ClaimType{ShareClaim, DeleteClaim, "unknown"} {
		claim := readAttributeTestClaim(t, string(claimType), "")
		if got, err := claim.AttributeClaims(); err == nil || got != nil {
			t.Errorf("%s: got %#v, %v; want nil, error", claimType, got, err)
		}
	}
	if got, err := (Claim{}).AttributeClaims(); err == nil || got != nil {
		t.Errorf("zero claim: got %#v, %v; want nil, error", got, err)
	}
}
