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

package s3

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

var _ = log.Printf

// See http://docs.amazonwebservices.com/AmazonS3/latest/dev/index.html?RESTAuthentication.html

type Auth struct {
	AccessKey       string
	SecretAccessKey string
}

func (a *Auth) SignRequest(req *http.Request) {
	if date := req.Header.Get("Date"); date == "" {
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}
	hm := hmac.New(sha1.New, []byte(a.SecretAccessKey))
	ss := stringToSign(req)
	io.WriteString(hm, ss)

	authHeader := new(bytes.Buffer)
	fmt.Fprintf(authHeader, "AWS %s:", a.AccessKey)
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

// From the Amazon docs:
//
// StringToSign = HTTP-Verb + "\n" +
// 	 Content-MD5 + "\n" +
//	 Content-Type + "\n" +
//	 Date + "\n" +
//	 CanonicalizedAmzHeaders +
//	 CanonicalizedResource;
func stringToSign(req *http.Request) string {
	buf := new(bytes.Buffer)
	buf.WriteString(req.Method)
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Content-MD5"))
	buf.WriteByte('\n')
	buf.WriteString(req.Header.Get("Content-Type"))
	buf.WriteByte('\n')
	if req.Header.Get("x-amz-date") == "" {
		buf.WriteString(req.Header.Get("Date"))
	}
	buf.WriteByte('\n')
	writeCanonicalizedAmzHeaders(buf, req)
	writeCanonicalizedResource(buf, req)
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

func writeCanonicalizedAmzHeaders(buf *bytes.Buffer, req *http.Request) {
	amzHeaders := make([]string, 0)
	vals := make(map[string][]string)
	for k, vv := range req.Header {
		if hasPrefixCaseInsensitive(k, "x-amz-") {
			lk := strings.ToLower(k)
			amzHeaders = append(amzHeaders, lk)
			vals[lk] = vv
		}
	}
	sort.Strings(amzHeaders)
	for _, k := range amzHeaders {
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

// From the Amazon docs:
//
// CanonicalizedResource = [ "/" + Bucket ] +
// 	  <HTTP-Request-URI, from the protocol name up to the query string> +
// 	  [ sub-resource, if present. For example "?acl", "?location", "?logging", or "?torrent"];
func writeCanonicalizedResource(buf *bytes.Buffer, req *http.Request) {
	if bucket := bucketFromHostname(req); bucket != "" {
		buf.WriteByte('/')
		buf.WriteString(bucket)
	}
	buf.WriteString(req.URL.Path)
	// TODO: subresource
}

const standardUSRegionAWSSuffix = ".s3.amazonaws.com"
const standardUSRegionAWS = "s3.amazonaws.com"

func bucketFromHostname(req *http.Request) string {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	if host == standardUSRegionAWS {
		return ""
	}
	if strings.HasSuffix(host, standardUSRegionAWSSuffix) {
		return host[:len(host)-len(standardUSRegionAWSSuffix)]
	}
	if lastColon := strings.LastIndex(host, ":"); lastColon != -1 {
		return host[:lastColon]
	}
	return host
}
