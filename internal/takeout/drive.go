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

package takeout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DriveScope is the OAuth scope that Drive needs: reading Google Drive.
const DriveScope = "https://www.googleapis.com/auth/drive.readonly"

// Drive reads Takeout exports that Google Takeout saved to Google Drive,
// with the Drive API, reading each zip file's table of contents with one
// or two requests and each of its files with one.
type Drive struct {
	// Client makes the requests. It must add OAuth credentials with
	// DriveScope.
	Client *http.Client

	// CacheDir, if non-empty, is a directory to cache zip files'
	// tables of contents in, by the ID of the revision read.
	CacheDir string

	// apiBase is the Drive API's URL, if not the usual one, for tests.
	apiBase string
}

func (d *Drive) base() string {
	if d.apiBase != "" {
		return d.apiBase
	}
	return "https://www.googleapis.com"
}

// driveFile is a file in a Drive API files.list response.
type driveFile struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Size           int64  `json:"size,string"`
	HeadRevisionID string `json:"headRevisionId"`
	CreatedTime    string `json:"createdTime"`
}

// Exports returns the Takeout exports in the Drive folder folderID, or if
// it's empty, in all the folders named "Takeout" in the root folder,
// where Google Takeout saves them. folderID may also be the folder's URL.
func (d *Drive) Exports(ctx context.Context, folderID string) ([]*Export, error) {
	if i := strings.LastIndexByte(folderID, '/'); i >= 0 {
		folderID = folderID[i+1:]
	}
	folderID, _, _ = strings.Cut(folderID, "?")
	folders := []string{folderID}
	if folderID == "" {
		fs, err := d.list(ctx, "name = 'Takeout' and mimeType = 'application/vnd.google-apps.folder' and 'root' in parents and trashed = false")
		if err != nil {
			return nil, fmt.Errorf("finding Takeout folders: %w", err)
		}
		if len(fs) == 0 {
			return nil, errors.New("no folder named Takeout in the root of Google Drive")
		}
		folders = nil
		for _, f := range fs {
			folders = append(folders, f.ID)
		}
	}
	var zips []*Zip
	for _, id := range folders {
		if strings.ContainsAny(id, `'\`) {
			return nil, fmt.Errorf("invalid Drive folder ID %q", id)
		}
		fs, err := d.list(ctx, fmt.Sprintf("'%s' in parents and trashed = false and name contains '.zip'", id))
		if err != nil {
			return nil, fmt.Errorf("listing Drive folder %s: %w", id, err)
		}
		for _, f := range fs {
			if f.Size <= 0 || f.HeadRevisionID == "" {
				continue
			}
			zips = append(zips, &Zip{
				Name: f.Name,
				Size: f.Size,
				src:  &driveZip{d: d, id: f.ID, rev: f.HeadRevisionID, size: f.Size},
			})
		}
	}
	return groupExports(zips), nil
}

// list returns the files matching the Drive query q.
func (d *Drive) list(ctx context.Context, q string) ([]driveFile, error) {
	var files []driveFile
	pageToken := ""
	for {
		v := url.Values{
			"q":        {q},
			"fields":   {"nextPageToken,files(id,name,size,headRevisionId,createdTime)"},
			"pageSize": {"1000"},
		}
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, "GET", d.base()+"/drive/v3/files?"+v.Encode(), nil)
		if err != nil {
			return nil, err
		}
		res, err := d.Client.Do(req)
		if err != nil {
			return nil, err
		}
		var page struct {
			NextPageToken string      `json:"nextPageToken"`
			Files         []driveFile `json:"files"`
		}
		err = decodeResponse(res, &page)
		if err != nil {
			return nil, err
		}
		files = append(files, page.Files...)
		if page.NextPageToken == "" {
			return files, nil
		}
		pageToken = page.NextPageToken
	}
}

func decodeResponse(res *http.Response, v any) error {
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
		return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// driveZip is a revision of a zip file in Google Drive.
type driveZip struct {
	d    *Drive
	id   string
	rev  string
	size int64
}

func (z *driveZip) readTOC(ctx context.Context) ([]byte, error) {
	var cacheFile string
	if z.d.CacheDir != "" {
		cacheFile = filepath.Join(z.d.CacheDir, url.PathEscape(z.rev)+".toc")
		if toc, err := os.ReadFile(cacheFile); err == nil {
			return toc, nil
		}
	}
	toc, err := readTOC(ctx, z.size, func(ctx context.Context, off, n int64) ([]byte, error) {
		rc, err := z.openRange(ctx, off, n)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		buf := make([]byte, n)
		_, err = io.ReadFull(rc, buf)
		return buf, err
	})
	if err != nil {
		return nil, err
	}
	if cacheFile != "" {
		// The cache is only an optimization.
		writeFileAtomic(cacheFile, toc)
	}
	return toc, nil
}

func writeFileAtomic(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".tmp*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), name)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

func (z *driveZip) openRange(ctx context.Context, off, n int64) (io.ReadCloser, error) {
	if off < 0 || n <= 0 || off+n > z.size {
		return nil, fmt.Errorf("invalid range of %d bytes at %d of a %d byte file", n, off, z.size)
	}
	u := fmt.Sprintf("%s/drive/v3/files/%s/revisions/%s?alt=media", z.d.base(), url.PathEscape(z.id), url.PathEscape(z.rev))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	res, err := z.d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case res.StatusCode == http.StatusPartialContent:
		return res.Body, nil
	case res.StatusCode == http.StatusOK && off == 0 && n == z.size:
		return res.Body, nil
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
	res.Body.Close()
	return nil, fmt.Errorf("reading %d bytes at %d of Drive file %s: %s: %s", n, off, z.id, res.Status, strings.TrimSpace(string(body)))
}
