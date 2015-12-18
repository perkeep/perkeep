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
	"crypto/sha1"
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

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/protocol"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/env"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/schema"
)

// multipartOverhead is how many extra bytes mime/multipart's
// Writer adds around content
var multipartOverhead = calculateMultipartOverhead()

// UploadHandle contains the parameters is a request to upload a blob.
type UploadHandle struct {
	// BlobRef is the required blobref of the blob to upload.
	BlobRef blob.Ref

	// Contents is the blob data.
	Contents io.Reader

	// Size optionally specifies the size of Contents.
	// If <= 0, the Contents are slurped into memory to count the size.
	Size uint32

	// Vivify optionally instructs the server to create a
	// permanode for this blob. If used, the blob should be a
	// "file" schema blob. This is typically used by
	// lesser-trusted clients (such a mobile phones) which don't
	// have rights to do signing directly.
	Vivify bool

	// SkipStat indicates whether the stat check (checking whether
	// the server already has the blob) will be skipped and the
	// blob should be uploaded immediately. This is useful for
	// small blobs that the server is unlikely to already have
	// (e.g. new claims).
	SkipStat bool
}

type PutResult struct {
	BlobRef blob.Ref
	Size    uint32
	Skipped bool // already present on blobserver
}

func (pr *PutResult) SizedBlobRef() blob.SizedRef {
	return blob.SizedRef{pr.BlobRef, pr.Size}
}

// TODO: ditch this type and use protocol.StatResponse directly?
// Or at least make HaveMap keyed by a blob.Ref instead of a string.
type statResponse struct {
	HaveMap     map[string]blob.SizedRef
	canLongPoll bool
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

func parseStatResponse(res *http.Response) (*statResponse, error) {
	var s = &statResponse{HaveMap: make(map[string]blob.SizedRef)}
	var pres protocol.StatResponse
	if err := httputil.DecodeJSON(res, &pres); err != nil {
		return nil, ResponseFormatError(err)
	}

	s.canLongPoll = pres.CanLongPoll
	for _, statItem := range pres.Stat {
		br := statItem.Ref
		if !br.Valid() {
			continue
		}
		s.HaveMap[br.String()] = blob.SizedRef{br, uint32(statItem.Size)}
	}
	return s, nil
}

// NewUploadHandleFromString returns an upload handle
func NewUploadHandleFromString(data string) *UploadHandle {
	bref := blob.SHA1FromString(data)
	r := strings.NewReader(data)
	return &UploadHandle{BlobRef: bref, Size: uint32(len(data)), Contents: r}
}

// TODO(bradfitz): delete most of this. use new camlistore.org/pkg/blobserver/protocol types instead
// of a map[string]interface{}.
func (c *Client) responseJSONMap(requestName string, resp *http.Response) (map[string]interface{}, error) {
	if resp.StatusCode != 200 {
		log.Printf("After %s request, failed to JSON from response; status code is %d", requestName, resp.StatusCode)
		io.Copy(os.Stderr, resp.Body)
		return nil, fmt.Errorf("After %s request, HTTP response code is %d; no JSON to parse.", requestName, resp.StatusCode)
	}
	jmap := make(map[string]interface{})
	if err := httputil.DecodeJSON(resp, &jmap); err != nil {
		return nil, err
	}
	return jmap, nil
}

// statReq is a request to stat a blob.
type statReq struct {
	br   blob.Ref
	dest chan<- blob.SizedRef // written to on success
	errc chan<- error         // written to on both failure and success (after any dest)
}

func (c *Client) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	if c.sto != nil {
		return c.sto.StatBlobs(dest, blobs)
	}
	var needStat []blob.Ref
	for _, br := range blobs {
		if !br.Valid() {
			panic("invalid blob")
		}
		if size, ok := c.haveCache.StatBlobCache(br); ok {
			dest <- blob.SizedRef{br, size}
		} else {
			if needStat == nil {
				needStat = make([]blob.Ref, 0, len(blobs))
			}
			needStat = append(needStat, br)
		}
	}
	if len(needStat) == 0 {
		return nil
	}

	// Here begins all the batching logic. In a SPDY world, this
	// will all be somewhat useless, so consider detecting SPDY on
	// the underlying connection and just always calling doStat
	// instead.  The one thing this code below is also cut up
	// >1000 stats into smaller batches.  But with SPDY we could
	// even just do lots of little 1-at-a-time stats.

	var errcs []chan error // one per blob to stat

	c.pendStatMu.Lock()
	{
		if c.pendStat == nil {
			c.pendStat = make(map[blob.Ref][]statReq)
		}
		for _, blob := range needStat {
			errc := make(chan error, 1)
			errcs = append(errcs, errc)
			c.pendStat[blob] = append(c.pendStat[blob], statReq{blob, dest, errc})
		}
	}
	c.pendStatMu.Unlock()

	// Kick off at least one worker. It may do nothing and lose
	// the race, but somebody will handle our requests in
	// pendStat.
	go c.doSomeStats()

	for _, errc := range errcs {
		if err := <-errc; err != nil {
			return err
		}
	}
	return nil
}

const maxStatPerReq = 1000 // TODO: detect this from client discovery? add it on server side too.

func (c *Client) doSomeStats() {
	c.httpGate.Start()
	defer c.httpGate.Done()

	var batch map[blob.Ref][]statReq

	c.pendStatMu.Lock()
	{
		if len(c.pendStat) == 0 {
			// Lost race. Another batch got these.
			c.pendStatMu.Unlock()
			return
		}
		batch = make(map[blob.Ref][]statReq)
		for br, reqs := range c.pendStat {
			batch[br] = reqs
			delete(c.pendStat, br)
			if len(batch) == maxStatPerReq {
				go c.doSomeStats() // kick off next batch
				break
			}
		}
	}
	c.pendStatMu.Unlock()

	if env.DebugUploads() {
		println("doing stat batch of", len(batch))
	}

	blobs := make([]blob.Ref, 0, len(batch))
	for br := range batch {
		blobs = append(blobs, br)
	}

	ourDest := make(chan blob.SizedRef)
	errc := make(chan error, 1)
	go func() {
		// false for not gated, since we already grabbed the
		// token at the beginning of this function.
		errc <- c.doStat(ourDest, blobs, 0, false)
		close(ourDest)
	}()

	for sb := range ourDest {
		for _, req := range batch[sb.Ref] {
			req.dest <- sb
		}
	}

	// Copy the doStat's error to all waiters for all blobrefs in this batch.
	err := <-errc
	for _, reqs := range batch {
		for _, req := range reqs {
			req.errc <- err
		}
	}
}

// doStat does an HTTP request for the stat. the number of blobs is used verbatim. No extra splitting
// or batching is done at this layer.
func (c *Client) doStat(dest chan<- blob.SizedRef, blobs []blob.Ref, wait time.Duration, gated bool) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "camliversion=1")
	if wait > 0 {
		secs := int(wait.Seconds())
		if secs == 0 {
			secs = 1
		}
		fmt.Fprintf(&buf, "&maxwaitsec=%d", secs)
	}
	for i, blob := range blobs {
		fmt.Fprintf(&buf, "&blob%d=%s", i+1, blob)
	}

	pfx, err := c.prefix()
	if err != nil {
		return err
	}
	req := c.newRequest("POST", fmt.Sprintf("%s/camli/stat", pfx), &buf)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var resp *http.Response
	if gated {
		resp, err = c.doReqGated(req)
	} else {
		resp, err = c.httpClient.Do(req)
	}
	if err != nil {
		return fmt.Errorf("stat HTTP error: %v", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("stat response had http status %d", resp.StatusCode)
	}

	stat, err := parseStatResponse(resp)
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
func (h *UploadHandle) readerAndSize() (io.Reader, int64, error) {
	if h.Size > 0 {
		return h.Contents, int64(h.Size), nil
	}
	var b bytes.Buffer
	n, err := io.Copy(&b, h.Contents)
	if err != nil {
		return nil, 0, err
	}
	return &b, n, nil
}

// Upload uploads a blob, as described by the provided UploadHandle parameters.
func (c *Client) Upload(h *UploadHandle) (*PutResult, error) {
	errorf := func(msg string, arg ...interface{}) (*PutResult, error) {
		err := fmt.Errorf(msg, arg...)
		c.log.Print(err.Error())
		return nil, err
	}

	bodyReader, bodySize, err := h.readerAndSize()
	if err != nil {
		return nil, fmt.Errorf("client: error slurping upload handle to find its length: %v", err)
	}
	if bodySize > constants.MaxBlobSize {
		return nil, errors.New("client: body is bigger then max blob size")
	}

	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	c.stats.UploadRequests.Bytes += bodySize
	c.statsMutex.Unlock()

	pr := &PutResult{BlobRef: h.BlobRef, Size: uint32(bodySize)}

	if c.sto != nil {
		// TODO: stat first so we can show skipped?
		_, err := blobserver.Receive(c.sto, h.BlobRef, bodyReader)
		if err != nil {
			return nil, err
		}
		return pr, nil
	}

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

	if !h.SkipStat {
		url_ := fmt.Sprintf("%s/camli/stat", pfx)
		req := c.newRequest("POST", url_, strings.NewReader("camliversion=1&blob1="+blobrefStr))
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		resp, err := c.doReqGated(req)
		if err != nil {
			return errorf("stat http error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return errorf("stat response had http status %d", resp.StatusCode)
		}

		stat, err := parseStatResponse(resp)
		if err != nil {
			return nil, err
		}
		for _, sbr := range stat.HaveMap {
			c.haveCache.NoteBlobExists(sbr.Ref, uint32(sbr.Size))
		}
		_, serverHasIt := stat.HaveMap[blobrefStr]
		if env.DebugUploads() {
			log.Printf("HTTP Stat(%s) = %v", blobrefStr, serverHasIt)
		}
		if !h.Vivify && serverHasIt {
			pr.Skipped = true
			if closer, ok := h.Contents.(io.Closer); ok {
				// TODO(bradfitz): I did this
				// Close-if-possible thing early on, before I
				// knew better.  Fix the callers instead, and
				// fix the docs.
				closer.Close()
			}
			c.haveCache.NoteBlobExists(h.BlobRef, uint32(bodySize))
			return pr, nil
		}
	}

	if env.DebugUploads() {
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
	// c.log.Printf("Uploading %s", br)

	uploadURL := fmt.Sprintf("%s/camli/upload", pfx)
	req := c.newRequest("POST", uploadURL)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	if h.Vivify {
		req.Header.Add("X-Camlistore-Vivify", "1")
	}
	req.Body = ioutil.NopCloser(pipeReader)
	req.ContentLength = multipartOverhead + bodySize + int64(len(blobrefStr))*2
	resp, err := c.doReqGated(req)
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
		baseURL, _ := url.Parse(uploadURL)
		absURL, err := baseURL.Parse(otherLocation)
		if err != nil {
			return errorf("303 Location URL relative resolve error: %v", err)
		}
		otherLocation = absURL.String()
		resp, err = http.Get(otherLocation)
		if err != nil {
			return errorf("error following 303 redirect after upload: %v", err)
		}
	}

	var ures protocol.UploadResponse
	if err := httputil.DecodeJSON(resp, &ures); err != nil {
		return errorf("error in upload response: %v", err)
	}

	if ures.ErrorText != "" {
		c.log.Printf("Blob server reports error: %s", ures.ErrorText)
	}

	expectedSize := uint32(bodySize)

	for _, sb := range ures.Received {
		if sb.Ref != h.BlobRef {
			continue
		}
		if sb.Size != expectedSize {
			return errorf("Server got blob %v, but reports wrong length (%v; we sent %d)",
				sb.Ref, sb.Size, expectedSize)
		}
		c.statsMutex.Lock()
		c.stats.Uploads.Blobs++
		c.stats.Uploads.Bytes += bodySize
		c.statsMutex.Unlock()
		if pr.Size <= 0 {
			pr.Size = sb.Size
		}
		c.haveCache.NoteBlobExists(pr.BlobRef, pr.Size)
		return pr, nil
	}

	return nil, errors.New("Server didn't receive blob.")
}

// FileUploadOptions is optionally provided to UploadFile.
type FileUploadOptions struct {
	// FileInfo optionally specifies the FileInfo to populate the schema of the file blob.
	FileInfo os.FileInfo
	// WholeRef optionally specifies the digest of the uploaded file contents, which
	// allows UploadFile to skip computing the digest (needed to check if the contents
	// are already on the server).
	WholeRef blob.Ref
}

// UploadFile uploads the contents of the file, as well as a file blob with
// filename for these contents. If the contents or the file blob are found on
// the server, they're not uploaded.
//
// Note: this method is still a work in progress, and might change to accomodate
// the needs of camput file.
func (cl *Client) UploadFile(filename string, contents io.Reader, opts *FileUploadOptions) (blob.Ref, error) {
	fileMap := schema.NewFileMap(filename)
	if opts != nil && opts.FileInfo != nil {
		fileMap = schema.NewCommonFileMap(filename, opts.FileInfo)
		modTime := opts.FileInfo.ModTime()
		if !modTime.IsZero() {
			fileMap.SetModTime(modTime)
		}
	}

	var wholeRef blob.Ref
	if opts != nil && opts.WholeRef.Valid() {
		wholeRef = opts.WholeRef
	} else {
		var buf bytes.Buffer
		var err error
		wholeRef, err = cl.wholeRef(io.TeeReader(contents, &buf))
		if err != nil {
			return blob.Ref{}, err
		}
		contents = io.MultiReader(&buf, contents)
	}

	// TODO(mpl): should we consider the case (not covered by fileMapFromDuplicate)
	// where all the parts are there, but the file schema/blob does not exist? Can that
	// even happen ? I'm naively assuming it can't for now, since that's what camput file
	// does too.
	fileRef, err := cl.fileMapFromDuplicate(fileMap, wholeRef)
	if err != nil {
		return blob.Ref{}, err
	}
	if fileRef.Valid() {
		return fileRef, nil
	}

	return schema.WriteFileMap(cl, fileMap, contents)
}

func (cl *Client) wholeRef(contents io.Reader) (blob.Ref, error) {
	// TODO(mpl): use a trackDigestReader once pulled from camput.
	// and allow for different hash type. maybe also move to another pkg.
	h := sha1.New()
	_, err := io.Copy(h, contents)
	if err != nil {
		return blob.Ref{}, err
	}
	s := fmt.Sprintf("sha1-%x", h.Sum(nil))
	ref, ok := blob.Parse(s)
	if !ok {
		return blob.Ref{}, fmt.Errorf("Invalid blobref: %q", s)
	}
	return ref, nil
}

// fileMapFromDuplicate queries the server's search interface for an
// existing file blob for the file contents of wholeRef.
// If the server has it, it's validated, and then fileMap (which must
// already be partially populated) has its "parts" field populated,
// and then fileMap is uploaded (if necessary).
// If no file blob is found, a zero blob.Ref (and no error) is returned.
func (cl *Client) fileMapFromDuplicate(fileMap *schema.Builder, wholeRef blob.Ref) (blob.Ref, error) {
	dupFileRef, err := cl.SearchExistingFileSchema(wholeRef)
	if err != nil {
		return blob.Ref{}, err
	}
	if !dupFileRef.Valid() {
		// because SearchExistingFileSchema returns blob.Ref{}, nil when file is not found.
		return blob.Ref{}, nil
	}
	dupMap, err := cl.FetchSchemaBlob(dupFileRef)
	if err != nil {
		return blob.Ref{}, fmt.Errorf("could not find existing file blob for wholeRef %q: %v", wholeRef, err)
	}
	fileMap.PopulateParts(dupMap.PartsSize(), dupMap.ByteParts())
	json, err := fileMap.JSON()
	if err != nil {
		return blob.Ref{}, fmt.Errorf("could not write file map for wholeRef %q: %v", wholeRef, err)
	}
	if blob.SHA1FromString(json) == dupFileRef {
		// Unchanged (same filename, modtime, JSON serialization, etc)
		return dupFileRef, nil
	}
	sbr, err := cl.ReceiveBlob(dupFileRef, strings.NewReader(json))
	if err != nil {
		return blob.Ref{}, err
	}
	return sbr.Ref, nil
}
