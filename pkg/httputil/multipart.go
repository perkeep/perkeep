/*
Copyright 2015 The Camlistore Authors.

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

// This file provides an alternative function to the stdlib's (r
// *http.Request).MultiMultipartReader(), because we want to obtain a multipart
// Reader from the vendored "future/mime/multipart" instead of from the stdlib's
// "mime/multipart".

package httputil

import (
	"future/mime/multipart" // vendored copy of Go tip "mime/multipart"
	"mime"
	"net/http"
)

// TODO(mpl, bradfitz): remove that whole file once we depend on Go 1.6
// See https://camlistore.org/issue/644

// MultipartReader returns a MIME multipart reader if this is a
// multipart/form-data POST request, else returns nil and an error.
// Use this function instead of ParseMultipartForm to
// process the request body as a stream.
func MultipartReader(r *http.Request) (*multipart.Reader, error) {
	_, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	return multipartReader(r)
}

func multipartReader(r *http.Request) (*multipart.Reader, error) {
	v := r.Header.Get("Content-Type")
	if v == "" {
		return nil, http.ErrNotMultipart
	}
	d, params, err := mime.ParseMediaType(v)
	if err != nil || d != "multipart/form-data" {
		return nil, http.ErrNotMultipart
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, http.ErrMissingBoundary
	}
	return multipart.NewReader(r.Body, boundary), nil
}
