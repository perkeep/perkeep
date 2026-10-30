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

// Package livejournal reads a LiveJournal journal: its entries, with
// LiveJournal's "flat" client protocol, and the comments on them, with
// its comment export (export_comments.bml), and keeps permanodes of them
// in Perkeep (see Sync).
//
// Logins use challenge-response, so the password itself is never sent.
package livejournal

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// DefaultBaseURL is LiveJournal's.
const DefaultBaseURL = "https://www.livejournal.com"

// A Client reads a user's own journal.
type Client struct {
	// BaseURL is the server's URL, such as DefaultBaseURL.
	BaseURL string
	// User is the user to log in as, whose journal is read.
	User string
	// HTTPClient, if non-nil, is used instead of http.DefaultClient.
	HTTPClient *http.Client
	// Limiter, if non-nil, limits the rate of requests.
	Limiter *rate.Limiter
	// Retries is how many times to retry a request that fails because
	// of the network or the server, with backoff.
	Retries int

	hpass string // MD5 of the password, in hex

	mu sync.Mutex
	// session is the session cookie, once logged in, with which
	// requests are authenticated, rather than each with a login by
	// challenge-response, as LiveJournal temporarily bans the addresses
	// of clients that log in too often.
	session string
	// noCookieAuth is whether the server rejected a request
	// authenticated with session, so requests use challenge-response.
	noCookieAuth bool
}

// NewClient returns a client of the server at baseURL that logs in as
// user with password.
func NewClient(baseURL, user, password string) *Client {
	sum := md5.Sum([]byte(password))
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		User:    user,
		Limiter: rate.NewLimiter(5, 1),
		Retries: 5,
		hpass:   hex.EncodeToString(sum[:]),
	}
}

// retriableError is an error of a request worth retrying.
type retriableError struct{ err error }

func (e retriableError) Error() string { return e.err.Error() }
func (e retriableError) Unwrap() error { return e.err }

// retry calls fn until it succeeds, fails with an error that isn't a
// retriableError, or has been retried c.Retries times.
func (c *Client) retry(ctx context.Context, fn func() error) error {
	backoff := time.Second
	for try := 0; ; try++ {
		err := fn()
		var re retriableError
		if err == nil || !errors.As(err, &re) || try >= c.Retries {
			return err
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
	}
}

// UserAgent is the User-Agent of the client's requests. LiveJournal
// resets the connections of requests with Go's default User-Agent.
const UserAgent = "perkeep.org-livejournal/1.0"

// do sends req, after waiting for c.Limiter, and returns its response's
// body. Network errors and server errors are retriableErrors.
func (c *Client) do(req *http.Request) ([]byte, error) {
	req.Header.Set("User-Agent", UserAgent)
	if c.Limiter != nil {
		if err := c.Limiter.Wait(req.Context()); err != nil {
			return nil, err
		}
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, err
		}
		return nil, retriableError{err}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, retriableError{err}
	}
	if res.StatusCode != http.StatusOK {
		err := fmt.Errorf("%s", res.Status)
		if res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests {
			return nil, retriableError{err}
		}
		return nil, err
	}
	return body, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// flatError is a failure that a flat protocol response reports.
type flatError struct {
	mode, msg string
}

func (e *flatError) Error() string { return "livejournal: " + e.mode + ": " + e.msg }

// flat makes a request of the flat protocol and returns its response's
// variables. If session isn't empty, the request is authenticated with
// it.
func (c *Client) flat(ctx context.Context, args url.Values, session string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/interface/flat", strings.NewReader(args.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if session != "" {
		req.Header.Set("X-LJ-Auth", "cookie")
		req.AddCookie(&http.Cookie{Name: "ljsession", Value: session})
	}
	body, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("livejournal: %s: %w", args.Get("mode"), err)
	}
	// The response is lines of alternating names and values.
	lines := strings.Split(string(body), "\n")
	vars := make(map[string]string, len(lines)/2)
	for i := 0; i+1 < len(lines); i += 2 {
		vars[lines[i]] = strings.TrimSuffix(lines[i+1], "\r")
	}
	if vars["success"] != "OK" {
		msg := vars["errmsg"]
		if msg == "" {
			msg = "no success in response"
		}
		return nil, &flatError{args.Get("mode"), msg}
	}
	return vars, nil
}

// call makes an authenticated request of the flat protocol, mode, with
// args, retrying after network and server errors.
func (c *Client) call(ctx context.Context, mode string, args url.Values) (vars map[string]string, err error) {
	err = c.retry(ctx, func() error {
		vars, err = c.callOnce(ctx, mode, args)
		return err
	})
	return vars, err
}

func (c *Client) callOnce(ctx context.Context, mode string, args url.Values) (map[string]string, error) {
	c.mu.Lock()
	noCookie := c.noCookieAuth
	c.mu.Unlock()
	if noCookie {
		return c.callChallenge(ctx, mode, args)
	}
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}
	all := url.Values{
		"mode":        {mode},
		"user":        {c.User},
		"auth_method": {"cookie"},
		"ver":         {"1"},
	}
	maps.Copy(all, args)
	vars, err := c.flat(ctx, all, session)
	var fe *flatError
	if errors.As(err, &fe) {
		// Perhaps the server doesn't take the session (it may have
		// expired), so log in for each request from now on.
		c.mu.Lock()
		c.noCookieAuth = true
		c.mu.Unlock()
		return c.callChallenge(ctx, mode, args)
	}
	return vars, err
}

// callChallenge is callOnce, logging in by challenge-response.
func (c *Client) callChallenge(ctx context.Context, mode string, args url.Values) (map[string]string, error) {
	ch, err := c.flat(ctx, url.Values{"mode": {"getchallenge"}}, "")
	if err != nil {
		return nil, err
	}
	chal := ch["challenge"]
	resp := md5.Sum([]byte(chal + c.hpass))
	all := url.Values{
		"mode":           {mode},
		"user":           {c.User},
		"auth_method":    {"challenge"},
		"auth_challenge": {chal},
		"auth_response":  {hex.EncodeToString(resp[:])},
		"ver":            {"1"},
	}
	maps.Copy(all, args)
	return c.flat(ctx, all, "")
}

// Login logs in, checking that the user and password work, and gets a
// session with which to authenticate later requests.
func (c *Client) Login(ctx context.Context) error {
	_, err := c.getSession(ctx)
	return err
}

// A SyncItem is an entry or comment of the journal, with when it was
// last made or changed.
type SyncItem struct {
	Item   string    // such as "L-123" for entry 123
	Action string    // "create" or "update"
	Time   time.Time // in UTC
}

// syncTimeLayout is the layout of the flat protocol's times in UTC.
const syncTimeLayout = "2006-01-02 15:04:05"

// SyncItems returns all of the journal's sync items.
func (c *Client) SyncItems(ctx context.Context) ([]SyncItem, error) {
	var items []SyncItem
	seen := map[string]int{} // item to index in items
	var last time.Time
	stuck := false
	for {
		// Ask from a second before the newest so far, as the server lists
		// what changed after lastsync, and a page can end between items
		// of the same second. If more items changed in one second than
		// fit in a page, that gets nothing new, so then ask from the
		// second itself, missing the rest of that second's.
		arg := lastSyncArg(last)
		if stuck {
			arg = last.Format(syncTimeLayout)
		}
		v, err := c.call(ctx, "syncitems", url.Values{"lastsync": {arg}})
		if err != nil {
			return nil, err
		}
		n, _ := strconv.Atoi(v["sync_count"])
		total, _ := strconv.Atoi(v["sync_total"])
		added := 0
		for i := 1; i <= n; i++ {
			p := "sync_" + strconv.Itoa(i) + "_"
			t, err := time.Parse(syncTimeLayout, v[p+"time"])
			if err != nil {
				return nil, fmt.Errorf("livejournal: syncitems: bad time %q", v[p+"time"])
			}
			it := SyncItem{Item: v[p+"item"], Action: v[p+"action"], Time: t}
			if j, ok := seen[it.Item]; ok {
				items[j] = it
			} else {
				seen[it.Item] = len(items)
				items = append(items, it)
				added++
			}
			if t.After(last) {
				last = t
			}
		}
		if n == total || (added == 0 && stuck) {
			return items, nil
		}
		stuck = added == 0
	}
}

// An Entry is an entry of a journal.
type Entry struct {
	ItemID    int
	Anum      int
	EventTime string // the poster's local time, "2006-01-02 15:04:05", without a zone
	Subject   string
	Event     string // the text, as LiveJournal's HTML
	Security  string // "public", "private", or "usemask"
	AllowMask string // with Security "usemask", the friends groups allowed
	Poster    string // if not the journal's user
	URL       string
	Props     map[string]string // such as "taglist", "current_mood", "current_music"

	// SyncTime is when the entry was made or last changed, in UTC.
	SyncTime time.Time
}

// DItemID returns the entry's number in its URLs.
func (e *Entry) DItemID() int { return e.ItemID*256 + e.Anum }

// Tags returns the entry's tags.
func (e *Entry) Tags() []string {
	var tags []string
	for t := range strings.SplitSeq(e.Props["taglist"], ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// Entries returns the journal's entries, given its sync items. An entry
// that the server lists but doesn't return (such as one since deleted)
// is left out.
func (c *Client) Entries(ctx context.Context, items []SyncItem) ([]*Entry, error) {
	syncTime := map[int]time.Time{}
	for _, it := range items {
		if id, ok := strings.CutPrefix(it.Item, "L-"); ok {
			n, err := strconv.Atoi(id)
			if err != nil {
				return nil, fmt.Errorf("livejournal: bad sync item %q", it.Item)
			}
			syncTime[n] = it.Time
		}
	}
	got := map[int]*Entry{}
	add := func(v map[string]string) error {
		es, err := parseEvents(v)
		if err != nil {
			return err
		}
		for _, e := range es {
			e.SyncTime = syncTime[e.ItemID]
			got[e.ItemID] = e
		}
		return nil
	}

	// Get the entries in order of when they changed, a batch at a time,
	// each after the newest of the last batch.
	last := time.Time{}
	for {
		v, err := c.call(ctx, "getevents", url.Values{
			"selecttype":  {"syncitems"},
			"lastsync":    {lastSyncArg(last)},
			"lineendings": {"unix"},
		})
		if err != nil {
			return nil, err
		}
		before := len(got)
		if err := add(v); err != nil {
			return nil, err
		}
		next := last
		for _, e := range got {
			if e.SyncTime.After(next) {
				next = e.SyncTime
			}
		}
		if len(got) == before || !next.After(last) {
			break
		}
		last = next
	}

	// Batches can split entries that changed in the same second, so get
	// any missed one by one.
	for id := range syncTime {
		if got[id] != nil {
			continue
		}
		v, err := c.call(ctx, "getevents", url.Values{
			"selecttype":  {"one"},
			"itemid":      {strconv.Itoa(id)},
			"lineendings": {"unix"},
		})
		if err != nil {
			return nil, err
		}
		if err := add(v); err != nil {
			return nil, err
		}
	}

	es := make([]*Entry, 0, len(got))
	for _, e := range got {
		es = append(es, e)
	}
	sortEntries(es)
	return es, nil
}

func lastSyncArg(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	// The server returns the entries changed after lastsync, so ask from
	// a second before, to not miss others of the same second.
	return t.Add(-time.Second).UTC().Format(syncTimeLayout)
}

// parseEvents returns the entries of a getevents response.
func parseEvents(v map[string]string) ([]*Entry, error) {
	n, _ := strconv.Atoi(v["events_count"])
	byID := map[string]*Entry{}
	var es []*Entry
	for i := 1; i <= n; i++ {
		p := "events_" + strconv.Itoa(i) + "_"
		id, err := strconv.Atoi(v[p+"itemid"])
		if err != nil {
			return nil, fmt.Errorf("livejournal: getevents: bad itemid %q", v[p+"itemid"])
		}
		// The text is percent-encoded, with spaces as %20, so a "+" is
		// itself.
		event, err := url.PathUnescape(v[p+"event"])
		if err != nil {
			return nil, fmt.Errorf("livejournal: getevents: entry %d: %w", id, err)
		}
		anum, _ := strconv.Atoi(v[p+"anum"])
		e := &Entry{
			ItemID:    id,
			Anum:      anum,
			EventTime: v[p+"eventtime"],
			Subject:   fixString(v[p+"subject"]),
			Event:     fixString(event),
			Security:  v[p+"security"],
			AllowMask: v[p+"allowmask"],
			Poster:    v[p+"poster"],
			URL:       v[p+"url"],
			Props:     map[string]string{},
		}
		if e.Security == "" {
			e.Security = "public"
		}
		byID[v[p+"itemid"]] = e
		es = append(es, e)
	}
	np, _ := strconv.Atoi(v["prop_count"])
	for i := 1; i <= np; i++ {
		p := "prop_" + strconv.Itoa(i) + "_"
		if e := byID[v[p+"itemid"]]; e != nil {
			e.Props[v[p+"name"]] = fixString(v[p+"value"])
		}
	}
	return es, nil
}

// getSession returns the client's session cookie, logging in by
// challenge-response to get one if it has none. The comment export uses
// it, as do flat protocol requests, unless the server rejects them.
func (c *Client) getSession(ctx context.Context) (string, error) {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s != "" {
		return s, nil
	}
	var v map[string]string
	err := c.retry(ctx, func() (err error) {
		v, err = c.callChallenge(ctx, "sessiongenerate", url.Values{"expiration": {"short"}})
		return err
	})
	if err != nil {
		return "", err
	}
	if v["ljsession"] == "" {
		return "", errors.New("livejournal: sessiongenerate: no session")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = v["ljsession"]
	return c.session, nil
}
