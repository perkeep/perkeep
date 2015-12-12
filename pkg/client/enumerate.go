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
	"log"
	"math"
	"net/url"
	"time"

	"camlistore.org/pkg/blob"
	"golang.org/x/net/context"
)

// EnumerateOpts are the options to Client.EnumerateBlobsOpts.
type EnumerateOpts struct {
	After   string        // last blobref seen; start with ones greater than this
	MaxWait time.Duration // how long to poll for (second granularity), waiting for any blob, or 0 for no limit
	Limit   int           // if non-zero, the max blobs to return
}

// SimpleEnumerateBlobs sends all blobs to the provided channel.
// The channel will be closed, regardless of whether an error is returned.
func (c *Client) SimpleEnumerateBlobs(ctx context.Context, ch chan<- blob.SizedRef) error {
	return c.EnumerateBlobsOpts(ctx, ch, EnumerateOpts{})
}

func (c *Client) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	if c.sto != nil {
		return c.sto.EnumerateBlobs(ctx, dest, after, limit)
	}
	if limit == 0 {
		log.Printf("Warning: Client.EnumerateBlobs called with a limit of zero")
		close(dest)
		return nil
	}
	return c.EnumerateBlobsOpts(ctx, dest, EnumerateOpts{
		After: after,
		Limit: limit,
	})
}

const enumerateBatchSize = 1000

// EnumerateBlobsOpts sends blobs to the provided channel, as directed by opts.
// The channel will be closed, regardless of whether an error is returned.
func (c *Client) EnumerateBlobsOpts(ctx context.Context, ch chan<- blob.SizedRef, opts EnumerateOpts) error {
	defer close(ch)
	if opts.After != "" && opts.MaxWait != 0 {
		return errors.New("client error: it's invalid to use enumerate After and MaxWaitSec together")
	}
	pfx, err := c.prefix()
	if err != nil {
		return err
	}

	error := func(msg string, e error) error {
		err := fmt.Errorf("client enumerate error: %s: %v", msg, e)
		c.log.Print(err.Error())
		return err
	}

	nSent := 0
	keepGoing := true
	after := opts.After
	for keepGoing {
		waitSec := 0
		if after == "" {
			if opts.MaxWait > 0 {
				waitSec = int(opts.MaxWait.Seconds())
				if waitSec == 0 {
					waitSec = 1
				}
			}
		}
		url_ := fmt.Sprintf("%s/camli/enumerate-blobs?after=%s&limit=%d&maxwaitsec=%d",
			pfx, url.QueryEscape(after), enumerateBatchSize, waitSec)
		req := c.newRequest("GET", url_)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return error("http request", err)
		}

		json, err := c.responseJSONMap("enumerate-blobs", resp)
		if err != nil {
			return error("stat json parse error", err)
		}

		blobs, ok := getJSONMapArray(json, "blobs")
		if !ok {
			return error("response JSON didn't contain 'blobs' array", nil)
		}
		for _, v := range blobs {
			itemJSON, ok := v.(map[string]interface{})
			if !ok {
				return error("item in 'blobs' was malformed", nil)
			}
			blobrefStr, ok := getJSONMapString(itemJSON, "blobRef")
			if !ok {
				return error("item in 'blobs' was missing string 'blobRef'", nil)
			}
			size, ok := getJSONMapUint32(itemJSON, "size")
			if !ok {
				return error("item in 'blobs' was missing numeric 'size'", nil)
			}
			br, ok := blob.Parse(blobrefStr)
			if !ok {
				return error("item in 'blobs' had invalid blobref.", nil)
			}
			select {
			case ch <- blob.SizedRef{Ref: br, Size: uint32(size)}:
			case <-ctx.Done():
				return ctx.Err()
			}
			nSent++
			if opts.Limit == nSent {
				// nSent can't be zero at this point, so opts.Limit being 0
				// is okay.
				return nil
			}
		}

		after, keepGoing = getJSONMapString(json, "continueAfter")
	}
	return nil
}

func getJSONMapString(m map[string]interface{}, key string) (string, bool) {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

func getJSONMapInt64(m map[string]interface{}, key string) (int64, bool) {
	if v, ok := m[key]; ok {
		if n, ok := v.(float64); ok {
			return int64(n), true
		}
	}
	return 0, false
}

func getJSONMapUint32(m map[string]interface{}, key string) (uint32, bool) {
	u, ok := getJSONMapInt64(m, key)
	if !ok {
		return 0, false
	}
	if u < 0 || u > math.MaxUint32 {
		return 0, false
	}
	return uint32(u), true
}

func getJSONMapArray(m map[string]interface{}, key string) ([]interface{}, bool) {
	if v, ok := m[key]; ok {
		if a, ok := v.([]interface{}); ok {
			return a, true
		}
	}
	return nil, false
}
