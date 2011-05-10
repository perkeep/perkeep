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
	"io/ioutil"
	"json"
	"log"
	"os"
	"strings"
)

var _ = log.Printf

type UploadHandle struct {
	BlobRef  *blobref.BlobRef
	Size     int64 // or -1 if size isn't known
	Contents io.Reader
}

type PutResult struct {
	BlobRef *blobref.BlobRef
	Size    int64
	Skipped bool // already present on blobserver
}

type statResponse struct {
	HaveMap                    map[string]blobref.SizedBlobRef
	maxUploadSize              int64
	uploadUrl                  string
	uploadUrlExpirationSeconds int
	canLongPoll                bool
}

type ResponseFormatError os.Error

func newResFormatError(s string, arg ...interface{}) ResponseFormatError {
	return ResponseFormatError(fmt.Errorf(s, arg...))
}

// TODO-GO: if outerr is replaced by a "_", gotest(!) fails with a 6g error.
func parseStatResponse(r io.Reader) (sr *statResponse, outerr os.Error) {
	var (
		ok   bool
		err  os.Error
		s    = &statResponse{HaveMap: make(map[string]blobref.SizedBlobRef)}
		jmap = make(map[string]interface{})
	)
	if err = json.NewDecoder(io.LimitReader(r, 5<<20)).Decode(&jmap); err != nil {
		return nil, ResponseFormatError(err)
	}
	defer func() {
		if sr == nil {
			log.Printf("parseStatResponse got map: %#v", jmap)
		}
	}()

	s.uploadUrl, ok = jmap["uploadUrl"].(string)
	if !ok {
		return nil, newResFormatError("no 'uploadUrl' in stat response")
	}

	if n, ok := jmap["maxUploadSize"].(float64); ok {
		s.maxUploadSize = int64(n)
	} else {
		return nil, newResFormatError("no 'maxUploadSize' in stat response")
	}

	if n, ok := jmap["uploadUrlExpirationSeconds"].(float64); ok {
		s.uploadUrlExpirationSeconds = int(n)
	} else {
		return nil, newResFormatError("no 'uploadUrlExpirationSeconds' in stat response")
	}

	if v, ok := jmap["canLongPoll"].(bool); ok {
		s.canLongPoll = v
	}

	alreadyHave, ok := jmap["stat"].([]interface{})
	if !ok {
		return nil, newResFormatError("no 'stat' key in stat response")
	}

	for _, li := range alreadyHave {
		m, ok := li.(map[string]interface{})
		if !ok {
			return nil, newResFormatError("'stat' list value of unexpected type %T", li)
		}
		blobRefStr, ok := m["blobRef"].(string)
		if !ok {
			return nil, newResFormatError("'stat' list item has non-string 'blobRef' key")
		}
		size, ok := m["size"].(float64)
		if !ok {
			return nil, newResFormatError("'stat' list item has non-number 'size' key")
		}
		br := blobref.Parse(blobRefStr)
		if br == nil {
			return nil, newResFormatError("'stat' list item has invalid 'blobRef' key")
		}
		s.HaveMap[br.String()] = blobref.SizedBlobRef{br, int64(size)}
	}

	return s, nil
}

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

func (c *Client) jsonFromResponse(requestName string, resp *http.Response) (map[string]interface{}, os.Error) {
	if resp.StatusCode != 200 {
		log.Printf("After %s request, failed to JSON from response; status code is %d", requestName, resp.StatusCode)
		io.Copy(os.Stderr, resp.Body)
		return nil, os.NewError(fmt.Sprintf("After %s request, HTTP response code is %d; no JSON to parse.", requestName, resp.StatusCode))
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

func (c *Client) Stat(dest chan<- *blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	if len(blobs) == 0 {
		return nil
	}

	// TODO: if len(blobs) > 1000 or something, cut this up into
	// multiple http requests, and also if the server returns a
	// 400 error, per the blob-stat-protocol.txt document.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "camliversion=1")
	for n, blob := range blobs {
		if blob == nil {
			panic("nil blob")
		}
		fmt.Fprintf(&buf, "&blob%d=%s", n+1, blob)
	}

	if waitSeconds > 0 {
		fmt.Fprintf(&buf, "&maxwaitsec=%d", waitSeconds)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/camli/stat", c.server), strings.NewReader(buf.String()))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = int64(buf.Len())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stat HTTP error: %v", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("stat response had http status %d", resp.StatusCode)
	}

	stat, err := parseStatResponse(resp.Body)
	if err != nil {
		return err
	}

	for _, sb := range stat.HaveMap {
		lsb := sb // local one to take pointer of: TODO: change channel type to non-pointer?
		dest <- &lsb
	}
	return nil
}

func (c *Client) Upload(h *UploadHandle) (*PutResult, os.Error) {
	error := func(msg string, arg ...interface{}) (*PutResult, os.Error) {
		err := fmt.Errorf(msg, arg...)
		c.log.Print(err.String())
		return nil, err
	}

	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	if h.Size != -1 {
		c.stats.UploadRequests.Bytes += h.Size
	}
	c.statsMutex.Unlock()

	blobRefString := h.BlobRef.String()

	// Pre-upload.  Check whether the blob already exists on the
	// server and if not, the URL to upload it to.
	url := fmt.Sprintf("%s/camli/stat", c.server)
	requestBody := "camliversion=1&blob1=" + blobRefString
	req := c.newRequest("POST", url)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Body = ioutil.NopCloser(strings.NewReader(requestBody))
	req.ContentLength = int64(len(requestBody))
	req.TransferEncoding = nil

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return error("stat http error: %v", err)
	}

	if resp.StatusCode != 200 {
		return error("stat response had http status %d", resp.StatusCode)
	}

	stat, err := parseStatResponse(resp.Body)
	if err != nil {
		return nil, err
	}

	pr := &PutResult{BlobRef: h.BlobRef, Size: h.Size}
	if _, ok := stat.HaveMap[h.BlobRef.String()]; ok {
		pr.Skipped = true

		// Consume the buffer that was provided, just for
		// consistency. But if it's a closer, do that
		// instead. But if they didn't provide a size,
		// we consume it anyway just to get the size
		// for stats.
		closer, _ := h.Contents.(io.Closer)
		if h.Size >= 0 && closer != nil {
			closer.Close()
		} else {
			n, err := io.Copy(ioutil.Discard, h.Contents)
			if err != nil {
				return nil, err
			}
			if h.Size == -1 {
				pr.Size = n
				c.statsMutex.Lock()
				c.stats.UploadRequests.Bytes += pr.Size
				c.statsMutex.Unlock()
			}
		}
		return pr, nil
	}

	// TODO: use a proper random boundary
	boundary := "sdf8sd8f7s9df9s7df9sd7sdf9s879vs7d8v7sd8v7sd8v"

	// TODO-GO: add a multipart writer class.
	multiPartHeader := fmt.Sprintf(
		"--%s\r\nContent-Type: application/octet-stream\r\n"+
			"Content-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\n\r\n",
		boundary,
		h.BlobRef, h.BlobRef)
	multiPartFooter := "\r\n--" + boundary + "--\r\n"

	c.log.Printf("Uploading to URL: %s", stat.uploadUrl)
	req = c.newRequest("POST", stat.uploadUrl)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	contentsSize := int64(0)
	req.Body = ioutil.NopCloser(io.MultiReader(
		strings.NewReader(multiPartHeader),
		countingReader{h.Contents, &contentsSize},
		strings.NewReader(multiPartFooter)))

	if h.Size >= 0 {
		req.ContentLength = int64(len(multiPartHeader)) + h.Size + int64(len(multiPartFooter))
	}
	req.TransferEncoding = nil
	resp, err = c.httpClient.Do(req)
	if err != nil {
		return error("upload http error: %v", err)
	}

	if h.Size >= 0 {
		if contentsSize != h.Size {
			return error("UploadHandle declared size %d but Contents length was %d", h.Size, contentsSize)
		}
	} else {
		h.Size = contentsSize
	}

	// The only valid HTTP responses are 200 and 303.
	if resp.StatusCode != 200 && resp.StatusCode != 303 {
		return error("invalid http response %d in upload response", resp.StatusCode)
	}

	if resp.StatusCode == 303 {
		otherLocation := resp.Header.Get("Location")
		if otherLocation == "" {
			return error("303 without a Location")
		}
		baseUrl, _ := http.ParseURL(stat.uploadUrl)
		absUrl, err := baseUrl.ParseURL(otherLocation)
		if err != nil {
			return error("303 Location URL relative resolve error: %v", err)
		}
		otherLocation = absUrl.String()
		resp, _, err = http.Get(otherLocation)
		if err != nil {
			return error("error following 303 redirect after upload: %v", err)
		}
	}

	ures, err := c.jsonFromResponse("upload", resp)
	if err != nil {
		return error("json parse from upload error: %v", err)
	}

	errorText, ok := ures["errorText"].(string)
	if ok {
		c.log.Printf("Blob server reports error: %s", errorText)
	}

	received, ok := ures["received"].([]interface{})
	if !ok {
		return error("upload json validity error: no 'received'")
	}

	for _, rit := range received {
		it, ok := rit.(map[string]interface{})
		if !ok {
			return error("upload json validity error: 'received' is malformed")
		}
		if it["blobRef"] == blobRefString {
			switch size := it["size"].(type) {
			case nil:
				return error("upload json validity error: 'received' is missing 'size'")
			case float64:
				if int64(size) == h.Size {
					// Success!
					c.statsMutex.Lock()
					c.stats.Uploads.Blobs++
					c.stats.Uploads.Bytes += h.Size
					c.statsMutex.Unlock()
					return pr, nil
				} else {
					return error("Server got blob, but reports wrong length (%v; expected %d)",
						size, h.Size)
				}
			default:
				return error("unsupported type of 'size' in received response")
			}
		}
	}

	return nil, os.NewError("Server didn't receive blob.")
}

type countingReader struct {
	r io.Reader
	n *int64
}

func (cr countingReader) Read(p []byte) (n int, err os.Error) {
	n, err = cr.r.Read(p)
	*cr.n += int64(n)
	return
}
