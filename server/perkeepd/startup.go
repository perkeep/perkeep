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

package main

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/auth"
)

// startupLogLines is how many of the most recent log lines a startupLog keeps.
const startupLogLines = 500

// startupLog is an io.Writer for the log package that keeps the most
// recent lines written to it, to show while the server starts.
type startupLog struct {
	mu      sync.Mutex
	lines   []string // ring buffer of the last startupLogLines lines
	next    int      // index in lines of the next line to write, once full
	partial []byte   // the start of a line not yet ended with a newline
}

func (l *startupLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		line, rest, ok := bytes.Cut(p, []byte("\n"))
		if !ok {
			l.partial = append(l.partial, line...)
			break
		}
		l.addLocked(string(l.partial) + string(line))
		l.partial = l.partial[:0]
		p = rest
	}
	return n, nil
}

func (l *startupLog) addLocked(line string) {
	if len(l.lines) < startupLogLines {
		l.lines = append(l.lines, line)
		return
	}
	l.lines[l.next] = line
	l.next = (l.next + 1) % startupLogLines
}

// Lines returns the kept lines, oldest first.
func (l *startupLog) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append(l.lines[l.next:len(l.lines):len(l.lines)], l.lines[:l.next]...)
}

// startupHandler serves all requests while the server starts, before its
// handlers are installed (see webserver.Server.SetStartupHandler). It
// shows the startup log at / and /ui/ to the server's owner, passes
// /healthz to the health handler, and debug requests from the owner (for
// profiles and goroutines' stacks, as of a server stuck starting) to the
// debug handler, and tells all other requests to try again soon.
type startupHandler struct {
	log     *startupLog
	start   time.Time
	healthz http.Handler // or nil
	debug   http.Handler // or nil; see serverinit.DebugHandler
}

func (h *startupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && h.healthz != nil {
		h.healthz.ServeHTTP(w, r)
		return
	}
	if h.debug != nil && (strings.HasPrefix(r.URL.Path, "/debug/pprof/") || r.URL.Path == "/debug/goroutines") {
		// Only the owner, as for the startup log. Until the auth
		// mode is configured, first thing when the handlers are
		// installed, no one is.
		if !auth.Allowed(r, auth.OpAll) {
			auth.SendUnauthorized(w, r)
			return
		}
		h.debug.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Retry-After", "5")
	if !isStartupPage(r) {
		http.Error(w, "Perkeep is starting up; try again soon.", http.StatusServiceUnavailable)
		return
	}
	// The log can include file paths, hostnames, and blobrefs, so
	// only the owner gets to see it. The auth mode is configured
	// first thing when the handlers are installed.
	if !auth.Allowed(r, auth.OpAll) {
		auth.SendUnauthorized(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	var buf strings.Builder
	for _, line := range h.log.Lines() {
		buf.WriteString(html.EscapeString(line))
		buf.WriteByte('\n')
	}
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta http-equiv="refresh" content="2"><title>Perkeep is starting</title></head>
<body><h1>Perkeep is starting</h1>
<p>Started %v ago. This page reloads until the server is ready.</p>
<pre>%s</pre></body></html>
`, time.Since(h.start).Round(time.Second), buf.String())
}

// isStartupPage reports whether r is for the page that shows startup
// progress, rather than an API request (such as discovery, at the same
// paths) that should just be told to try again.
func isStartupPage(r *http.Request) bool {
	if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/ui/") {
		return false
	}
	if r.Method != "GET" || r.URL.Query().Get("camli.mode") != "" ||
		r.Header.Get("Accept") == "text/x-camli-configuration" {
		return false
	}
	return true
}
