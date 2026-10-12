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

package serverinit_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/serverinit"
	_ "perkeep.org/pkg/sorted/leveldb" // Register metadata storage, as perkeepd does.
	"perkeep.org/pkg/types/serverconfig"
	"perkeep.org/pkg/webserver"

	"tailscale.com/tsnet"
)

type immichHTTPTestClient struct {
	client *http.Client
	apiURL string
}

func (c *immichHTTPTestClient) request(t *testing.T, method, path string, body io.Reader, contentType string, status int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, c.apiURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.do(t, req, status)
}

func (c *immichHTTPTestClient) do(t *testing.T, req *http.Request, status int) []byte {
	t.Helper()
	resp, err := c.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d; body: %s", req.Method, req.URL.Path, resp.StatusCode, status, data)
	}
	return data
}

func (c *immichHTTPTestClient) json(t *testing.T, method, path string, body any, status int) []byte {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return c.request(t, method, path, bytes.NewReader(data), "application/json", status)
}

func (c *immichHTTPTestClient) upload(t *testing.T, name string, original []byte) string {
	t.Helper()
	return c.uploadStatus(t, name, original, nil, http.StatusCreated)
}

func (c *immichHTTPTestClient) uploadStatus(t *testing.T, name string, original []byte, fields map[string]string, status int) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("assetData", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(original); err != nil {
		t.Fatal(err)
	}
	// Send timestamps after the file, as mobile background workers may do.
	values := map[string]string{
		"deviceId":       "http-test-phone",
		"deviceAssetId":  name,
		"fileCreatedAt":  "2026-10-12T03:04:05.123Z",
		"fileModifiedAt": "2026-10-12T04:05:06.123Z",
	}
	for key, value := range fields {
		values[key] = value
	}
	for key, value := range values {
		if err := form.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	response := c.request(t, http.MethodPost, "assets", &body, form.FormDataContentType(), status)
	var receipt struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response, &receipt); err != nil {
		t.Fatal(err)
	}
	wantStatus := "created"
	if status == http.StatusOK {
		wantStatus = "duplicate"
	}
	if receipt.ID == "" || receipt.Status != wantStatus {
		t.Fatalf("invalid upload receipt: %s", response)
	}
	got := c.request(t, http.MethodGet, "assets/"+receipt.ID+"/original", nil, "", http.StatusOK)
	if !bytes.Equal(got, original) {
		t.Fatalf("retrieved original differs: got %d bytes, want %d", len(got), len(original))
	}
	return receipt.ID
}

type immichHTTPSyncEvent struct {
	Type string `json:"type"`
	Ack  string `json:"ack"`
	Data struct {
		ID               string `json:"id"`
		Checksum         string `json:"checksum"`
		OriginalFileName string `json:"originalFileName"`
		Type             string `json:"type"`
	} `json:"data"`
}

func (c *immichHTTPTestClient) sync(t *testing.T) []immichHTTPSyncEvent {
	t.Helper()
	// Mobile requests metadata even when an upload has none. Its empty
	// snapshot must be checkpointed by the control event, not completion.
	data := c.json(t, http.MethodPost, "sync/stream", map[string]any{
		"types": []string{"AssetsV2", "AssetMetadataV1"},
	}, http.StatusOK)
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("sync stream is not newline terminated")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var events []immichHTTPSyncEvent
	for {
		var event immichHTTPSyncEvent
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event.Ack == "" {
			t.Fatalf("sync event missing acknowledgement: %+v", event)
		}
		events = append(events, event)
	}
	if len(events) == 0 || events[len(events)-1].Type != "SyncCompleteV1" {
		t.Fatalf("sync stream missing completion event: %+v", events)
	}
	return events
}

func checkImmichHTTPAsset(t *testing.T, events []immichHTTPSyncEvent, id, name string, original []byte) immichHTTPSyncEvent {
	t.Helper()
	var assets []immichHTTPSyncEvent
	for _, event := range events {
		switch event.Type {
		case "AssetV2":
			assets = append(assets, event)
		case "SyncAckV1", "SyncCompleteV1":
		default:
			t.Fatalf("unexpected sync event: %+v", event)
		}
	}
	if len(assets) != 1 {
		t.Fatalf("want one AssetV2, got %+v", events)
	}
	asset := assets[0].Data
	hash := sha1.Sum(original)
	checksum := base64.StdEncoding.EncodeToString(hash[:])
	if asset.ID != id || asset.OriginalFileName != name || asset.Type != "IMAGE" || asset.Checksum != checksum {
		t.Fatalf("unexpected AssetV2: %+v; want id %q, name %q, checksum %q", asset, id, name, checksum)
	}
	return assets[0]
}

func (c *immichHTTPTestClient) ack(t *testing.T, events []immichHTTPSyncEvent) {
	t.Helper()
	acks := make([]string, 0, len(events))
	for _, event := range events {
		acks = append(acks, event.Ack)
	}
	c.json(t, http.MethodPost, "sync/ack", map[string]any{"acks": acks}, http.StatusNoContent)
}

func TestImmichInstalledHTTP(t *testing.T) {
	// Authentication modes are process-global; these tests must not run in parallel.
	for _, test := range []struct {
		name        string
		corpus      bool
		perkeepAuth bool
	}{
		{name: "standalone/corpus", corpus: true},
		{name: "standalone/without corpus"},
		{name: "tailscale/corpus", corpus: true, perkeepAuth: true},
		{name: "tailscale/without corpus", perkeepAuth: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
			keyring, err := filepath.Abs(relativeRing)
			if err != nil {
				t.Fatal(err)
			}
			high := serverconfig.Config{
				Listen:             ":3179",
				Auth:               "userpass:perkeep:native-password",
				Identity:           "26F5ABDA",
				IdentitySecretRing: keyring,
				MemoryStorage:      true,
				MemoryIndex:        true,
				Immich: &serverconfig.Immich{
					Email:        "photos@example.com",
					Password:     "immich-only-password",
					MetadataFile: filepath.Join(t.TempDir(), "metadata.leveldb"),
				},
			}
			if test.perkeepAuth {
				// InstallHandlers configures the real process-global Tailscale
				// auth mode, not a test stub that trusts request headers.
				high.Auth = "tailscale:full-access-to-tailnet"
				high.Immich.Email = ""
				high.Immich.Password = ""
			}
			if !test.corpus {
				if err := json.Unmarshal([]byte("false"), &high.CopyIndexToMemory); err != nil {
					t.Fatal(err)
				}
			}
			data, err := json.Marshal(high)
			if err != nil {
				t.Fatal(err)
			}
			config, err := serverinit.Load(data)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			var trustedTSNet atomic.Bool
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if trustedTSNet.Load() {
					// Simulate the trusted listener's context without starting
					// a daemon. No client-controlled header can set this gate.
					r = r.WithContext(webserver.TSNetCtxKey.WithValue(r.Context(), new(tsnet.Server)))
				}
				mux.ServeHTTP(w, r)
			}))
			var shutdown io.Closer
			t.Cleanup(func() {
				testServer.Close()
				if shutdown != nil {
					if err := shutdown.Close(); err != nil {
						t.Errorf("server shutdown: %v", err)
					}
				}
			})
			shutdown, err = config.InstallHandlers(mux, testServer.URL)
			if err != nil {
				t.Fatal(err)
			}
			// Match daemon startup: the index must be able to verify upload claims.
			if err := config.UploadPublicKey(context.Background()); err != nil {
				t.Fatal(err)
			}
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			client := testServer.Client()
			client.Jar = jar
			client.Timeout = 15 * time.Second
			t.Cleanup(client.CloseIdleConnections)
			api := &immichHTTPTestClient{client: client, apiURL: testServer.URL + "/immich/api/"}

			api.request(t, http.MethodGet, "users/me", nil, "", http.StatusUnauthorized)
			loginFields := map[string]string{"email": "arbitrary@example.net", "password": "not-a-perkeep-password"}
			if test.perkeepAuth {
				api.request(t, http.MethodGet, "server/ping", nil, "", http.StatusUnauthorized)
				api.json(t, http.MethodPost, "auth/login", loginFields, http.StatusUnauthorized)
				for _, endpoint := range []struct{ method, path string }{
					{http.MethodGet, "server/ping"},
					{http.MethodPost, "auth/login"},
				} {
					body, err := json.Marshal(loginFields)
					if err != nil {
						t.Fatal(err)
					}
					req, err := http.NewRequest(endpoint.method, api.apiURL+endpoint.path, bytes.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Tailscale-User-Login", "owner@example.net")
					req.Header.Set("Tailscale-User-Name", "Owner")
					req.Header.Set("X-Tailscale-User", "owner@example.net")
					req.Header.Set("X-Forwarded-For", "100.64.0.1")
					api.do(t, req, http.StatusUnauthorized)
				}
				trustedTSNet.Store(true)
				api.request(t, http.MethodGet, "server/ping", nil, "", http.StatusOK)
				// Native auth is necessary but does not replace the Immich session.
				api.request(t, http.MethodGet, "users/me", nil, "", http.StatusUnauthorized)
			} else {
				api.request(t, http.MethodGet, "server/ping", nil, "", http.StatusOK)
				api.json(t, http.MethodPost, "auth/login", loginFields, http.StatusUnauthorized)
				loginFields = map[string]string{"email": high.Immich.Email, "password": high.Immich.Password}
			}
			login := api.json(t, http.MethodPost, "auth/login", loginFields, http.StatusCreated)
			var session struct {
				AccessToken string `json:"accessToken"`
				UserEmail   string `json:"userEmail"`
			}
			if err := json.Unmarshal(login, &session); err != nil || session.AccessToken == "" {
				t.Fatalf("invalid login response: %s, %v", login, err)
			}
			wantEmail := high.Immich.Email
			if test.perkeepAuth {
				wantEmail = "owner@perkeep.invalid"
			}
			if session.UserEmail != wantEmail {
				t.Fatalf("login userEmail = %q, want %q", session.UserEmail, wantEmail)
			}
			apiURL, err := url.Parse(api.apiURL)
			if err != nil {
				t.Fatal(err)
			}
			var sessionCookie *http.Cookie
			for _, cookie := range jar.Cookies(apiURL) {
				if cookie.Name == "immich_access_token" {
					sessionCookie = cookie
				}
			}
			if sessionCookie == nil || sessionCookie.Value != session.AccessToken {
				t.Fatal("login did not populate the cookie jar with the session token")
			}
			// All API helpers authenticate using the cookie jar, never a bearer header.
			var user struct {
				Email string `json:"email"`
			}
			if err := json.Unmarshal(api.request(t, http.MethodGet, "users/me", nil, "", http.StatusOK), &user); err != nil {
				t.Fatal(err)
			}
			if user.Email != wantEmail {
				t.Fatalf("users/me email = %q, want %q", user.Email, wantEmail)
			}

			nativeURL := testServer.URL + "/my-search/"
			parsed, err := url.Parse(nativeURL)
			if err != nil {
				t.Fatal(err)
			}
			for _, cookie := range jar.Cookies(parsed) {
				if strings.HasPrefix(cookie.Name, "immich_") {
					t.Error("Immich cookie is not scoped to its API prefix")
				}
			}
			// Even if a client sends it outside its scoped path, the cookie
			// must not grant access to a native Perkeep handler without
			// native authentication.
			trustedTSNet.Store(false)
			request, err := http.NewRequest(http.MethodGet, nativeURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.AddCookie(sessionCookie)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("Immich cookie granted native search access: status %d", response.StatusCode)
			}
			trustedTSNet.Store(test.perkeepAuth)

			img := image.NewRGBA(image.Rect(0, 0, 3, 2))
			img.Set(0, 0, color.RGBA{R: 255, A: 255})
			img.Set(1, 1, color.RGBA{G: 255, A: 255})
			var jpegBytes, pngBytes bytes.Buffer
			if err := jpeg.Encode(&jpegBytes, img, nil); err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(&pngBytes, img); err != nil {
				t.Fatal(err)
			}
			firstID := api.upload(t, "first.jpg", jpegBytes.Bytes())
			first := api.sync(t)
			firstAsset := checkImmichHTTPAsset(t, first, firstID, "first.jpg", jpegBytes.Bytes())
			// Streaming alone must not advance the checkpoint.
			checkImmichHTTPAsset(t, api.sync(t), firstID, "first.jpg", jpegBytes.Bytes())
			// Completion only is not an entity/snapshot acknowledgement.
			api.json(t, http.MethodPost, "sync/ack", map[string]any{
				"acks": []string{first[len(first)-1].Ack},
			}, http.StatusNoContent)
			checkImmichHTTPAsset(t, api.sync(t), firstID, "first.jpg", jpegBytes.Bytes())
			// Acknowledge the opaque snapshot control token as the mobile app does.
			api.ack(t, first)
			if events := api.sync(t); len(events) != 1 {
				t.Fatalf("acknowledged asset repeated: %+v", events)
			}
			var acks []struct {
				Type string `json:"type"`
				Ack  string `json:"ack"`
			}
			if err := json.Unmarshal(api.request(t, http.MethodGet, "sync/ack", nil, "", http.StatusOK), &acks); err != nil {
				t.Fatal(err)
			}
			var stored, metadataStored bool
			for _, ack := range acks {
				stored = stored || ack.Type == "AssetV2" && ack.Ack == firstAsset.Ack
				metadataStored = metadataStored || ack.Type == "AssetMetadataV1" && ack.Ack != ""
			}
			if !stored {
				t.Fatalf("AssetV2 checkpoint was not persisted: %+v", acks)
			}
			if !metadataStored {
				t.Fatalf("empty metadata snapshot was not checkpointed: %+v", acks)
			}
			secondID := api.upload(t, "second.png", pngBytes.Bytes())
			if secondID == firstID {
				t.Fatal("distinct original files received the same asset ID")
			}
			second := api.sync(t)
			secondAsset := checkImmichHTTPAsset(t, second, secondID, "second.png", pngBytes.Bytes())
			if secondAsset.Ack == firstAsset.Ack {
				t.Fatal("incremental upload did not advance the asset checkpoint")
			}
			api.ack(t, second)
			if events := api.sync(t); len(events) != 1 {
				t.Fatalf("incremental acknowledged asset repeated: %+v", events)
			}
			if test.perkeepAuth {
				// The same valid session must not cache native authentication.
				// Revoke the listener context, keeping the cookie jar intact.
				trustedTSNet.Store(false)
				api.request(t, http.MethodGet, "server/ping", nil, "", http.StatusUnauthorized)
				api.json(t, http.MethodPost, "auth/login", loginFields, http.StatusUnauthorized)
				api.request(t, http.MethodGet, "users/me", nil, "", http.StatusUnauthorized)
				api.json(t, http.MethodPost, "sync/stream", map[string]any{
					"types": []string{"AssetsV2", "AssetMetadataV1"},
				}, http.StatusUnauthorized)
				api.request(t, http.MethodGet, "sync/ack", nil, "", http.StatusUnauthorized)
				api.json(t, http.MethodPost, "sync/ack", map[string]any{"acks": []string{secondAsset.Ack}}, http.StatusUnauthorized)
				api.request(t, http.MethodPost, "assets", nil, "", http.StatusUnauthorized)
				api.request(t, http.MethodGet, "assets/"+secondID+"/original", nil, "", http.StatusUnauthorized)
				// Restoring native authentication permits the original session
				// again and leaves its incremental checkpoint unchanged.
				trustedTSNet.Store(true)
				api.request(t, http.MethodGet, "users/me", nil, "", http.StatusOK)
				if events := api.sync(t); len(events) != 1 {
					t.Fatalf("revoked requests changed the checkpoint: %+v", events)
				}
			}
		})
	}
}

func TestImmichInstalledHTTPMetadataRecovery(t *testing.T) {
	// The native blob store and index must survive loss of the adapter's
	// metadata, both with and without an in-memory search corpus.
	for _, corpus := range []bool{true, false} {
		name := "corpus"
		if !corpus {
			name = "without corpus"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
			keyring, err := filepath.Abs(relativeRing)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			blobDir := filepath.Join(root, "blobs")
			// Other serverinit tests suppress generated-directory creation.
			// Provide real filesystem roots explicitly for this disk server.
			if err := os.MkdirAll(filepath.Join(blobDir, "cache"), 0700); err != nil {
				t.Fatal(err)
			}
			high := serverconfig.Config{
				Listen:             ":3179",
				Auth:               "userpass:perkeep:native-password",
				Identity:           "26F5ABDA",
				IdentitySecretRing: keyring,
				BlobPath:           blobDir,
				LevelDB:            filepath.Join(root, "index.leveldb"),
				Immich: &serverconfig.Immich{
					Email:    "photos@example.com",
					Password: "immich-only-password",
				},
			}
			if !corpus {
				if err := json.Unmarshal([]byte("false"), &high.CopyIndexToMemory); err != nil {
					t.Fatal(err)
				}
			}
			start := func(metadataFile string) (*immichHTTPTestClient, func()) {
				t.Helper()
				high.Immich.MetadataFile = metadataFile
				data, err := json.Marshal(high)
				if err != nil {
					t.Fatal(err)
				}
				config, err := serverinit.Load(data)
				if err != nil {
					t.Fatal(err)
				}
				// Schema uploads synchronously replicate to the native index.
				// Disable the unrelated asynchronous sync worker: its current
				// shutdown does not release the persistent queue's lock.
				prefixes := config.Export_Obj()["prefixes"].(map[string]any)
				syncArgs := prefixes["/sync/"].(map[string]any)["handlerArgs"].(map[string]any)
				syncArgs["idle"] = true
				// The UI thumbnail cache likewise has no shutdown hook.
				// It is unrelated to the durable native index under test.
				uiArgs := prefixes["/ui/"].(map[string]any)["handlerArgs"].(map[string]any)
				uiArgs["scaledImage"] = map[string]any{"type": "memory"}
				// No importers are exercised here. Their asynchronous startup
				// search can race global named-search keyword registration.
				delete(prefixes, "/importer/")
				mux := http.NewServeMux()
				testServer := httptest.NewServer(mux)
				client := testServer.Client()
				client.Timeout = 15 * time.Second
				client.Jar, err = cookiejar.New(nil)
				if err != nil {
					t.Fatal(err)
				}
				var shutdown io.Closer
				var closed bool
				stop := func() {
					if closed {
						return
					}
					closed = true
					testServer.Close()
					client.CloseIdleConnections()
					if shutdown != nil {
						if err := shutdown.Close(); err != nil {
							t.Errorf("server shutdown: %v", err)
						}
					}
				}
				t.Cleanup(stop)
				shutdown, err = config.InstallHandlers(mux, testServer.URL)
				if err != nil {
					t.Fatal(err)
				}
				if err := config.UploadPublicKey(context.Background()); err != nil {
					t.Fatal(err)
				}
				api := &immichHTTPTestClient{client: client, apiURL: testServer.URL + "/immich/api/"}
				api.json(t, http.MethodPost, "auth/login", map[string]string{
					"email": high.Immich.Email, "password": high.Immich.Password,
				}, http.StatusCreated)
				return api, stop
			}
			nativePermanode := func(api *immichHTTPTestClient) (blob.Ref, url.Values) {
				t.Helper()
				query := search.SearchQuery{
					Constraint: &search.Constraint{Permanode: &search.PermanodeConstraint{}},
					Limit:      -1,
					Sort:       search.Unsorted,
					Describe:   &search.DescribeRequest{Depth: 1},
				}
				body, err := json.Marshal(query)
				if err != nil {
					t.Fatal(err)
				}
				queryURL := strings.TrimSuffix(api.apiURL, "immich/api/") + "my-search/camli/search/query"
				req, err := http.NewRequest(http.MethodPost, queryURL, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.SetBasicAuth("perkeep", "native-password")
				var result search.SearchResult
				if err := json.Unmarshal(api.do(t, req, http.StatusOK), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Blobs) != 1 {
					t.Fatalf("native search found %d permanodes, want exactly one: %+v", len(result.Blobs), result.Blobs)
				}
				pn := result.Blobs[0].Blob
				if !pn.Valid() || result.Describe == nil {
					t.Fatalf("invalid native search result: %+v", result)
				}
				description := result.Describe.Meta.Get(pn)
				if description == nil || description.Permanode == nil {
					t.Fatalf("native search did not describe permanode %v", pn)
				}
				return pn, description.Permanode.Attr
			}

			img := image.NewRGBA(image.Rect(0, 0, 3, 2))
			img.Set(1, 1, color.RGBA{B: 255, A: 255})
			var original bytes.Buffer
			if err := png.Encode(&original, img); err != nil {
				t.Fatal(err)
			}
			api, stop := start(filepath.Join(t.TempDir(), "metadata.leveldb"))
			api.upload(t, "first.png", original.Bytes())
			firstPN, firstAttrs := nativePermanode(api)
			if firstAttrs.Get("title") != "first.png" || firstAttrs.Get("camliContent") == "" {
				t.Fatalf("incomplete original native permanode: %v", firstAttrs)
			}
			// Fully close the first daemon so the second must reopen the
			// durable index and storage, not reuse a live handler or corpus.
			stop()
			api, _ = start(filepath.Join(t.TempDir(), "fresh-metadata.leveldb"))
			if pn, attrs := nativePermanode(api); pn != firstPN || attrs.Encode() != firstAttrs.Encode() {
				t.Fatalf("native permanode changed across restart: %v, %v; want %v, %v", pn, attrs, firstPN, firstAttrs)
			}
			// Only original bytes match: the new filename and timestamps
			// produce a different file schema, and the metadata cache is empty.
			recoveredID := api.uploadStatus(t, "renamed.png", original.Bytes(), map[string]string{
				"fileCreatedAt":  "2026-10-13T07:08:09.456Z",
				"fileModifiedAt": "2026-10-14T08:09:10.456Z",
			}, http.StatusOK)
			if pn, attrs := nativePermanode(api); pn != firstPN || attrs.Encode() != firstAttrs.Encode() {
				t.Fatalf("recovery created or modified native permanode: %v, %v; want %v, %v", pn, attrs, firstPN, firstAttrs)
			}
			checkImmichHTTPAsset(t, api.sync(t), recoveredID, "renamed.png", original.Bytes())
		})
	}
}
