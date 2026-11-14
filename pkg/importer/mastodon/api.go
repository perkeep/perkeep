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

package mastodon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The API's rate limit is of calls per account in a window of time,
// shared with the account's other apps: by default, 300 every 5 minutes.
// A run leaves rateReserveDiv-th of each window's calls (and at least
// minRateReserve) for the others, waiting out the rest of a window when
// it has used the rest.
const (
	rateReserveDiv = 5
	minRateReserve = 10

	// maxRetries is how many times a call refused for the rate limit is
	// retried, after waiting for its window to end.
	maxRetries = 5

	// defaultRateWait is how long to wait after a call refused for the
	// rate limit without saying when its window ends.
	defaultRateWait = time.Minute
)

// statusesPageSize is the most statuses the API returns at once.
const statusesPageSize = 40

// apiClient calls the API of a Mastodon server, as an account, keeping
// within its rate limit.
type apiClient struct {
	hc    *http.Client
	base  *url.URL // of the server
	token string   // the account's access token, or empty before it has one

	// logf, if non-nil, logs waits for the rate limit.
	logf func(format string, args ...any)

	mu        sync.Mutex
	limit     int       // calls in a window, or 0 before a response says
	remaining int       // calls left in the window, less those in flight
	reset     time.Time // when the window ends
}

func newAPIClient(hc *http.Client, instanceURL, token string) (*apiClient, error) {
	base, err := url.Parse(instanceURL)
	if err != nil {
		return nil, err
	}
	if base.Scheme != "https" && base.Scheme != "http" || base.Host == "" {
		return nil, fmt.Errorf("instance URL %q isn't an http or https URL", instanceURL)
	}
	return &apiClient{hc: hc, base: base, token: token}, nil
}

// An apiError is a call's response of an HTTP status other than 200.
type apiError struct {
	URL        string
	StatusCode int
	Message    string // the response's error message, if any
}

func (e *apiError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("mastodon: %s: HTTP status %d: %s", e.URL, e.StatusCode, msg)
}

// isNotFound reports whether err is of a call to something the server
// doesn't have, or doesn't show the account.
func isNotFound(err error) bool {
	ae, ok := errors.AsType[*apiError](err)
	return ok && (ae.StatusCode == http.StatusNotFound || ae.StatusCode == http.StatusGone)
}

// endpoint returns the URL of the server's API path, with query q.
func (c *apiClient) endpoint(path string, q url.Values) string {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = q.Encode()
	return u.String()
}

// get calls the API at rawURL, of the server, decoding its JSON into v.
// It returns the URL of the response's next page, if any.
func (c *apiClient) get(ctx context.Context, rawURL string, v any) (next string, err error) {
	res, err := c.do(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		return "", fmt.Errorf("mastodon: decoding %s: %w", rawURL, err)
	}
	if next = nextLink(res.Header); next != "" {
		u, err := url.Parse(next)
		if err != nil || u.Scheme != c.base.Scheme || u.Host != c.base.Host {
			// It'd be sent the account's token.
			return "", fmt.Errorf("mastodon: %s's next page is elsewhere, %q", rawURL, next)
		}
	}
	return next, nil
}

// post calls the API at path with form, decoding its JSON into v.
func (c *apiClient) post(ctx context.Context, path string, form url.Values, v any) error {
	res, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint(path, nil), strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return req, err
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		return fmt.Errorf("mastodon: decoding %s: %w", path, err)
	}
	return nil
}

// do makes the request newReq makes, as the account, within the rate
// limit, until it's not refused for it, and returns its response if it's
// of HTTP status 200.
func (c *apiClient) do(ctx context.Context, newReq func() (*http.Request, error)) (*http.Response, error) {
	for try := 0; ; try++ {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		res, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		limited := res.StatusCode == http.StatusTooManyRequests
		c.update(res.Header, limited)
		if res.StatusCode == http.StatusOK {
			return res, nil
		}
		ae := &apiError{URL: req.URL.Redacted(), StatusCode: res.StatusCode}
		var body struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body) == nil {
			ae.Message = body.Error
		}
		res.Body.Close()
		if !limited || try == maxRetries {
			return nil, ae
		}
	}
}

// wait waits until a call is within the rate limit, and counts it.
func (c *apiClient) wait(ctx context.Context) error {
	for {
		c.mu.Lock()
		now := time.Now()
		reserve := max(c.limit/rateReserveDiv, minRateReserve)
		if c.limit == 0 || c.remaining > reserve || !now.Before(c.reset) {
			c.remaining--
			c.mu.Unlock()
			return nil
		}
		d := c.reset.Sub(now)
		left, limit := c.remaining, c.limit
		c.mu.Unlock()

		if c.logf != nil {
			c.logf("waiting %v for the API's rate limit: %d of %d calls left, kept for other apps", d.Round(time.Second), max(left, 0), limit)
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// update updates the client's rate limit from the headers h of a
// response, which was a refusal for the limit if limited.
func (c *apiClient) update(h http.Header, limited bool) {
	limit, _ := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, err := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, rerr := time.Parse(time.RFC3339, h.Get("X-RateLimit-Reset"))
	c.mu.Lock()
	defer c.mu.Unlock()
	if limited {
		if rerr != nil || !reset.After(time.Now()) {
			reset = time.Now().Add(defaultRateWait)
		}
		c.limit = max(c.limit, limit, 1)
		c.remaining = 0
		c.reset = reset
		return
	}
	if limit <= 0 || err != nil || rerr != nil {
		return
	}
	c.limit = limit
	if reset.After(c.reset) {
		// A new window.
		c.reset = reset
		c.remaining = remaining
	} else {
		// Of the window, responses may come out of order.
		c.remaining = min(c.remaining, remaining)
	}
}

// nextLink returns the URL of the next page that the Link header of h
// gives, if any, as in:
//
//	Link: <https://example.com/api/v1/...?max_id=7>; rel="next", <...>; rel="prev"
func nextLink(h http.Header) string {
	for _, v := range h.Values("Link") {
		for link := range strings.SplitSeq(v, ",") {
			target, params, ok := strings.Cut(link, ";")
			if !ok {
				continue
			}
			target = strings.TrimSpace(target)
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			for p := range strings.SplitSeq(params, ";") {
				k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
				isNext := func(rel string) bool { return strings.EqualFold(rel, "next") }
				if strings.EqualFold(k, "rel") && slices.ContainsFunc(strings.Fields(strings.Trim(v, `"`)), isNext) {
					return target[1 : len(target)-1]
				}
			}
		}
	}
	return ""
}

// app is a registered OAuth application.
type app struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// registerApp registers an app of the scopes, which redirects to
// redirectURI.
func (c *apiClient) registerApp(ctx context.Context, scopes, redirectURI string) (*app, error) {
	a := new(app)
	err := c.post(ctx, "/api/v1/apps", url.Values{
		"client_name":   {clientName},
		"redirect_uris": {redirectURI},
		"scopes":        {scopes},
		"website":       {"https://perkeep.org/"},
	}, a)
	if err == nil && (a.ClientID == "" || a.ClientSecret == "") {
		err = errors.New("mastodon: registered app has no client credentials")
	}
	return a, err
}

// account is an account, of the API's Account entity.
type account struct {
	ID          string         `json:"id"` // of the server
	URI         string         `json:"uri"`
	Username    string         `json:"username"`
	Acct        string         `json:"acct"` // the username, and @domain if remote
	DisplayName string         `json:"display_name"`
	URL         string         `json:"url"`  // of its profile page
	Note        string         `json:"note"` // its bio, as HTML
	Avatar      string         `json:"avatar"`
	Header      string         `json:"header"`
	Source      *accountSource `json:"source"` // only of the client's own account
}

// accountSource is the account's profile as its owner wrote it, of the
// API's Account entity's source.
type accountSource struct {
	Note   string  `json:"note"` // plain text
	Fields []field `json:"fields"`
}

// field is a name and value of an account's profile.
type field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// actor returns the account's ActivityPub ID, or, from servers before
// Mastodon 4.2, its profile's URL.
func (a *account) actor() string {
	if a.URI != "" {
		return a.URI
	}
	return a.URL
}

// verifyCredentials returns the client's account.
func (c *apiClient) verifyCredentials(ctx context.Context) (*account, error) {
	a := new(account)
	_, err := c.get(ctx, c.endpoint("/api/v1/accounts/verify_credentials", nil), a)
	if err == nil && a.ID == "" {
		err = errors.New("mastodon: verify_credentials returned no account ID")
	}
	return a, err
}

// status is a post, of the API's Status entity.
type status struct {
	ID                 string       `json:"id"` // of the server
	URI                string       `json:"uri"`
	URL                string       `json:"url"`
	CreatedAt          time.Time    `json:"created_at"`
	EditedAt           *time.Time   `json:"edited_at"`
	InReplyToID        string       `json:"in_reply_to_id"`
	InReplyToAccountID string       `json:"in_reply_to_account_id"`
	Sensitive          bool         `json:"sensitive"`
	SpoilerText        string       `json:"spoiler_text"`
	Visibility         string       `json:"visibility"` // "public", "unlisted", "private" or "direct"
	Language           string       `json:"language"`
	Content            string       `json:"content"` // HTML
	Reblog             *status      `json:"reblog"`
	MediaAttachments   []attachment `json:"media_attachments"`
	Card               *card        `json:"card"`
	Account            account      `json:"account"` // its author
}

// attachment is a status's medium, of the API's MediaAttachment entity.
type attachment struct {
	ID          string `json:"id"`
	Type        string `json:"type"` // "image", "gifv", "video", "audio" or "unknown"
	URL         string `json:"url"`
	PreviewURL  string `json:"preview_url"`
	RemoteURL   string `json:"remote_url"`
	Description string `json:"description"` // alt text
}

// card is the preview of a status's link, of the API's PreviewCard entity.
type card struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// statusesURL returns the URL of the first page of the statuses of the
// account of ID id, newest first, its reblogs included.
func (c *apiClient) statusesURL(id string) string {
	return c.endpoint("/api/v1/accounts/"+url.PathEscape(id)+"/statuses", url.Values{
		"limit": {strconv.Itoa(statusesPageSize)},
	})
}

// favouritesURL returns the URL of the first page of the statuses that
// the client's account favourited, most recently favourited first.
func (c *apiClient) favouritesURL() string {
	return c.endpoint("/api/v1/favourites", url.Values{"limit": {strconv.Itoa(statusesPageSize)}})
}

// bookmarksURL returns the URL of the first page of the statuses that
// the client's account bookmarked, most recently bookmarked first.
func (c *apiClient) bookmarksURL() string {
	return c.endpoint("/api/v1/bookmarks", url.Values{"limit": {strconv.Itoa(statusesPageSize)}})
}

// statuses returns the page of statuses at pageURL, of statusesURL,
// favouritesURL or bookmarksURL or a page after, and the URL of the next
// page, if any.
func (c *apiClient) statuses(ctx context.Context, pageURL string) (sts []*status, next string, err error) {
	next, err = c.get(ctx, pageURL, &sts)
	return sts, next, err
}

// statusesByIDMax is the most statuses statusesByID gets at once.
const statusesByIDMax = 20

// statusesByID returns the statuses of ids that exist and the client's
// account may see, by ID. It needs Mastodon 4.3 or later.
func (c *apiClient) statusesByID(ctx context.Context, ids []string) (map[string]*status, error) {
	m := make(map[string]*status)
	for chunk := range slices.Chunk(ids, statusesByIDMax) {
		var sts []*status
		if _, err := c.get(ctx, c.endpoint("/api/v1/statuses", url.Values{"id[]": chunk}), &sts); err != nil {
			return nil, err
		}
		for _, st := range sts {
			m[st.ID] = st
		}
	}
	return m, nil
}

// accountsPageSize is the most accounts the API returns at once.
const accountsPageSize = 80

// followingURL returns the URL of the first page of the accounts that
// the account of ID id follows, most recently followed first.
func (c *apiClient) followingURL(id string) string {
	return c.endpoint("/api/v1/accounts/"+url.PathEscape(id)+"/following", url.Values{
		"limit": {strconv.Itoa(accountsPageSize)},
	})
}

// accounts returns the page of accounts at pageURL, of followingURL or a
// page after, and the URL of the next page, if any.
func (c *apiClient) accounts(ctx context.Context, pageURL string) (as []*account, next string, err error) {
	next, err = c.get(ctx, pageURL, &as)
	return as, next, err
}

// statusSource is the source of a status as its author wrote it, of the
// API's StatusSource entity.
type statusSource struct {
	ID          string `json:"id"`
	Text        string `json:"text"` // plain text
	SpoilerText string `json:"spoiler_text"`
}

// statusSource returns the source of the account's status of ID id.
func (c *apiClient) statusSource(ctx context.Context, id string) (*statusSource, error) {
	src := new(statusSource)
	_, err := c.get(ctx, c.endpoint("/api/v1/statuses/"+url.PathEscape(id)+"/source", nil), src)
	return src, err
}
