/*
Copyright 2013 The Perkeep Authors

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
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go4.org/types"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
)

// MaxResults and DefaultNumResults are the limits used by legacy search requests.
const MaxResults = 1000
const DefaultNumResults = 50

func SanitizeNumResults(n int) int {
	if n <= 0 || n > MaxResults {
		return DefaultNumResults
	}
	return n
}

type RecentRequest struct {
	N      int       // if zero, default number of results
	Before time.Time // if zero, now
}

type RecentResponse struct {
	Recent []*RecentItem `json:"recent"`
	Meta   MetaMap       `json:"meta"`

	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

type RecentItem struct {
	BlobRef blob.Ref       `json:"blobref"`
	ModTime types.Time3339 `json:"modtime"`
	Owner   blob.Ref       `json:"owner"`
}

type WithAttrRequest struct {
	N      int      // max number of results
	Signer blob.Ref // if nil, will use the server's default owner (if configured)
	// Requested attribute. If blank, all attributes are searched (for Value)
	// as fulltext.
	Attr string
	// Value of the requested attribute. If blank, permanodes which have
	// request.Attr as an attribute are searched.
	Value string
	Fuzzy bool // fulltext search (if supported).
	// At, if non-zero, specifies that the attribute must have been set at
	// the latest at At.
	At time.Time
}

type WithAttrResponse struct {
	WithAttr []*WithAttrItem `json:"withAttr"`
	Meta     MetaMap         `json:"meta"`

	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

type WithAttrItem struct {
	Permanode blob.Ref `json:"permanode"`
}

type ClaimsRequest struct {
	Permanode blob.Ref

	// AttrFilter optionally filters claims about the given attribute.
	// If empty, all claims for the given Permanode are returned.
	AttrFilter string
}

type ClaimsResponse struct {
	Claims []*ClaimsItem `json:"claims"`
}

type ClaimsItem struct {
	BlobRef   blob.Ref       `json:"blobref"`
	Signer    blob.Ref       `json:"signer"`
	Permanode blob.Ref       `json:"permanode"`
	Date      types.Time3339 `json:"date"`
	Type      string         `json:"type"`
	Attr      string         `json:"attr,omitempty"`
	Value     string         `json:"value,omitempty"`
}

func (r *RecentRequest) URLSuffix() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "camli/search/recent?n=%d", r.n())
	if !r.Before.IsZero() {
		fmt.Fprintf(&buf, "&before=%s", types.Time3339(r.Before))
	}
	return buf.String()
}

func RecentRequestFromHTTP(r *RecentRequest, req *http.Request) {
	r.N, _ = strconv.Atoi(req.FormValue("n"))
	if before := req.FormValue("before"); before != "" {
		r.Before = time.Time(types.ParseTime3339OrZero(before))
	}
}

func (r *RecentRequest) n() int {
	return SanitizeNumResults(r.N)
}

func (r *WithAttrRequest) URLSuffix() string {
	s := fmt.Sprintf("camli/search/permanodeattr?signer=%v&value=%v&fuzzy=%v&attr=%v&max=%v",
		r.Signer, url.QueryEscape(r.Value), r.Fuzzy, r.Attr, r.N)
	if !r.At.IsZero() {
		s += fmt.Sprintf("&at=%s", types.Time3339(r.At))
	}
	return s
}

func WithAttrRequestFromHTTP(r *WithAttrRequest, req *http.Request) {
	r.Signer = blob.ParseOrZero(req.FormValue("signer"))
	r.Value = req.FormValue("value")
	fuzzy := req.FormValue("fuzzy") // exact match if empty
	fuzzyMatch := false
	if fuzzy != "" {
		lowered := strings.ToLower(fuzzy)
		if lowered == "true" || lowered == "t" {
			fuzzyMatch = true
		}
	}
	r.Attr = req.FormValue("attr") // all attributes if empty
	if r.Attr == "" {              // and force fuzzy in that case.
		fuzzyMatch = true
	}
	r.Fuzzy = fuzzyMatch
	max := req.FormValue("max")
	if max != "" {
		maxR, err := strconv.Atoi(max)
		if err != nil {
			panic(httputil.InvalidParameterError("max"))
		}
		r.N = maxR
	}
	r.N = r.n()
	if at := req.FormValue("at"); at != "" {
		r.At = time.Time(types.ParseTime3339OrZero(at))
	}
}

func (r *WithAttrRequest) n() int {
	return SanitizeNumResults(r.N)
}

func (r *ClaimsRequest) URLSuffix() string {
	return fmt.Sprintf("camli/search/claims?permanode=%v&attrFilter=%s",
		r.Permanode, url.QueryEscape(r.AttrFilter))
}

func ClaimsRequestFromHTTP(r *ClaimsRequest, req *http.Request) {
	r.Permanode = httputil.MustGetBlobRef(req, "permanode")
	r.AttrFilter = req.FormValue("attrFilter")
}

func (r *RecentResponse) Err() error {
	if r.Error != "" || r.ErrorType != "" {
		if r.ErrorType != "" {
			return fmt.Errorf("%s: %s", r.ErrorType, r.Error)
		}
		return errors.New(r.Error)
	}
	return nil
}

func (r *WithAttrResponse) Err() error {
	if r.Error != "" || r.ErrorType != "" {
		if r.ErrorType != "" {
			return fmt.Errorf("%s: %s", r.ErrorType, r.Error)
		}
		return errors.New(r.Error)
	}
	return nil
}
