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
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/sorted"
)

func immichMobileTestJSON(t *testing.T, h *ImmichHandler, token, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return immichTestRequest(t, h, token, method, path, bytes.NewReader(data), "application/json")
}

func immichMobileTestStream(t *testing.T, h *ImmichHandler, token string, types ...string) []immichMobileSyncEvent {
	t.Helper()
	w := immichMobileTestJSON(t, h, token, "POST", "sync/stream", map[string]any{"types": types})
	if w.Code != 200 {
		t.Fatalf("sync: %d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Content-Type"); got != "application/jsonlines+json" {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.HasSuffix(w.Body.String(), "\n") {
		t.Fatal("sync must terminate the final event with a newline")
	}
	var events []immichMobileSyncEvent
	decoder := json.NewDecoder(w.Body)
	for {
		var e immichMobileSyncEvent
		err := decoder.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.Type == "" || e.Ack == "" || e.Data == nil {
			t.Fatalf("incomplete event: %+v", e)
		}
		events = append(events, e)
	}
	if len(events) == 0 || events[len(events)-1].Type != "SyncCompleteV1" {
		t.Fatalf("missing completion: %+v", events)
	}
	return events
}

func immichMobileTestAck(t *testing.T, h *ImmichHandler, token string, acks ...string) {
	t.Helper()
	w := immichMobileTestJSON(t, h, token, "POST", "sync/ack", map[string]any{"acks": acks})
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("ack: %d %s", w.Code, w.Body)
	}
}

func TestImmichMobileStartup(t *testing.T) {
	h := newImmichTestHandler(t)
	for _, path := range []string{"server/ping", "server/version", "server/features", "server/config", "server/about"} {
		w := immichTestRequest(t, h, "", "GET", path, nil, "")
		if w.Code != 200 {
			t.Fatalf("public %s: %d %s", path, w.Code, w.Body)
		}
	}
	w := immichTestRequest(t, h, "", "GET", "users/me", nil, "")
	if w.Code != 401 {
		t.Fatalf("users without auth: %d", w.Code)
	}
	token := immichTestLogin(t, h)
	for _, path := range []string{"users/me", "users/me/preferences", "users", "users/me/onboarding"} {
		w := immichTestRequest(t, h, token, "GET", path, nil, "")
		if w.Code != 200 {
			t.Fatalf("startup %s: %d %s", path, w.Code, w.Body)
		}
	}
	events := immichMobileTestStream(t, h, token, "AuthUsersV2", "UsersV1", "AssetsV2")
	if len(events) != 4 || events[0].Type != "AuthUserV2" || events[1].Type != "UserV1" {
		t.Fatalf("empty library sync: %+v", events)
	}
	immichMobileTestAck(t, h, token, events[0].Ack, events[1].Ack, events[2].Ack)
	if events = immichMobileTestStream(t, h, token, "AuthUsersV2", "UsersV1", "AssetsV2"); len(events) != 1 {
		t.Fatalf("empty incremental sync: %+v", events)
	}
	w = immichTestRequest(t, h, token, "POST", "users/me", nil, "")
	if w.Code != 405 {
		t.Fatalf("unsupported method reported %d", w.Code)
	}
}

func TestImmichMobileIncrementalSync(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	id := immichUploadedID(t, immichTestUpload(t, h, token, "first photo", nil), 201)
	events := immichMobileTestStream(t, h, token, "AssetsV2")
	if len(events) != 3 || events[0].Type != "AssetV2" {
		t.Fatalf("initial events: %+v", events)
	}
	data := events[0].Data.(map[string]any)
	sum := sha1.Sum([]byte("first photo"))
	if data["checksum"] != base64.StdEncoding.EncodeToString(sum[:]) || data["id"] != id || data["ownerId"] != h.userID() {
		t.Fatalf("bad asset DTO: %+v", data)
	}
	if _, ok := data["duration"].(float64); !ok {
		t.Fatalf("V2 duration is not numeric: %T", data["duration"])
	}
	// Sending and even acknowledging completion alone must not acknowledge assets.
	again := immichMobileTestStream(t, h, token, "AssetsV2")
	if len(again) != 3 || again[0].Ack != events[0].Ack {
		t.Fatal("stream prematurely advanced checkpoint")
	}
	immichMobileTestAck(t, h, token, events[len(events)-1].Ack)
	if got := immichMobileTestStream(t, h, token, "AssetsV2"); len(got) != 3 {
		t.Fatal("completion ack advanced asset cursor")
	}
	immichMobileTestAck(t, h, token, events[0].Ack)
	second := immichUploadedID(t, immichTestUpload(t, h, token, "second photo", nil), 201)
	got := immichMobileTestStream(t, h, token, "AssetsV2")
	if len(got) != 3 || got[0].Data.(map[string]any)["id"] != second {
		t.Fatalf("delta events: %+v", got)
	}
	immichMobileTestAck(t, h, token, got[0].Ack)
	// Delayed acknowledgements cannot roll back a newer checkpoint.
	immichMobileTestAck(t, h, token, events[0].Ack)
	if got := immichMobileTestStream(t, h, token, "AssetsV2"); len(got) != 1 {
		t.Fatal("old ack rolled back cursor")
	}
	// A newly requested entity type has an independent checkpoint.
	if got := immichMobileTestStream(t, h, token, "AssetExifsV1"); len(got) != 4 {
		t.Fatalf("new type lost assets: %+v", got)
	}
	// A second mobile login gets its own complete view of the library.
	otherToken := immichTestLogin(t, h)
	if got := immichMobileTestStream(t, h, otherToken, "AssetsV2"); len(got) != 4 {
		t.Fatal("checkpoints leaked between sessions")
	}
	// A new handler around the same metadata recovers the explicit acknowledgements.
	restarted := &ImmichHandler{metadata: h.metadata, uid: h.uid, email: h.email, password: h.password, prefix: h.prefix}
	if got := immichMobileTestStream(t, restarted, token, "AssetsV2"); len(got) != 1 {
		t.Fatal("checkpoint was not persisted")
	}
}

func TestImmichMobileSyncResetAndValidation(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	immichUploadedID(t, immichTestUpload(t, h, token, "photo", nil), 201)
	events := immichMobileTestStream(t, h, token, "AssetsV2", "AssetExifsV1")
	for _, e := range events {
		immichMobileTestAck(t, h, token, e.Ack)
	}
	// Fresh-install migrations delete some obsolete entity checkpoints too.
	w := immichMobileTestJSON(t, h, token, "DELETE", "sync/ack", map[string]any{"types": []string{"AlbumV1", "AssetV1", "AssetV2"}})
	if w.Code != 204 {
		t.Fatalf("migration ack delete: %d %s", w.Code, w.Body)
	}
	got := immichMobileTestStream(t, h, token, "AssetsV2", "AssetExifsV1")
	if len(got) != 3 || got[0].Type != "AssetV2" {
		t.Fatalf("selective reset: %+v", got)
	}
	// No partial mutation if a later acknowledgement in a request is malformed.
	w = immichMobileTestJSON(t, h, token, "POST", "sync/ack", map[string]any{"acks": []string{got[0].Ack, "AssetV2|999999999"}})
	if w.Code != 400 {
		t.Fatalf("future ack status: %d %s", w.Code, w.Body)
	}
	if again := immichMobileTestStream(t, h, token, "AssetsV2"); len(again) != 3 {
		t.Fatal("invalid ack request partially committed")
	}
	w = immichMobileTestJSON(t, h, token, "POST", "sync/stream", map[string]any{"types": []string{"UnknownType"}})
	if w.Code != 400 {
		t.Fatal("unknown sync request type accepted")
	}
	w = immichMobileTestJSON(t, h, token, "POST", "sync/stream", map[string]any{"types": []string{"AssetsV2", "AssetExifsV1"}, "reset": true})
	if w.Code != 200 || strings.Count(w.Body.String(), "\n") != 4 {
		t.Fatalf("full reset: %d %s", w.Code, w.Body)
	}
	w = immichMobileTestJSON(t, h, token, "DELETE", "sync/ack", map[string]any{})
	if w.Code != 204 {
		t.Fatalf("delete all checkpoints: %d", w.Code)
	}
}

func TestImmichMobileLivePhotoMetadataAndDetail(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	videoID := immichUploadedID(t, immichTestUpload(t, h, token, "video bytes", map[string]string{"filename": "live.mov", "visibility": "hidden", "duration": "1500"}), 201)
	metadata := `[{"key":"mobile-app","value":{"iCloudId":"cloud-local-id","createdAt":"2025-01-02T03:04:05Z"}},{"key":"test-extra","value":{"second":true}}]`
	stillID := immichUploadedID(t, immichTestUpload(t, h, token, "still bytes", map[string]string{"livePhotoVideoId": videoID, "metadata": metadata}), 201)
	events := immichMobileTestStream(t, h, token, "AssetsV2", "AssetMetadataV1")
	if len(events) != 6 {
		t.Fatalf("live photo events: %+v", events)
	}
	video := events[0].Data.(map[string]any)
	still := events[1].Data.(map[string]any)
	if video["type"] != "VIDEO" || video["visibility"] != "hidden" || video["duration"] != float64(1500) {
		t.Fatalf("video: %+v", video)
	}
	if still["livePhotoVideoId"] != videoID || still["id"] != stillID {
		t.Fatalf("still: %+v", still)
	}
	md := events[2].Data.(map[string]any)
	if md["key"] != "mobile-app" || md["value"].(map[string]any)["iCloudId"] != "cloud-local-id" {
		t.Fatalf("cloud metadata: %+v", md)
	}
	// Checkpoints can split two metadata items for the same asset.
	immichMobileTestAck(t, h, token, events[2].Ack)
	resumed := immichMobileTestStream(t, h, token, "AssetMetadataV1")
	if len(resumed) != 3 || resumed[0].Data.(map[string]any)["key"] != "test-extra" {
		t.Fatalf("partial metadata resume: %+v", resumed)
	}
	w := immichTestRequest(t, h, token, "GET", "assets/"+stillID, nil, "")
	var detail map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil {
		t.Fatalf("asset detail: %d %s", w.Code, w.Body)
	}
	if detail["livePhotoVideoId"] != videoID || detail["checksum"] != still["checksum"] {
		t.Fatalf("detail disagrees with sync: %+v", detail)
	}
	user, err := h.mobileUser()
	if err != nil || user["quotaUsageInBytes"] != int64(len("video bytes")+len("still bytes")) {
		t.Fatalf("usage: %+v, %v", user, err)
	}
}

func TestImmichMobilePreferences(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	w := immichMobileTestJSON(t, h, token, "PUT", "users/me/preferences", map[string]any{"memories": map[string]any{"enabled": true}})
	if w.Code != 200 {
		t.Fatalf("update prefs: %d %s", w.Code, w.Body)
	}
	w = immichTestRequest(t, h, token, "GET", "users/me/preferences", nil, "")
	var prefs map[string]map[string]any
	if json.Unmarshal(w.Body.Bytes(), &prefs) != nil || prefs["memories"]["enabled"] != true || prefs["memories"]["duration"] != float64(5) {
		t.Fatalf("lost complete preference DTO: %s", w.Body)
	}
	for _, value := range []any{nil, "wrong-type", 0} {
		w = immichMobileTestJSON(t, h, token, "PATCH", "users/me/preferences", map[string]any{"memories": map[string]any{"enabled": value}})
		if w.Code != 400 {
			t.Fatalf("invalid preference %v accepted", value)
		}
	}
}

type immichMobileCountingKV struct {
	sorted.KeyValue
	assetReads int
}

func (kv *immichMobileCountingKV) Get(key string) (string, error) {
	if strings.HasPrefix(key, "asset|") {
		kv.assetReads++
	}
	return kv.KeyValue.Get(key)
}

func TestImmichMobileSyncReadsOnlyUnacknowledgedChanges(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	for i := 0; i < 4; i++ {
		immichUploadedID(t, immichTestUpload(t, h, token, fmt.Sprintf("photo %d", i), nil), 201)
	}
	events := immichMobileTestStream(t, h, token, "AssetsV2")
	for _, e := range events {
		immichMobileTestAck(t, h, token, e.Ack)
	}
	immichUploadedID(t, immichTestUpload(t, h, token, "one new photo", nil), 201)
	counted := &immichMobileCountingKV{KeyValue: h.metadata}
	h.metadata = counted
	if got := immichMobileTestStream(t, h, token, "AssetsV2"); len(got) != 3 {
		t.Fatalf("bad incremental stream: %+v", got)
	}
	if counted.assetReads != 1 {
		t.Fatalf("incremental sync loaded %d assets, want only one new asset", counted.assetReads)
	}
}

// Optional interoperability check using an independently supplied protocol
// specification. Normal tests do not depend on an upstream checkout or network.
// IMMICH_OPENAPI_SPEC=/path/to/immich-openapi-specs.json go test ./pkg/server -run TestImmichMobileOpenAPIContract
func TestImmichMobileOpenAPIContract(t *testing.T) {
	filename := os.Getenv("IMMICH_OPENAPI_SPEC")
	if filename == "" {
		t.Skip("IMMICH_OPENAPI_SPEC not supplied")
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	id := immichUploadedID(t, immichTestUpload(t, h, token, "contract bytes", map[string]string{"metadata": `[{"key":"mobile-app","value":{"iCloudId":"contract"}}]`}), 201)
	for path, schema := range map[string]string{"server/ping": "ServerPingResponse", "server/version": "ServerVersionResponseDto", "server/features": "ServerFeaturesDto", "server/config": "ServerConfigDto", "server/about": "ServerAboutResponseDto", "users/me": "UserAdminResponseDto", "users/me/preferences": "UserPreferencesResponseDto", "assets/" + id: "AssetResponseDto"} {
		t.Run(schema, func(t *testing.T) {
			w := immichTestRequest(t, h, token, "GET", path, nil, "")
			var value any
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil {
				t.Fatalf("response: %d %s", w.Code, w.Body)
			}
			immichMobileValidateSchema(t, spec.Components.Schemas, spec.Components.Schemas[schema], value, schema)
		})
	}
	for _, event := range immichMobileTestStream(t, h, token, "AuthUsersV2", "UsersV1", "AssetsV2", "AssetExifsV1", "AssetMetadataV1", "UserMetadataV1") {
		schema := "Sync" + event.Type
		if strings.HasPrefix(event.Type, "Sync") {
			schema = event.Type
		}
		immichMobileValidateSchema(t, spec.Components.Schemas, spec.Components.Schemas[schema], event.Data, schema)
	}
}

func immichMobileValidateSchema(t *testing.T, schemas map[string]map[string]any, schema map[string]any, value any, path string) {
	t.Helper()
	if schema == nil {
		t.Fatalf("missing schema %s", path)
	}
	if value == nil && schema["nullable"] == true {
		return
	}
	if ref, ok := schema["$ref"].(string); ok {
		immichMobileValidateSchema(t, schemas, schemas[strings.TrimPrefix(ref, "#/components/schemas/")], value, path)
		return
	}
	if variants, ok := schema["allOf"].([]any); ok {
		for _, v := range variants {
			immichMobileValidateSchema(t, schemas, v.(map[string]any), value, path)
		}
	}
	if variants, ok := schema["enum"].([]any); ok {
		match := false
		for _, v := range variants {
			if v == value {
				match = true
			}
		}
		if !match {
			t.Errorf("%s = %v not in enum %v", path, value, variants)
		}
	}
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			t.Errorf("%s: want object, got %T", path, value)
			return
		}
		if required, ok := schema["required"].([]any); ok {
			for _, name := range required {
				if _, exists := object[name.(string)]; !exists {
					t.Errorf("%s missing required %s", path, name)
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, child := range props {
				if val, exists := object[name]; exists {
					immichMobileValidateSchema(t, schemas, child.(map[string]any), val, path+"."+name)
				}
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			t.Errorf("%s: want array, got %T", path, value)
			return
		}
		for i, v := range array {
			immichMobileValidateSchema(t, schemas, schema["items"].(map[string]any), v, fmt.Sprintf("%s[%d]", path, i))
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			t.Errorf("%s: want string, got %T", path, value)
			return
		}
		if schema["format"] == "uuid" && !immichValidUUID(s) {
			t.Errorf("%s: invalid UUID %q", path, s)
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				t.Errorf("%s: invalid timestamp %q", path, s)
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			t.Errorf("%s: want boolean, got %T", path, value)
		}
	case "integer", "number":
		n, ok := value.(float64)
		if !ok {
			t.Errorf("%s: want number, got %T", path, value)
			return
		}
		if schema["type"] == "integer" && n != float64(int64(n)) {
			t.Errorf("%s: noninteger %v", path, n)
		}
		if min, ok := schema["minimum"].(float64); ok && n < min {
			t.Errorf("%s: %v below minimum %v", path, n, min)
		}
		if max, ok := schema["maximum"].(float64); ok && n > max {
			t.Errorf("%s: %v above maximum %v", path, n, max)
		}
	}
}

func TestImmichMobileSparseMetadataCheckpoint(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	for i := 0; i < 3; i++ {
		immichUploadedID(t, immichTestUpload(t, h, token, fmt.Sprintf("android photo %d", i), nil), 201)
	}
	types := []string{"AssetsV2", "AssetExifsV1", "AssetMetadataV1"}
	events := immichMobileTestStream(t, h, token, types...)
	// Mirror the real mobile processing algorithm: only the LAST acknowledgement
	// in each consecutive same-wire-type batch is sent to the server.
	for i, e := range events {
		if i == len(events)-1 || e.Type != events[i+1].Type {
			immichMobileTestAck(t, h, token, e.Ack)
		}
	}
	counted := &immichMobileCountingKV{KeyValue: h.metadata}
	h.metadata = counted
	again := immichMobileTestStream(t, h, token, types...)
	if len(again) != 1 || counted.assetReads != 0 {
		t.Fatalf("empty Android delta rescanned library: events=%+v reads=%d", again, counted.assetReads)
	}
	// Resetting all entity checkpoints still yields a complete library.
	w := immichMobileTestJSON(t, h, token, "DELETE", "sync/ack", map[string]any{})
	if w.Code != 204 {
		t.Fatalf("reset: %d", w.Code)
	}
	if got := immichMobileTestStream(t, h, token, types...); len(got) != 8 {
		t.Fatalf("reset sparse metadata: %+v", got)
	}
}

func TestImmichMobilePreferencesSyncAndSizeLimit(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	events := immichMobileTestStream(t, h, token, "UserMetadataV1")
	if len(events) != 3 || events[0].Type != "UserMetadataV1" {
		t.Fatalf("initial prefs metadata: %+v", events)
	}
	for _, e := range events {
		immichMobileTestAck(t, h, token, e.Ack)
	}
	w := immichMobileTestJSON(t, h, token, "PATCH", "users/me/preferences", map[string]any{"memories": map[string]any{"enabled": true}})
	if w.Code != 200 {
		t.Fatalf("prefs update: %d %s", w.Code, w.Body)
	}
	got := immichMobileTestStream(t, h, token, "UserMetadataV1", "AssetsV2")
	if len(got) != 3 || got[0].Type != "UserMetadataV1" || got[0].Ack == events[0].Ack {
		t.Fatalf("prefs update not synced: %+v", got)
	}
	value := got[0].Data.(map[string]any)["value"].(map[string]any)
	if value["memories"].(map[string]any)["enabled"] != true {
		t.Fatalf("stale prefs value: %+v", value)
	}
	for _, e := range got {
		immichMobileTestAck(t, h, token, e.Ack)
	}
	w = immichMobileTestJSON(t, h, token, "PATCH", "users/me/preferences", map[string]any{"purchase": map[string]any{"hideBuyButtonUntil": strings.Repeat("x", sorted.MaxValueSize+1)}})
	if w.Code != 413 {
		t.Fatalf("oversized preference succeeded: %d %s", w.Code, w.Body)
	}
	again := immichMobileTestStream(t, h, token, "UserMetadataV1", "AssetsV2")
	if len(again) != 1 {
		t.Fatalf("oversized request changed sequence: %+v", again)
	}
}

func TestImmichMobileCorruptStreamAbortsBeforeCheckpoint(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	immichUploadedID(t, immichTestUpload(t, h, token, "valid first photo", nil), 201)
	second := immichUploadedID(t, immichTestUpload(t, h, token, "corrupted second photo", nil), 201)
	a, err := h.asset(second)
	if err != nil {
		t.Fatal(err)
	}
	a.Checksum = "broken checksum"
	data, _ := json.Marshal(a)
	if err := h.metadata.Set("asset|"+second, string(data)); err != nil {
		t.Fatal(err)
	}
	requestData := strings.NewReader(`{"types":["AssetsV2"]}`)
	r := httptest.NewRequest("POST", h.prefix+"sync/stream", requestData)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	aborted := false
	func() {
		defer func() {
			if got := recover(); got == http.ErrAbortHandler {
				aborted = true
			} else if got != nil {
				panic(got)
			}
		}()
		h.ServeHTTP(w, r)
	}()
	if !aborted {
		t.Fatal("corrupt stream did not abort connection")
	}
	if strings.Contains(w.Body.String(), "SyncAckV1") || strings.Contains(w.Body.String(), "SyncCompleteV1") || strings.Count(w.Body.String(), "\n") != 1 {
		t.Fatalf("failed stream prematurely completed: %s", w.Body)
	}
}
