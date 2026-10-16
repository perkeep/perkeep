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

package search

import (
	"errors"
	"net/http"
	"os"

	"perkeep.org/internal/httputil"
)

func (h *Handler) serveEXIF(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	fileRef := httputil.MustGetBlobRef(req, "blobref")
	h.index.RLock()
	defer h.index.RUnlock()
	res, err := h.index.GetEXIF(req.Context(), fileRef)
	if errors.Is(err, os.ErrNotExist) {
		httputil.ReturnJSONCode(rw, http.StatusNotFound, map[string]string{
			"error": "File not found", "errorType": http.StatusText(http.StatusNotFound),
		})
		return
	}
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}
