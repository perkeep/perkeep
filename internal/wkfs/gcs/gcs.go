/*
Copyright 2026 The Perkeep Authors

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

// Package gcs registers the /gcs/ well-known filesystem on Google Compute
// Engine. It is a small-file configuration adapter, not a general storage
// client. Reads are limited to 1 MiB, as in go4.org/wkfs/gcs.
//
// Requests use the Cloud Storage JSON API directly, avoiding the storage SDK.
package gcs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/compute/metadata"
	"go4.org/wkfs"
	"golang.org/x/oauth2/google"
	"tailscale.com/util/testenv"
)

const maxSize = 1 << 20

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

func init() {
	// Tests construct their own clients without probing metadata or credentials.
	if testenv.InTest() {
		return
	}
	registerFS(metadata.OnGCE, google.DefaultClient, wkfs.RegisterFS)
}

// registerFS retains go4's metadata.OnGCE gate, not env.OnGCE's stricter
// config-attribute gate. Its dependencies are injected for offline gate and
// credential-initialization tests, without registering global filesystems.
func registerFS(
	onGCE func() bool,
	defaultClient func(context.Context, ...string) (*http.Client, error),
	register func(string, wkfs.FileSystem),
) {
	if !onGCE() {
		return
	}
	client, err := defaultClient(context.Background())
	fs := newFS(client, "https://storage.googleapis.com")
	if err != nil {
		fs.err = fmt.Errorf("could not get http client for context: %w", err)
	}
	register("/gcs/", fs)
}

type gcsFS struct {
	client     *http.Client
	baseURL    string
	retryDelay time.Duration // initial GET backoff; tests may set it to zero
	err        error         // sticky credential initialization error
}

var _ wkfs.FileSystem = (*gcsFS)(nil)

// newFS separates transport and endpoint selection from registration, so tests
// do not need environment variables or production credential discovery.
func newFS(client *http.Client, baseURL string) *gcsFS {
	return &gcsFS{client: client, baseURL: strings.TrimRight(baseURL, "/"), retryDelay: 100 * time.Millisecond}
}

func (fs *gcsFS) parseName(name string) (bucket, object string, err error) {
	if fs.err != nil {
		return "", "", fs.err
	}
	if strings.HasPrefix(name, "/gcs/") {
		bucket, object, _ = strings.Cut(strings.TrimPrefix(name, "/gcs/"), "/")
	}
	if bucket == "" || object == "" {
		return "", "", &os.PathError{Op: "gcs", Path: name, Err: os.ErrInvalid}
	}
	return bucket, object, nil
}

func (fs *gcsFS) objectURL(name string) (string, error) {
	bucket, object, err := fs.parseName(name)
	if err != nil {
		return "", err
	}
	// An object name is one URI component, even when it contains slashes.
	// Do not clean paths or unescape names: '%' is an ordinary key character.
	return fs.baseURL + "/storage/v1/b/" + url.PathEscape(bucket) + "/o/" + url.PathEscape(object), nil
}

// request owns error response bodies; callers own successful response bodies.
func (fs *gcsFS) request(name, method, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, endpoint, body)
	if err == nil {
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/octet-stream")
		}
		var resp *http.Response
		resp, err = fs.client.Do(req)
		if err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return resp, nil
			}
			// Error messages can be useful, but need not be read without bound.
			msg, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
			closeErr := resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusNotFound:
				err = os.ErrNotExist
			case resp.StatusCode == http.StatusPreconditionFailed &&
				method == http.MethodPost && req.URL.Query().Get("ifGenerationMatch") == "0":
				err = os.ErrExist
			default:
				err = errors.Join(&httpError{resp.StatusCode, strings.TrimSpace(string(msg))}, readErr, closeErr)
			}
		}
	}
	return nil, &os.PathError{Op: method, Path: name, Err: err}
}

type httpError struct {
	code   int
	detail string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("gcs: HTTP %d %s: %s", e.code, http.StatusText(e.code), e.detail)
}

func retryableGET(err error) bool {
	var status *httpError
	if errors.As(err, &status) {
		switch status.code {
		case 408, 429, 500, 502, 503, 504:
			return true
		}
		// An auth/client error must not become retryable just because reading
		// its error response also failed.
		return false
	}
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) ||
		(errors.As(err, &network) && (network.Timeout() || network.Temporary()))
}

// readGET retries the entire bounded read, never appending an interrupted body.
// A single loop covers HTTP, body, and validation failures: at most three GETs.
// Writes and deletes deliberately do not use this helper.
func (fs *gcsFS) readGET(name, endpoint string, validate func(*http.Response, []byte) error) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		resp, err := fs.request(name, http.MethodGet, endpoint, nil)
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
			err = errors.Join(err, resp.Body.Close())
			if len(data) > maxSize {
				err = fmt.Errorf("gcs: response too large for %s", name)
			} else if err == nil && validate != nil {
				err = validate(resp, data)
			}
		}
		if err == nil {
			return data, nil
		}
		if attempt == 2 || !retryableGET(err) {
			return nil, err
		}
		time.Sleep(fs.retryDelay << attempt)
	}
}

type objectAttrs struct {
	Name            string  `json:"name"`
	Size            int64   `json:"size,string"`
	Generation      string  `json:"generation"`
	ContentEncoding string  `json:"contentEncoding"`
	CRC32C          *string `json:"crc32c"`
}

func (fs *gcsFS) attrs(name string) (*objectAttrs, error) {
	endpoint, err := fs.objectURL(name)
	if err != nil {
		return nil, err
	}
	var attrs objectAttrs
	_, err = fs.readGET(name, endpoint, func(_ *http.Response, data []byte) error {
		if err := json.Unmarshal(data, &attrs); err != nil {
			return err
		}
		if attrs.Size < 0 {
			return fmt.Errorf("gcs: negative size for %s", name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &attrs, nil
}

func (fs *gcsFS) Open(name string) (wkfs.File, error) {
	attrs, err := fs.attrs(name)
	if err != nil {
		return nil, err
	}
	if attrs.Size > maxSize {
		return nil, fmt.Errorf("file %s too large (%d bytes) for /gcs/ filesystem", name, attrs.Size)
	}
	endpoint, err := fs.objectURL(name)
	if err != nil {
		return nil, err
	}
	query := url.Values{"alt": {"media"}}
	// Read the generation whose size we checked, rather than a replacement.
	if attrs.Generation != "" {
		query.Set("generation", attrs.Generation)
	}
	data, err := fs.readGET(name, endpoint+"?"+query.Encode(), func(resp *http.Response, data []byte) error {
		return attrs.validate(name, resp, data)
	})
	if err != nil {
		return nil, err
	}
	return &file{Reader: bytes.NewReader(data), name: name, info: attrs.info()}, nil
}

func (attrs *objectAttrs) validate(name string, resp *http.Response, data []byte) error {
	// Size and CRC describe stored bytes, so skip their checks only when GCS
	// transcoded gzip or net/http actually decompressed it. Raw gzip responses
	// (Content-Encoding: gzip, Uncompressed: false) still require both checks.
	storedGzip := strings.EqualFold(attrs.ContentEncoding, "gzip") ||
		strings.EqualFold(resp.Header.Get("X-Goog-Stored-Content-Encoding"), "gzip")
	transcoded := resp.Uncompressed || (storedGzip && !strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip"))
	if !transcoded {
		if int64(len(data)) != attrs.Size {
			err := fmt.Errorf("gcs: size mismatch for %s: read %d bytes, want %d", name, len(data), attrs.Size)
			if int64(len(data)) < attrs.Size {
				return fmt.Errorf("%v: %w", err, io.ErrUnexpectedEOF)
			}
			return err
		}
		if attrs.CRC32C != nil {
			sum, err := base64.StdEncoding.DecodeString(*attrs.CRC32C)
			if err != nil || len(sum) != 4 {
				return fmt.Errorf("gcs: invalid CRC32C metadata for %s", name)
			}
			if crc32.Checksum(data, crc32cTable) != binary.BigEndian.Uint32(sum) {
				return fmt.Errorf("gcs: CRC32C checksum mismatch for %s", name)
			}
		}
	}
	return nil
}

func (fs *gcsFS) Stat(name string) (os.FileInfo, error) { return fs.Lstat(name) }

func (fs *gcsFS) Lstat(name string) (os.FileInfo, error) {
	attrs, err := fs.attrs(name)
	if err != nil {
		return nil, err
	}
	return attrs.info(), nil
}

func (*gcsFS) MkdirAll(string, os.FileMode) error { return nil }

func (fs *gcsFS) OpenFile(name string, flag int, perm os.FileMode) (wkfs.FileWriter, error) {
	bucket, object, err := fs.parseName(name)
	if err != nil {
		return nil, err
	}
	switch flag {
	case os.O_WRONLY | os.O_CREATE | os.O_EXCL:
	case os.O_WRONLY | os.O_CREATE | os.O_TRUNC:
	default:
		return nil, fmt.Errorf("unsupported OpenFile flag mode %d on Google Cloud Storage", flag)
	}
	exclusive := flag&os.O_EXCL != 0
	if exclusive {
		// Preserve the early error for existing files, but do not rely on this
		// check for exclusivity: the upload also has an atomic precondition.
		if _, err := fs.Stat(name); err == nil {
			return nil, os.ErrExist
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	query := url.Values{"uploadType": {"media"}, "name": {object}}
	if exclusive {
		query.Set("ifGenerationMatch", "0")
	}
	endpoint := fs.baseURL + "/upload/storage/v1/b/" + url.PathEscape(bucket) + "/o?" + query.Encode()
	return &writer{fs: fs, name: name, endpoint: endpoint}, nil
}

func (fs *gcsFS) Remove(name string) error {
	endpoint, err := fs.objectURL(name)
	if err != nil {
		return err
	}
	resp, err := fs.request(name, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	return discardAndClose(resp.Body)
}

func discardAndClose(body io.ReadCloser) error {
	_, err := io.Copy(io.Discard, body)
	return errors.Join(err, body.Close())
}

// writer buffers small configuration files until Close. Like the old adapter,
// writes have no size cap; maxSize applies only to reads.
type writer struct {
	fs       *gcsFS
	name     string
	endpoint string
	buf      bytes.Buffer
	closed   bool
	err      error
}

func (w *writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, os.ErrClosed
	}
	return w.buf.Write(p)
}

func (w *writer) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	resp, err := w.fs.request(w.name, http.MethodPost, w.endpoint, bytes.NewReader(w.buf.Bytes()))
	if err == nil {
		err = discardAndClose(resp.Body)
	}
	w.err = err
	w.buf = bytes.Buffer{}
	return err
}

type statInfo struct {
	name string
	size int64
}

func (attrs *objectAttrs) info() *statInfo { return &statInfo{name: attrs.Name, size: attrs.Size} }

func (*statInfo) IsDir() bool        { return false }
func (*statInfo) ModTime() time.Time { return time.Time{} }
func (*statInfo) Mode() os.FileMode  { return 0644 }
func (s *statInfo) Name() string     { return path.Base(s.name) }
func (s *statInfo) Size() int64      { return s.size }
func (*statInfo) Sys() interface{}   { return nil }

type file struct {
	*bytes.Reader
	name string
	info os.FileInfo
}

func (*file) Close() error                 { return nil }
func (f *file) Name() string               { return path.Base(f.name) }
func (f *file) Stat() (os.FileInfo, error) { return f.info, nil }
