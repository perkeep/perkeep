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
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/time/rate"
	"perkeep.org/pkg/filesource"
	"perkeep.org/pkg/filesource/gdrive"
)

// DriveScope is the OAuth scope that Drive needs: reading Google Drive.
const DriveScope = gdrive.Scope

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

	// Limiter, if non-nil, limits the rate of requests to Drive. (See
	// gdrive.Source.Limiter.)
	Limiter *rate.Limiter

	// apiBase is the Drive API's URL, if not the usual one, for tests.
	apiBase string
}

// Exports returns the Takeout exports in the Drive folder folderID, or if
// it's empty, in all the folders named "Takeout" in the root folder,
// where Google Takeout saves them. folderID may also be the folder's URL.
func (d *Drive) Exports(ctx context.Context, folderID string) ([]*Export, error) {
	src := &gdrive.Source{Client: d.Client, Limiter: d.Limiter, APIBase: d.apiBase}
	if folderID != "" {
		var err error
		if folderID, err = gdrive.FolderID(folderID); err != nil {
			return nil, err
		}
	}
	dirs := []filesource.File{{ID: folderID, Name: folderID, Dir: true}}
	if folderID == "" {
		root, err := src.Root(ctx)
		if err != nil {
			return nil, err
		}
		dirs, err = DefaultDirs(ctx, src)
		if err != nil {
			return nil, fmt.Errorf("finding Takeout folders: %w", err)
		}
		if len(dirs) == 1 && dirs[0].ID == root.ID {
			return nil, errors.New("no folder named Takeout in the root of Google Drive")
		}
	}
	var cache Cache
	if d.CacheDir != "" {
		cache = DirCache(d.CacheDir)
	}
	// An export's zip files may be in more than one of the folders.
	var files []filesource.File
	for _, dir := range dirs {
		fs, err := src.List(ctx, dir)
		if err != nil {
			return nil, fmt.Errorf("listing Drive folder %s: %w", dir.ID, err)
		}
		files = append(files, fs...)
	}
	return ExportsFromFiles(src, files, cache), nil
}
