/*
Copyright 2026 The Perkeep Authors.

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

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

// ImporterRoot returns the discovered importer handler URL.
func (c *Client) ImporterRoot() (string, error) {
	if err := c.condDiscovery(); err != nil {
		return "", err
	}
	if c.importerRoot == "" {
		return "", errors.New("client: server has no importer handler")
	}
	return c.importerRoot, nil
}

func (c *Client) archiveURL(provider string) (string, error) {
	if provider == "" {
		return "", errors.New("client: empty archive importer name")
	}
	for _, r := range provider {
		if r < 'a' || r > 'z' {
			if r < '0' || r > '9' {
				if r != '-' && r != '_' {
					return "", fmt.Errorf("client: invalid archive importer name %q", provider)
				}
			}
		}
	}
	root, err := c.ImporterRoot()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(root, "/") + "/" + provider + "/archive", nil
}

// InspectArchive inspects an uploaded file without creating an importer account
// or starting an import. fileRef must identify a file schema, not a content blob.
func (c *Client) InspectArchive(ctx context.Context, provider string, fileRef blob.Ref) (*camtypes.ArchiveImportResult, error) {
	return c.submitArchive(ctx, provider, "inspect", fileRef)
}

// ImportArchive starts a managed import of an already uploaded file. It returns
// promptly; use ArchiveImportStatus to follow completion. Canceling ctx does not
// cancel an import already accepted by the server.
func (c *Client) ImportArchive(ctx context.Context, provider string, fileRef blob.Ref) (*camtypes.ArchiveImportResult, error) {
	return c.submitArchive(ctx, provider, "start", fileRef)
}

func (c *Client) submitArchive(ctx context.Context, provider, action string, fileRef blob.Ref) (*camtypes.ArchiveImportResult, error) {
	if !fileRef.Valid() {
		return nil, errors.New("client: invalid archive file-schema ref")
	}
	endpoint, err := c.archiveURL(provider)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(camtypes.ArchiveImportRequest{Action: action, FileRef: fileRef})
	if err != nil {
		return nil, err
	}
	req := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	var result camtypes.ArchiveImportResult
	if err := c.archiveResponse(req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ArchiveImportStatus returns the current or durable last result for an archive
// importer account. An interrupted run can be retried using its FileRef.
func (c *Client) ArchiveImportStatus(ctx context.Context, provider string, accountRef blob.Ref) (*camtypes.ArchiveImportStatus, error) {
	if !accountRef.Valid() {
		return nil, errors.New("client: invalid archive account ref")
	}
	endpoint, err := c.archiveURL(provider)
	if err != nil {
		return nil, err
	}
	req := c.newRequest(ctx, http.MethodGet, endpoint+"?account="+url.QueryEscape(accountRef.String()))
	var status camtypes.ArchiveImportStatus
	if err := c.archiveResponse(req, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *Client) archiveResponse(req *http.Request, dst any) error {
	res, err := c.expect2XX(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// Status and identity responses are bounded metadata, never archive data.
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponse {
		return errors.New("client: archive response exceeds size limit")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("client: invalid archive response: %w", err)
	}
	return nil
}
