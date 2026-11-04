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

// Package dropbox implements a [filesource.Source] of a Dropbox, with the
// Dropbox API v2's HTTP interface.
package dropbox // import "perkeep.org/pkg/filesource/dropbox"

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"perkeep.org/pkg/filesource"
)

// Endpoint is Dropbox's OAuth 2 endpoint. Its token URL takes the client
// ID in the request's parameters, as Dropbox's PKCE flow, without a client
// secret, needs.
var Endpoint = oauth2.Endpoint{
	AuthURL:   "https://www.dropbox.com/oauth2/authorize",
	TokenURL:  "https://api.dropboxapi.com/oauth2/token",
	AuthStyle: oauth2.AuthStyleInParams,
}

// Scopes are the scopes of Dropbox's permissions that a Source needs:
// listing files and reading them. A scoped app must have them, set in its
// settings; apps from before Dropbox's scopes have all of them.
var Scopes = []string{"files.metadata.read", "files.content.read"}

// rootID is the ID of a Source's root File. Dropbox's own name for the
// root is the empty path, which filesource uses for no file.
const rootID = "/"

// Source is a user's Dropbox. A File's ID is its Dropbox ID, such as
// "id:a4ayc_80_OEAAAAAAAAAYa", except the root's, "/", and its Version
// is its revision, which Source reads, so a File's contents don't
// change.
type Source struct {
	// Client makes the requests. It must add OAuth credentials with
	// Scopes.
	Client *http.Client

	// Limiter, if non-nil, limits the rate of requests.
	Limiter *rate.Limiter

	// APIBase and ContentBase are the URLs of the Dropbox API's hosts,
	// if not the usual ones, for tests.
	APIBase, ContentBase string
}

var _ filesource.Source = (*Source)(nil)

// maxTries is how many times a request is tried, when Dropbox says to
// try it again later.
const maxTries = 10

func (s *Source) apiBase() string {
	if s.APIBase != "" {
		return s.APIBase
	}
	return "https://api.dropboxapi.com"
}

func (s *Source) contentBase() string {
	if s.ContentBase != "" {
		return s.ContentBase
	}
	return "https://content.dropboxapi.com"
}

// Root returns the Dropbox's root folder.
func (s *Source) Root(context.Context) (filesource.File, error) {
	return filesource.File{ID: rootID, Name: "Dropbox", Dir: true}, nil
}

// FolderPath returns the path of the Dropbox folder s, which is the
// folder's URL, as the Dropbox web app shows it, such as
// "https://www.dropbox.com/home/Apps/Google%20Download%20Your%20Data",
// or its path, such as "/Apps/Google Download Your Data". Stat takes
// paths, as well as IDs.
func FolderPath(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		u, err := url.Parse(s)
		if err != nil || (u.Host != "www.dropbox.com" && u.Host != "dropbox.com") {
			return "", fmt.Errorf("%q is not a Dropbox URL", s)
		}
		rest, ok := strings.CutPrefix(u.Path, "/home")
		if !ok || (rest != "" && rest[0] != '/') {
			return "", fmt.Errorf("%q is not the URL of a Dropbox folder", s)
		}
		s = rest
	}
	s = "/" + strings.Trim(path.Clean("/"+s), "/")
	if s == "/" {
		return rootID, nil
	}
	return s, nil
}

// metadata is a file's or folder's metadata, as the API has it.
type metadata struct {
	Tag            string    `json:".tag"` // file, folder, or deleted
	Name           string    `json:"name"`
	ID             string    `json:"id"`
	Rev            string    `json:"rev"`
	Size           int64     `json:"size"`
	ServerModified time.Time `json:"server_modified"`
}

// file returns the File of m, and whether it's of a file or folder.
func (m *metadata) file() (filesource.File, bool) {
	switch m.Tag {
	case "file":
		return filesource.File{ID: m.ID, Name: m.Name, Size: m.Size, ModTime: m.ServerModified, Version: m.Rev}, true
	case "folder":
		return filesource.File{ID: m.ID, Name: m.Name, Dir: true}, true
	}
	return filesource.File{}, false
}

// apiPath returns the API's path of the file or folder id.
func apiPath(id string) string {
	if id == rootID {
		return ""
	}
	return id
}

// List lists the folder dir's files and folders.
func (s *Source) List(ctx context.Context, dir filesource.File) ([]filesource.File, error) {
	if !dir.Dir {
		return nil, fmt.Errorf("dropbox: %s is not a folder", dir.Name)
	}
	var page struct {
		Entries []metadata `json:"entries"`
		Cursor  string     `json:"cursor"`
		HasMore bool       `json:"has_more"`
	}
	err := s.rpc(ctx, "files/list_folder", map[string]any{"path": apiPath(dir.ID), "limit": 2000}, &page)
	var files []filesource.File
	for err == nil {
		for _, m := range page.Entries {
			if f, ok := m.file(); ok {
				files = append(files, f)
			}
		}
		if !page.HasMore {
			return files, nil
		}
		cursor := page.Cursor
		page.Entries = nil
		err = s.rpc(ctx, "files/list_folder/continue", map[string]string{"cursor": cursor}, &page)
	}
	return nil, err
}

// Stat returns the file or folder id.
func (s *Source) Stat(ctx context.Context, id string) (filesource.File, error) {
	if id == rootID {
		return s.Root(ctx)
	}
	var m metadata
	if err := s.rpc(ctx, "files/get_metadata", map[string]string{"path": id}, &m); err != nil {
		return filesource.File{}, err
	}
	f, ok := m.file()
	if !ok {
		return filesource.File{}, fmt.Errorf("dropbox: %s is deleted", id)
	}
	return f, nil
}

// OpenRange opens n bytes of f's contents at off, of the revision f was
// listed with. If that revision's gone, it returns an error matching
// [filesource.ErrChanged].
func (s *Source) OpenRange(ctx context.Context, f filesource.File, off, n int64) (io.ReadCloser, error) {
	if err := filesource.CheckRange(f, off, n); err != nil {
		return nil, err
	}
	if n == 0 {
		return io.NopCloser(strings.NewReader("")), nil
	}
	arg, err := json.Marshal(map[string]string{"path": "rev:" + f.Version})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.contentBase()+"/2/files/download", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Dropbox-API-Arg", string(arg))
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	res, err := s.do(req, nil)
	if err != nil {
		return nil, err
	}
	switch res.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		// The whole file, despite the Range header: skip to off.
		if _, err := io.CopyN(io.Discard, res.Body, off); err != nil {
			res.Body.Close()
			return nil, err
		}
	default:
		err := apiError(res)
		res.Body.Close()
		if strings.Contains(err.Error(), "not_found") {
			return nil, fmt.Errorf("%s: %w", f.Name, filesource.ErrChanged)
		}
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(res.Body, n), res.Body}, nil
}

// rpc calls the API's endpoint with the JSON of arg, decoding its result
// into res.
func (s *Source) rpc(ctx context.Context, endpoint string, arg, result any) error {
	body, err := json.Marshal(arg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.apiBase()+"/2/"+endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.do(req, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("dropbox: %s: %w", endpoint, apiError(res))
	}
	return json.NewDecoder(res.Body).Decode(result)
}

// apiError returns the error of res, an unsuccessful response, with the
// API's summary of it, if it has one.
func apiError(res *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	var e struct {
		Summary string `json:"error_summary"`
	}
	if json.Unmarshal(data, &e) == nil && e.Summary != "" {
		return fmt.Errorf("%s (%s)", e.Summary, res.Status)
	}
	return fmt.Errorf("%s: %s", res.Status, bytes.TrimSpace(data))
}

// do does req, with body, if non-nil, retrying it after waiting as long
// as Dropbox says to when it's rate limited, or briefly when Dropbox
// fails, with exponential backoff.
func (s *Source) do(req *http.Request, body []byte) (*http.Response, error) {
	delay := time.Second
	for try := 1; ; try++ {
		if s.Limiter != nil {
			if err := s.Limiter.Wait(req.Context()); err != nil {
				return nil, err
			}
		}
		r := req.Clone(req.Context())
		if body != nil {
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
		res, err := s.Client.Do(r)
		if err != nil || try == maxTries || !(res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500) {
			return res, err
		}
		wait := delay + time.Duration(rand.Int64N(int64(delay/2)))
		if secs, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && secs > 0 {
			wait = time.Duration(secs) * time.Second
		}
		io.CopyN(io.Discard, res.Body, 4<<10)
		res.Body.Close()
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
