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
	"os"
	"log"
	"strings"

	"camli/blobserver"
)

var _ = log.Printf

type SyncHandler struct {
	fromName, toName string
	from, fromq, to  blobserver.Storage
}

func createSyncHandler(fromName, toName string, from, to blobserver.Storage) (*SyncHandler, os.Error) {
	h := &SyncHandler{
		from:     from,
		to:       to,
		fromName: fromName,
		toName:   toName,
	}

	qc, ok := from.(blobserver.QueueCreator)
	if !ok {
		return nil, fmt.Errorf(
			"Prefix %s (type %T) does not support being efficient replication source (queueing)",
			fromName, from)
	}
	queueName := strings.Replace(strings.Trim(toName, "/"), "/", "-", -1)
	var err os.Error
	h.fromq, err = qc.CreateQueue(queueName)
	if err != nil {
		return nil, fmt.Errorf("Prefix %s (type %T) failed to create queue %q: %v",
			fromName, from, queueName, err)
	}

	return h, nil
}

func (sh *SyncHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(rw, "sync handler, from %s to %s", sh.fromName, sh.toName)
}
