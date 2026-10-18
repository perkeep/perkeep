/*
Copyright 2026 The Perkeep Authors.

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

package integration

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/types/camtypes"
)

// TestRunkeeperBrowser exercises the HTTP workflow used by the archive widget:
// ordinary streamed file upload, inspect, explicit start, and asynchronous
// status. It deliberately uses discovery's token rather than Basic auth after
// the initial discovery, and never uploads an archive to the importer itself.
func TestRunkeeperBrowser(t *testing.T) {
	w := archiveWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	base := strings.TrimRight(w.ServerBaseURL(), "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	initial := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/?camli.mode=config", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("testuser", "passTestWorld")
	var discovery camtypes.Discovery
	if err := json.Unmarshal(archiveBrowserResponse(t, initial, req, http.StatusOK), &discovery); err != nil {
		t.Fatal(err)
	}
	if discovery.AuthToken == "" || discovery.AuthToken == auth.OmitAuthToken {
		t.Fatal("userpass discovery did not supply an authentication token")
	}
	if discovery.ImporterRoot != "/manual-imports/" || discovery.UIDiscovery == nil ||
		discovery.UIRoot != "/browser-files/" || discovery.UploadHelper != "/browser-files/?camli.mode=uploadhelper" {
		t.Fatalf("unexpected discovery routes: %+v, UI %+v", discovery, discovery.UIDiscovery)
	}
	mode, err := auth.TokenOrNone(discovery.AuthToken)
	if err != nil {
		t.Fatal(err)
	}
	tokenTransport := &archiveBrowserTransport{base: transport, mode: mode}
	browser := &http.Client{Transport: tokenTransport, Timeout: 15 * time.Second}
	c, err := client.New(client.OptionServer(base), client.OptionNoExternalConfig(), client.OptionAuthMode(mode))
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(browser)
	defer c.Close()
	resolve := func(path string) string {
		t.Helper()
		root, err := url.Parse(base + "/")
		if err != nil {
			t.Fatal(err)
		}
		ref, err := url.Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		return root.ResolveReference(ref).String()
	}
	request := func(method, path, body string, code int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, resolve(path), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", base)
		}
		return archiveBrowserResponse(t, browser, req, code)
	}
	query := func(expr string) *search.SearchResult {
		t.Helper()
		result, err := c.Query(ctx, &search.SearchQuery{Expression: expr, Limit: -1})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	checkAccounts := func(want int) {
		t.Helper()
		if got := len(query("attr:camliNodeType:importerAccount").Blobs); got != want {
			t.Fatalf("accounts = %d, want %d", got, want)
		}
	}
	importerPath := discovery.ImporterRoot + "runkeeper"
	page := string(request(http.MethodGet, importerPath, "", http.StatusOK))
	archiveBrowserPageContains(t, page,
		`type="file"`, `accept=".zip,application/zip"`,
		`data-discovery-url="/?camli.mode=config"`,
		`data-archive-url="/manual-imports/runkeeper/archive"`,
		`data-account-base="/manual-imports/runkeeper/"`,
		`data-account-ref=""`, `data-file-ref=""`,
		`src="/manual-imports/runkeeper/archive.js"`)
	for _, bad := range []string{`value="newacct"`, "Add new account"} {
		if strings.Contains(page, bad) {
			t.Errorf("archive-only importer page contains %q", bad)
		}
	}
	if script := request(http.MethodGet, importerPath+"/archive.js", "", http.StatusOK); len(script) == 0 {
		t.Fatal("empty archive browser script")
	}
	checkAccounts(0)

	// A token, not a same-user localhost exemption, must authorize the
	// browser workflow. archiveWorld disables the base config's +localhost.
	badReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resolve(importerPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	badReq.Header.Set("Authorization", "Token invalid-browser-token")
	archiveBrowserResponse(t, initial, badReq, http.StatusUnauthorized)

	// Ordinary uploads accept these files; archive inspection rejects them
	// without leaving an empty importer account behind.
	badZIP := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(badZIP, []byte("not a ZIP archive"), 0600); err != nil {
		t.Fatal(err)
	}
	unknownZIP := filepath.Join(t.TempDir(), "unknown.zip")
	f, err := os.Create(unknownZIP)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	member, err := z.Create("unrelated.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(member, "not a Runkeeper export"); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	upload := func(filename string) blob.Ref {
		t.Helper()
		ref := archiveBrowserUpload(t, ctx, browser, resolve(discovery.UploadHelper), filename)
		// Verify uploadHelper returned a readable file schema, even for
		// archives that the importer will subsequently reject.
		fr, err := schema.NewFileReader(ctx, c, ref)
		if err != nil {
			t.Fatal(err)
		}
		n, copyErr := io.Copy(io.Discard, fr)
		closeErr := fr.Close()
		info, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}
		if copyErr != nil || closeErr != nil || n != info.Size() {
			t.Fatalf("uploaded file: %d bytes, read %v, close %v; want %d bytes", n, copyErr, closeErr, info.Size())
		}
		return ref
	}
	for _, filename := range []string{badZIP, unknownZIP} {
		ref := upload(filename)
		body := request(http.MethodPost, importerPath+"/archive",
			`{"action":"inspect","fileRef":"`+ref.String()+`"}`, http.StatusBadRequest)
		var failure struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &failure); err != nil || failure.Error == "" {
			t.Fatalf("invalid archive did not return a JSON error: %s (%v)", body, err)
		}
		checkAccounts(0)
	}

	fileRef := upload(runkeeperFixture(t))
	uploads := tokenTransport.uploads.Load()
	if uploads != 3 {
		t.Fatalf("multipart uploads = %d, want 3", uploads)
	}
	submit := func(action string) *camtypes.ArchiveImportResult {
		t.Helper()
		body := request(http.MethodPost, importerPath+"/archive",
			`{"action":"`+action+`","fileRef":"`+fileRef.String()+`"}`, http.StatusOK)
		var result camtypes.ArchiveImportResult
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if result.Identity.UserName != "runner" || result.Identity.Email != "runner@example.com" {
			t.Fatalf("%s identity = %+v", action, result.Identity)
		}
		return &result
	}
	inspection := submit("inspect")
	if inspection.AccountRef.Valid() || inspection.Created || inspection.Status != nil {
		t.Fatalf("inspection unexpectedly created an account: %+v", inspection)
	}
	checkAccounts(0)
	started := submit("start")
	accountRef := started.AccountRef
	if !accountRef.Valid() || !started.Created || started.Status == nil ||
		(started.Status.State != "running" && started.Status.State != "complete") {
		t.Fatalf("start did not accept a managed asynchronous import: %+v", started)
	}
	wait := func(unchanged int) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			body := request(http.MethodGet, importerPath+"/archive?account="+accountRef.String(), "", http.StatusOK)
			var status camtypes.ArchiveImportStatus
			if err := json.Unmarshal(body, &status); err != nil {
				t.Fatal(err)
			}
			if status.AccountRef != accountRef || status.FileRef != fileRef {
				t.Fatalf("status lost account or stored file identity: %+v", status)
			}
			if status.State != "running" {
				if status.State != "complete" || status.Error != "" || status.Progress.ItemsTotal != 4 ||
					status.Progress.ItemsDone != 4 || status.Progress.ItemsUnchanged != unchanged {
					t.Fatalf("completed status = %+v; want 4 items, %d unchanged", status, unchanged)
				}
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
	}
	wait(0)
	checkAccounts(1)
	activities := query("is:fitness").Blobs
	if len(activities) != 4 {
		t.Fatalf("activities = %d, want 4", len(activities))
	}
	claimCounts := make(map[blob.Ref]int)
	for _, activity := range activities {
		claims, err := c.GetClaims(ctx, &search.ClaimsRequest{Permanode: activity.Blob})
		if err != nil {
			t.Fatal(err)
		}
		claimCounts[activity.Blob] = len(claims.Claims)
	}
	// A fresh account-page GET provides all the state a reloaded browser
	// needs to follow status or retry without retaining the local File.
	page = string(request(http.MethodGet, importerPath+"/"+accountRef.String(), "", http.StatusOK))
	archiveBrowserPageContains(t, page,
		`data-account-ref="`+accountRef.String()+`"`,
		`data-file-ref="`+fileRef.String()+`"`,
		`id="archive-upload-form" hidden`, "Upload another ZIP")
	for _, bad := range []string{"Re-login", "Toggle auto", "Start Import"} {
		if strings.Contains(page, bad) {
			t.Errorf("archive-only account page contains %q", bad)
		}
	}
	inspection = submit("inspect")
	if inspection.AccountRef != accountRef || inspection.Created {
		t.Fatalf("inspection did not resolve the existing account: %+v", inspection)
	}
	repeated := submit("start")
	if repeated.AccountRef != accountRef || repeated.Created {
		t.Fatalf("retry did not reuse the existing account: %+v", repeated)
	}
	wait(4)
	checkAccounts(1)
	if got := len(query("is:fitness").Blobs); got != 4 {
		t.Fatalf("retry created duplicate activities: %d", got)
	}
	for ref, count := range claimCounts {
		claims, err := c.GetClaims(ctx, &search.ClaimsRequest{Permanode: ref})
		if err != nil {
			t.Fatal(err)
		}
		if len(claims.Claims) != count {
			t.Errorf("retry added activity claims on %s: %d -> %d", ref, count, len(claims.Claims))
		}
	}
	if got := tokenTransport.uploads.Load(); got != uploads {
		t.Fatalf("stored-file retry uploaded again: %d -> %d multipart requests", uploads, got)
	}
}

type archiveBrowserTransport struct {
	base    http.RoundTripper
	mode    auth.AuthMode
	uploads atomic.Int64
}

func (tr *archiveBrowserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	tr.mode.AddAuthHeader(req)
	if strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data;") {
		tr.uploads.Add(1)
	}
	return tr.base.RoundTrip(req)
}

func archiveBrowserResponse(t *testing.T, c *http.Client, req *http.Request, want int) []byte {
	t.Helper()
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s", req.Method, req.URL, res.StatusCode, want, body)
	}
	return body
}

// archiveBrowserUpload streams the multipart File from disk. Neither the
// request body nor the archive is assembled into an in-memory HTTP buffer.
func archiveBrowserUpload(t *testing.T, ctx context.Context, c *http.Client, endpoint, filename string) blob.Ref {
	t.Helper()
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	mw := multipart.NewWriter(writer)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, reader)
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	done := make(chan error, 1)
	go func() {
		err := func() error {
			part, err := mw.CreateFormFile("file", filepath.Base(filename))
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, f); err != nil {
				return err
			}
			return mw.Close()
		}()
		writer.CloseWithError(err)
		done <- err
	}()
	res, requestErr := c.Do(req)
	// Also release the writer if the server rejected the request early.
	reader.Close()
	writeErr := <-done
	if requestErr != nil {
		t.Fatal(requestErr)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || writeErr != nil {
		t.Fatalf("streamed upload = %d, writer %v: %s", res.StatusCode, writeErr, body)
	}
	var uploaded struct {
		Got []struct {
			FileName string   `json:"filename"`
			FormName string   `json:"formname"`
			FileRef  blob.Ref `json:"fileref"`
		} `json:"got"`
	}
	if err := json.Unmarshal(body, &uploaded); err != nil {
		t.Fatal(err)
	}
	if len(uploaded.Got) != 1 || !uploaded.Got[0].FileRef.Valid() ||
		uploaded.Got[0].FileName != filepath.Base(filename) || uploaded.Got[0].FormName != "file" {
		t.Fatalf("unexpected uploadHelper response: %s", body)
	}
	return uploaded.Got[0].FileRef
}

func archiveBrowserPageContains(t *testing.T, page string, want ...string) {
	t.Helper()
	for _, text := range want {
		if !strings.Contains(page, text) {
			t.Errorf("archive page missing %q:\n%s", text, page)
		}
	}
}
