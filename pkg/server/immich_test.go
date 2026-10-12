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

package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
	_ "perkeep.org/pkg/sorted/leveldb"
	"perkeep.org/pkg/test"
)

func newImmichTestHandler(t *testing.T) *ImmichHandler {
	return newImmichTestHandlerWithCorpus(t, true)
}

// Keep publication and indexing synchronous, like the normal ingestion route.
type immichTestStorage struct {
	*test.Fetcher
	index *index.Index
}

func (s *immichTestStorage) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return blob.SizedRef{}, err
	}
	sb, err := s.Fetcher.ReceiveBlob(ctx, br, bytes.NewReader(data))
	if err == nil {
		_, err = s.index.ReceiveBlob(ctx, br, bytes.NewReader(data))
	}
	return sb, err
}

func newImmichTestHandlerWithCorpus(t *testing.T, corpus bool) *ImmichHandler {
	t.Helper()
	signer, pub := newSigner(t)
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	sto := &immichTestStorage{Fetcher: new(test.Fetcher), index: idx}
	idx.InitBlobSource(sto)
	var inMemory *index.Corpus
	if corpus {
		var err error
		if inMemory, err = idx.KeepInMemory(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := blobserver.Receive(context.Background(), sto, blob.RefFromString(pub), strings.NewReader(pub)); err != nil {
		t.Fatal(err)
	}
	h := &ImmichHandler{
		storage: sto, signer: signer, metadata: sorted.NewMemoryKeyValue(),
		search: search.NewHandler(idx, index.NewOwner(signer.KeyIDLong(), blob.RefFromString(pub))),
		email:  "photo@example.com", password: "secret", prefix: "/immich/api/", maxUploadBytes: 32 << 30,
	}
	if inMemory != nil {
		h.search.SetCorpus(inMemory)
	}
	if err := h.initIdentity(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Close()
		idx.Close()
	})
	return h
}

func immichTestRequest(t *testing.T, h *ImmichHandler, token, method, p string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, h.prefix+p, body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func immichTestLogin(t *testing.T, h *ImmichHandler) string {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"email": h.email, "password": h.password})
	w := immichTestRequest(t, h, "", "POST", "auth/login", bytes.NewReader(data), "application/json")
	if w.Code != 201 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var out struct{ AccessToken string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.AccessToken == "" {
		t.Fatalf("login response: %s, %v", w.Body, err)
	}
	return out.AccessToken
}

func immichUploadBody(t *testing.T, data string, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	b := new(bytes.Buffer)
	mw := multipart.NewWriter(b)
	// Deliberately send the file before metadata.
	p, err := mw.CreateFormFile("assetData", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(p, data)
	all := map[string]string{
		"fileCreatedAt":  "2025-01-02T03:04:05.123Z",
		"fileModifiedAt": "2025-01-03T03:04:05.123Z",
	}
	for k, v := range fields {
		all[k] = v
	}
	for k, v := range all {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return b, mw.FormDataContentType()
}

func immichTestUpload(t *testing.T, h *ImmichHandler, token, data string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, ct := immichUploadBody(t, data, fields)
	return immichTestRequest(t, h, token, "POST", "assets", b, ct)
}

func immichUploadedID(t *testing.T, w *httptest.ResponseRecorder, status int) string {
	t.Helper()
	if w.Code != status {
		t.Fatalf("upload: %d %s; want %d", w.Code, w.Body, status)
	}
	var out struct{ ID, Status string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !immichValidUUID(out.ID) {
		t.Fatalf("upload response %s, %v", w.Body, err)
	}
	want := "created"
	if status == 200 {
		want = "duplicate"
	}
	if out.Status != want {
		t.Fatalf("upload status %q, want %q", out.Status, want)
	}
	return out.ID
}

func TestImmichLoginAndIsolation(t *testing.T) {
	h := newImmichTestHandler(t)
	for _, p := range []string{"server/ping", "server/version", "server/config", "server/features"} {
		w := immichTestRequest(t, h, "", "GET", p, nil, "")
		if w.Code != 200 {
			t.Errorf("%s = %d: %s", p, w.Code, w.Body)
		}
	}
	for _, p := range []string{"users/me", "sync/ack", "assets/invalid/original"} {
		w := immichTestRequest(t, h, "", "GET", p, nil, "")
		if w.Code != 401 {
			t.Errorf("%s allowed unauthenticated: %d", p, w.Code)
		}
	}
	for _, data := range []string{
		`{"email":"photo@example.com","password":"wrong"}`,
		`{"email":"other@example.com","password":"secret"}`,
	} {
		w := immichTestRequest(t, h, "", "POST", "auth/login", strings.NewReader(data), "application/json")
		if w.Code != 401 {
			t.Errorf("bad login: %d", w.Code)
		}
	}
	data := `{"email":"photo@example.com","password":"secret"}`
	w := immichTestRequest(t, h, "", "POST", "auth/login", strings.NewReader(data), "application/json")
	if w.Code != 201 {
		t.Fatal(w.Body)
	}
	var out struct{ AccessToken string }
	json.Unmarshal(w.Body.Bytes(), &out)
	cookies := w.Result().Cookies()
	if len(cookies) != 3 {
		t.Fatalf("cookies = %v", cookies)
	}
	for _, c := range cookies {
		if c.MaxAge <= 0 || !c.Expires.After(time.Now()) || c.Path != h.prefix {
			t.Errorf("native upload workers need a persistent, scoped cookie: %+v", c)
		}
	}
	for _, how := range []string{"cookie", "bearer", "user-token", "session-token"} {
		r := httptest.NewRequest("GET", h.prefix+"users/me", nil)
		switch how {
		case "cookie":
			for _, c := range cookies {
				r.AddCookie(c)
			}
		case "bearer":
			r.Header.Set("Authorization", "Bearer "+out.AccessToken)
		case "user-token":
			r.Header.Set("x-immich-user-token", out.AccessToken)
		case "session-token":
			r.Header.Set("x-immich-session-token", out.AccessToken)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Errorf("%s: %d %s", how, rec.Code, rec.Body)
		}
	}
	h.password = "changed"
	if rec := immichTestRequest(t, h, out.AccessToken, "GET", "users/me", nil, ""); rec.Code != 401 {
		t.Errorf("password change did not invalidate token: %d", rec.Code)
	}
}

func TestImmichPerkeepAuthentication(t *testing.T) {
	// Authentication is process-global. Do not run this test in parallel.
	vivifyPassword := "upload-only"
	auth.SetMode(&auth.UserPass{
		Username: "perkeep", Password: "native-secret", VivifyPass: &vivifyPassword,
	})
	t.Cleanup(func() { auth.SetMode(auth.None{}) })

	h := newImmichTestHandler(t)
	standaloneToken := immichTestLogin(t, h)
	h.perkeepAuth = true
	h.email = "owner@perkeep.invalid"
	h.password = ""

	request := func(password, token, method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, h.prefix+path, strings.NewReader(body))
		if password != "" {
			r.SetBasicAuth("perkeep", password)
		}
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "immich_access_token", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d, want %d; %s", method, path, w.Code, status, w.Body)
		}
		return w
	}
	// Normal authentication is required even for discovery and login. Limited
	// native credentials must not be promoted to full collection access.
	for _, password := range []string{"", "incorrect", vivifyPassword} {
		request(password, "", "GET", "server/ping", "", 401)
		request(password, "", "POST", "auth/login", `{"email":"any@example.com","password":"anything"}`, 401)
	}
	request("native-secret", "", "GET", "server/ping", "", 200)
	request("native-secret", "", "GET", "users/me", "", 401)
	request("native-secret", standaloneToken, "GET", "users/me", "", 401)

	var token string
	for _, body := range []string{
		`{"email":"any@example.com","password":"anything"}`,
		`{"email":"different@example.net","password":"different"}`,
		`{"email":"","password":""}`,
	} {
		w := request("native-secret", "", "POST", "auth/login", body, 201)
		var out struct {
			AccessToken string
			UserID      string
			UserEmail   string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.AccessToken == "" || out.UserID != h.userID() || out.UserEmail != h.email {
			t.Fatalf("login fields changed the single owner: %s", w.Body)
		}
		token = out.AccessToken
		request("native-secret", token, "GET", "users/me", "", 200)
	}
	// A valid Immich session never substitutes for current native auth.
	for _, password := range []string{"", "incorrect", vivifyPassword} {
		request(password, token, "GET", "users/me", "", 401)
		request(password, token, "POST", "assets", "", 401)
		request(password, token, "POST", "sync/ack", `{"acks":[]}`, 401)
	}
	if w := immichTestRequest(t, h, token, "GET", "users/me", nil, ""); w.Code != 401 {
		t.Fatalf("Immich bearer bypassed native authentication: %d", w.Code)
	}
	h.perkeepAuth = false
	h.password = "standalone-secret"
	request("", token, "GET", "users/me", "", 401)
}

func TestImmichUploadRoundTrip(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	const original = "original image bytes\x00\x01"
	id := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 201)
	again := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 200)
	if id != again {
		t.Fatalf("duplicate changed identity: %q vs %q", id, again)
	}
	a, err := h.asset(id)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(original))
	if a.Checksum != hex.EncodeToString(sum[:]) || a.Sequence != 1 {
		t.Fatalf("asset = %+v", a)
	}
	if a.FileCreatedAt.Format(time.RFC3339Nano) != "2025-01-02T03:04:05.123Z" {
		t.Fatal(a.FileCreatedAt)
	}
	w := immichTestRequest(t, h, token, "GET", "assets/"+id+"/original", nil, "")
	if w.Code != 200 || w.Body.String() != original {
		t.Fatalf("original: %d %q", w.Code, w.Body.String())
	}
	req := httptest.NewRequest("GET", h.prefix+"assets/"+id+"/original", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Range", "bytes=2-5")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 206 || w.Body.String() != original[2:6] {
		t.Fatalf("range: %d %q", w.Code, w.Body.String())
	}
	// Permanode is signed and searchable via an ordinary camliContent claim,
	// not merely an opaque media blob.
	rc, _, err := h.storage.Fetch(context.Background(), a.PermanodeRef)
	if err != nil {
		t.Fatal(err)
	}
	pn, _ := io.ReadAll(rc)
	rc.Close()
	vr := jsonsign.NewVerificationRequest(string(pn), h.storage)
	if _, err := vr.Verify(context.Background()); err != nil {
		t.Fatalf("permanode signature: %v", err)
	}
	if !strings.Contains(string(pn), `"camliType": "permanode"`) {
		t.Fatalf("not a permanode: %s", pn)
	}
	check := fmt.Sprintf(`{"assets":[{"id":"local","checksum":%q},{"id":"new","checksum":%q}]}`,
		base64.StdEncoding.EncodeToString(sum[:]), strings.Repeat("0", 40))
	w = immichTestRequest(t, h, token, "POST", "assets/bulk-upload-check", strings.NewReader(check), "application/json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"assetId":"`+id+`"`) ||
		!strings.Contains(w.Body.String(), `"action":"accept"`) {
		t.Fatalf("bulk check: %d %s", w.Code, w.Body)
	}
}

func TestImmichUploadValidation(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	for _, tt := range []struct {
		name, data string
		fields     map[string]string
	}{
		{"empty", "", nil},
		{"creation date", "image", map[string]string{"fileCreatedAt": "invalid"}},
		{"modification date", "image", map[string]string{"fileModifiedAt": ""}},
		{"duration", "image", map[string]string{"duration": "-1"}},
		{"favorite", "image", map[string]string{"isFavorite": "invalid"}},
		{"visibility", "image", map[string]string{"visibility": "unknown"}},
		{"metadata", "image", map[string]string{"metadata": "not-json"}},
		{"null metadata", "image", map[string]string{"metadata": "null"}},
		{"metadata value type", "image", map[string]string{"metadata": `[{"key":"mobile-app","value":"string"}]`}},
		{"metadata null value", "image", map[string]string{"metadata": `[{"key":"mobile-app","value":null}]`}},
		{"metadata missing key", "image", map[string]string{"metadata": `[{"value":{}}]`}},
		{"live video missing", "image", map[string]string{"livePhotoVideoId": "missing"}},
		{"filename", "image", map[string]string{"filename": "\x00"}},
		{"filename parent", "image", map[string]string{"filename": "../"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := immichTestUpload(t, h, token, tt.data, tt.fields)
			if w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	for _, checksum := range []string{"bogus", strings.Repeat("0", 40)} {
		b, ct := immichUploadBody(t, "checksum image", nil)
		r := httptest.NewRequest("POST", h.prefix+"assets", b)
		r.Header.Set("Content-Type", ct)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("x-immich-checksum", checksum)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("bad checksum: %d %s", w.Code, w.Body)
		}
	}
	if _, err := h.metadata.Get("sequence"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("failed upload published sequence: %v", err)
	}
	h.maxUploadBytes = 32
	w := immichTestUpload(t, h, token, "too big", nil)
	if w.Code != 413 {
		t.Fatalf("size limit: %d %s", w.Code, w.Body)
	}
}

func TestImmichOversizeMetadataNeverAcknowledged(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	before := h.storage.(*immichTestStorage).NumBlobs()
	// HTML escaping performed by encoding/json expands this otherwise-small
	// multipart value beyond sorted.MaxValueSize.
	metadata := `[{"key":"mobile-app","value":{"description":"` + strings.Repeat("<", 12000) + `"}}]`
	w := immichTestUpload(t, h, token, "metadata image", map[string]string{"metadata": metadata})
	if w.Code != 400 {
		t.Fatalf("oversized metadata = %d %s", w.Code, w.Body)
	}
	if got := h.storage.(*immichTestStorage).NumBlobs(); got != before {
		t.Errorf("invalid metadata ingested blobs: before=%d after=%d", before, got)
	}
	for _, key := range []string{"sequence", "usage"} {
		if _, err := h.metadata.Get(key); !errors.Is(err, sorted.ErrNotFound) {
			t.Errorf("rejected metadata published %s: %v", key, err)
		}
	}
	sum := sha1.Sum([]byte("metadata image"))
	if _, err := h.metadata.Get("checksum|" + hex.EncodeToString(sum[:])); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("rejected metadata marked backed up: %v", err)
	}
}

func TestImmichImageThumbnailNotStored(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	im := image.NewRGBA(image.Rect(0, 0, 1000, 800))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	id := immichUploadedID(t, immichTestUpload(t, h, token, b.String(), map[string]string{"filename": "image.png"}), 201)
	before := h.storage.(*immichTestStorage).NumBlobs()
	w := immichTestRequest(t, h, token, "GET", "assets/"+id+"/thumbnail?size=thumbnail", nil, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("thumbnail = %d %s", w.Code, w.Body)
	}
	cfg, err := png.DecodeConfig(w.Body)
	if err != nil || cfg.Width > 720 || cfg.Height > 720 {
		t.Fatalf("thumbnail dimensions = %+v, %v", cfg, err)
	}
	if got := h.storage.(*immichTestStorage).NumBlobs(); got != before {
		t.Fatalf("thumbnail generation stored derived blobs: before=%d after=%d", before, got)
	}
	a, err := h.asset(id)
	if err != nil {
		t.Fatal(err)
	}
	a.Size = 65 << 20
	record, _ := json.Marshal(a)
	if err := h.metadata.Set("asset|"+id, string(record)); err != nil {
		t.Fatal(err)
	}
	w = immichTestRequest(t, h, token, "GET", "assets/"+id+"/thumbnail", nil, "")
	if w.Code != 404 {
		t.Fatalf("large asset must not enter thumbnail decoder: %d", w.Code)
	}
}

func TestImmichOriginalCannotExecuteOnPerkeepOrigin(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	id := immichUploadedID(t, immichTestUpload(t, h, token, "<script>alert(1)</script>",
		map[string]string{"filename": "untrusted.html"}), 201)
	w := immichTestRequest(t, h, token, "GET", "assets/"+id+"/original", nil, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("unsafe original response: %d %v", w.Code, w.Header())
	}
	w = immichTestRequest(t, h, token, "GET", "assets/"+id+"/video/playback", nil, "")
	if w.Code != 404 {
		t.Fatalf("non-video playback = %d", w.Code)
	}
}

func TestImmichThumbnailRejectsExcessiveJPEGHeaders(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	// APP0 markers are legal before SOF. A long stream of them used to be
	// buffered in full by ImageHandler's config probe before pixel admission.
	var body bytes.Buffer
	body.Write([]byte{0xff, 0xd8})
	payload := bytes.Repeat([]byte{'x'}, 65533)
	for range 260 {
		body.Write([]byte{0xff, 0xe0, 0xff, 0xff})
		body.Write(payload)
	}
	body.Write([]byte{0xff, 0xd9})
	if body.Len() <= 16<<20 {
		t.Fatal("fixture must exceed the header probe limit")
	}
	id := immichUploadedID(t, immichTestUpload(t, h, token, body.String(), nil), 201)
	w := immichTestRequest(t, h, token, "GET", "assets/"+id+"/thumbnail", nil, "")
	if w.Code != 404 {
		t.Fatalf("excessive JPEG header = %d, want unavailable thumbnail", w.Code)
	}
	// Failure to generate a thumbnail must not revoke the backed-up original.
	if _, err := h.asset(id); err != nil {
		t.Fatal(err)
	}
}

func TestImmichLivePhotoAndSidecar(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	videoID := immichUploadedID(t, immichTestUpload(t, h, token, "video bytes",
		map[string]string{"filename": "motion.mov", "visibility": "hidden", "duration": "1234"}), 201)
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, file := range []struct{ field, name, value string }{
		{"assetData", "photo.heic", "still bytes"},
		{"sidecarData", "photo.xmp", "<xmp/>"},
	} {
		p, err := mw.CreateFormFile(file.field, file.name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(p, file.value)
	}
	for key, value := range map[string]string{
		"fileCreatedAt": "2025-01-02T03:04:05Z", "fileModifiedAt": "2025-01-03T03:04:05Z",
		"livePhotoVideoId": videoID, "metadata": `[{"key":"mobile-app","value":{"id":"local-id"}}]`,
	} {
		mw.WriteField(key, value)
	}
	mw.Close()
	id := immichUploadedID(t, immichTestRequest(t, h, token, "POST", "assets", &b, mw.FormDataContentType()), 201)
	a, err := h.asset(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.LivePhotoVideoID != videoID || !a.SidecarRef.Valid() || len(a.Metadata) == 0 {
		t.Fatalf("association missing: %+v", a)
	}
	fr, err := schema.NewFileReader(context.Background(), h.storage, a.SidecarRef)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(fr)
	fr.Close()
	if err != nil || string(data) != "<xmp/>" {
		t.Fatalf("sidecar: %q, %v", data, err)
	}
	video, _ := h.asset(videoID)
	if video.Type != "VIDEO" || video.Visibility != "hidden" || video.Duration != 1234 {
		t.Fatalf("video = %+v", video)
	}
}

func TestImmichConcurrentDuplicateUploads(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	const n = 8
	results := make(chan *httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { results <- immichTestUpload(t, h, token, "concurrent image", nil) })
	}
	wg.Wait()
	close(results)
	ids := map[string]bool{}
	created := 0
	for w := range results {
		if w.Code == 201 {
			created++
		} else if w.Code != 200 {
			t.Fatalf("upload: %d %s", w.Code, w.Body)
		}
		ids[immichUploadedID(t, w, w.Code)] = true
	}
	if created != 1 || len(ids) != 1 {
		t.Fatalf("created=%d identities=%v", created, ids)
	}
}

func TestImmichMetadataRestart(t *testing.T) {
	h := newImmichTestHandler(t)
	dir := filepath.Join(t.TempDir(), "metadata")
	open := func() sorted.KeyValue {
		t.Helper()
		kv, err := sorted.NewKeyValue(jsonconfig.Obj{"type": "leveldb", "file": dir})
		if err != nil {
			t.Fatal(err)
		}
		return kv
	}
	h.metadata.Close()
	h.metadata = open()
	if err := h.initIdentity(); err != nil {
		t.Fatal(err)
	}
	token := immichTestLogin(t, h)
	id := immichUploadedID(t, immichTestUpload(t, h, token, "durable original", nil), 201)
	user := h.userID()
	h.metadata.Close()
	h.metadata = open()
	h.uid = ""
	if err := h.initIdentity(); err != nil {
		t.Fatal(err)
	}
	if h.userID() != user {
		t.Fatalf("identity changed on restart")
	}
	if w := immichTestRequest(t, h, token, "GET", "users/me", nil, ""); w.Code != 200 {
		t.Fatalf("session lost: %d %s", w.Code, w.Body)
	}
	if got := immichUploadedID(t, immichTestUpload(t, h, token, "durable original", nil), 200); got != id {
		t.Fatalf("asset lost on restart: %s vs %s", got, id)
	}
}

type immichFailStore struct {
	blobserver.Storage
}

func (s immichFailStore) ReceiveBlob(context.Context, blob.Ref, io.Reader) (blob.SizedRef, error) {
	return blob.SizedRef{}, errors.New("injected blob storage failure")
}

type immichFailMetadata struct{ sorted.KeyValue }

func (s immichFailMetadata) CommitBatch(sorted.BatchMutation) error {
	return errors.New("injected metadata commit failure")
}

func TestImmichFailedUploadNeverAcknowledged(t *testing.T) {
	for _, mode := range []string{"blobs", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			h := newImmichTestHandler(t)
			token := immichTestLogin(t, h)
			if mode == "blobs" {
				h.storage = immichFailStore{h.storage}
			} else {
				h.metadata = immichFailMetadata{h.metadata}
			}
			w := immichTestUpload(t, h, token, "failed original", nil)
			if w.Code != 500 {
				t.Fatalf("failed upload = %d %s", w.Code, w.Body)
			}
			checksum := sha1.Sum([]byte("failed original"))
			if _, err := h.metadata.Get("checksum|" + hex.EncodeToString(checksum[:])); !errors.Is(err, sorted.ErrNotFound) {
				t.Fatalf("failed upload falsely backed up: %v", err)
			}
		})
	}
}
