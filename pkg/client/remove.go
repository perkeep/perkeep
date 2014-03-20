/*
Copyright 2011 Google Inc.

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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
)

type removeResponse struct {
	Removed []string `json:"removed"`
}

// Remove the list of blobs. An error is returned if the server failed to
// remove a blob. Removing a non-existent blob isn't an error.
func (c *Client) RemoveBlobs(blobs []blob.Ref) error {
	if c.sto != nil {
		return c.sto.RemoveBlobs(blobs)
	}
	pfx, err := c.prefix()
	if err != nil {
		return err
	}
	url_ := fmt.Sprintf("%s/camli/remove", pfx)
	params := make(url.Values)           // "blobN" -> BlobRefStr
	needsDelete := make(map[string]bool) // BlobRefStr -> true
	for n, b := range blobs {
		if !b.Valid() {
			return errors.New("Cannot delete invalid blobref")
		}
		key := fmt.Sprintf("blob%v", n+1)
		params.Add(key, b.String())
		needsDelete[b.String()] = true
	}

	req, err := http.NewRequest("POST", url_, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("Error creating RemoveBlobs POST request: %v", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	c.authMode.AddAuthHeader(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		resp.Body.Close()
		return fmt.Errorf("Got status code %d from blobserver for remove %s", resp.StatusCode, params.Encode())
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return fmt.Errorf("Invalid http response %d in remove response", resp.StatusCode)
	}
	var remResp removeResponse
	if err := httputil.DecodeJSON(resp, &remResp); err != nil {
		return fmt.Errorf("Failed to parse remove response: %v", err)
	}
	for _, value := range remResp.Removed {
		delete(needsDelete, value)
	}
	if len(needsDelete) > 0 {
		return fmt.Errorf("Failed to remove blobs %s", strings.Join(stringKeys(needsDelete), ", "))
	}
	return nil
}

// Remove the single blob. An error is returned if the server failed to remove
// the blob. Removing a non-existent blob isn't an error.
func (c *Client) RemoveBlob(b blob.Ref) error {
	return c.RemoveBlobs([]blob.Ref{b})
}

func stringKeys(m map[string]bool) (s []string) {
	s = make([]string, 0, len(m))
	for key, _ := range m {
		s = append(s, key)
	}
	return
}
