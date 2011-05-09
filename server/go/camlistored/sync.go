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

package main

import (
	"fmt"
	"http"
	"log"

	"camli/blobserver"
)

var _ = log.Printf

type SyncHandler struct {
	fromName, toName string
	from, to         blobserver.Storage
}

func createSyncHandler(fromName, toName string, from, to blobserver.Storage) *SyncHandler {
	return &SyncHandler{
		from:     from,
		to:       to,
		fromName: fromName,
		toName:   toName,
	}
}

func (sh *SyncHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(rw, "sync handler, from %s to %s", sh.fromName, sh.toName)
}
