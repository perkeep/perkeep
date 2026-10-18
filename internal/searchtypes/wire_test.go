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

package searchtypes

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go4.org/types"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

func TestWireJSON(t *testing.T) {
	const refText = "sha1-0123456789012345678901234567890123456789"
	br := blob.MustParse(refText)
	at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	sc := &StringConstraint{Regexp: "a+"}
	if _, err := StringRegexp(sc); err != nil {
		t.Fatal(err)
	}
	query := &SearchQuery{
		Constraint: &Constraint{Logical: &LogicalConstraint{
			Op: "and",
			A:  &Constraint{CamliType: schema.TypePermanode},
			B: &Constraint{Permanode: &PermanodeConstraint{
				At: at, Attr: "camliMember", ValueMatches: sc,
				ValueInSet: &Constraint{BlobRefPrefix: "sha1-"},
			}},
		}},
		Limit: 7, Sort: LastModifiedDesc, Around: br,
		Describe: &DescribeRequest{
			BlobRef: br, At: types.Time3339(at),
			Rules: []*DescribeRule{{Attrs: []string{"camliContent"}, Rules: []*DescribeRule{{Attrs: []string{"camliMember"}}}}},
		},
	}
	// An opaque cache payload, even one JSON cannot marshal, must stay off the wire.
	CachedMatcher(query.Constraint, func() any { return func() {} })
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "recursive query",
			value: query,
			want:  fmt.Sprintf(`{"constraint":{"logical":{"op":"and","a":{"camliType":"permanode"},"b":{"permanode":{"at":"2020-01-02T03:04:05Z","attr":"camliMember","valueMatches":{"regexp":"a+"},"valueInSet":{"blobRefPrefix":"sha1-"}}}}},"limit":7,"sort":"-mod","around":%q,"describe":{"blobref":%q,"at":"2020-01-02T03:04:05Z","rules":[{"attrs":["camliContent"],"rules":[{"attrs":["camliMember"]}]}]}}`, refText, refText),
		},
		{
			name: "description",
			value: &DescribeResponse{Meta: MetaMap{refText: &DescribedBlob{
				Request: query.Describe, BlobRef: br, CamliType: schema.TypePermanode, Size: 12, Stub: true,
				Permanode: &DescribedPermanode{Attr: url.Values{"title": []string{"example"}}, ModTime: at},
			}}},
			want: fmt.Sprintf(`{"meta":{%q:{"blobRef":%q,"camliType":"permanode","size":12,"permanode":{"attr":{"title":["example"]},"modtime":"2020-01-02T03:04:05Z"}}}}`, refText, refText),
		},
		{
			name: "claims",
			value: &ClaimsResponse{Claims: []*ClaimsItem{{
				BlobRef: br, Signer: br, Permanode: br, Date: types.Time3339(at),
				Type: "set-attribute", Attr: "title", Value: "example",
			}}},
			want: fmt.Sprintf(`{"claims":[{"blobref":%q,"signer":%q,"permanode":%q,"date":"2020-01-02T03:04:05Z","type":"set-attribute","attr":"title","value":"example"}]}`, refText, refText, refText),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("wire JSON = %s; want %s", got, tt.want)
			}
		})
	}
	encoded, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SearchQuery
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	roundtrip, err := json.Marshal(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(roundtrip) != string(encoded) {
		t.Errorf("roundtrip = %s; want %s", roundtrip, encoded)
	}
}

func TestSortTypeJSON(t *testing.T) {
	tests := []struct {
		sort SortType
		wire string
	}{
		{Unsorted, `"unsorted"`},
		{LastModifiedDesc, `"-mod"`},
		{LastModifiedAsc, `"mod"`},
		{CreatedDesc, `"-created"`},
		{CreatedAsc, `"created"`},
		{BlobRefAsc, `"blobref"`},
		{MapSort, `"map"`},
	}
	for _, tt := range tests {
		t.Run(tt.wire, func(t *testing.T) {
			got, err := json.Marshal(tt.sort)
			if err != nil || string(got) != tt.wire {
				t.Fatalf("Marshal = %s, %v; want %s, nil", got, err, tt.wire)
			}
			var decoded SortType
			if err := json.Unmarshal(got, &decoded); err != nil || decoded != tt.sort {
				t.Errorf("Unmarshal = %v, %v; want %v, nil", decoded, err, tt.sort)
			}
		})
	}
	for _, invalid := range []string{`"unknown"`, `null`, `2`, `{}`, `""`} {
		sort := Unsorted
		if err := json.Unmarshal([]byte(invalid), &sort); err == nil {
			t.Errorf("invalid sort %s accepted", invalid)
		}
		if sort != Unsorted {
			t.Errorf("invalid sort %s changed destination to %v", invalid, sort)
		}
	}
	for _, invalid := range []SortType{UnspecifiedSort, -1, MaxSortType} {
		t.Run(fmt.Sprintf("marshal-invalid-%d", invalid), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("invalid sort did not panic")
				}
			}()
			_, _ = json.Marshal(invalid)
		})
	}
}

func TestSearchQueryFromHTTP(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		wantErr    bool
	}{
		{"valid", `{"constraint":{"anything":true},"limit":3,"sort":"map"}`, false},
		{"unknown root field", `{"expression":"tag:foo","unknown":true}`, true},
		{"unknown constraint field", `{"constraint":{"anything":true,"unknown":true}}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/camli/search/query", strings.NewReader(tt.body))
			var query SearchQuery
			err := query.FromHTTP(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FromHTTP error = %v; wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (query.Constraint == nil || !query.Constraint.Anything || query.Limit != 3 || query.Sort != MapSort) {
				t.Errorf("decoded query = %+v", query)
			}
		})
	}
}
