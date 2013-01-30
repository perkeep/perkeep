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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
)

var _ = log.Printf

var debugUploads = os.Getenv("CAMLI_DEBUG_UPLOADS") != ""

// multipartOverhead is how many extra bytes mime/multipart's
// Writer adds around content
var multipartOverhead = calculateMultipartOverhead()

type UploadHandle struct {
	BlobRef  *blobref.BlobRef
	Size     int64 // or -1 if size isn't known
	Contents io.Reader
	Vivify   bool
}

type PutResult struct {
	BlobRef *blobref.BlobRef
	Size    int64
	Skipped bool // already present on blobserver
}

func (pr *PutResult) SizedBlobRef() blobref.SizedBlobRef {
	return blobref.SizedBlobRef{pr.BlobRef, pr.Size}
}

type statResponse struct {
	HaveMap                    map[string]blobref.SizedBlobRef
	maxUploadSize              int64
	uploadUrl                  string
	uploadUrlExpirationSeconds int
	canLongPoll                bool
}

type ResponseFormatError error

func calculateMultipartOverhead() int64 {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, _ := w.CreateFormFile("0", "0")

	dummyContents := []byte("0")
	part.Write(dummyContents)

	w.Close()
	return int64(b.Len()) - 3 // remove what was added
}

func newResFormatError(s string, arg ...interface{}) ResponseFormatError {
	return ResponseFormatError(fmt.Errorf(s, arg...))
}

func parseStatResponse(r io.Reader) (sr *statResponse, err error) {
	var (
		ok   bool
		s    = &statResponse{HaveMap: make(map[string]blobref.SizedBlobRef)}
		jmap = make(map[string]interface{})
	)
	if err := json.NewDecoder(io.LimitReader(r, 5<<20)).Decode(&jmap); err != nil {
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
	bref := blobref.SHA1FromString(data)
	r := strings.NewReader(data)
	return &UploadHandle{BlobRef: bref, Size: int64(len(data)), Contents: r}
}

func (c *Client) jsonFromResponse(requestName string, resp *http.Response) (map[string]interface{}, error) {
	if resp.StatusCode != 200 {
		log.Printf("After %s request, failed to JSON from response; status code is %d", requestName, resp.StatusCode)
		io.Copy(os.Stderr, resp.Body)
		return nil, errors.New(fmt.Sprintf("After %s request, HTTP response code is %d; no JSON to parse.", requestName, resp.StatusCode))
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

func (c *Client) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, wait time.Duration) error {
	if len(blobs) == 0 {
		return nil
	}

	// TODO: if len(blobs) > 1000 or something, cut this up into
	// multiple http requests, and also if the server returns a
	// 400 error, per the blob-stat-protocol.txt document.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "camliversion=1")
	needed := 0
	for _, blob := range blobs {
		if blob == nil {
			panic("nil blob")
		}
		if size, ok := c.haveCache.StatBlobCache(blob); ok {
			dest <- blobref.SizedBlobRef{blob, size}
			continue
		}
		needed++
		fmt.Fprintf(&buf, "&blob%d=%s", needed, blob)
	}
	if needed == 0 {
		return nil
	}

	if wait > 0 {
		secs := int(wait.Seconds())
		if secs == 0 {
			secs = 1
		}
		fmt.Fprintf(&buf, "&maxwaitsec=%d", secs)
	}

	pfx, err := c.prefix()
	if err != nil {
		return err
	}
	req := c.newRequest("POST", fmt.Sprintf("%s/camli/stat", pfx), &buf)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.doReq(req)
	if err != nil {
		return fmt.Errorf("stat HTTP error: %v", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("stat response had http status %d", resp.StatusCode)
	}

	stat, err := parseStatResponse(resp.Body)
	if err != nil {
		return err
	}

	for _, sb := range stat.HaveMap {
		dest <- sb
	}
	return nil
}

// Figure out the size of the contents.
// If the size was provided, trust it.
// If the size was not provided (-1), slurp.
func readerAndSize(h *UploadHandle) (io.Reader, int64, error) {
	if h.Size != -1 {
		return h.Contents, h.Size, nil
	}
	var b bytes.Buffer
	n, err := io.Copy(&b, h.Contents)
	if err != nil {
		return nil, 0, err
	}
	return &b, n, nil
}

func (c *Client) Upload(h *UploadHandle) (*PutResult, error) {
	errorf := func(msg string, arg ...interface{}) (*PutResult, error) {
		err := fmt.Errorf(msg, arg...)
		c.log.Print(err.Error())
		return nil, err
	}

	bodyReader, bodySize, err := readerAndSize(h)
	if err != nil {
		return nil, fmt.Errorf("client: error slurping upload handle to find its length: %v", err)
	}

	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	c.stats.UploadRequests.Bytes += bodySize
	c.statsMutex.Unlock()

	pr := &PutResult{BlobRef: h.BlobRef, Size: bodySize}
	if !h.Vivify {
		if _, ok := c.haveCache.StatBlobCache(h.BlobRef); ok {
			pr.Skipped = true
			return pr, nil
		}
	}

	blobrefStr := h.BlobRef.String()

	// Pre-upload. Check whether the blob already exists on the
	// server and if not, the URL to upload it to.
	pfx, err := c.prefix()
	if err != nil {
		return nil, err
	}
	url_ := fmt.Sprintf("%s/camli/stat", pfx)
	req := c.newRequest("POST", url_, strings.NewReader("camliversion=1&blob1="+blobrefStr))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.doReq(req)
	if err != nil {
		return errorf("stat http error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return errorf("stat response had http status %d", resp.StatusCode)
	}

	stat, err := parseStatResponse(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	for _, sbr := range stat.HaveMap {
		c.haveCache.NoteBlobExists(sbr.BlobRef, sbr.Size)
	}
	if !h.Vivify {
		if _, ok := stat.HaveMap[blobrefStr]; ok {
			pr.Skipped = true
			if closer, ok := h.Contents.(io.Closer); ok {
				// TODO(bradfitz): I did this
				// Close-if-possible thing early on, before I
				// knew better.  Fix the callers instead, and
				// fix the docs.
				closer.Close()
			}
			return pr, nil
		}
	}

	if debugUploads {
		log.Printf("Uploading: %s (%d bytes)", blobrefStr, bodySize)
	}

	pipeReader, pipeWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(pipeWriter)

	copyResult := make(chan error, 1)
	go func() {
		defer pipeWriter.Close()
		part, err := multipartWriter.CreateFormFile(blobrefStr, blobrefStr)
		if err != nil {
			copyResult <- err
			return
		}
		_, err = io.Copy(part, bodyReader)
		if err == nil {
			err = multipartWriter.Close()
		}
		copyResult <- err
	}()

	// TODO(bradfitz): verbosity levels. make this VLOG(2) or something. it's noisy:
	// c.log.Printf("Uploading %s to URL: %s", blobrefStr, stat.uploadUrl)

	req = c.newRequest("POST", stat.uploadUrl)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	if h.Vivify {
		req.Header.Add("X-Camlistore-Vivify", "1")
	}
	req.Body = ioutil.NopCloser(pipeReader)
	req.ContentLength = multipartOverhead + bodySize + int64(len(blobrefStr))*2
	resp, err = c.doReq(req)
	if err != nil {
		return errorf("upload http error: %v", err)
	}
	defer resp.Body.Close()

	// check error from earlier copy
	if err := <-copyResult; err != nil {
		return errorf("failed to copy contents into multipart writer: %v", err)
	}

	// The only valid HTTP responses are 200 and 303.
	if resp.StatusCode != 200 && resp.StatusCode != 303 {
		return errorf("invalid http response %d in upload response", resp.StatusCode)
	}

	if resp.StatusCode == 303 {
		otherLocation := resp.Header.Get("Location")
		if otherLocation == "" {
			return errorf("303 without a Location")
		}
		baseUrl, _ := url.Parse(stat.uploadUrl)
		absUrl, err := baseUrl.Parse(otherLocation)
		if err != nil {
			return errorf("303 Location URL relative resolve error: %v", err)
		}
		otherLocation = absUrl.String()
		resp, err = http.Get(otherLocation)
		if err != nil {
			return errorf("error following 303 redirect after upload: %v", err)
		}
	}

	ures, err := c.jsonFromResponse("upload", resp)
	if err != nil {
		return errorf("json parse from upload error: %v", err)
	}

	errorText, ok := ures["errorText"].(string)
	if ok {
		c.log.Printf("Blob server reports error: %s", errorText)
	}

	received, ok := ures["received"].([]interface{})
	if !ok {
		return errorf("upload json validity error: no 'received'")
	}

	expectedSize := bodySize

	for _, rit := range received {
		it, ok := rit.(map[string]interface{})
		if !ok {
			return errorf("upload json validity error: 'received' is malformed")
		}
		if it["blobRef"] == blobrefStr {
			switch size := it["size"].(type) {
			case nil:
				return errorf("upload json validity error: 'received' is missing 'size'")
			case float64:
				if int64(size) == expectedSize {
					// Success!
					c.statsMutex.Lock()
					c.stats.Uploads.Blobs++
					c.stats.Uploads.Bytes += expectedSize
					c.statsMutex.Unlock()
					if pr.Size == -1 {
						pr.Size = expectedSize
					}
					c.haveCache.NoteBlobExists(pr.BlobRef, expectedSize)
					return pr, nil
				} else {
					return errorf("Server got blob, but reports wrong length (%v; we sent %d)",
						size, expectedSize)
				}
			default:
				return errorf("unsupported type of 'size' in received response")
			}
		}
	}

	return nil, errors.New("Server didn't receive blob.")
}
