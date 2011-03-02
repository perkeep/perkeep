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
	"bytes"
	"camli/blobref"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"http"
	"io"
	"json"
	"log"
	"os"
	"strings"
)

type UploadHandle struct {
	BlobRef  *blobref.BlobRef
	Size     int64
	Contents io.Reader
}

type PutResult struct {
	BlobRef  *blobref.BlobRef
	Size     int64
	Skipped  bool    // already present on blobserver
}

type nopCloser struct {
	io.Reader
}

func (nopCloser) Close() os.Error { return nil }

// Note: must not touch data after calling this.
func NewUploadHandleFromString(data string) *UploadHandle {
	s1 := sha1.New()
	s1.Write([]byte(data))
	bref := blobref.FromHash("sha1", s1)
	buf := bytes.NewBufferString(data)
	return &UploadHandle{BlobRef: bref, Size: int64(len(data)), Contents: buf}
}

func encodeBase64(s string) string {
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(s)))
	base64.StdEncoding.Encode(buf, []byte(s))
	return string(buf)
}

func (c *Client) jsonFromResponse(resp *http.Response) (map[string]interface{}, os.Error) {
	if resp.StatusCode != 200 {
		log.Printf("Failed to JSON from response; status code is %d", resp.StatusCode)
		io.Copy(os.Stderr, resp.Body)
		return nil, os.NewError(fmt.Sprintf("HTTP response code is %d; no JSON to parse.", resp.StatusCode))
	}
	// TODO: LimitReader here for paranoia
	buf := new(bytes.Buffer)
	io.Copy(buf, resp.Body)
	resp.Body.Close()
	jmap := make(map[string]interface{})
	if jerr := json.Unmarshal(buf.Bytes(), &jmap); jerr != nil {
		return nil, jerr
	}
	return jmap, nil
}

func (c *Client) Upload(h *UploadHandle) (*PutResult, os.Error) {
	error := func(msg string, e os.Error) (*PutResult, os.Error) {
		err := os.NewError(fmt.Sprintf("Error uploading blob %s: %s; err=%s",
			h.BlobRef, msg, e))
		c.log.Print(err.String())
		return nil, err
	}

	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	c.stats.UploadRequests.Bytes += h.Size
	c.statsMutex.Unlock()

	blobRefString := h.BlobRef.String()

	// Pre-upload.  Check whether the blob already exists on the
	// server and if not, the URL to upload it to.
	url := fmt.Sprintf("%s/camli/stat", c.server)
	requestBody := "camliversion=1&blob1="+blobRefString
	req := c.newRequest("POST", url)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Body = &nopCloser{strings.NewReader(requestBody)}
	req.ContentLength = int64(len(requestBody))
	req.TransferEncoding = nil

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return error("stat http error", err)
	}

	pur, err := c.jsonFromResponse(resp)
	if err != nil {
		return error("stat json parse error", err)
	}
	
	uploadUrl, ok := pur["uploadUrl"].(string)
	if uploadUrl == "" {
		return error("stat json validity error: no 'uploadUrl'", nil)
	}

	alreadyHave, ok := pur["stat"].([]interface{})
	if !ok {
		return error("stat json validity error: no 'stat'", nil)
	}

	pr := &PutResult{BlobRef: h.BlobRef, Size: h.Size}

	for _, haveObj := range alreadyHave {
		haveObj := haveObj.(map[string]interface{})
		if haveObj["blobRef"].(string) == h.BlobRef.String() {
			pr.Skipped = true
			return pr, nil
		}
	}

	// TODO: use a proper random boundary
	boundary := "sdf8sd8f7s9df9s7df9sd7sdf9s879vs7d8v7sd8v7sd8v"

	multiPartHeader := fmt.Sprintf(
		                "--%s\r\nContent-Type: application/octet-stream\r\n" +
		                "Content-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\n\r\n",
				boundary,
				h.BlobRef, h.BlobRef)
	multiPartFooter := "\r\n--"+boundary+"--\r\n"

	c.log.Printf("Uploading to URL: %s", uploadUrl)
	req = c.newRequest("POST", uploadUrl)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Body = &nopCloser{io.MultiReader(
		strings.NewReader(multiPartHeader),
		h.Contents,
			strings.NewReader(multiPartFooter))}

	req.ContentLength = int64(len(multiPartHeader)) + h.Size + int64(len(multiPartFooter))
	req.TransferEncoding = nil
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return error("upload http error", err)
	}

	// The only valid HTTP responses are 200 and 303.
	if resp.StatusCode != 200 && resp.StatusCode != 303 {
		return error(fmt.Sprintf("invalid http response %d in upload response", resp.StatusCode), nil)
	}

	if resp.StatusCode == 303 {
		otherLocation := resp.Header.Get("Location")
		if otherLocation == "" {
			return error("303 without a Location", nil)
		}
		baseUrl, _ := http.ParseURL(uploadUrl)
		absUrl, err := baseUrl.ParseURL(otherLocation)
		if err != nil {
			return error("303 Location URL relative resolve error", err)
		}
		otherLocation = absUrl.String()
		resp, _, err = http.Get(otherLocation)
		if err != nil {
			return error("error following 303 redirect after upload", err)
		}
	}

	ures, err := c.jsonFromResponse(resp)
	if err != nil {
		return error("json parse from upload error", err)
	}

	errorText, ok := ures["errorText"].(string)
	if ok {
		c.log.Printf("Blob server reports error: %s", errorText)
	}

	received, ok := ures["received"].([]interface{})
	if !ok {
		return error("upload json validity error: no 'received'", nil)
	}

	for _, rit := range received {
		it, ok := rit.(map[string]interface{})
		if !ok {
			return error("upload json validity error: 'received' is malformed", nil)
		}
		if it["blobRef"] == blobRefString {
			switch size := it["size"].(type) {
			case nil:
				return error("upload json validity error: 'received' is missing 'size'", nil)
			case float64:
				if int64(size) == h.Size {
					// Success!
					c.statsMutex.Lock()
					c.stats.Uploads.Blobs++
					c.stats.Uploads.Bytes += h.Size
					c.statsMutex.Unlock()
					return pr, nil
				} else {
					return error(fmt.Sprintf("Server got blob, but reports wrong length (%v; expected %d)",
						size, h.Size), nil)
				}
			default:
				return error("unsupported type of 'size' in received response", nil)
			}
		}
	}

	return nil, os.NewError("Server didn't receive blob.")
}
