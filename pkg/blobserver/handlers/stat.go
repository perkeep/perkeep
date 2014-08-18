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
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/protocol"
	"camlistore.org/pkg/httputil"
)

func CreateStatHandler(storage blobserver.BlobStatter) http.Handler {
	return http.HandlerFunc(func(conn http.ResponseWriter, req *http.Request) {
		handleStat(conn, req, storage)
	})
}

const maxStatBlobs = 1000

func handleStat(conn http.ResponseWriter, req *http.Request, storage blobserver.BlobStatter) {
	res := new(protocol.StatResponse)

	if configer, ok := storage.(blobserver.Configer); ok {
		if conf := configer.Config(); conf != nil {
			res.CanLongPoll = conf.CanLongPoll
		}
	}

	needStat := map[blob.Ref]bool{}

	switch req.Method {
	case "POST":
		fallthrough
	case "GET", "HEAD":
		camliVersion := req.FormValue("camliversion")
		if camliVersion == "" {
			httputil.BadRequestError(conn, "No camliversion")
			return
		}
		n := 0
		for {
			n++
			key := fmt.Sprintf("blob%v", n)
			value := req.FormValue(key)
			if value == "" {
				n--
				break
			}
			if n > maxStatBlobs {
				httputil.BadRequestError(conn, "Too many stat blob checks")
				return
			}
			ref, ok := blob.Parse(value)
			if !ok {
				httputil.BadRequestError(conn, "Bogus blobref for key "+key)
				return
			}
			needStat[ref] = true
		}
	default:
		httputil.BadRequestError(conn, "Invalid method.")
		return

	}

	waitSeconds := 0
	if waitStr := req.FormValue("maxwaitsec"); waitStr != "" {
		waitSeconds, _ = strconv.Atoi(waitStr)
		switch {
		case waitSeconds < 0:
			waitSeconds = 0
		case waitSeconds > 30:
			// TODO: don't hard-code 30.  push this up into a blobserver interface
			// for getting the configuration of the server (ultimately a flag in
			// in the binary)
			waitSeconds = 30
		}
	}

	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)

	toStat := make([]blob.Ref, 0, len(needStat))
	buildToStat := func() {
		toStat = toStat[:0]
		for br := range needStat {
			toStat = append(toStat, br)
		}
	}

	for len(needStat) > 0 {
		buildToStat()
		blobch := make(chan blob.SizedRef)
		resultch := make(chan error, 1)
		go func() {
			err := storage.StatBlobs(blobch, toStat)
			close(blobch)
			resultch <- err
		}()

		for sb := range blobch {
			res.Stat = append(res.Stat, blob.SizedRef{
				Ref:  sb.Ref,
				Size: uint32(sb.Size),
			})
			delete(needStat, sb.Ref)
		}

		err := <-resultch
		if err != nil {
			log.Printf("Stat error: %v", err)
			conn.WriteHeader(http.StatusInternalServerError)
			return
		}

		if len(needStat) == 0 || waitSeconds == 0 || time.Now().After(deadline) {
			break
		}

		buildToStat()
		blobserver.WaitForBlob(storage, deadline, toStat)
	}

	httputil.ReturnJSON(conn, res)
}
