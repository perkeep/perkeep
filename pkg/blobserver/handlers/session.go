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

package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/blobserver/uploadsession"
)

// CreateSessionHandler returns the handler that opens, flushes, reports
// on, and closes upload sessions, at camli/session and below it. See
// doc/protocol/blob-upload-session.md.
func CreateSessionHandler(sessions *uploadsession.Manager) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		_, rest, ok := strings.Cut(req.URL.Path, "/camli/session")
		if !ok {
			httputil.BadRequestError(rw, "Inconfigured handler.")
			return
		}
		if rest == "" {
			if req.Method != "POST" {
				httputil.BadRequestError(rw, "Upload sessions are opened with POST.")
				return
			}
			openSession(rw, req, sessions)
			return
		}
		id, op, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
		s, err := sessions.Session(id)
		if err != nil {
			serveSessionError(rw, req, err)
			return
		}
		switch {
		case req.Method == "DELETE" && op == "":
			s.Close()
			rw.WriteHeader(http.StatusNoContent)
		case req.Method == "POST" && (op == "flush" || op == "status"):
			sessionStatus(rw, req, s, op == "flush")
		default:
			httputil.BadRequestError(rw, "Unsupported upload session request.")
		}
	})
}

func openSession(rw http.ResponseWriter, req *http.Request, sessions *uploadsession.Manager) {
	var idle time.Duration
	if v := req.FormValue("idleTimeout"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs < 0 {
			httputil.BadRequestError(rw, "Invalid idleTimeout %q.", v)
			return
		}
		idle = time.Duration(secs) * time.Second
	}
	s, err := sessions.Open(idle)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusServiceUnavailable)
		return
	}
	httputil.ReturnJSON(rw, &protocol.SessionOpenResponse{
		Session:            s.ID(),
		IdleTimeoutSeconds: int(s.IdleTimeout() / time.Second),
		MaxBufferedBytes:   sessions.Limits().SessionBytes,
	})
}

// sessionStatus serves a session's status, after flushing it if flush.
func sessionStatus(rw http.ResponseWriter, req *http.Request, s *uploadsession.Session, flush bool) {
	if err := req.ParseForm(); err != nil {
		httputil.BadRequestError(rw, "Invalid form: %v", err)
		return
	}
	var ack []blob.Ref
	for _, v := range req.PostForm["ack"] {
		br, ok := blob.Parse(v)
		if !ok {
			httputil.BadRequestError(rw, "Invalid ack %q.", v)
			return
		}
		ack = append(ack, br)
	}
	var durable []blob.SizedRef
	var err error
	if flush {
		durable, err = s.Flush(req.Context(), ack)
	} else {
		durable, err = s.Status(ack)
	}
	res := &protocol.SessionStatusResponse{Durable: durable}
	if be, ok := errors.AsType[*blobserver.BlobsError](err); ok {
		for br, err := range be.All() {
			res.Failed = append(res.Failed, protocol.FailedBlob{BlobRef: br, Error: err.Error()})
		}
	} else if err != nil {
		serveSessionError(rw, req, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

// serveSessionError serves err from an upload session operation, with a
// 404 if the session is unknown.
func serveSessionError(rw http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, uploadsession.ErrUnknownSession) {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	}
	httputil.ServeError(rw, req, err)
}
