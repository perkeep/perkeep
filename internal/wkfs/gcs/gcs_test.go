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

package gcs

import (
	"bytes"
	"compress/gzip"
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
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go4.org/jsonconfig"
	"go4.org/wkfs"
	"golang.org/x/oauth2"
)

// Package initializers run before the generated test main calls testing.Init.
// Register its flags now so the production testenv.InTest guard is effective
// during init, without changing production metadata or credential discovery.
var _ = func() bool {
	testing.Init()
	return true
}()

const testName = "/gcs/bucket/config"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeFS(f roundTripFunc) *gcsFS {
	fs := newFS(&http.Client{Transport: f}, "https://gcs.invalid")
	fs.retryDelay = 0
	return fs
}

func trackedHTTPFS(server *httptest.Server) (*gcsFS, *[]*trackedBody) {
	var bodies []*trackedBody
	base := server.Client().Transport
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := base.RoundTrip(r)
		if err == nil {
			body := &trackedBody{Reader: resp.Body, closeFunc: resp.Body.Close}
			bodies = append(bodies, body)
			resp.Body = body
		}
		return resp, err
	})}
	fs := newFS(client, server.URL)
	fs.retryDelay = 0
	return fs, &bodies
}

func response(status int, body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       body,
	}
}

func textResponse(status int, text string) *http.Response {
	return response(status, io.NopCloser(strings.NewReader(text)))
}

func metadataJSON(name string, size int) string {
	b, _ := json.Marshal(map[string]string{
		"name": name, "size": fmt.Sprint(size), "generation": "123",
	})
	return string(b)
}

func crc32cBase64(data []byte) string {
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.Checksum(data, crc32cTable))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func TestRegistration(t *testing.T) {
	wantErr := errors.New("credential discovery failed")
	for _, tt := range []struct {
		name  string
		onGCE bool
		err   error
	}{
		{"off GCE", false, nil},
		{"on GCE", true, nil},
		{"broken credentials", true, wantErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gateCalls, clientCalls, registrations, httpCalls := 0, 0, 0, 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				httpCalls++
				return nil, errors.New("unexpected request")
			})}
			var registered *gcsFS
			registerFS(func() bool {
				gateCalls++
				return tt.onGCE
			}, func(ctx context.Context, scopes ...string) (*http.Client, error) {
				clientCalls++
				if ctx != context.Background() || len(scopes) != 0 {
					t.Error("default client must use context.Background and no added scopes")
				}
				if tt.err != nil {
					return nil, tt.err
				}
				return client, nil
			}, func(prefix string, fs wkfs.FileSystem) {
				registrations++
				if prefix != "/gcs/" {
					t.Errorf("registered prefix = %q", prefix)
				}
				var ok bool
				registered, ok = fs.(*gcsFS)
				if !ok {
					t.Fatalf("registered filesystem type = %T", fs)
				}
			})
			if gateCalls != 1 {
				t.Errorf("gate called %d times, want 1", gateCalls)
			}
			if !tt.onGCE {
				if clientCalls != 0 || registrations != 0 || registered != nil {
					t.Errorf("off GCE: client calls = %d, registrations = %d", clientCalls, registrations)
				}
				return
			}
			if clientCalls != 1 || registrations != 1 || registered == nil {
				t.Fatalf("on GCE: client calls = %d, registrations = %d", clientCalls, registrations)
			}
			if registered.baseURL != "https://storage.googleapis.com" {
				t.Errorf("registered endpoint = %q", registered.baseURL)
			}
			if registered.retryDelay != 100*time.Millisecond {
				t.Errorf("initial GET backoff = %v, want 100ms", registered.retryDelay)
			}
			if tt.err == nil {
				if registered.client != client || registered.err != nil {
					t.Error("registered filesystem did not retain the credential client")
				}
			} else {
				for _, op := range operations {
					if err := op.run(registered); !errors.Is(err, wantErr) {
						t.Errorf("%s error = %v, want sticky credential failure", op.name, err)
					}
				}
			}
			if httpCalls != 0 {
				t.Error("registration or sticky errors made HTTP requests")
			}
		})
	}
}

type trackedBody struct {
	io.Reader
	closed    bool
	read      int
	closeErr  error
	closeFunc func() error
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackedBody) Close() error {
	b.closed = true
	if b.closeFunc != nil {
		return errors.Join(b.closeErr, b.closeFunc())
	}
	return b.closeErr
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadStatSeekAndEscaping(t *testing.T) {
	for _, tt := range []struct{ key, escaped string }{
		{"config", "config"},
		{"dir/雪 ?#%+%2F.json", "dir%2F%E9%9B%AA%20%3F%23%25+%252F.json"},
		{"a//../b", "a%2F%2F..%2Fb"},
		{"/leading/trailing/", "%2Fleading%2Ftrailing%2F"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			const data = "configuration"
			var bodies []*trackedBody
			fs := fakeFS(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.EscapedPath() != "/storage/v1/b/bucket/o/"+tt.escaped {
					t.Errorf("request = %s %s", r.Method, r.URL)
				}
				text := metadataJSON(tt.key, len(data))
				if r.URL.Query().Get("alt") == "media" {
					if got := r.URL.Query().Get("generation"); got != "123" {
						t.Errorf("generation = %q, want 123", got)
					}
					text = data
				} else if r.URL.RawQuery != "" {
					t.Errorf("unexpected metadata query: %s", r.URL.RawQuery)
				}
				body := &trackedBody{Reader: strings.NewReader(text)}
				bodies = append(bodies, body)
				return response(http.StatusOK, body), nil
			})
			name := "/gcs/bucket/" + tt.key
			for _, stat := range []func(string) (os.FileInfo, error){fs.Stat, fs.Lstat} {
				info, err := stat(name)
				if err != nil {
					t.Fatal(err)
				}
				checkInfo(t, info, path.Base(tt.key), int64(len(data)))
			}
			f, err := fs.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			if f.Name() != path.Base(tt.key) {
				t.Errorf("Name = %q", f.Name())
			}
			if info, err := f.Stat(); err != nil {
				t.Fatal(err)
			} else {
				checkInfo(t, info, path.Base(tt.key), int64(len(data)))
			}
			if got, err := io.ReadAll(f); err != nil || string(got) != data {
				t.Fatalf("Read = %q, %v", got, err)
			}
			if pos, err := f.Seek(-4, io.SeekEnd); err != nil || pos != int64(len(data)-4) {
				t.Fatalf("Seek = %d, %v", pos, err)
			}
			if got, err := io.ReadAll(f); err != nil || string(got) != "tion" {
				t.Fatalf("read after Seek = %q, %v", got, err)
			}
			buf := make([]byte, 6)
			if n, err := f.ReadAt(buf, 3); err != nil || n != 6 || string(buf) != "figura" {
				t.Fatalf("ReadAt = %q, %d, %v", buf, n, err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 4 {
				t.Errorf("requests = %d, want 4", len(bodies))
			}
			for i, body := range bodies {
				if !body.closed {
					t.Errorf("body %d not closed", i)
				}
			}
		})
	}
}

func checkInfo(t *testing.T, info os.FileInfo, name string, size int64) {
	t.Helper()
	if info.Name() != name || info.Size() != size || info.Mode() != 0644 ||
		info.IsDir() || !info.ModTime().IsZero() || info.Sys() != nil {
		t.Errorf("unexpected file info: %#v", info)
	}
}

func TestReadLimit(t *testing.T) {
	for _, tt := range []struct {
		name          string
		size, actual  int
		wantErr       bool
		wantMediaRead bool
	}{
		{"empty", 0, 0, false, true},
		{"exact limit", maxSize, maxSize, false, true},
		{"metadata too large", maxSize + 1, 0, true, false},
		{"media too large", 1, maxSize + 100, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var media *trackedBody
			fs := fakeFS(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("alt") == "" {
					return textResponse(200, metadataJSON("config", tt.size)), nil
				}
				media = &trackedBody{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), tt.actual))}
				return response(200, media), nil
			})
			f, err := fs.Open(testName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Open error = %v", err)
			}
			if err == nil {
				b, err := io.ReadAll(f)
				if err != nil || len(b) != tt.actual {
					t.Errorf("Read length = %d, error = %v", len(b), err)
				}
				f.Close()
			}
			if (media != nil) != tt.wantMediaRead {
				t.Fatalf("media requested = %v", media != nil)
			}
			if media != nil && (!media.closed || media.read > maxSize+1) {
				t.Errorf("media body closed = %v, bytes read = %d", media.closed, media.read)
			}
		})
	}
}

func TestInvalidMetadata(t *testing.T) {
	for _, text := range []string{"not JSON", `{"size":"bogus"}`, `{"size":"-1"}`, `{"size":1}`,
		metadataJSON(strings.Repeat("x", maxSize), 1)} {
		label := text
		if len(label) > 80 {
			label = "oversized metadata"
		}
		t.Run(label, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(text)}
			calls := 0
			fs := fakeFS(func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, body), nil
			})
			if _, err := fs.Open(testName); err == nil {
				t.Fatal("Open succeeded")
			}
			if !body.closed || calls != 1 {
				t.Errorf("metadata body closed = %v, calls = %d; want 1 terminal attempt", body.closed, calls)
			}
		})
	}
}

type finalErrorReader struct {
	text string
	err  error
}

func (r *finalErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.text)
	r.text = r.text[n:]
	if r.text == "" {
		return n, r.err
	}
	return n, nil
}

func TestMetadataReadErrorAfterValidJSON(t *testing.T) {
	wantErr := errors.New("failure after complete JSON")
	body := &trackedBody{Reader: &finalErrorReader{text: metadataJSON("config", 5), err: wantErr}}
	fs := fakeFS(func(*http.Request) (*http.Response, error) { return response(200, body), nil })
	if _, err := fs.Stat(testName); !errors.Is(err, wantErr) {
		t.Errorf("Stat error = %v, want read error after complete JSON", err)
	}
	if !body.closed {
		t.Error("metadata body not closed")
	}
}

func TestMediaSizeMismatch(t *testing.T) {
	for _, size := range []int{1, 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var bodies []*trackedBody
			fs := fakeFS(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("alt") == "" {
					return textResponse(200, metadataJSON("config", size)), nil
				}
				body := &trackedBody{Reader: strings.NewReader("config")}
				bodies = append(bodies, body)
				return response(200, body), nil
			})
			if _, err := fs.Open(testName); err == nil || !strings.Contains(err.Error(), "size mismatch") {
				t.Errorf("Open error = %v, want size mismatch", err)
			}
			wantCalls := 1
			if size > len("config") {
				wantCalls = 3
			}
			if len(bodies) != wantCalls {
				t.Errorf("media attempts = %d, want %d", len(bodies), wantCalls)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("media body not closed")
				}
			}
		})
	}
}

func TestReadCRC32C(t *testing.T) {
	// The standard CRC32C test vector has checksum 0xe3069283.
	const data = "123456789"
	for _, tt := range []struct {
		name, checksum string
		wantErr        string
	}{
		{"valid", "4waSgw==", ""},
		{"wrong", "AAAAAA==", "checksum mismatch"},
		{"invalid base64", "%%%", "invalid CRC32C"},
		{"empty", "", "invalid CRC32C"},
		{"short", "AAA=", "invalid CRC32C"},
		{"long", "AAAAAAA=", "invalid CRC32C"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(data)}
			mediaCalls := 0
			fs := fakeFS(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("alt") == "" {
					meta, _ := json.Marshal(map[string]string{
						"name": "config", "size": fmt.Sprint(len(data)), "crc32c": tt.checksum,
					})
					return textResponse(200, string(meta)), nil
				}
				mediaCalls++
				return response(200, body), nil
			})
			f, err := fs.Open(testName)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if got, err := io.ReadAll(f); err != nil || string(got) != data {
					t.Errorf("Read = %q, %v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Open error = %v, want %s", err, tt.wantErr)
			}
			if !body.closed || mediaCalls != 1 {
				t.Errorf("media body closed = %v, attempts = %d; checksums must not retry", body.closed, mediaCalls)
			}
		})
	}
}

func TestGzipMedia(t *testing.T) {
	data := strings.Repeat("configuration", 1000)
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(gw, data); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, alreadyTranscoded := range []bool{false, true} {
		t.Run(fmt.Sprintf("serverTranscoded=%v", alreadyTranscoded), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("alt") == "" {
					json.NewEncoder(w).Encode(map[string]string{
						"name": "config", "size": fmt.Sprint(compressed.Len()),
						"generation": "123", "contentEncoding": "gzip",
						"crc32c": crc32cBase64(compressed.Bytes()),
					})
					return
				}
				if alreadyTranscoded {
					io.WriteString(w, data)
				} else {
					w.Header().Set("Content-Encoding", "gzip")
					w.Write(compressed.Bytes())
				}
			}))
			defer server.Close()
			fs := newFS(server.Client(), server.URL)
			f, err := fs.Open(testName)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if got, err := io.ReadAll(f); err != nil || string(got) != data {
				t.Errorf("gzip read length = %d, error = %v", len(got), err)
			}
		})
	}
}

func TestRawGzipIntegrity(t *testing.T) {
	const data = "raw gzip content"
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(gw, data); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name       string
		checksum   string
		size       int
		decoded    bool
		serverGzip bool
		wantErr    string
	}{
		{"raw valid", crc32cBase64(compressed.Bytes()), compressed.Len(), false, false, ""},
		{"raw checksum mismatch", "AAAAAA==", compressed.Len(), false, false, "checksum mismatch"},
		{"raw invalid checksum", "%%%", compressed.Len(), false, false, "invalid CRC32C"},
		{"raw size mismatch", crc32cBase64(compressed.Bytes()), 1, false, false, "size mismatch"},
		{"decoded skip", "AAAAAA==", compressed.Len(), true, false, ""},
		{"server transcoded skip", "AAAAAA==", compressed.Len(), false, true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wireData := compressed.Bytes()
			if tt.decoded || tt.serverGzip {
				wireData = []byte(data)
			}
			body := &trackedBody{Reader: bytes.NewReader(wireData)}
			fs := fakeFS(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("alt") == "" {
					meta, _ := json.Marshal(map[string]string{
						"name": "config", "size": fmt.Sprint(tt.size),
						"contentEncoding": "gzip", "crc32c": tt.checksum,
					})
					return textResponse(200, string(meta)), nil
				}
				resp := response(200, body)
				if tt.decoded {
					resp.Uncompressed = true
				} else if tt.serverGzip {
					resp.Header.Set("X-Goog-Stored-Content-Encoding", "gzip")
				} else {
					resp.Header.Set("Content-Encoding", "gzip")
				}
				return resp, nil
			})
			f, err := fs.Open(testName)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if got, err := io.ReadAll(f); err != nil || !bytes.Equal(got, wireData) {
					t.Errorf("raw/decoded data differs: length = %d, error = %v", len(got), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Open error = %v, want %s", err, tt.wantErr)
			}
			if !body.closed {
				t.Error("media body not closed")
			}
		})
	}
}

func TestConfigParserRemoteRootAndLocalInclude(t *testing.T) {
	// go4's _fileobj resolver uses os.Stat, so included paths must still
	// resolve locally. Only the root config is remote in this supported case.
	localInclude := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(localInclude, []byte(`{"home":{"server":"https://perkeep.invalid"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := json.Marshal(map[string]any{
		"identity": "test-key-id",
		"servers":  []any{"_fileobj", localInclude},
	})
	if err != nil {
		t.Fatal(err)
	}
	const rootName = "/gcs/bucket/clients/root.json"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/storage/v1/b/bucket/o/clients%2Froot.json" {
			t.Errorf("unexpected config request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected config request", 400)
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			if r.URL.Query().Get("generation") != "123" {
				t.Error("config download was not generation-pinned")
			}
			w.Write(root)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"name": "clients/root.json", "size": fmt.Sprint(len(root)),
			"generation": "123", "crc32c": crc32cBase64(root),
		})
	}))
	defer server.Close()
	fs := newFS(server.Client(), server.URL)
	remoteOpens, localOpens := 0, 0
	cp := &jsonconfig.ConfigParser{
		Open: func(name string) (jsonconfig.File, error) {
			if strings.HasPrefix(name, "/gcs/") {
				remoteOpens++
				return fs.Open(name)
			}
			localOpens++
			return os.Open(name)
		},
	}
	got, err := cp.ReadFile(rootName)
	if err != nil {
		t.Fatal(err)
	}
	if remoteOpens != 1 || localOpens != 1 || got["identity"] != "test-key-id" {
		t.Fatalf("config opens remote/local = %d/%d, identity = %v", remoteOpens, localOpens, got["identity"])
	}
	servers, ok := got["servers"].(map[string]any)
	if !ok {
		t.Fatalf("expanded servers type = %T", got["servers"])
	}
	home, ok := servers["home"].(map[string]any)
	if !ok || home["server"] != "https://perkeep.invalid" {
		t.Errorf("expanded home server = %v", servers["home"])
	}
}

func upload(fs *gcsFS) error {
	w, err := fs.OpenFile(testName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("config")); err != nil {
		return err
	}
	return w.Close()
}

var operations = []struct {
	name string
	run  func(*gcsFS) error
}{
	{"stat", func(fs *gcsFS) error { _, err := fs.Stat(testName); return err }},
	{"lstat", func(fs *gcsFS) error { _, err := fs.Lstat(testName); return err }},
	{"open", func(fs *gcsFS) error { _, err := fs.Open(testName); return err }},
	{"upload", upload},
	{"delete", func(fs *gcsFS) error { return fs.Remove(testName) }},
	{"exclusive", func(fs *gcsFS) error {
		_, err := fs.OpenFile(testName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		return err
	}},
}

func TestHTTPErrorsAndBodyClosing(t *testing.T) {
	for _, op := range operations {
		for _, code := range []int{401, 403, 404, 500} {
			// EXCL intentionally permits a missing object at OpenFile.
			if op.name == "exclusive" && code == 404 {
				continue
			}
			t.Run(fmt.Sprintf("%s/%d", op.name, code), func(t *testing.T) {
				var bodies []*trackedBody
				calls := 0
				fs := fakeFS(func(*http.Request) (*http.Response, error) {
					calls++
					body := &trackedBody{Reader: strings.NewReader("denied: useful error detail")}
					bodies = append(bodies, body)
					return response(code, body), nil
				})
				err := op.run(fs)
				if err == nil {
					t.Fatal("operation succeeded")
				}
				if code == 404 {
					if !os.IsNotExist(err) || !errors.Is(err, os.ErrNotExist) {
						t.Errorf("error = %v, want os.ErrNotExist", err)
					}
				} else if !strings.Contains(err.Error(), "useful error detail") ||
					!strings.Contains(err.Error(), fmt.Sprint(code)) {
					t.Errorf("missing HTTP status or response body: %v", err)
				}
				wantCalls := 1
				if code == 500 && op.name != "upload" && op.name != "delete" {
					wantCalls = 3
				}
				if calls != wantCalls {
					t.Errorf("requests = %d, want %d", calls, wantCalls)
				}
				for _, body := range bodies {
					if !body.closed {
						t.Error("HTTP error body not closed")
					}
				}
			})
		}
	}
	// Also cover a successful metadata request followed by a media error.
	body := &trackedBody{Reader: strings.NewReader("media missing")}
	fs := fakeFS(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("alt") == "" {
			return textResponse(200, metadataJSON("config", 5)), nil
		}
		return response(404, body), nil
	})
	if _, err := fs.Open(testName); !os.IsNotExist(err) {
		t.Errorf("media error = %v", err)
	}
	if !body.closed {
		t.Error("media error body not closed")
	}
}

func TestHTTPErrorBodyLimit(t *testing.T) {
	var bodies []*trackedBody
	fs := fakeFS(func(*http.Request) (*http.Response, error) {
		body := &trackedBody{Reader: strings.NewReader(strings.Repeat("x", 10<<10))}
		bodies = append(bodies, body)
		return response(500, body), nil
	})
	if _, err := fs.Stat(testName); err == nil {
		t.Fatal("Stat succeeded")
	}
	if len(bodies) != 3 {
		t.Errorf("attempts = %d, want 3", len(bodies))
	}
	for _, body := range bodies {
		if !body.closed || body.read > 4096 {
			t.Errorf("error body closed = %v, bytes read = %d", body.closed, body.read)
		}
	}
}

func TestShortMediaResponse(t *testing.T) {
	var mediaCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") == "" {
			io.WriteString(w, metadataJSON("config", 5))
			return
		}
		mediaCalls.Add(1)
		w.Header().Set("Content-Length", "5")
		io.WriteString(w, "ab")
	}))
	defer server.Close()
	fs, bodies := trackedHTTPFS(server)
	if _, err := fs.Open(testName); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Open error = %v, want truncated-response error", err)
	}
	if mediaCalls.Load() != 3 || len(*bodies) != 4 {
		t.Errorf("media attempts = %d, response bodies = %d; want 3/4", mediaCalls.Load(), len(*bodies))
	}
	for _, body := range *bodies {
		if !body.closed {
			t.Error("short response body not closed")
		}
	}
}

func TestGETHTTPRetries(t *testing.T) {
	for _, codes := range [][2]int{{429, 503}, {408, 500}, {502, 504}} {
		t.Run(fmt.Sprint(codes), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				if call <= 2 {
					http.Error(w, "temporary storage error", codes[call-1])
					return
				}
				io.WriteString(w, metadataJSON("config", 6))
			}))
			defer server.Close()
			fs, bodies := trackedHTTPFS(server)
			if info, err := fs.Stat(testName); err != nil || info.Size() != 6 {
				t.Fatalf("Stat after HTTP retries = %v, %v", info, err)
			}
			if calls.Load() != 3 || len(*bodies) != 3 {
				t.Fatalf("requests/bodies = %d/%d, want 3/3", calls.Load(), len(*bodies))
			}
			for _, body := range *bodies {
				if !body.closed {
					t.Error("retry response body not closed")
				}
			}
		})
	}
}

func TestMediaRetrySameGeneration(t *testing.T) {
	for _, framed := range []bool{false, true} {
		t.Run(fmt.Sprintf("content-length=%v", framed), func(t *testing.T) {
			var metadataCalls, mediaCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("alt") == "" {
					metadataCalls.Add(1)
					json.NewEncoder(w).Encode(map[string]string{
						"name": "config", "size": "6", "generation": "123",
						"crc32c": crc32cBase64([]byte("config")),
					})
					return
				}
				if r.URL.Query().Get("generation") != "123" {
					t.Errorf("retry lost generation pin: %s", r.URL)
				}
				if mediaCalls.Add(1) == 1 {
					if framed {
						w.Header().Set("Content-Length", "6")
					} else {
						// A clean chunked EOF is still incomplete compared
						// with the pinned object's metadata size.
						w.(http.Flusher).Flush()
					}
					io.WriteString(w, "co")
					return
				}
				io.WriteString(w, "config")
			}))
			defer server.Close()
			fs, bodies := trackedHTTPFS(server)
			f, err := fs.Open(testName)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if got, err := io.ReadAll(f); err != nil || string(got) != "config" {
				t.Fatalf("retried media = %q, %v", got, err)
			}
			if metadataCalls.Load() != 1 || mediaCalls.Load() != 2 || len(*bodies) != 3 {
				t.Errorf("metadata/media/bodies = %d/%d/%d, want 1/2/3",
					metadataCalls.Load(), mediaCalls.Load(), len(*bodies))
			}
			for _, body := range *bodies {
				if !body.closed {
					t.Error("partial/retried media response body not closed")
				}
			}
		})
	}
}

func TestGETNetworkRetries(t *testing.T) {
	for _, cause := range []error{
		io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE,
		&net.DNSError{Err: "test timeout", IsTimeout: true},
		&net.DNSError{Err: "test temporary failure", IsTemporary: true},
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			calls := 0
			fs := fakeFS(func(*http.Request) (*http.Response, error) {
				calls++
				if calls <= 2 {
					return nil, cause
				}
				return textResponse(200, metadataJSON("config", 6)), nil
			})
			if _, err := fs.Stat(testName); err != nil || calls != 3 {
				t.Errorf("Stat after transient failures: error = %v, attempts = %d", err, calls)
			}
			// Exhaustion remains bounded and preserves the last underlying error.
			calls = 0
			fs = fakeFS(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, cause
			})
			if _, err := fs.Stat(testName); !errors.Is(err, cause) || calls != 3 {
				t.Errorf("exhausted retries: error = %v, attempts = %d", err, calls)
			}
		})
	}
	for _, cause := range []error{
		os.ErrNotExist, context.Canceled, errors.New("permanent error"),
		&net.DNSError{Err: "not found", IsNotFound: true},
		errors.Join(&httpError{403, "denied"}, io.ErrUnexpectedEOF),
	} {
		if retryableGET(cause) {
			t.Errorf("terminal error classified as retryable: %v", cause)
		}
	}
}

func TestMutationsNeverRetried(t *testing.T) {
	for _, op := range operations {
		if op.name != "upload" && op.name != "delete" {
			continue
		}
		for _, failure := range []string{"HTTP 503", "transport EOF", "body EOF"} {
			t.Run(op.name+"/"+failure, func(t *testing.T) {
				calls := 0
				var body *trackedBody
				fs := fakeFS(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method == http.MethodGet {
						t.Error("mutation unexpectedly issued a GET")
					}
					if failure == "transport EOF" {
						return nil, io.ErrUnexpectedEOF
					}
					code := 503
					body = &trackedBody{Reader: strings.NewReader("temporary failure")}
					if failure == "body EOF" {
						code = 200
						body.Reader = errorReader{io.ErrUnexpectedEOF}
					}
					return response(code, body), nil
				})
				if err := op.run(fs); err == nil || calls != 1 {
					t.Errorf("mutation error = %v, attempts = %d; want 1", err, calls)
				}
				if body != nil && !body.closed {
					t.Error("mutation response body not closed")
				}
			})
		}
	}
}

func TestTransportAndCredentialErrors(t *testing.T) {
	wantErr := errors.New("test transport or credential failure")
	for _, op := range operations {
		t.Run(op.name+"/transport", func(t *testing.T) {
			fs := fakeFS(func(*http.Request) (*http.Response, error) { return nil, wantErr })
			if err := op.run(fs); !errors.Is(err, wantErr) {
				t.Errorf("error = %v, want transport error", err)
			}
		})
		t.Run(op.name+"/credentials", func(t *testing.T) {
			// A sticky DefaultClient error must not dereference a nil client.
			fs := newFS(nil, "https://gcs.invalid")
			fs.err = wantErr
			if err := op.run(fs); !errors.Is(err, wantErr) {
				t.Errorf("error = %v, want credential error", err)
			}
		})
	}
	// A token-source failure is surfaced without making an HTTP request.
	client := oauth2.NewClient(context.Background(), failingTokenSource{wantErr})
	client.Transport.(*oauth2.Transport).Base = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("HTTP request made despite token failure")
		return nil, wantErr
	})
	if _, err := newFS(client, "https://gcs.invalid").Open(testName); !errors.Is(err, wantErr) {
		t.Errorf("token error = %v", err)
	}
}

type failingTokenSource struct{ err error }

func (s failingTokenSource) Token() (*oauth2.Token, error) { return nil, s.err }

func TestBodyReadAndCloseErrors(t *testing.T) {
	wantErr := errors.New("test body failure")
	for _, kind := range []string{"metadata", "media", "upload", "delete", "http error"} {
		for _, closeFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/close=%v", kind, closeFailure), func(t *testing.T) {
				text := metadataJSON("config", 4)
				body := &trackedBody{Reader: strings.NewReader(text)}
				if closeFailure {
					body.closeErr = wantErr
				} else {
					body.Reader = errorReader{wantErr}
				}
				fs := fakeFS(func(r *http.Request) (*http.Response, error) {
					if kind == "media" && r.URL.Query().Get("alt") == "" {
						return textResponse(200, text), nil
					}
					code := 200
					if kind == "http error" {
						code = 500
					}
					return response(code, body), nil
				})
				var err error
				switch kind {
				case "metadata", "media":
					_, err = fs.Open(testName)
				case "http error":
					_, err = fs.Stat(testName)
				case "upload":
					err = upload(fs)
				case "delete":
					err = fs.Remove(testName)
				}
				if !errors.Is(err, wantErr) {
					t.Errorf("error = %v, want body failure", err)
				}
				if !body.closed {
					t.Error("body not closed")
				}
			})
		}
	}
}

func TestFlagsAndNoopDirectories(t *testing.T) {
	fs := fakeFS(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected HTTP request")
		return nil, errors.New("unexpected request")
	})
	for _, flag := range []int{0, os.O_RDWR, os.O_WRONLY, os.O_CREATE,
		os.O_WRONLY | os.O_CREATE, os.O_WRONLY | os.O_CREATE | os.O_APPEND,
		os.O_WRONLY | os.O_CREATE | os.O_TRUNC | os.O_EXCL} {
		if _, err := fs.OpenFile(testName, flag, 0600); err == nil {
			t.Errorf("OpenFile(%d) succeeded", flag)
		}
	}
	for _, name := range []string{"/gcs/", "/gcs/bucket", "/gcs//config", "/gcs/bucket/", "/elsewhere/config"} {
		if _, err := fs.Open(name); !errors.Is(err, os.ErrInvalid) {
			t.Errorf("Open(%q) error = %v", name, err)
		}
	}
	if err := fs.MkdirAll("/gcs/bucket/dir", 0700); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTruncateExclusiveAndDelete(t *testing.T) {
	// Exercise real HTTP encoding and OAuth headers against a local-only server.
	const key = "dir/雪 ?#%+%2F.json"
	const name = "/gcs/bucket/" + key
	const objectPath = "/storage/v1/b/bucket/o/dir%2F%E9%9B%AA%20%3F%23%25+%252F.json"
	var mu sync.Mutex
	var content []byte
	exists := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
			http.Error(w, "missing token", 401)
			return
		}
		if r.Method == http.MethodPost {
			query := r.URL.Query()
			if r.URL.EscapedPath() != "/upload/storage/v1/b/bucket/o" ||
				query.Get("uploadType") != "media" || query.Get("name") != key ||
				r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("bad upload: %s, content-type %q", r.URL, r.Header.Get("Content-Type"))
			}
			if query.Get("ifGenerationMatch") == "0" && exists {
				http.Error(w, "precondition failed", 412)
				return
			}
			var err error
			content, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading upload: %v", err)
			}
			if r.ContentLength != int64(len(content)) {
				t.Errorf("Content-Length = %d, want %d", r.ContentLength, len(content))
			}
			exists = true
			io.WriteString(w, "{}")
			return
		}
		if r.URL.EscapedPath() != objectPath {
			t.Errorf("wrong object path: %s", r.URL)
		}
		if !exists {
			http.Error(w, "not found", 404)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("alt") == "media" {
				w.Write(content)
			} else {
				io.WriteString(w, metadataJSON(key, len(content)))
			}
		case http.MethodDelete:
			content, exists = nil, false
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	client := &http.Client{Transport: &oauth2.Transport{
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}),
		Base:   server.Client().Transport,
	}}
	fs := newFS(client, server.URL)
	const excl = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	const trunc = os.O_WRONLY | os.O_CREATE | os.O_TRUNC

	// Two opens can both see an absent object; only one upload may succeed.
	first, err := fs.OpenFile(name, excl, 0600)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fs.OpenFile(name, excl, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("winner")); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := first.Write(nil); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Write after Close = %v", err)
	}
	if _, err := second.Write([]byte("loser")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := second.Close(); !os.IsExist(err) {
			t.Fatalf("collision Close = %v, want os.ErrExist", err)
		}
	}
	if _, err := fs.OpenFile(name, excl, 0600); !os.IsExist(err) {
		t.Fatalf("existing OpenFile = %v, want os.ErrExist", err)
	}
	checkContents := func(want []byte) {
		t.Helper()
		f, err := fs.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		got, err := io.ReadAll(f)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("contents = %q, %v; want %q", got, err, want)
		}
	}
	checkContents([]byte("winner"))
	// Writes are deliberately not limited to the read cap.
	for _, data := range [][]byte{bytes.Repeat([]byte("x"), maxSize+1), []byte("short"), nil} {
		w, err := fs.OpenFile(name, trunc, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := w.Write(data); err != nil || n != len(data) {
			t.Fatalf("Write = %d, %v", n, err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if len(data) > maxSize {
			if info, err := fs.Stat(name); err != nil || info.Size() != int64(len(data)) {
				t.Fatalf("large written file Stat = %v, %v", info, err)
			}
		} else {
			checkContents(data)
		}
	}
	if err := fs.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove(name); !os.IsNotExist(err) {
		t.Errorf("second Remove = %v", err)
	}
}
