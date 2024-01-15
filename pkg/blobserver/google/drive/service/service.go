/*
Copyright 2013 The Perkeep Authors

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

// Package service translates blobserver.Storage methods
// into Google Drive API methods.
package service // import "perkeep.org/pkg/blobserver/google/drive/service"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"

	client "google.golang.org/api/drive/v2"
	"google.golang.org/api/option"
)

const (
	MimeTypeDriveFolder = "application/vnd.google-apps.folder"
	MimeTypeCamliBlob   = "application/vnd.camlistore.blob"
)

// DriveService wraps Google Drive API to implement utility methods to
// be performed on the root Drive destination folder.
type DriveService struct {
	client     *http.Client
	apiservice *client.Service
	parentID   string
}

// New initiates a new DriveService. parentID is the ID of the directory
// that will be used as the current directory in methods on the returned
// DriveService (such as Get). If empty, it defaults to the root of the
// drive.
func New(oauthClient *http.Client, parentID string) (*DriveService, error) {
	apiservice, err := client.NewService(context.TODO(), option.WithHTTPClient(oauthClient))
	if err != nil {
		return nil, err
	}
	if parentID == "" {
		// because "root" is known as a special alias for the root directory in drive.
		parentID = "root"
	}
	service := &DriveService{client: oauthClient, apiservice: apiservice, parentID: parentID}
	return service, err
}

// Get retrieves a file with its title equal to the provided title and a child of
// the parentID as given to New. If not found, os.ErrNotExist is returned.
func (s *DriveService) Get(ctx context.Context, title string) (*client.File, error) {
	// TODO: use field selectors
	query := fmt.Sprintf("'%s' in parents and title = '%s'", s.parentID, title)
	req := s.apiservice.Files.List().Context(ctx).Q(query)
	files, err := req.Do()
	if err != nil {
		return nil, err
	}
	if len(files.Items) < 1 {
		return nil, os.ErrNotExist
	}
	return files.Items[0], nil
}

// List returns a list of files. When limit is greater than zero a paginated list is returned
// using the next response as a pageToken in subsequent calls.
func (s *DriveService) List(pageToken string, limit int) (files []*client.File, next string, err error) {
	req := s.apiservice.Files.List()
	req.Q(fmt.Sprintf("'%s' in parents and mimeType != '%s'", s.parentID, MimeTypeDriveFolder))

	if pageToken != "" {
		req.PageToken(pageToken)
	}

	if limit > 0 {
		req.MaxResults(int64(limit))
	}

	result, err := req.Do()
	if err != nil {
		return
	}
	return result.Items, result.NextPageToken, err
}

// Upsert inserts a file, or updates if such a file exists.
func (s *DriveService) Upsert(ctx context.Context, title string, data io.Reader) (file *client.File, err error) {
	if file, err = s.Get(ctx, title); err != nil {
		if !os.IsNotExist(err) {
			return
		}
	}
	if file == nil {
		file = &client.File{Title: title}
		file.Parents = []*client.ParentReference{
			{Id: s.parentID},
		}
		file.MimeType = MimeTypeCamliBlob
		return s.apiservice.Files.Insert(file).Media(data).Context(ctx).Do()
	}

	// TODO: handle large blobs
	return s.apiservice.Files.Update(file.Id, file).Media(data).Context(ctx).Do()
}

var errNoDownload = errors.New("file can not be downloaded directly (conversion needed?)")

// Fetch retrieves the metadata and contents of a file.
func (s *DriveService) Fetch(ctx context.Context, title string) (body io.ReadCloser, size uint32, err error) {
	file, err := s.Get(ctx, title)
	if err != nil {
		return
	}
	// TODO: maybe in the case of no download link, remove the file.
	// The file should have malformed or converted to a Docs file
	// unwantedly.
	// TODO(mpl): I do not think the above comment is accurate. It
	// looks like at least one case we do not get a DownloadUrl is when
	// the UI would make you pick a conversion format first (spreadsheet,
	// doc, etc). -> we should see if the API offers the possibility to do
	// that conversion. and we could pass the type(s) we want (pdf, xls, doc...)
	// as arguments (in an options struct) to Fetch.
	if file.DownloadUrl == "" {
		err = errNoDownload
		return
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", file.DownloadUrl, nil)
	var resp *http.Response
	if resp, err = s.client.Transport.RoundTrip(req); err != nil {
		return
	}
	if file.FileSize > math.MaxUint32 || file.FileSize < 0 {
		err = errors.New("file too big")
	}
	return resp.Body, uint32(file.FileSize), err
}

// Stat retrieves file metadata and returns
// file size. Returns error if file is not found.
func (s *DriveService) Stat(ctx context.Context, title string) (int64, error) {
	file, err := s.Get(ctx, title)
	if err != nil || file == nil {
		return 0, err
	}
	return file.FileSize, err
}

// Trash trashes the file with the given title.
func (s *DriveService) Trash(ctx context.Context, title string) error {
	file, err := s.Get(ctx, title)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, err = s.apiservice.Files.Trash(file.Id).Context(ctx).Do()
	return err
}
