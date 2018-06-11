/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/handlers"
)

// RemoveBlobs removes the list of blobs. An error is returned if the
// server failed to remove a blob. Removing a non-existent blob isn't
// an error.
func (c *Client) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	if c.sto != nil {
		return c.sto.RemoveBlobs(ctx, blobs)
	}
	pfx, err := c.prefix()
	if err != nil {
		return err
	}
	url_ := fmt.Sprintf("%s/camli/remove", pfx)
	params := make(url.Values)             // "blobN" -> BlobRefStr
	needsDelete := make(map[blob.Ref]bool) // BlobRefStr -> true
	for n, b := range blobs {
		if !b.Valid() {
			return errors.New("Cannot delete invalid blobref")
		}
		key := fmt.Sprintf("blob%v", n+1)
		params.Add(key, b.String())
		needsDelete[b] = true
	}

	req, err := http.NewRequest("POST", url_, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("Error creating RemoveBlobs POST request: %v", err)
	}
	req = req.WithContext(ctx)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	c.authMode.AddAuthHeader(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		resp.Body.Close()
		return fmt.Errorf("Got status code %d from blobserver for remove %s", resp.StatusCode, params.Encode())
	}
	var remResp handlers.RemoveResponse
	decodeErr := httputil.DecodeJSON(resp, &remResp)
	if resp.StatusCode != 200 {
		resp.Body.Close()
		if decodeErr == nil {
			return fmt.Errorf("invalid http response %d in remove response: %v", resp.StatusCode, remResp.Error)
		} else {
			return fmt.Errorf("invalid http response %d in remove response", resp.StatusCode)
		}
	}
	if decodeErr != nil {
		return fmt.Errorf("failed to parse remove response: %v", err)
	}
	for _, br := range remResp.Removed {
		delete(needsDelete, br)
	}
	if len(needsDelete) > 0 {
		return fmt.Errorf("failed to remove blobs %s", strings.Join(stringKeys(needsDelete), ", "))
	}
	return nil
}

// RemoveBlob removes the provided blob. An error is returned if the server failed to remove
// the blob. Removing a non-existent blob isn't an error.
func (c *Client) RemoveBlob(ctx context.Context, b blob.Ref) error {
	return c.RemoveBlobs(ctx, []blob.Ref{b})
}

func stringKeys(m map[blob.Ref]bool) (s []string) {
	s = make([]string, 0, len(m))
	for key := range m {
		s = append(s, key.String())
	}
	return
}
