package signhandler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

func setupHandler(t *testing.T) http.Handler {
	handler, err := newJSONSignFromConfig(
		test.NewLoader(),
		jsonconfig.Obj{
			"keyId":      "2931A67C26F5ABDA",
			"secretRing": "./testdata/test-secring.gpg",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func setupServer(t *testing.T) *httptest.Server {
	handler := &httputil.PrefixHandler{
		Prefix:  "/",
		Handler: setupHandler(t),
	}
	return httptest.NewServer(handler)
}

func TestDiscovery(t *testing.T) {
	ts := setupServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/camli/sig/discovery")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 status: %v", resp)
	}

	var disco camtypes.SignDiscovery
	if err := httputil.DecodeJSON(resp, &disco); err != nil {
		t.Fatal(err)
	}

	if expectedFingerprint := "FBB89AA320A2806FE497C0492931A67C26F5ABDA"; disco.PublicKeyFingerprint != expectedFingerprint {
		t.Errorf("Got Fingerprint %s, expected %s ", disco.PublicKeyFingerprint, expectedFingerprint)
	}
}
