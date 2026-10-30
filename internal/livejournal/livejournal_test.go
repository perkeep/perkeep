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

package livejournal

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

const testPassword = "secret"

type fakeEntry struct {
	Entry
	syncTime string
}

type fakeComment struct {
	Comment
	body string // raw bytes of its body in the export, which may be invalid UTF-8
}

// fakeLJ is a LiveJournal server with a user "brad" and one journal.
type fakeLJ struct {
	t        *testing.T
	entries  []fakeEntry
	comments []fakeComment
	users    map[int]string
	pageSize int // of syncitems, getevents, and comment pages

	mu         sync.Mutex
	challenges map[string]bool // unused ones
	nextChal   int
	sessions   map[string]bool
	failNext   int // how many requests to fail with 503 first
	sawPass    bool
	logins     int  // requests authenticated by challenge-response
	noCookie   bool // reject requests authenticated with a session cookie
}

func (f *fakeLJ) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("User-Agent") != UserAgent {
		f.t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
	}
	if f.failNext > 0 {
		f.failNext--
		http.Error(w, "try later", http.StatusServiceUnavailable)
		return
	}
	switch r.URL.Path {
	case "/interface/flat":
		r.ParseForm()
		f.flat(w, r)
	case "/export_comments.bml":
		c, err := r.Cookie("ljsession")
		if err != nil || !f.sessions[c.Value] {
			http.Error(w, "not logged in", http.StatusForbidden)
			return
		}
		start, _ := strconv.Atoi(r.FormValue("startid"))
		f.exportComments(w, r.FormValue("get"), start)
	default:
		http.NotFound(w, r)
	}
}

func writeFlat(w http.ResponseWriter, kv ...string) {
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(w, "%s\n%s\n", kv[i], kv[i+1])
	}
}

func (f *fakeLJ) flat(w http.ResponseWriter, r *http.Request) {
	form := r.Form
	if strings.Contains(form.Encode(), testPassword) {
		f.sawPass = true
	}
	mode := form.Get("mode")
	if mode == "getchallenge" {
		f.nextChal++
		chal := "c0:" + strconv.Itoa(f.nextChal)
		f.challenges[chal] = true
		writeFlat(w, "success", "OK", "challenge", chal)
		return
	}
	if form.Get("auth_method") == "cookie" {
		c, err := r.Cookie("ljsession")
		if f.noCookie || err != nil || !f.sessions[c.Value] || r.Header.Get("X-LJ-Auth") != "cookie" || form.Get("user") != "brad" {
			writeFlat(w, "success", "FAIL", "errmsg", "Invalid session")
			return
		}
		f.serveMode(w, mode, form, "")
		return
	}
	chal := form.Get("auth_challenge")
	hp := md5.Sum([]byte(testPassword))
	want := md5.Sum([]byte(chal + hex.EncodeToString(hp[:])))
	if form.Get("user") != "brad" || form.Get("auth_method") != "challenge" || !f.challenges[chal] || form.Get("auth_response") != hex.EncodeToString(want[:]) {
		writeFlat(w, "success", "FAIL", "errmsg", "Invalid password")
		return
	}
	delete(f.challenges, chal)
	f.logins++
	f.serveMode(w, mode, form, chal)
}

// serveMode serves an authenticated request of mode. chal is the
// challenge it logged in with, if any.
func (f *fakeLJ) serveMode(w http.ResponseWriter, mode string, form url.Values, chal string) {
	switch mode {
	case "login":
		writeFlat(w, "success", "OK", "name", "Brad")
	case "sessiongenerate":
		s := "sess" + chal
		f.sessions[s] = true
		writeFlat(w, "success", "OK", "ljsession", s)
	case "syncitems":
		es := f.entriesAfter(form.Get("lastsync"))
		kv := []string{"success", "OK", "sync_total", strconv.Itoa(len(es))}
		es = es[:min(len(es), f.pageSize)]
		kv = append(kv, "sync_count", strconv.Itoa(len(es)))
		for i, e := range es {
			p := "sync_" + strconv.Itoa(i+1) + "_"
			kv = append(kv, p+"item", "L-"+strconv.Itoa(e.ItemID), p+"action", "create", p+"time", e.syncTime)
		}
		writeFlat(w, kv...)
	case "getevents":
		var es []fakeEntry
		switch form.Get("selecttype") {
		case "syncitems":
			es = f.entriesAfter(form.Get("lastsync"))
			es = es[:min(len(es), f.pageSize)]
		case "one":
			id, _ := strconv.Atoi(form.Get("itemid"))
			for _, e := range f.entries {
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
				p+"event", ljEscape(e.Event),
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

// ljEscape percent-encodes s as LiveJournal's flat protocol does an
// entry's text: everything but letters, digits, and some punctuation,
// with spaces as %20 and "+" as itself.
func ljEscape(s string) string {
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
func (f *fakeLJ) entriesAfter(lastsync string) []fakeEntry {
	var es []fakeEntry
	for _, e := range f.entries {
		if e.syncTime > lastsync {
			es = append(es, e)
		}
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].syncTime < es[j].syncTime })
	return es
}

func (f *fakeLJ) exportComments(w http.ResponseWriter, get string, start int) {
	maxID := 0
	for _, c := range f.comments {
		maxID = max(maxID, c.ID)
	}
	var page []fakeComment
	for _, c := range f.comments {
		if c.ID >= start && len(page) < f.pageSize {
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
		for id, u := range f.users {
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
			fmt.Fprintf(&b, "<subject>%s</subject><body>%s</body><date>%s</date></comment>\n", c.Subject, c.body, c.Date)
		}
		b.WriteString("</comments>\n")
	}
	b.WriteString("</livejournal>\n")
	w.Write(b.Bytes())
}

func newFake(t *testing.T) *fakeLJ {
	f := &fakeLJ{
		t:          t,
		pageSize:   2,
		users:      map[int]string{2: "brad", 7: "friend"},
		challenges: map[string]bool{},
		sessions:   map[string]bool{},
	}
	// Entries 2 and 3 changed in the same second, which a page of two
	// splits.
	for i, st := range []string{"2001-01-01 00:00:01", "2001-01-01 00:00:02", "2001-01-01 00:00:02", "2001-01-01 00:00:03", "2002-05-05 10:00:00"} {
		id := i + 1
		e := fakeEntry{Entry: Entry{
			ItemID:    id,
			Anum:      10 + id,
			EventTime: fmt.Sprintf("2000-12-%02d 23:15:00", id),
			Subject:   "Entry " + strconv.Itoa(id),
			Event:     "Hello & <b>bye</b>\nline two +100%",
			Security:  "public",
			URL:       fmt.Sprintf("https://brad.livejournal.com/%d.html", id*256+10+id),
			Props:     map[string]string{},
		}, syncTime: st}
		if id == 2 {
			e.Security, e.AllowMask = "usemask", "1"
			e.Props["taglist"] = "food, portland"
			e.Props["current_mood"] = "hungry"
		}
		f.entries = append(f.entries, e)
	}
	f.comments = []fakeComment{
		{Comment: Comment{ID: 1, JItemID: 1, PosterID: 7, State: "A", Subject: "Hi", Date: "2001-01-02T00:00:00Z"}, body: "caf\xe9 \x93quoted\x94"},
		{Comment: Comment{ID: 2, JItemID: 1, ParentID: 1, PosterID: 2, State: "A", Date: "2001-01-03T00:00:00Z"}, body: "reply"},
		{Comment: Comment{ID: 4, JItemID: 2, State: "D"}},
	}
	return f
}

func newTestClient(t *testing.T, f *fakeLJ) *Client {
	// The in-memory network's client sends requests to any host to ts.
	ts := httptest.NewTestServer(t, f)
	c := NewClient("https://www.livejournal.example", "brad", testPassword)
	c.HTTPClient = ts.Client()
	c.Limiter = nil
	c.Retries = 0
	return c
}

func TestEntries(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	ctx := context.Background()
	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := c.SyncItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(f.entries) {
		t.Fatalf("got %d sync items; want %d", len(items), len(f.entries))
	}
	es, err := c.Entries(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != len(f.entries) {
		t.Fatalf("got %d entries; want %d", len(es), len(f.entries))
	}
	for i, e := range es {
		want := f.entries[i].Entry
		want.SyncTime, _ = time.Parse(syncTimeLayout, f.entries[i].syncTime)
		if !reflect.DeepEqual(*e, want) {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i+1, *e, want)
		}
	}
	if got, want := es[1].Tags(), []string{"food", "portland"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags = %q; want %q", got, want)
	}
	if es[1].DItemID() != 2*256+12 {
		t.Errorf("DItemID = %d", es[1].DItemID())
	}
	if f.sawPass {
		t.Error("the password was sent")
	}
	if f.logins != 1 {
		t.Errorf("logged in %d times; want once, then use the session", f.logins)
	}
}

func TestCookieAuthRejected(t *testing.T) {
	f := newFake(t)
	f.noCookie = true
	c := newTestClient(t, f)
	ctx := context.Background()
	items, err := c.SyncItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	es, err := c.Entries(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != len(f.entries) {
		t.Fatalf("got %d entries; want %d", len(es), len(f.entries))
	}
	if f.logins < 3 {
		t.Errorf("logged in %d times; want each request to log in", f.logins)
	}
}

func TestBadPassword(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	c.hpass = "wrong"
	err := c.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid password") {
		t.Fatalf("Login = %v; want Invalid password", err)
	}
}

func TestComments(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	var calls int
	cs, err := c.Comments(context.Background(), func(done, max int) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	want := []*Comment{
		{ID: 1, JItemID: 1, PosterID: 7, Poster: "friend", State: "A", Subject: "Hi", Body: "café “quoted”", Date: "2001-01-02T00:00:00Z"},
		{ID: 2, JItemID: 1, ParentID: 1, PosterID: 2, Poster: "brad", State: "A", Body: "reply", Date: "2001-01-03T00:00:00Z"},
		{ID: 4, JItemID: 2, State: "D"},
	}
	if !reflect.DeepEqual(cs, want) {
		for _, c := range cs {
			t.Logf("got %+v", *c)
		}
		t.Fatal("wrong comments")
	}
	if calls == 0 {
		t.Error("progress not called")
	}
}

func TestRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake(t)
		f.failNext = 3
		c := newTestClient(t, f)
		c.Retries = 3
		start := time.Now()
		if err := c.Login(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, want := time.Since(start), 7*time.Second; got != want {
			t.Errorf("took %v; want %v of backoff", got, want)
		}

		// A new client, without a session, gives up after its retries.
		f.failNext = 2
		c = newTestClient(t, f)
		c.Retries = 1
		if err := c.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
			t.Errorf("Login = %v; want a 503 error", err)
		}
	})
}

func TestFixText(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"plain", "plain"},
		{"caf\xe9", "café"},
		{"\x93hi\x94 \x80", "“hi” €"},
		{"already ✓ utf-8", "already ✓ utf-8"},
		{"bell\x07\ttab", "bell\ufffd\ttab"},
		{"", ""},
	} {
		if got := fixString(tt.in); got != tt.want {
			t.Errorf("fixString(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}
