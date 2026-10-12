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
	"encoding/hex"
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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/serverinit"
	_ "perkeep.org/pkg/sorted/leveldb" // Register the native index backend, as perkeepd does.
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
					Email:    "photos@example.com",
					Password: "immich-only-password",
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
			thumbnail := api.request(t, http.MethodGet, "assets/"+firstID+"/thumbnail", nil, "", http.StatusOK)
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(thumbnail)); err != nil || cfg.Width != 3 || cfg.Height != 2 {
				t.Fatalf("thumbnail dimensions = %+v, %v; want 3x2", cfg, err)
			}
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
			if duplicateID := api.uploadStatus(t, "renamed.jpg", jpegBytes.Bytes(), nil, http.StatusOK); duplicateID != firstID {
				t.Fatalf("duplicate ID = %q, want %q", duplicateID, firstID)
			}
			if events := api.sync(t); len(events) != 1 {
				t.Fatalf("duplicate upload repeated acknowledged assets: %+v", events)
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

func TestImmichInstalledHTTPNativeRecovery(t *testing.T) {
	// Both a cold restart and an index rebuilt from only native blobs must
	// preserve the catalog, session and checkpoints, with or without a corpus.
	for _, corpus := range []bool{true, false} {
		name := "corpus"
		if !corpus {
			name = "without corpus"
		}
		t.Run(name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("CAMLI_CONFIG_DIR", configDir)
			keyring, err := filepath.Abs(relativeRing)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			high := serverconfig.Config{
				Listen:             ":3179",
				Auth:               "userpass:perkeep:native-password",
				Identity:           "26F5ABDA",
				IdentitySecretRing: keyring,
				BlobPath:           filepath.Join(root, "blobs"),
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
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			start := func(reindex bool) (*immichHTTPTestClient, func()) {
				t.Helper()
				// Other serverinit tests suppress generated-directory creation.
				if err := os.MkdirAll(filepath.Join(high.BlobPath, "cache"), 0700); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(high)
				if err != nil {
					t.Fatal(err)
				}
				config, err := serverinit.Load(data)
				if err != nil {
					t.Fatal(err)
				}
				prefixes := config.Export_Obj()["prefixes"].(map[string]any)
				immichArgs := prefixes["/immich/api/"].(map[string]any)["handlerArgs"].(map[string]any)
				for _, key := range []string{"metadata", "legacyMetadata"} {
					if _, ok := immichArgs[key]; ok {
						t.Fatalf("fresh Immich config unexpectedly has %s: %v", key, immichArgs)
					}
				}
				// Schema uploads synchronously replicate to the native index.
				// Disable the unrelated asynchronous sync worker: its current
				// shutdown does not release the persistent queue's lock.
				syncArgs := prefixes["/sync/"].(map[string]any)["handlerArgs"].(map[string]any)
				syncArgs["idle"] = true
				// The UI thumbnail cache likewise has no shutdown hook.
				uiArgs := prefixes["/ui/"].(map[string]any)["handlerArgs"].(map[string]any)
				uiArgs["scaledImage"] = map[string]any{"type": "memory"}
				// No importer startup searches should race global keyword setup.
				delete(prefixes, "/importer/")
				config.SetReindex(reindex)
				mux := http.NewServeMux()
				testServer := httptest.NewServer(mux)
				client := testServer.Client()
				client.Timeout = 15 * time.Second
				client.Jar = jar // Reuse the actual session; never log in on recovery.
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
				// Registered after each root's TempDir cleanup, so Windows can
				// remove its LevelDB files only after all handlers have closed.
				t.Cleanup(stop)
				shutdown, err = config.InstallHandlers(mux, testServer.URL)
				if err != nil {
					t.Fatal(err)
				}
				if err := config.UploadPublicKey(context.Background()); err != nil {
					t.Fatal(err)
				}
				return &immichHTTPTestClient{client: client, apiURL: testServer.URL + "/immich/api/"}, stop
			}
			checkNoSideStorage := func(root string) {
				t.Helper()
				entries, err := os.ReadDir(root)
				if err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				if !reflect.DeepEqual(names, []string{"blobs", "index.leveldb"}) {
					t.Fatalf("unexpected side storage in server root: %v", names)
				}
				entries, err = os.ReadDir(filepath.Join(root, "blobs"))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.Name() == "cache" || entry.Name() == "GENERATION.dat" {
						continue
					}
					if _, err := blob.NewHashOfType(entry.Name()); err != nil || !entry.IsDir() {
						t.Errorf("unexpected non-native storage in blob root: %s", entry.Name())
					}
				}
				if entries, err := os.ReadDir(configDir); err != nil || len(entries) != 0 {
					t.Fatalf("fresh config directory acquired side storage: %v, %v", entries, err)
				}
			}
			type nativePhoto struct {
				Ref   blob.Ref
				Attrs url.Values
			}
			nativePhotos := func(api *immichHTTPTestClient, count int) map[string]nativePhoto {
				t.Helper()
				query := search.SearchQuery{
					// The account/device state has its own PN. Count only pinned
					// originals, not every permanode owned by the signer.
					Constraint: &search.Constraint{Permanode: &search.PermanodeConstraint{
						Attr: "camliContent", ValueMatches: &search.StringConstraint{
							ByteLength: &search.IntConstraint{Min: 1},
						},
					}},
					Limit: -1, Sort: search.Unsorted,
					Describe: &search.DescribeRequest{Depth: 1},
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
				if len(result.Blobs) != count || result.Describe == nil {
					t.Fatalf("native search found %d photo permanodes, want %d: %+v", len(result.Blobs), count, result)
				}
				photos := make(map[string]nativePhoto)
				for _, item := range result.Blobs {
					description := result.Describe.Meta.Get(item.Blob)
					if !item.Blob.Valid() || description == nil || description.Permanode == nil {
						t.Fatalf("native search did not describe photo %v", item.Blob)
					}
					attrs := description.Permanode.Attr
					id := attrs.Get("immichAssetId")
					recordRef, recordValid := blob.Parse(attrs.Get("immichRecord"))
					if id == "" || attrs.Get("immichChecksum") == "" || !recordValid {
						t.Fatalf("photo %v has no authoritative Immich record: %v", item.Blob, attrs)
					}
					recordURL := strings.TrimSuffix(api.apiURL, "immich/api/") + "bs/camli/" + recordRef.String()
					req, err := http.NewRequest(http.MethodGet, recordURL, nil)
					if err != nil {
						t.Fatal(err)
					}
					req.SetBasicAuth("perkeep", "native-password")
					data := api.do(t, req, http.StatusOK)
					hash := recordRef.Hash()
					if hash == nil {
						t.Fatalf("unsupported native record reference %v", recordRef)
					}
					hash.Write(data)
					if !recordRef.HashMatches(hash) {
						t.Fatalf("native record bytes do not match %v", recordRef)
					}
					var record struct {
						ID, Name, Checksum    string
						FileRef, PermanodeRef blob.Ref
						Sequence              uint64
					}
					if err := json.Unmarshal(data, &record); err != nil || record.ID != id ||
						record.PermanodeRef != item.Blob || record.FileRef.String() != attrs.Get("camliContent") ||
						record.Name != attrs.Get("title") || record.Checksum != attrs.Get("immichChecksum") || record.Sequence == 0 {
						t.Fatalf("native photo and record disagree: %v, %s, %v", attrs, data, err)
					}
					if _, exists := photos[id]; exists {
						t.Fatalf("multiple native photos have asset ID %q", id)
					}
					photos[id] = nativePhoto{item.Blob, attrs}
				}
				return photos
			}
			type original struct {
				name string
				data []byte
				id   string
				dto  []byte
			}
			var originals []original
			for i, name := range []string{"first.png", "second.png", "after-restart.png", "after-reindex.png"} {
				img := image.NewRGBA(image.Rect(0, 0, 3, 2))
				img.Set(1, 1, color.RGBA{B: uint8(50 * (i + 1)), A: 255})
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, img); err != nil {
					t.Fatal(err)
				}
				originals = append(originals, original{name: name, data: encoded.Bytes()})
			}
			var previousAck string
			upload := func(api *immichHTTPTestClient, original *original) {
				t.Helper()
				original.id = api.upload(t, original.name, original.data)
				original.dto = api.request(t, http.MethodGet, "assets/"+original.id, nil, "", http.StatusOK)
				events := api.sync(t)
				asset := checkImmichHTTPAsset(t, events, original.id, original.name, original.data)
				if asset.Ack == previousAck {
					t.Fatal("new upload did not advance the asset checkpoint")
				}
				previousAck = asset.Ack
				api.ack(t, events)
				if events := api.sync(t); len(events) != 1 {
					t.Fatalf("acknowledged assets repeated: %+v", events)
				}
			}
			api, stop := start(false)
			api.json(t, http.MethodPost, "auth/login", map[string]string{
				"email": high.Immich.Email, "password": high.Immich.Password,
			}, http.StatusCreated)
			upload(api, &originals[0])
			upload(api, &originals[1])
			owner := api.request(t, http.MethodGet, "users/me", nil, "", http.StatusOK)
			var user struct{ ID string }
			if err := json.Unmarshal(owner, &user); err != nil || user.ID == "" {
				t.Fatalf("invalid owner: %s, %v", owner, err)
			}

			for _, reindex := range []bool{false, true} {
				count := 2
				if reindex {
					count = 3
				}
				before := nativePhotos(api, count)
				for _, original := range originals[:count] {
					photo, ok := before[original.id]
					sum := sha1.Sum(original.data)
					if !ok || photo.Attrs.Get("title") != original.name ||
						photo.Attrs.Get("immichChecksum") != hex.EncodeToString(sum[:]) {
						t.Fatalf("original %q has no matching native photo: %+v", original.id, photo)
					}
				}
				checkpoints := api.request(t, http.MethodGet, "sync/ack", nil, "", http.StatusOK)
				if bytes.Equal(bytes.TrimSpace(checkpoints), []byte("[]")) {
					t.Fatal("fixture has no checkpoints to recover")
				}
				// Close all old handlers: recovery must not reuse a live index,
				// corpus, or in-memory Immich lookup table.
				stop()
				checkNoSideStorage(root)
				if reindex {
					// Restore only blobs into an entirely new server root. Do not
					// copy the native index, caches, or any adapter metadata.
					source, err := localdisk.New(high.BlobPath)
					if err != nil {
						t.Fatal(err)
					}
					root = t.TempDir()
					high.BlobPath = filepath.Join(root, "blobs")
					high.LevelDB = filepath.Join(root, "index.leveldb")
					if err := os.Mkdir(high.BlobPath, 0700); err != nil {
						t.Fatal(err)
					}
					destination, err := localdisk.New(high.BlobPath)
					if err != nil {
						t.Fatal(err)
					}
					ctx := context.Background()
					if err := blobserver.EnumerateAll(ctx, source, func(sb blob.SizedRef) error {
						reader, _, err := source.Fetch(ctx, sb.Ref)
						if err != nil {
							return err
						}
						defer reader.Close()
						_, err = blobserver.Receive(ctx, destination, sb.Ref, reader)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					if _, err := os.Stat(high.LevelDB); !os.IsNotExist(err) {
						t.Fatalf("restore unexpectedly has a native index: %v", err)
					}
				}
				api, stop = start(reindex)
				// No login: the original cookie must restore its native session.
				var recoveredUser struct{ ID string }
				if err := json.Unmarshal(api.request(t, http.MethodGet, "users/me", nil, "", http.StatusOK), &recoveredUser); err != nil || recoveredUser.ID != user.ID {
					t.Fatalf("recovered owner = %q, %v; want %q", recoveredUser.ID, err, user.ID)
				}
				if got := api.request(t, http.MethodGet, "sync/ack", nil, "", http.StatusOK); !bytes.Equal(got, checkpoints) {
					t.Fatalf("recovery changed checkpoints: %s; want %s", got, checkpoints)
				}
				if events := api.sync(t); len(events) != 1 {
					t.Fatalf("recovery replayed acknowledged assets: %+v", events)
				}
				for _, original := range originals[:count] {
					if got := api.request(t, http.MethodGet, "assets/"+original.id, nil, "", http.StatusOK); !bytes.Equal(got, original.dto) {
						t.Fatalf("recovery changed asset %q: %s; want %s", original.id, got, original.dto)
					}
					if got := api.request(t, http.MethodGet, "assets/"+original.id+"/original", nil, "", http.StatusOK); !bytes.Equal(got, original.data) {
						t.Fatalf("recovery changed original %q", original.id)
					}
				}
				// The new filename and dates give a different file schema, but
				// identical original bytes must retain the first catalog record.
				recoveredID := api.uploadStatus(t, "renamed.png", originals[0].data, map[string]string{
					"fileCreatedAt":  "2026-10-13T07:08:09.456Z",
					"fileModifiedAt": "2026-10-14T08:09:10.456Z",
				}, http.StatusOK)
				if recoveredID != originals[0].id {
					t.Fatalf("duplicate after recovery has ID %q, want %q", recoveredID, originals[0].id)
				}
				if got := api.request(t, http.MethodGet, "assets/"+recoveredID, nil, "", http.StatusOK); !bytes.Equal(got, originals[0].dto) {
					t.Fatalf("duplicate changed the first asset record: %s; want %s", got, originals[0].dto)
				}
				if after := nativePhotos(api, count); !reflect.DeepEqual(after, before) {
					t.Fatalf("recovery or duplicate changed native photos: %v; want %v", after, before)
				}
				if events := api.sync(t); len(events) != 1 {
					t.Fatalf("duplicate changed the recovered checkpoint: %+v", events)
				}
				// Exactly the newly uploaded asset should follow the durable
				// checkpoint; no old asset may be repeated or renumbered.
				upload(api, &originals[count])
				nativePhotos(api, count+1)
			}
			stop()
			checkNoSideStorage(root)
		})
	}
}
