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
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
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

	// Size is the required size of the blob, in bytes. Zero means
	// an empty blob.
	Size uint32

	// Contents holds the blob's contents at offsets [0, Size). Upload
	// may read them more than once, and never closes them.
	Contents io.ReaderAt

	// Vivify optionally instructs the server to create a
	// permanode for this blob. If used, the blob should be a
	// "file" schema blob. This is typically used by
	// lesser-trusted clients (such a mobile phones) which don't
	// have rights to do signing directly.
	Vivify bool
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

// NewUploadHandleFromString returns an upload handle to upload
// the provided string as a blob.
func NewUploadHandleFromString(data string) *UploadHandle {
	bref := blob.RefFromString(data)
	r := strings.NewReader(data)
	return &UploadHandle{BlobRef: bref, Size: uint32(len(data)), Contents: r}
}

// NewUploadHandleFromReader returns an upload handle to upload br, reading
// its contents from r into memory. It fails if r has more than
// MaxBlobSize bytes.
func NewUploadHandleFromReader(br blob.Ref, r io.Reader) (*UploadHandle, error) {
	data, err := io.ReadAll(io.LimitReader(r, constants.MaxBlobSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > constants.MaxBlobSize {
		return nil, fmt.Errorf("client: blob %v is bigger than the max blob size of %d bytes", br, constants.MaxBlobSize)
	}
	return &UploadHandle{BlobRef: br, Size: uint32(len(data)), Contents: bytes.NewReader(data)}, nil
}

// TODO(bradfitz): delete most of this. use new perkeep.org/pkg/blobserver/protocol types instead
// of a map[string]interface{}.
func (c *Client) responseJSONMap(requestName string, resp *http.Response) (map[string]any, error) {
	if resp.StatusCode != 200 {
		c.printf("After %s request, failed to JSON from response; status code is %d", requestName, resp.StatusCode)
		io.Copy(os.Stderr, resp.Body)
		return nil, fmt.Errorf("after %s request, HTTP response code is %d; no JSON to parse", requestName, resp.StatusCode)
	}
	jmap := make(map[string]any)
	if err := httputil.DecodeJSON(resp, &jmap); err != nil {
		return nil, err
	}
	return jmap, nil
}

// StatBlobs implements blobserver.BlobStatter. Blobs in the client's
// HaveCache are reported without asking the server; it asks about the
// rest many at a time.
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

	// Ask about up to statBatchSize blobs per request, a few requests at
	// a time, calling fn serially.
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(statBatchConcurrency)
	var (
		mu    sync.Mutex
		fnErr error // first error from fn, after which it's not called
	)
	for batch := range slices.Chunk(needStat, statBatchSize) {
		g.Go(func() error {
			return c.doStat(ctx, batch, 0, true, func(sb blob.SizedRef) error {
				c.haveCache.NoteBlobExists(sb.Ref, sb.Size)
				mu.Lock()
				defer mu.Unlock()
				if fnErr == nil {
					fnErr = fn(sb)
				}
				return fnErr
			})
		})
	}
	return g.Wait()
}

// StatBlobs asks about up to statBatchSize blobs per request (the most the
// server's stat handler accepts), with up to statBatchConcurrency requests
// at once.
const (
	statBatchSize        = 1000
	statBatchConcurrency = 4
)

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

// check returns an error if h can't be right: a Size of 0 for a ref that
// isn't of the empty blob (which most likely means Size isn't set), a Size
// over MaxBlobSize, or no Contents.
func (h *UploadHandle) check() error {
	if !h.BlobRef.Valid() {
		return errors.New("client: UploadHandle has no valid BlobRef")
	}
	if h.Size > constants.MaxBlobSize {
		return fmt.Errorf("client: UploadHandle for %v has Size %d, over the max blob size of %d", h.BlobRef, h.Size, constants.MaxBlobSize)
	}
	if h.Size == 0 {
		hash := h.BlobRef.Hash()
		if hash == nil {
			return fmt.Errorf("client: UploadHandle for %v has an unsupported hash", h.BlobRef)
		}
		if blob.RefFromHash(hash) != h.BlobRef {
			return fmt.Errorf("client: UploadHandle for %v has Size 0, but that's not the empty blob; is Size unset?", h.BlobRef)
		}
		return nil
	}
	if h.Contents == nil {
		return fmt.Errorf("client: UploadHandle for %v has no Contents", h.BlobRef)
	}
	return nil
}

// contents returns a new reader of h's contents.
func (h *UploadHandle) contents() io.Reader {
	return io.NewSectionReader(h.Contents, 0, int64(h.Size))
}

// UploadIfMissing is like Upload, but first asks the server whether it
// already has the blob, and if so, doesn't upload it (unless h.Vivify is
// set). The extra round trip is only worth it for big blobs the server may
// already have; to check many blobs, use one StatBlobs call for all of
// them, and upload those it doesn't report.
func (c *Client) UploadIfMissing(ctx context.Context, h *UploadHandle) (*PutResult, error) {
	if err := h.check(); err != nil {
		return nil, err
	}
	if !h.Vivify {
		sb, err := blobserver.StatBlob(ctx, c, h.BlobRef)
		if err == nil {
			return &PutResult{BlobRef: sb.Ref, Size: sb.Size, Skipped: true}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return c.Upload(ctx, h)
}

// Upload uploads a blob, as described by the provided UploadHandle
// parameters. It doesn't check whether the server already has the blob,
// other than in the client's HaveCache, if any; see UploadIfMissing.
func (c *Client) Upload(ctx context.Context, h *UploadHandle) (*PutResult, error) {
	errorf := func(msg string, arg ...any) (*PutResult, error) {
		err := fmt.Errorf(msg, arg...)
		c.printf("%v", err)
		return nil, err
	}

	if err := h.check(); err != nil {
		return nil, err
	}
	bodySize := int64(h.Size)

	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	c.stats.UploadRequests.Bytes += bodySize
	c.statsMutex.Unlock()

	pr := &PutResult{BlobRef: h.BlobRef, Size: uint32(bodySize)}

	if c.sto != nil {
		_, err := blobserver.Receive(ctx, c.sto, h.BlobRef, h.contents())
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
	pfx, err := c.prefix()
	if err != nil {
		return nil, err
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
		_, err = io.Copy(part, h.contents())
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
	req.Body = io.NopCloser(pipeReader)
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

	if resp.StatusCode != http.StatusOK {
		return errorf("invalid http response %d in upload response", resp.StatusCode)
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
		return nil, fmt.Errorf("cannot discover if server has legacy sha1: %w", err)
	}
	td := hashutil.NewTrackDigestReader(contents)
	td.DoLegacySHA1 = hasLegacySHA1
	if _, err := io.Copy(io.Discard, td); err != nil {
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
		return blob.Ref{}, fmt.Errorf("could not find existing file blob for wholeRef %q: %w", wholeRef, err)
	}
	fileMap.PopulateParts(dupMap.PartsSize(), dupMap.ByteParts())
	json, err := fileMap.JSON()
	if err != nil {
		return blob.Ref{}, fmt.Errorf("could not write file map for wholeRef %q: %w", wholeRef, err)
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
