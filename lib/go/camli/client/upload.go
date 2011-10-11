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
	"fmt"
	"http"
	"io"
	"io/ioutil"
	"json"
	"log"
	"mime/multipart"
	"os"
	"strings"

	"camli/blobref"
	"url"
)

var _ = log.Printf

// multipartOverhead is how many extra bytes mime/multipart's
// Writer adds around content
var multipartOverhead = calculateMultipartOverhead()

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

type ResponseFormatError os.Error

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
	bref := blobref.Sha1FromString(data)
	r := strings.NewReader(data)
	return &UploadHandle{BlobRef: bref, Size: int64(len(data)), Contents: r}
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

func (c *Client) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
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

	req := c.newRequest("POST", fmt.Sprintf("%s/camli/stat", c.server))
	bodyStr := buf.String()
	req.Body = ioutil.NopCloser(strings.NewReader(bodyStr))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = int64(len(bodyStr))

	resp, err := c.httpClient.Do(req)
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
func readerAndSize(h *UploadHandle) (io.Reader, int64, os.Error) {
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

func (c *Client) Upload(h *UploadHandle) (*PutResult, os.Error) {
	errorf := func(msg string, arg ...interface{}) (*PutResult, os.Error) {
		err := fmt.Errorf(msg, arg...)
		c.log.Print(err.String())
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

	blobrefStr := h.BlobRef.String()

	// Pre-upload.  Check whether the blob already exists on the
	// server and if not, the URL to upload it to.
	url_ := fmt.Sprintf("%s/camli/stat", c.server)
	requestBody := "camliversion=1&blob1=" + blobrefStr
	req := c.newRequest("POST", url_)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Body = ioutil.NopCloser(strings.NewReader(requestBody))
	req.ContentLength = int64(len(requestBody))
	req.TransferEncoding = nil

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errorf("stat http error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return errorf("stat response had http status %d", resp.StatusCode)
	}

	stat, err := parseStatResponse(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	pr := &PutResult{BlobRef: h.BlobRef, Size: bodySize}
	if _, ok := stat.HaveMap[blobrefStr]; ok {
		pr.Skipped = true
		if closer, ok := h.Contents.(io.Closer); ok {
			closer.Close()
		}
		return pr, nil
	}

	pipeReader, pipeWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(pipeWriter)

	copyResult := make(chan os.Error, 1)
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

	c.log.Printf("Uploading to URL: %s", stat.uploadUrl)
	req = c.newRequest("POST", stat.uploadUrl)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Body = ioutil.NopCloser(pipeReader)
	req.ContentLength = multipartOverhead + bodySize + int64(len(blobrefStr))*2
	req.TransferEncoding = nil
	resp, err = c.httpClient.Do(req)
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

	return nil, os.NewError("Server didn't receive blob.")
}
