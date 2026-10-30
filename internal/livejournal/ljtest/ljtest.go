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

// Package ljtest is a fake LiveJournal server, for tests of package
// livejournal and its users.
package ljtest

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"perkeep.org/internal/livejournal"
)

// The fake's user, whose journal it has, and its password.
const (
	User     = "brad"
	Password = "secret"
)

// BaseURL is a base URL for clients of the fake. Server.HTTPClient sends
// requests to any host to the fake.
const BaseURL = "https://www.livejournal.example"

// An Entry is an entry of the fake's journal.
type Entry struct {
	livejournal.Entry
	SyncTime string // when it was made or last changed, as the server writes it
}

// A Comment is a comment of the fake's journal.
type Comment struct {
	livejournal.Comment
	RawBody string // the bytes of its body in the export, which may be invalid UTF-8
}

// A Server is a fake LiveJournal server, with the flat protocol and the
// comment export, on an in-memory network.
type Server struct {
	t  testing.TB
	ts *httptest.Server

	mu         sync.Mutex
	entries    []Entry
	comments   []Comment
	users      map[int]string
	pageSize   int             // of syncitems, getevents, and comment pages
	challenges map[string]bool // unused ones
	nextChal   int
	sessions   map[string]bool
	failNext   int  // how many requests to fail with 503 first
	sawPass    bool // whether a request had the password
	logins     int  // requests authenticated by challenge-response
	noCookie   bool // reject requests authenticated with a session cookie
}

// New returns a fake server with a journal of five entries (one
// friends-only, with tags and a mood; two changed in the same second,
// which a page of two splits) and three comments on them (one with text
// that isn't UTF-8, a reply, and a deleted one).
func New(t testing.TB) *Server {
	s := &Server{
		t:          t,
		pageSize:   2,
		users:      map[int]string{2: User, 7: "friend"},
		challenges: map[string]bool{},
		sessions:   map[string]bool{},
	}
	for i, st := range []string{"2001-01-01 00:00:01", "2001-01-01 00:00:02", "2001-01-01 00:00:02", "2001-01-01 00:00:03", "2002-05-05 10:00:00"} {
		id := i + 1
		e := Entry{Entry: livejournal.Entry{
			ItemID:    id,
			Anum:      10 + id,
			EventTime: fmt.Sprintf("2000-12-%02d 23:15:00", id),
			Subject:   "Entry " + strconv.Itoa(id),
			Event:     "Hello & <b>bye</b>\nline two +100%",
			Security:  "public",
			URL:       fmt.Sprintf("https://brad.livejournal.com/%d.html", id*256+10+id),
			Props:     map[string]string{},
		}, SyncTime: st}
		if id == 2 {
			e.Security, e.AllowMask = "usemask", "1"
			e.Props["taglist"] = "food, portland"
			e.Props["current_mood"] = "hungry"
		}
		s.entries = append(s.entries, e)
	}
	s.comments = []Comment{
		{Comment: livejournal.Comment{ID: 1, JItemID: 1, PosterID: 7, State: "A", Subject: "Hi", Date: "2001-01-02T00:00:00Z"}, RawBody: "caf\xe9 \x93quoted\x94"},
		{Comment: livejournal.Comment{ID: 2, JItemID: 1, ParentID: 1, PosterID: 2, State: "A", Date: "2001-01-03T00:00:00Z"}, RawBody: "reply"},
		{Comment: livejournal.Comment{ID: 4, JItemID: 2, State: "D"}},
	}
	s.ts = httptest.NewTestServer(t, s)
	return s
}

// HTTPClient returns a client whose requests to any host go to s.
func (s *Server) HTTPClient() *http.Client { return s.ts.Client() }

// Client returns a client of s for User, logging in with password, that
// doesn't limit its rate or retry.
func (s *Server) Client(password string) *livejournal.Client {
	c := livejournal.NewClient(BaseURL, User, password)
	c.HTTPClient = s.HTTPClient()
	c.Limiter = nil
	c.Retries = 0
	return c
}

// Entries returns the journal's entries.
func (s *Server) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.entries...)
}

// Logins returns how many requests have logged in by challenge-response.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// SawPassword reports whether a request had the password.
func (s *Server) SawPassword() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawPass
}

// FailNext makes the next n requests fail with 503 Service Unavailable.
func (s *Server) FailNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = n
}

// RejectCookieAuth makes s reject requests authenticated with a session
// cookie, rather than by challenge-response.
func (s *Server) RejectCookieAuth() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noCookie = true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("User-Agent") != livejournal.UserAgent {
		s.t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
	}
	if s.failNext > 0 {
		s.failNext--
		http.Error(w, "try later", http.StatusServiceUnavailable)
		return
	}
	switch r.URL.Path {
	case "/interface/flat":
		r.ParseForm()
		s.flat(w, r)
	case "/export_comments.bml":
		c, err := r.Cookie("ljsession")
		if err != nil || !s.sessions[c.Value] {
			http.Error(w, "not logged in", http.StatusForbidden)
			return
		}
		start, _ := strconv.Atoi(r.FormValue("startid"))
		s.exportComments(w, r.FormValue("get"), start)
	default:
		http.NotFound(w, r)
	}
}

func writeFlat(w http.ResponseWriter, kv ...string) {
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(w, "%s\n%s\n", kv[i], kv[i+1])
	}
}

func (s *Server) flat(w http.ResponseWriter, r *http.Request) {
	form := r.Form
	if strings.Contains(form.Encode(), Password) {
		s.sawPass = true
	}
	mode := form.Get("mode")
	if mode == "getchallenge" {
		s.nextChal++
		chal := "c0:" + strconv.Itoa(s.nextChal)
		s.challenges[chal] = true
		writeFlat(w, "success", "OK", "challenge", chal)
		return
	}
	if form.Get("auth_method") == "cookie" {
		c, err := r.Cookie("ljsession")
		if s.noCookie || err != nil || !s.sessions[c.Value] || r.Header.Get("X-LJ-Auth") != "cookie" || form.Get("user") != User {
			writeFlat(w, "success", "FAIL", "errmsg", "Invalid session")
			return
		}
		s.serveMode(w, mode, form, "")
		return
	}
	chal := form.Get("auth_challenge")
	hp := md5.Sum([]byte(Password))
	want := md5.Sum([]byte(chal + hex.EncodeToString(hp[:])))
	if form.Get("user") != User || form.Get("auth_method") != "challenge" || !s.challenges[chal] || form.Get("auth_response") != hex.EncodeToString(want[:]) {
		writeFlat(w, "success", "FAIL", "errmsg", "Invalid password")
		return
	}
	delete(s.challenges, chal)
	s.logins++
	s.serveMode(w, mode, form, chal)
}

// serveMode serves an authenticated request of mode. chal is the
// challenge it logged in with, if any.
func (s *Server) serveMode(w http.ResponseWriter, mode string, form url.Values, chal string) {
	switch mode {
	case "login":
		writeFlat(w, "success", "OK", "name", "Brad")
	case "sessiongenerate":
		sess := "sess" + chal
		s.sessions[sess] = true
		writeFlat(w, "success", "OK", "ljsession", sess)
	case "syncitems":
		es := s.entriesAfter(form.Get("lastsync"))
		kv := []string{"success", "OK", "sync_total", strconv.Itoa(len(es))}
		es = es[:min(len(es), s.pageSize)]
		kv = append(kv, "sync_count", strconv.Itoa(len(es)))
		for i, e := range es {
			p := "sync_" + strconv.Itoa(i+1) + "_"
			kv = append(kv, p+"item", "L-"+strconv.Itoa(e.ItemID), p+"action", "create", p+"time", e.SyncTime)
		}
		writeFlat(w, kv...)
	case "getevents":
		var es []Entry
		switch form.Get("selecttype") {
		case "syncitems":
			es = s.entriesAfter(form.Get("lastsync"))
			es = es[:min(len(es), s.pageSize)]
		case "one":
			id, _ := strconv.Atoi(form.Get("itemid"))
			for _, e := range s.entries {
				if e.ItemID == id {
					es = append(es, e)
				}
			}
		}
		kv := []string{"success", "OK", "events_count", strconv.Itoa(len(es))}
		np := 0
		for i, e := range es {
			p := "events_" + strconv.Itoa(i+1) + "_"
			kv = append(kv,
				p+"itemid", strconv.Itoa(e.ItemID),
				p+"anum", strconv.Itoa(e.Anum),
				p+"eventtime", e.EventTime,
				p+"subject", e.Subject,
				p+"event", Escape(e.Event),
				p+"url", e.URL)
			if e.Security != "public" {
				kv = append(kv, p+"security", e.Security, p+"allowmask", e.AllowMask)
			}
			names := make([]string, 0, len(e.Props))
			for n := range e.Props {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				np++
				q := "prop_" + strconv.Itoa(np) + "_"
				kv = append(kv, q+"itemid", strconv.Itoa(e.ItemID), q+"name", n, q+"value", e.Props[n])
			}
		}
		kv = append(kv, "prop_count", strconv.Itoa(np))
		writeFlat(w, kv...)
	default:
		writeFlat(w, "success", "FAIL", "errmsg", "unknown mode")
	}
}

// Escape percent-encodes s as LiveJournal's flat protocol does an entry's
// text: everything but letters, digits, and some punctuation, with spaces
// as %20 and "+" as itself.
func Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~'()+", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// entriesAfter returns the entries changed after lastsync, in order of
// when they changed, as the server does.
func (s *Server) entriesAfter(lastsync string) []Entry {
	var es []Entry
	for _, e := range s.entries {
		if e.SyncTime > lastsync {
			es = append(es, e)
		}
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].SyncTime < es[j].SyncTime })
	return es
}

func (s *Server) exportComments(w http.ResponseWriter, get string, start int) {
	maxID := 0
	for _, c := range s.comments {
		maxID = max(maxID, c.ID)
	}
	var page []Comment
	for _, c := range s.comments {
		if c.ID >= start && len(page) < s.pageSize {
			page = append(page, c)
		}
	}
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding='utf-8'?>\n<livejournal>\n")
	switch get {
	case "comment_meta":
		fmt.Fprintf(&b, "<maxid>%d</maxid>\n<comments>\n", maxID)
		for _, c := range page {
			st := ""
			if c.State != "A" {
				st = fmt.Sprintf(" state='%s'", c.State)
			}
			fmt.Fprintf(&b, "<comment id='%d' posterid='%d'%s />\n", c.ID, c.PosterID, st)
		}
		b.WriteString("</comments>\n<usermaps>\n")
		for id, u := range s.users {
			fmt.Fprintf(&b, "<usermap id='%d' user='%s' />\n", id, u)
		}
		b.WriteString("</usermaps>\n")
	case "comment_body":
		b.WriteString("<comments>\n")
		for _, c := range page {
			if c.State == "D" {
				fmt.Fprintf(&b, "<comment id='%d' jitemid='%d' state='D' />\n", c.ID, c.JItemID)
				continue
			}
			fmt.Fprintf(&b, "<comment id='%d' jitemid='%d' posterid='%d' parentid='%d'>", c.ID, c.JItemID, c.PosterID, c.ParentID)
			fmt.Fprintf(&b, "<subject>%s</subject><body>%s</body><date>%s</date></comment>\n", c.Subject, c.RawBody, c.Date)
		}
		b.WriteString("</comments>\n")
	}
	b.WriteString("</livejournal>\n")
	w.Write(b.Bytes())
}
