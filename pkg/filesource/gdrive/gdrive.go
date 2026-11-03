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

// Package gdrive implements a [filesource.Source] of Google Drive, with
// the Drive API's REST interface.
package gdrive // import "perkeep.org/pkg/filesource/gdrive"

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"perkeep.org/pkg/filesource"
)

// Scope is the OAuth scope that a Source needs: reading Google Drive.
const Scope = "https://www.googleapis.com/auth/drive.readonly"

// folderType is the MIME type of Drive folders.
const folderType = "application/vnd.google-apps.folder"

// Source is a user's Google Drive. A File's ID is its Drive file ID, and
// its Version is the ID of its head revision, which Source reads, so a
// File's contents don't change. Google Docs and other files without
// contents of their own aren't listed.
type Source struct {
	// Client makes the requests. It must add OAuth credentials with
	// Scope.
	Client *http.Client

	// Limiter, if non-nil, limits the rate of requests to Drive. Drive
	// rejects requests beyond a quota per minute, which it doesn't
	// report, so the requests are retried, but limiting them avoids
	// that, and leaves room for others using the quota.
	Limiter *rate.Limiter

	// APIBase, if non-empty, is the Drive API's URL, instead of the
	// usual one, as for tests.
	APIBase string
}

var _ filesource.Finder = (*Source)(nil)

func (s *Source) base() string {
	if s.APIBase != "" {
		return s.APIBase
	}
	return "https://www.googleapis.com"
}

// Root returns the root of the user's Drive, "My Drive".
func (s *Source) Root(context.Context) (filesource.File, error) {
	return filesource.File{ID: "root", Name: "My Drive", Dir: true}, nil
}

// List returns the files and folders in the folder dir.
func (s *Source) List(ctx context.Context, dir filesource.File) ([]filesource.File, error) {
	q, err := inFolder(dir)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, q)
}

// Find returns the files and folders named name in the folder dir.
func (s *Source) Find(ctx context.Context, dir filesource.File, name string) ([]filesource.File, error) {
	q, err := inFolder(dir)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, "name = "+quote(name)+" and "+q)
}

func inFolder(dir filesource.File) (string, error) {
	if !dir.Dir {
		return "", fmt.Errorf("%s is not a folder", dir.Name)
	}
	return quote(dir.ID) + " in parents and trashed = false", nil
}

// quote quotes s as a string in a Drive query.
func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// fileFields are the fields of the files that a Source requests.
const fileFields = "id,name,mimeType,size,modifiedTime,headRevisionId"

// driveFile is a file in a Drive API response.
type driveFile struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	MimeType       string    `json:"mimeType"`
	Size           int64     `json:"size,string"`
	ModifiedTime   time.Time `json:"modifiedTime"`
	HeadRevisionID string    `json:"headRevisionId"`
}

// file returns df as a File, and whether it's a folder or a file with
// contents of its own.
func (df *driveFile) file() (filesource.File, bool) {
	f := filesource.File{ID: df.ID, Name: df.Name, ModTime: df.ModifiedTime}
	if df.MimeType == folderType {
		f.Dir = true
		return f, true
	}
	if df.HeadRevisionID == "" {
		return f, false // a Google Doc or such
	}
	f.Size, f.Version = df.Size, df.HeadRevisionID
	return f, true
}

// list returns the files matching the Drive query q.
func (s *Source) list(ctx context.Context, q string) ([]filesource.File, error) {
	var files []filesource.File
	pageToken := ""
	for {
		v := url.Values{
			"q":        {q},
			"fields":   {"nextPageToken,files(" + fileFields + ")"},
			"pageSize": {"1000"},
		}
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		var page struct {
			NextPageToken string      `json:"nextPageToken"`
			Files         []driveFile `json:"files"`
		}
		if err := s.get(ctx, "/drive/v3/files?"+v.Encode(), &page); err != nil {
			return nil, err
		}
		for _, df := range page.Files {
			if f, ok := df.file(); ok {
				files = append(files, f)
			}
		}
		if page.NextPageToken == "" {
			return files, nil
		}
		pageToken = page.NextPageToken
	}
}

// Stat returns the file or folder with ID id.
func (s *Source) Stat(ctx context.Context, id string) (filesource.File, error) {
	var df driveFile
	if err := s.get(ctx, "/drive/v3/files/"+url.PathEscape(id)+"?fields="+fileFields, &df); err != nil {
		return filesource.File{}, err
	}
	f, ok := df.file()
	if !ok {
		return filesource.File{}, fmt.Errorf("Drive file %s (%s) has no contents of its own", id, df.Name)
	}
	return f, nil
}

// get gets the Drive API's JSON response at path into v.
func (s *Source) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.base()+path, nil)
	if err != nil {
		return err
	}
	res, err := s.do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
		return fmt.Errorf("Drive: %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// OpenRange reads f's head revision as of when it was listed, which
// stays the same, until Drive deletes it, as after the file changes,
// when OpenRange returns an error matching ErrChanged.
func (s *Source) OpenRange(ctx context.Context, f filesource.File, off, n int64) (io.ReadCloser, error) {
	if err := filesource.CheckRange(f, off, n); err != nil {
		return nil, err
	}
	if n == 0 {
		return io.NopCloser(strings.NewReader("")), nil
	}
	u := fmt.Sprintf("%s/drive/v3/files/%s/revisions/%s?alt=media", s.base(), url.PathEscape(f.ID), url.PathEscape(f.Version))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	res, err := s.do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case res.StatusCode == http.StatusPartialContent:
		return res.Body, nil
	case res.StatusCode == http.StatusOK && off == 0 && n == f.Size:
		return res.Body, nil
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
	res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("revision %s of Drive file %s: %w", f.Version, f.ID, filesource.ErrChanged)
	}
	return nil, fmt.Errorf("reading %d bytes at %d of Drive file %s: %s: %s", n, off, f.ID, res.Status, strings.TrimSpace(string(body)))
}

// maxTries is how many times do tries a request that Drive limits or
// fails with a server error.
const maxTries = 10

// do does req, retrying with exponential backoff while Drive rejects it
// for exceeding a rate limit or with a server error.
func (s *Source) do(req *http.Request) (*http.Response, error) {
	delay := time.Second
	for try := 1; ; try++ {
		if s.Limiter != nil {
			if err := s.Limiter.Wait(req.Context()); err != nil {
				return nil, err
			}
		}
		res, err := s.Client.Do(req.Clone(req.Context()))
		if err != nil || try == maxTries || !retryable(res) {
			return res, err
		}
		// Drain a little of the body so the connection can be reused.
		io.CopyN(io.Discard, res.Body, 4<<10)
		res.Body.Close()
		// Up to half again, so concurrent requests spread out.
		wait := delay + time.Duration(rand.Int64N(int64(delay/2)))
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-req.Context().Done():
			t.Stop()
			return nil, req.Context().Err()
		}
		delay = min(2*delay, time.Minute)
	}
}

// retryable reports whether res is Drive rejecting a request for
// exceeding a rate limit, or a server error, for which the request should
// be retried. It reads the start of res.Body, but leaves all of it to be
// read.
func retryable(res *http.Response) bool {
	switch {
	case res.StatusCode == http.StatusTooManyRequests, res.StatusCode >= 500:
		return true
	case res.StatusCode != http.StatusForbidden:
		return false
	}
	// Drive also says 403 for rate limits, rather than 429.
	start, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	res.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(start), res.Body), res.Body}
	body := string(start)
	return strings.Contains(body, "rateLimitExceeded") || strings.Contains(body, "RATE_LIMIT_EXCEEDED")
}
