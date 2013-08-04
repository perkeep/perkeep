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
	"camlistore.org/pkg/httputil"
)

func CreateStatHandler(storage blobserver.BlobStatter) http.Handler {
	return http.HandlerFunc(func(conn http.ResponseWriter, req *http.Request) {
		handleStat(conn, req, storage)
	})
}

const maxStatBlobs = 1000

func handleStat(conn http.ResponseWriter, req *http.Request, storage blobserver.BlobStatter) {
	if w, ok := storage.(blobserver.ContextWrapper); ok {
		storage = w.WrapContext(req)
	}

	toStat := make([]blob.Ref, 0)
	switch req.Method {
	case "POST":
		fallthrough
	case "GET":
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
			toStat = append(toStat, ref)
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

	statRes := make([]map[string]interface{}, 0)
	if len(toStat) > 0 {
		blobch := make(chan blob.SizedRef)
		resultch := make(chan error, 1)
		go func() {
			err := storage.StatBlobs(blobch, toStat, time.Duration(waitSeconds)*time.Second)
			close(blobch)
			resultch <- err
		}()

		for sb := range blobch {
			ah := make(map[string]interface{})
			ah["blobRef"] = sb.Ref.String()
			ah["size"] = sb.Size
			statRes = append(statRes, ah)
		}

		err := <-resultch
		if err != nil {
			log.Printf("Stat error: %v", err)
			conn.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	configer, _ := storage.(blobserver.Configer)
	ret, err := commonUploadResponse(configer, req)
	if err != nil {
		httputil.ServeError(conn, req, err)
	}
	ret["canLongPoll"] = false
	if configer != nil {
		if conf := configer.Config(); conf != nil {
			ret["canLongPoll"] = conf.CanLongPoll
		}
	}
	ret["stat"] = statRes
	httputil.ReturnJSON(conn, ret)
}
