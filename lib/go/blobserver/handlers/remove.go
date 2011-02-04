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

package handlers

import (
	"camli/blobref"
	"camli/blobserver"
	"camli/httputil"
	"fmt"
	"http"
	"log"
)

const maxRemovesPerRequest = 1000

func CreateRemoveHandler(storage blobserver.Storage, partition blobserver.Partition) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleRemove(conn, req, storage, partition)
	}
}

func handleRemove(conn http.ResponseWriter, req *http.Request, storage blobserver.Storage, partition blobserver.Partition) {
	if req.Method != "POST" {
		log.Fatalf("Invalid method; handlers misconfigured")
	}

	if partition == "" {
		conn.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(conn, "Can't remove blobs from the default partition.\n")
		return
	}

	n := 0
	toRemove := make([]*blobref.BlobRef, 0)
	toRemoveStr := make([]string, 0)
	for {
		n++
		if n > maxRemovesPerRequest {
			httputil.BadRequestError(conn,
				fmt.Sprintf("Too many removes in this request; max is %d", maxRemovesPerRequest))
			return
		}
		key := fmt.Sprintf("blob%v", n)
		value := req.FormValue(key)
		if value == "" {
			break
		}
		ref := blobref.Parse(value)
		if ref == nil {
			httputil.BadRequestError(conn, "Bogus blobref for key "+key)
			return
		}
		toRemove = append(toRemove, ref)
		toRemoveStr = append(toRemoveStr, ref.String())
	}

	err := storage.Remove(partition, toRemove)
	if err != nil {
		conn.WriteHeader(http.StatusInternalServerError)
		log.Printf("Server error during remove: %v", err)
		fmt.Fprintf(conn, "Server error")
		return
	}

	reply := make(map[string]interface{}, 0)
	reply["removed"] = toRemoveStr
	httputil.ReturnJson(conn, reply)
}
