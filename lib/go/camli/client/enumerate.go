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
	"camli/blobref"
	"fmt"
	"http"
	"os"
)

type EnumerateOpts struct {
	After      string
	MaxWaitSec int // max seconds to long poll for, waiting for any blob
}

// Note: closes ch.
func (c *Client) EnumerateBlobs(ch chan *blobref.SizedBlobRef) os.Error {
	return c.EnumerateBlobsOpts(ch, EnumerateOpts{})
}

const enumerateBatchSize = 1000

// Note: closes ch.
func (c *Client) EnumerateBlobsOpts(ch chan *blobref.SizedBlobRef, opts EnumerateOpts) os.Error {
	defer close(ch)
	if opts.After != "" && opts.MaxWaitSec != 0 {
		return os.NewError("client error: it's invalid to use enumerate After and MaxWaitSec together")
	}

	error := func(msg string, e os.Error) os.Error {
		err := os.NewError(fmt.Sprintf("client enumerate error: %s: %v", msg, e))
		c.log.Print(err.String())
		return err
	}

	keepGoing := true
	after := opts.After
	for keepGoing {
		waitSec := 0
		if after == "" {
			waitSec = opts.MaxWaitSec
		}
		url := fmt.Sprintf("%s/camli/enumerate-blobs?after=%s&limit=%d&maxwaitsec=%d",
			c.server, http.URLEscape(after), enumerateBatchSize, waitSec)
		req := c.newRequest("GET", url)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return error("http request", err)
		}

		json, err := c.jsonFromResponse("enumerate-blobs", resp)
		if err != nil {
			return error("stat json parse error", err)
		}

		blobs, ok := getJsonMapArray(json, "blobs")
		if !ok {
			return error("response JSON didn't contain 'blobs' array", nil)
		}
		for _, v := range blobs {
			itemJson, ok := v.(map[string]interface{})
			if !ok {
				return error("item in 'blobs' was malformed", nil)
			}
			blobrefStr, ok := getJsonMapString(itemJson, "blobRef")
			if !ok {
				return error("item in 'blobs' was missing string 'blobRef'", nil)
			}
			size, ok := getJsonMapInt64(itemJson, "size")
			if !ok {
				return error("item in 'blobs' was missing numeric 'size'", nil)
			}
			br := blobref.Parse(blobrefStr)
			if br == nil {
				return error("item in 'blobs' had invalid blobref.", nil)
			}
			ch <- &blobref.SizedBlobRef{BlobRef: br, Size: size}
		}

		after, keepGoing = getJsonMapString(json, "continueAfter")
	}
	return nil
}

func getJsonMapString(m map[string]interface{}, key string) (string, bool) {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

func getJsonMapInt64(m map[string]interface{}, key string) (int64, bool) {
	if v, ok := m[key]; ok {
		if n, ok := v.(float64); ok {
			return int64(n), true
		}
	}
	return 0, false
}

func getJsonMapArray(m map[string]interface{}, key string) ([]interface{}, bool) {
	if v, ok := m[key]; ok {
		if a, ok := v.([]interface{}); ok {
			return a, true
		}
	}
	return nil, false
}
