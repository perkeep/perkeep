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

package server

import (
	"io"
	"net/http"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/schema"
)

// uploadHelperResponse is the response from serveUploadHelper.
type uploadHelperResponse struct {
	Got []*uploadHelperGotItem `json:"got"`
}

type uploadHelperGotItem struct {
	FileName string   `json:"filename"`
	FormName string   `json:"formname"`
	FileRef  blob.Ref `json:"fileref"`
}

func (ui *UIHandler) serveUploadHelper(rw http.ResponseWriter, req *http.Request) {
	if ui.root.Storage == nil {
		httputil.ServeJSONError(rw, httputil.ServerError("No BlobRoot configured"))
		return
	}

	mr, err := req.MultipartReader()
	if err != nil {
		httputil.ServeJSONError(rw, httputil.ServerError("reading body: "+err.Error()))
		return
	}

	var got []*uploadHelperGotItem
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			httputil.ServeJSONError(rw, httputil.ServerError("reading body: "+err.Error()))
			break
		}
		fileName := part.FileName()
		if fileName == "" {
			continue
		}
		br, err := schema.WriteFileFromReader(ui.root.Storage, fileName, part)
		if err != nil {
			httputil.ServeJSONError(rw, httputil.ServerError("writing to blobserver: "+err.Error()))
			return
		}
		got = append(got, &uploadHelperGotItem{
			FileName: part.FileName(),
			FormName: part.FormName(),
			FileRef:  br,
		})
	}

	httputil.ReturnJSON(rw, &uploadHelperResponse{Got: got})
}
