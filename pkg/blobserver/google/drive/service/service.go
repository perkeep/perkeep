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

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	client "camlistore.org/third_party/code.google.com/p/google-api-go-client/drive/v2"
)

const (
	MimeTypeDriveFolder = "application/vnd.google-apps.folder"
	MimeTypeCamliBlob   = "application/vnd.camlistore.blob"
)

// DriveService wraps Google Drive API to implement utility methods to
// be performed on the root Drive destination folder.
type DriveService struct {
	transport  *oauth.Transport
	apiservice *client.Service
	parentId   string
}

// New initiates a new DriveService.
func New(transport *oauth.Transport, parentId string) (*DriveService, error) {
	apiservice, err := client.New(transport.Client())
	if err != nil {
		return nil, err
	}
	service := &DriveService{transport: transport, apiservice: apiservice, parentId: parentId}
	return service, err
}

// Get retrieves a file with its title
func (s *DriveService) Get(id string) (*client.File, error) {
	req := s.apiservice.Files.List()
	// TODO: use field selectors
	query := fmt.Sprintf("'%s' in parents and title = '%s'", s.parentId, id)
	req.Q(query)
	files, err := req.Do()

	if err != nil || len(files.Items) < 1 {
		return nil, err
	}
	return files.Items[0], err
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
func (s *DriveService) Upsert(id string, data io.Reader) (file *client.File, err error) {
	if file, err = s.Get(id); err != nil {
		return
	}
	if file == nil {
		file = &client.File{Title: id}
		file.Parents = []*client.ParentReference{
			&client.ParentReference{Id: s.parentId},
		}
		file.MimeType = MimeTypeCamliBlob
		return s.apiservice.Files.Insert(file).Media(data).Do()
	}

	// TODO: handle large blobs
	return s.apiservice.Files.Update(file.Id, file).Media(data).Do()
}

// Fetch retrieves the metadata and contents of a file.
func (s *DriveService) Fetch(id string) (body io.ReadCloser, size uint32, err error) {
	file, err := s.Get(id)

	// TODO: maybe in the case of no download link, remove the file.
	// The file should have malformed or converted to a Docs file
	// unwantedly.
	if err != nil || file == nil || file.DownloadUrl != "" {
		return
	}

	req, _ := http.NewRequest("GET", file.DownloadUrl, nil)
	var resp *http.Response
	if resp, err = s.transport.RoundTrip(req); err != nil {
		return
	}
	if file.FileSize > math.MaxUint32 || file.FileSize < 0 {
		err = errors.New("file too big")
	}
	return resp.Body, uint32(file.FileSize), err
}

// Stat retrieves file metadata and returns
// file size. Returns error if file is not found.
func (s *DriveService) Stat(id string) (int64, error) {
	file, err := s.Get(id)
	if err != nil || file == nil {
		return 0, err
	}
	return file.FileSize, err
}

// Trash trashes an existing file.
func (s *DriveService) Trash(id string) (err error) {
	_, err = s.apiservice.Files.Trash(id).Do()
	return
}
