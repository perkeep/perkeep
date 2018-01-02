/*
Copyright 2014 The Perkeep Authors

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

package storage

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// See http://msdn.microsoft.com/en-US/library/azure/dd179428

// Auth contains the credentials needed to connect to Azure storage
type Auth struct {
	Account   string
	AccessKey []byte
}

// SignRequest takes an existing *http.Request and signs it
// with the credentials in Auth. If no date header is set,
// SignRequest will set the date header to the current UTC time.
func (a *Auth) SignRequest(req *http.Request) {
	if date := req.Header.Get("Date"); date == "" {
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}

	hm := hmac.New(sha256.New, a.AccessKey)
	ss := a.stringToSign(req)

	io.WriteString(hm, ss)

	authHeader := new(bytes.Buffer)
	fmt.Fprintf(authHeader, "SharedKey %s:", a.Account)
	encoder := base64.NewEncoder(base64.StdEncoding, authHeader)
	encoder.Write(hm.Sum(nil))
	encoder.Close()
	req.Header.Set("Authorization", authHeader.String())
}

func firstNonEmptyString(strs ...string) string {
	for _, s := range strs {
		if s != "" {
			return s
		}
	}
	return ""
}

// From the Mirosoft docs:
// StringToSign = VERB + "\n" +
//                Content-Encoding + "\n"
//                Content-Language + "\n"
//                Content-Length + "\n"
//                Content-MD5 + "\n" +
//                Content-Type + "\n" +
//                Date + "\n" +
//                If-Modified-Since + "\n"
//                If-Match + "\n"
//                If-None-Match + "\n"
//                If-Unmodified-Since + "\n"
//                Range + "\n"
//                CanonicalizedHeaders +
//                CanonicalizedResource;
func (a *Auth) stringToSign(req *http.Request) string {
	buf := new(bytes.Buffer)
	buf.WriteString(req.Method)
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Content-Encoding"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Content-Language"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Content-Length"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Content-MD5"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Content-Type"))
	buf.WriteByte('\n')
	if req.Header.Get("x-ms-date") == "" {
		buf.WriteString(req.Header.Get("Date"))
	}
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("If-Modified-Since"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("If-Match"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("If-None-Match"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("If-Unmodified-Since"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Range"))
	buf.WriteByte('\n')
	a.writeCanonicalizedMSHeaders(buf, req)
	a.writeCanonicalizedResource(buf, req)
	return buf.String()
}

func hasPrefixCaseInsensitive(s, pfx string) bool {
	if len(pfx) > len(s) {
		return false
	}
	shead := s[:len(pfx)]
	if shead == pfx {
		return true
	}
	shead = strings.ToLower(shead)
	return shead == pfx || shead == strings.ToLower(pfx)
}

func (a *Auth) writeCanonicalizedMSHeaders(buf *bytes.Buffer, req *http.Request) {
	var msHeaders []string
	vals := make(map[string][]string)
	for k, vv := range req.Header {
		if hasPrefixCaseInsensitive(k, "x-ms-") {
			lk := strings.ToLower(k)
			msHeaders = append(msHeaders, lk)
			vals[lk] = vv
		}
	}
	sort.Strings(msHeaders)
	for _, k := range msHeaders {
		buf.WriteString(k)
		buf.WriteByte(':')
		for idx, v := range vals[k] {
			if idx > 0 {
				buf.WriteByte(',')
			}
			if strings.Contains(v, "\n") {
				// TODO: "Unfold" long headers that
				// span multiple lines (as allowed by
				// RFC 2616, section 4.2) by replacing
				// the folding white-space (including
				// new-line) by a single space.
				buf.WriteString(v)
			} else {
				buf.WriteString(v)
			}
		}
		buf.WriteByte('\n')
	}
}

// From the Azure docs at: http://msdn.microsoft.com/en-US/library/azure/dd179428
func (a *Auth) writeCanonicalizedResource(buf *bytes.Buffer, req *http.Request) {
	buf.WriteByte('/')
	buf.WriteString(a.Account)
	buf.WriteString(req.URL.Path)
	if req.URL.RawQuery != "" {
		vals, err := url.ParseQuery(req.URL.RawQuery)
		if err != nil {
			// This should never happen since we construct the URL ourselves in Go.
			panic(err)
		}

		keys := make([]string, len(vals))
		i := 0
		for key := range vals {
			keys[i] = key
			i++
		}
		sort.Strings(keys)

		for _, subres := range keys {
			vv := vals[subres]
			if len(vv) > 0 {
				buf.WriteByte('\n')
				buf.WriteString(strings.ToLower(subres))
				buf.WriteByte(':')
				for i, v := range vv {
					if len(v) > 0 {
						if i != 0 {
							buf.WriteByte(',')
						}
						buf.WriteString(url.QueryEscape(v))
					}
				}
			}
		}
	}
}
