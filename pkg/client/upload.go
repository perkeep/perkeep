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
	"bytes"
	"context"
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
	"sync"
	"time"

	"perkeep.org/internal/hashutil"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/schema"
)

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
	return blob.SizedRef{Ref: pr.BlobRef, Size: pr.Size}
}

// TODO: ditch this type and use protocol.StatResponse directly?
// Or at least make HaveMap keyed by a blob.Ref instead of a string.
type statResponse struct {
	HaveMap     map[string]blob.SizedRef
	canLongPoll bool
}

type ResponseFormatError error

var (
	multipartOnce     sync.Once
	multipartOverhead int64
)

// multipartOverhead is how many extra bytes mime/multipart's
// Writer adds around content
func getMultipartOverhead() int64 {
	multipartOnce.Do(func() {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		part, _ := w.CreateFormFile("0", "0")

		dummyContents := []byte("0")
		part.Write(dummyContents)

		w.Close()
		multipartOverhead = int64(b.Len()) - 3 // remove what was added
	})
	return multipartOverhead
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
		s.HaveMap[br.String()] = blob.SizedRef{Ref: br, Size: uint32(statItem.Size)}
	}
	return s, nil
}

// NewUploadHandleFromString returns an upload handle
func NewUploadHandleFromString(data string) *UploadHandle {
	bref := blob.RefFromString(data)
	r := strings.NewReader(data)
	return &UploadHandle{BlobRef: bref, Size: uint32(len(data)), Contents: r}
}

// TODO(bradfitz): delete most of this. use new perkeep.org/pkg/blobserver/protocol types instead
// of a map[string]interface{}.
func (c *Client) responseJSONMap(requestName string, resp *http.Response) (map[string]interface{}, error) {
	if resp.StatusCode != 200 {
		c.printf("After %s request, failed to JSON from response; status code is %d", requestName, resp.StatusCode)
		io.Copy(os.Stderr, resp.Body)
		return nil, fmt.Errorf("after %s request, HTTP response code is %d; no JSON to parse", requestName, resp.StatusCode)
	}
	jmap := make(map[string]interface{})
	if err := httputil.DecodeJSON(resp, &jmap); err != nil {
		return nil, err
	}
	return jmap, nil
}

func (c *Client) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	if c.sto != nil {
		return c.sto.StatBlobs(ctx, blobs, fn)
	}
	var needStat []blob.Ref
	for _, br := range blobs {
		if !br.Valid() {
			panic("invalid blob")
		}
		if size, ok := c.haveCache.StatBlobCache(br); ok {
			if err := fn(blob.SizedRef{Ref: br, Size: size}); err != nil {
				return err
			}
		} else {
			needStat = append(needStat, br)
		}
	}
	if len(needStat) == 0 {
		return nil
	}
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, c.httpGate, func(br blob.Ref) (workerSB blob.SizedRef, err error) {
		err = c.doStat(ctx, []blob.Ref{br}, 0, false, func(sb blob.SizedRef) error {
			workerSB = sb
			c.haveCache.NoteBlobExists(sb.Ref, sb.Size)
			return fn(sb)
		})
		return
	})
}

// doStat does an HTTP request for the stat. the number of blobs is used verbatim. No extra splitting
// or batching is done at this layer.
// The semantics are the same as blobserver.BlobStatter.
// gate controls whether it uses httpGate to pause on requests.
func (c *Client) doStat(ctx context.Context, blobs []blob.Ref, wait time.Duration, gated bool, fn func(blob.SizedRef) error) error {
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
	req := c.newRequest(ctx, "POST", fmt.Sprintf("%s/camli/stat", pfx), &buf)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var resp *http.Response
	if gated {
		resp, err = c.doReqGated(req)
	} else {
		resp, err = c.httpClient.Do(req)
	}
	if err != nil {
		return fmt.Errorf("stat HTTP error: %w", err)
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
		if err := fn(sb); err != nil {
			return err
		}
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
func (c *Client) Upload(ctx context.Context, h *UploadHandle) (*PutResult, error) {
	errorf := func(msg string, arg ...interface{}) (*PutResult, error) {
		err := fmt.Errorf(msg, arg...)
		c.printf("%v", err)
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
		_, err := blobserver.Receive(ctx, c.sto, h.BlobRef, bodyReader)
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
		req := c.newRequest(ctx, "POST", url_, strings.NewReader("camliversion=1&blob1="+blobrefStr))
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		resp, err := c.doReqGated(req)
		if err != nil {
			return errorf("stat http error: %w", err)
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

	uploadURL := fmt.Sprintf("%s/camli/upload", pfx)
	req := c.newRequest(ctx, "POST", uploadURL)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	if h.Vivify {
		req.Header.Add("X-Camlistore-Vivify", "1")
	}
	req.Body = ioutil.NopCloser(pipeReader)
	req.ContentLength = getMultipartOverhead() + bodySize + int64(len(blobrefStr))*2
	resp, err := c.doReqGated(req)
	if err != nil {
		return errorf("upload http error: %w", err)
	}
	defer resp.Body.Close()

	// check error from earlier copy
	if err := <-copyResult; err != nil {
		return errorf("failed to copy contents into multipart writer: %w", err)
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
			return errorf("303 Location URL relative resolve error: %w", err)
		}
		otherLocation = absURL.String()
		resp, err = http.Get(otherLocation)
		if err != nil {
			return errorf("error following 303 redirect after upload: %w", err)
		}
	}

	var ures protocol.UploadResponse
	if err := httputil.DecodeJSON(resp, &ures); err != nil {
		return errorf("error in upload response: %w", err)
	}

	if ures.ErrorText != "" {
		c.printf("Blob server reports error: %s", ures.ErrorText)
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

	return nil, errors.New("server didn't receive blob")
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
// Note: this method is still a work in progress, and might change to accommodate
// the needs of pk-put file.
func (c *Client) UploadFile(ctx context.Context, filename string, contents io.Reader, opts *FileUploadOptions) (blob.Ref, error) {
	fileMap := schema.NewFileMap(filename)
	if opts != nil && opts.FileInfo != nil {
		fileMap = schema.NewCommonFileMap(filename, opts.FileInfo)
		modTime := opts.FileInfo.ModTime()
		if !modTime.IsZero() {
			fileMap.SetModTime(modTime)
		}
	}
	fileMap.SetType(schema.TypeFile)

	var wholeRef []blob.Ref
	if opts != nil && opts.WholeRef.Valid() {
		wholeRef = append(wholeRef, opts.WholeRef)
	} else {
		var buf bytes.Buffer
		var err error
		wholeRef, err = c.wholeRef(io.TeeReader(contents, &buf))
		if err != nil {
			return blob.Ref{}, err
		}
		contents = io.MultiReader(&buf, contents)
	}

	fileRef, err := c.fileMapFromDuplicate(ctx, fileMap, wholeRef)
	if err != nil {
		return blob.Ref{}, err
	}
	if fileRef.Valid() {
		return fileRef, nil
	}

	return schema.WriteFileMap(ctx, c, fileMap, contents)
}

// TODO(mpl): replace up.wholeFileDigest in pk-put with c.wholeRef maybe.

// wholeRef returns the blob ref(s) of the regular file's contents
// as if it were one entire blob (ignoring blob size limits).
// By default, only one ref is returned, unless the server has advertised
// that it has indexes calculated for other hash functions.
func (c *Client) wholeRef(contents io.Reader) ([]blob.Ref, error) {
	hasLegacySHA1, err := c.HasLegacySHA1()
	if err != nil {
		return nil, fmt.Errorf("cannot discover if server has legacy sha1: %v", err)
	}
	td := hashutil.NewTrackDigestReader(contents)
	td.DoLegacySHA1 = hasLegacySHA1
	if _, err := io.Copy(ioutil.Discard, td); err != nil {
		return nil, err
	}
	refs := []blob.Ref{blob.RefFromHash(td.Hash())}
	if td.DoLegacySHA1 {
		refs = append(refs, blob.RefFromHash(td.LegacySHA1Hash()))
	}
	return refs, nil
}

// fileMapFromDuplicate queries the server's search interface for an
// existing file blob for the file contents any of wholeRef.
// If the server has it, it's validated, and then fileMap (which must
// already be partially populated) has its "parts" field populated,
// and then fileMap is uploaded (if necessary).
// If no file blob is found, a zero blob.Ref (and no error) is returned.
func (c *Client) fileMapFromDuplicate(ctx context.Context, fileMap *schema.Builder, wholeRef []blob.Ref) (blob.Ref, error) {
	dupFileRef, err := c.SearchExistingFileSchema(ctx, wholeRef...)
	if err != nil {
		return blob.Ref{}, err
	}
	if !dupFileRef.Valid() {
		// because SearchExistingFileSchema returns blob.Ref{}, nil when file is not found.
		return blob.Ref{}, nil
	}
	dupMap, err := c.FetchSchemaBlob(ctx, dupFileRef)
	if err != nil {
		return blob.Ref{}, fmt.Errorf("could not find existing file blob for wholeRef %q: %v", wholeRef, err)
	}
	fileMap.PopulateParts(dupMap.PartsSize(), dupMap.ByteParts())
	json, err := fileMap.JSON()
	if err != nil {
		return blob.Ref{}, fmt.Errorf("could not write file map for wholeRef %q: %v", wholeRef, err)
	}
	bref := blob.RefFromString(json)
	if bref == dupFileRef {
		// Unchanged (same filename, modtime, JSON serialization, etc)
		// Different signer (e.g. existing file has a sha1 signer, and
		// we're now using a sha224 signer) means we upload a new file schema.
		return dupFileRef, nil
	}
	sbr, err := c.ReceiveBlob(ctx, bref, strings.NewReader(json))
	if err != nil {
		return blob.Ref{}, err
	}
	return sbr.Ref, nil
}
