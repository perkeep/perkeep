/*
Copyright 2013 Google Inc.

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

// DriveService translates blobserver.Storage methods
// into Google Drive API methods.
package service

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"

	client "google.golang.org/api/drive/v2"
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
	parentId   string
}

// New initiates a new DriveService. parentId is the ID of the directory
// that will be used as the current directory in methods on the returned
// DriveService (such as Get). If empty, it defaults to the root of the
// drive.
func New(oauthClient *http.Client, parentId string) (*DriveService, error) {
	apiservice, err := client.New(oauthClient)
	if err != nil {
		return nil, err
	}
	if parentId == "" {
		// because "root" is known as a special alias for the root directory in drive.
		parentId = "root"
	}
	service := &DriveService{client: oauthClient, apiservice: apiservice, parentId: parentId}
	return service, err
}

// Get retrieves a file with its title equal to the provided title and a child of
// the parentId as given to New. If not found, os.ErrNotExist is returned.
func (s *DriveService) Get(title string) (*client.File, error) {
	req := s.apiservice.Files.List()
	// TODO: use field selectors
	query := fmt.Sprintf("'%s' in parents and title = '%s'", s.parentId, title)
	req.Q(query)
	files, err := req.Do()
	if err != nil {
		return nil, err
	}
	if len(files.Items) < 1 {
		return nil, os.ErrNotExist
	}
	return files.Items[0], nil
}

// Lists the folder identified by parentId.
func (s *DriveService) List(pageToken string, limit int) (files []*client.File, next string, err error) {
	req := s.apiservice.Files.List()
	req.Q(fmt.Sprintf("'%s' in parents and mimeType != '%s'", s.parentId, MimeTypeDriveFolder))

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
func (s *DriveService) Upsert(title string, data io.Reader) (file *client.File, err error) {
	if file, err = s.Get(title); err != nil {
		if !os.IsNotExist(err) {
			return
		}
	}
	if file == nil {
		file = &client.File{Title: title}
		file.Parents = []*client.ParentReference{
			&client.ParentReference{Id: s.parentId},
		}
		file.MimeType = MimeTypeCamliBlob
		return s.apiservice.Files.Insert(file).Media(data).Do()
	}

	// TODO: handle large blobs
	return s.apiservice.Files.Update(file.Id, file).Media(data).Do()
}

var errNoDownload = errors.New("file can not be downloaded directly (conversion needed?)")

// Fetch retrieves the metadata and contents of a file.
func (s *DriveService) Fetch(title string) (body io.ReadCloser, size uint32, err error) {
	file, err := s.Get(title)
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

	req, _ := http.NewRequest("GET", file.DownloadUrl, nil)
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
func (s *DriveService) Stat(title string) (int64, error) {
	file, err := s.Get(title)
	if err != nil || file == nil {
		return 0, err
	}
	return file.FileSize, err
}

// Trash trashes the file with the given title.
func (s *DriveService) Trash(title string) (err error) {
	file, err := s.Get(title)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, err = s.apiservice.Files.Trash(file.Id).Do()
	return
}
