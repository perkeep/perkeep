/*
Copyright 2011 The Perkeep Authors

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

package handlers

import (
	"fmt"
	"log"
	"net/http"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

const maxRemovesPerRequest = 1000

func CreateRemoveHandler(storage blobserver.Storage) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		handleRemove(rw, req, storage)
	})
}

// RemoveResponse is the JSON response to a remove request.
type RemoveResponse struct {
	Removed []blob.Ref `json:"removed"` // Refs of the removed blobs.
	Error   string     `json:"error"`
}

func handleRemove(w http.ResponseWriter, r *http.Request, storage blobserver.Storage) {
	ctx := r.Context()
	if r.Method != "POST" {
		log.Fatalf("Invalid method; handlers misconfigured")
	}

	configer, ok := storage.(blobserver.Configer)
	if !ok {
		msg := fmt.Sprintf("remove handler's blobserver.Storage isn't a blobserver.Configer, but a %T; can't remove", storage)
		log.Printf("blobserver/handlers: %v", msg)
		httputil.ReturnJSONCode(w, http.StatusForbidden, RemoveResponse{Error: msg})
		return
	}
	if !configer.Config().Deletable {
		msg := fmt.Sprintf("storage %T does not permit deletes", storage)
		log.Printf("blobserver/handlers: %v", msg)
		httputil.ReturnJSONCode(w, http.StatusForbidden, RemoveResponse{Error: msg})
		return
	}

	n := 0
	toRemove := make([]blob.Ref, 0)
	for {
		n++
		if n > maxRemovesPerRequest {
			httputil.BadRequestError(w,
				fmt.Sprintf("Too many removes in this request; max is %d", maxRemovesPerRequest))
			return
		}
		key := fmt.Sprintf("blob%v", n)
		value := r.FormValue(key)
		if value == "" {
			break
		}
		ref, ok := blob.Parse(value)
		if !ok {
			httputil.BadRequestError(w, "Bogus blobref for key "+key)
			return
		}
		toRemove = append(toRemove, ref)
	}

	err := storage.RemoveBlobs(ctx, toRemove)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("Server error during remove: %v", err)
		fmt.Fprintf(w, "Server error")
		return
	}

	httputil.ReturnJSON(w, &RemoveResponse{Removed: toRemove})
}
