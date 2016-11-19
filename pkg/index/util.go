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

package index

import (
	"net/url"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/types/camtypes"
)

var urle = url.QueryEscape

func urld(s string) string {
	d, _ := url.QueryUnescape(s)
	return d
}

type dupSkipper struct {
	m map[string]bool
}

// not thread safe.
func (s *dupSkipper) Dup(v string) bool {
	if s.m == nil {
		s.m = make(map[string]bool)
	}
	if s.m[v] {
		return true
	}
	s.m[v] = true
	return false
}

// ClaimsAttrValue returns the value of attr from claims,
// or the empty string if not found.
// Claims should be sorted by claim.Date.
func ClaimsAttrValue(claims []camtypes.Claim, attr string, at time.Time, signerFilter blob.Ref) string {
	return claimsIntfAttrValue(claimSlice(claims), attr, at, signerFilter)
}

// claimPtrsAttrValue returns the value of attr from claims,
// or the empty string if not found.
// Claims should be sorted by claim.Date.
func claimPtrsAttrValue(claims []*camtypes.Claim, attr string, at time.Time, signerFilter blob.Ref) string {
	return claimsIntfAttrValue(claimPtrSlice(claims), attr, at, signerFilter)
}

type claimsIntf interface {
	Len() int
	Claim(i int) *camtypes.Claim
}

type claimSlice []camtypes.Claim

func (s claimSlice) Len() int                    { return len(s) }
func (s claimSlice) Claim(i int) *camtypes.Claim { return &s[i] }

type claimPtrSlice []*camtypes.Claim

func (s claimPtrSlice) Len() int                    { return len(s) }
func (s claimPtrSlice) Claim(i int) *camtypes.Claim { return s[i] }

// claimsIntfAttrValue finds the value of an attribute in a list of claims
// or empty string if not found. claims must be non-nil.
func claimsIntfAttrValue(claims claimsIntf, attr string, at time.Time, signerFilter blob.Ref) string {
	if claims == nil {
		panic("nil claims argument in claimsIntfAttrValue")
	}

	if at.IsZero() {
		at = time.Now()
	}

	// use a small static buffer as it speeds up
	// search.BenchmarkQueryPermanodeLocation by 6-7%
	// with go 1.7.1
	var buf [8]string
	v := buf[:][:0]

	for i := 0; i < claims.Len(); i++ {
		cl := claims.Claim(i)
		if cl.Attr != attr || cl.Date.After(at) {
			continue
		}
		if signerFilter.Valid() && signerFilter != cl.Signer {
			continue
		}
		switch cl.Type {
		case string(schema.DelAttributeClaim):
			if cl.Value == "" {
				v = v[:0]
			} else {
				i := 0
				for _, w := range v {
					if w != cl.Value {
						v[i] = w
						i++
					}
				}
				v = v[:i]
			}
		case string(schema.SetAttributeClaim):
			v = append(v[:0], cl.Value)
		case string(schema.AddAttributeClaim):
			v = append(v, cl.Value)
		}
	}
	if len(v) != 0 {
		return v[0]
	}
	return ""
}
