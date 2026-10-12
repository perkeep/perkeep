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

package serverinit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blobserver"
)

func TestHandlerTypeWantsAuth(t *testing.T) {
	for _, handlerType := range []string{"immich", "root", "share", "app"} {
		if handlerTypeWantsAuth(handlerType) {
			t.Errorf("%s handler should not be wrapped in Perkeep auth", handlerType)
		}
	}
	for _, handlerType := range []string{"ui", "search", "jsonsign", "sync", "status", "help", "importer"} {
		if !handlerTypeWantsAuth(handlerType) {
			t.Errorf("%s handler should be wrapped in Perkeep auth", handlerType)
		}
	}
}

type testClosingHandler struct {
	closeCalls int
	closeErr   error
}

func (*testClosingHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (h *testClosingHandler) Close() error {
	h.closeCalls++
	return h.closeErr
}

func init() {
	blobserver.RegisterHandlerConstructor("serverinit-test-closer", func(blobserver.Loader, jsonconfig.Obj) (http.Handler, error) {
		return &testClosingHandler{}, nil
	})
}

func TestHTTPHandlerShutdown(t *testing.T) {
	loader := &handlerLoader{
		installer: http.NewServeMux(),
		config: map[string]*handlerConfig{
			"/one/": {prefix: "/one/", htype: "serverinit-test-closer"},
			"/two/": {prefix: "/two/", htype: "serverinit-test-closer", internal: true},
		},
		handler: make(map[string]any),
	}
	loader.setupAll()
	loader.setupAll()
	if got := len(loader.closers); got != 2 {
		t.Fatalf("registered %d closers; want one per handler", got)
	}
	closeErr := errors.New("test close error")
	loader.handler["/one/"].(*testClosingHandler).closeErr = closeErr
	if err := multiCloser(loader.closers).Close(); !errors.Is(err, closeErr) {
		t.Errorf("shutdown error = %v; want %v", err, closeErr)
	}
	for prefix, handler := range loader.handler {
		if got := handler.(*testClosingHandler).closeCalls; got != 1 {
			t.Errorf("%s closed %d times; want 1", prefix, got)
		}
	}
}

func TestImmichConfigPasswordRedacted(t *testing.T) {
	high := immichTestConfig(t)
	low, err := genLowLevelConfig(high)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	configHandler{low}.ServeHTTP(recorder, nil)
	// Escaped quotes must not interfere with the redaction regexp.
	secret, _ := json.Marshal(high.Immich.Password)
	if strings.Contains(recorder.Body.String(), string(secret)) {
		t.Fatal("debug config contains Immich password")
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("redacted config is not valid JSON: %v", err)
	}
	args := got["prefixes"].(map[string]any)["/immich/api/"].(map[string]any)["handlerArgs"].(map[string]any)
	if args["password"] != "REDACTED" {
		t.Errorf("password = %v; want REDACTED", args["password"])
	}
	if args["email"] != high.Immich.Email {
		t.Errorf("email = %v; want unchanged email", args["email"])
	}
}
