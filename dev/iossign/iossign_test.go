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

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseP8(t *testing.T) {
	k := testKey(t)
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseP8(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(k) {
		t.Error("parsed key differs")
	}
}

func TestToken(t *testing.T) {
	k := testKey(t)
	c := &ascClient{keyID: "KEY123", issuerID: "issuer-uuid", key: k}
	now := time.Unix(1_800_000_000, 0)
	tok, err := c.token(now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	dec := func(s string) map[string]any {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if h := dec(parts[0]); h["alg"] != "ES256" || h["kid"] != "KEY123" {
		t.Errorf("header = %v", h)
	}
	cl := dec(parts[1])
	if cl["iss"] != "issuer-uuid" || cl["aud"] != "appstoreconnect-v1" || cl["exp"].(float64)-cl["iat"].(float64) != 600 {
		t.Errorf("claims = %v", cl)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&k.PublicKey, h[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("bad signature")
	}
}

func TestEntitlements(t *testing.T) {
	dev := string(entitlementsPlist("TEAM", "org.example.app", true))
	for _, want := range []string{
		"<string>TEAM.org.example.app</string>",
		"<key>get-task-allow</key>\n\t<true/>",
	} {
		if !strings.Contains(dev, want) {
			t.Errorf("development entitlements missing %q:\n%s", want, dev)
		}
	}
	if strings.Contains(dev, "beta-reports-active") {
		t.Error("development entitlements have beta-reports-active")
	}
	store := string(entitlementsPlist("TEAM", "org.example.app", false))
	for _, want := range []string{
		"<key>get-task-allow</key>\n\t<false/>",
		"<key>beta-reports-active</key>\n\t<true/>",
	} {
		if !strings.Contains(store, want) {
			t.Errorf("store entitlements missing %q:\n%s", want, store)
		}
	}

	if _, err := exec.LookPath("plutil"); err == nil {
		for _, ent := range []string{dev, store} {
			f := filepath.Join(t.TempDir(), "ent.plist")
			os.WriteFile(f, []byte(ent), 0o644)
			if out, err := exec.Command("plutil", "-lint", f).CombinedOutput(); err != nil {
				t.Errorf("plutil -lint: %v, %s", err, out)
			}
		}
	}
}

func TestCheckProfileEntitlements(t *testing.T) {
	ents := func(appID string, gta bool) map[string]any {
		return map[string]any{"application-identifier": appID, "get-task-allow": gta}
	}
	tests := []struct {
		name  string
		teams []string
		ents  map[string]any
		dev   bool
		ok    bool
	}{
		{"exact dev", []string{"TEAM"}, ents("TEAM.org.example.app", true), true, true},
		{"exact store", []string{"TEAM"}, ents("TEAM.org.example.app", false), false, true},
		{"wildcard", []string{"TEAM"}, ents("TEAM.org.example.*", true), true, true},
		{"wrong team", []string{"OTHER"}, ents("TEAM.org.example.app", true), true, false},
		{"app ID prefix differs", []string{"TEAM"}, ents("PREFIX.org.example.app", true), true, false},
		{"other app", []string{"TEAM"}, ents("TEAM.org.example.other", true), true, false},
		{"store profile for dev", []string{"TEAM"}, ents("TEAM.org.example.app", false), true, false},
	}
	for _, tt := range tests {
		err := checkProfileEntitlements(tt.teams, tt.ents, "TEAM", "org.example.app", tt.dev)
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v; want ok=%v", tt.name, err, tt.ok)
		}
	}
}

func TestFindIssuer(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	newCA := func(name string, notAfter time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
		k := testKey(t)
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: name},
			NotBefore:             now.AddDate(-5, 0, 0),
			NotAfter:              notAfter,
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := x509.ParseCertificate(der)
		return c, k, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	// An expired CA and a current one with the same name, as with
	// Apple's WWDR generations.
	_, _, oldPEM := newCA("WWDR", now.AddDate(-1, 0, 0))
	ca, caKey, curPEM := newCA("WWDR", now.AddDate(5, 0, 0))
	_, _, otherPEM := newCA("Other", now.AddDate(5, 0, 0))

	leafKey := testKey(t)
	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Apple Development: test"},
		NotBefore:    now.AddDate(0, 0, -1),
		NotAfter:     now.AddDate(1, 0, 0),
	}, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(leafDER)

	if !findIssuer(leaf, slices.Concat(otherPEM, oldPEM, curPEM), now) {
		t.Error("didn't find current issuer")
	}
	if findIssuer(leaf, slices.Concat(otherPEM, oldPEM), now) {
		t.Error("found issuer with only an unrelated CA")
	}
	if findIssuer(leaf, curPEM, now.AddDate(10, 0, 0)) {
		t.Error("found expired issuer")
	}
}

func TestCheckChain(t *testing.T) {
	good := `Executable=/x/Perkeep.app/Perkeep
Identifier=org.perkeep.mobile
Authority=Apple Development: Created via API (ABC)
Authority=Apple Worldwide Developer Relations Certification Authority
Authority=Apple Root CA
TeamIdentifier=TEAM
`
	if err := checkChain(good); err != nil {
		t.Errorf("good chain: %v", err)
	}
	leafOnly := "Identifier=org.perkeep.mobile\nAuthority=Apple Development: Created via API (ABC)\nTeamIdentifier=TEAM\n"
	if err := checkChain(leafOnly); err == nil {
		t.Error("leaf-only chain: no error")
	}
}

func TestParseIdentities(t *testing.T) {
	out := `  1) 0123456789ABCDEF0123456789ABCDEF01234567 "Apple Development: Jane Doe (ABC123)"
  2) 89ABCDEF0123456789ABCDEF0123456789ABCDEF "Apple Distribution: Jane Doe (TEAM)"
     2 valid identities found
`
	ids := parseIdentities([]byte(out))
	if len(ids) != 2 || ids["0123456789ABCDEF0123456789ABCDEF01234567"] != "Apple Development: Jane Doe (ABC123)" {
		t.Errorf("got %v", ids)
	}
}

func TestParseDevices(t *testing.T) {
	j := `{"result":{"devices":[
		{"connectionProperties":{"pairingState":"paired"},"deviceProperties":{"name":"Phone"},"hardwareProperties":{"platform":"iOS","udid":"U1"}},
		{"connectionProperties":{"pairingState":"unpaired"},"deviceProperties":{"name":"Other"},"hardwareProperties":{"platform":"iOS","udid":"U2"}},
		{"connectionProperties":{"pairingState":"paired"},"deviceProperties":{"name":"Watch"},"hardwareProperties":{"platform":"watchOS","udid":"U3"}}
	]}}`
	devs, err := parseDevices([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0] != (device{UDID: "U1", Name: "Phone"}) {
		t.Errorf("got %v", devs)
	}
}

func TestCheckApp(t *testing.T) {
	mk := func(t *testing.T, files ...string) string {
		dir := filepath.Join(t.TempDir(), "X.app")
		for _, f := range append([]string{"Info.plist"}, files...) {
			p := filepath.Join(dir, f)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	if files, err := checkApp(mk(t, "X")); err != nil || strings.Join(files, ",") != "Info.plist,X" {
		t.Errorf("plain app: files %v, err %v", files, err)
	}
	for _, bad := range []string{"_CodeSignature/CodeResources", "embedded.mobileprovision", "Frameworks/F.framework/F", "PlugIns/E.appex/E"} {
		if _, err := checkApp(mk(t, bad)); err == nil {
			t.Errorf("app with %s: no error", bad)
		}
	}
	dir := mk(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := checkApp(dir); err == nil {
		t.Error("app with symlink: no error")
	}
}

// fakeASC is a fake of the parts of the App Store Connect API that
// iossign uses.
type fakeASC struct {
	t        *testing.T
	bundles  []resource
	profiles []resource
	profRels map[string]map[string][]ref // profile ID -> relationship -> refs
	deleted  []string
	created  []resource
}

func (f *fakeASC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "no auth", 401)
		return
	}
	reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"data": v}) }
	var in struct{ Data resource }
	if r.Method == "POST" {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &in); err != nil {
			f.t.Errorf("bad POST body: %v", err)
		}
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/bundleIds":
		reply(f.bundles)
	case r.Method == "POST" && r.URL.Path == "/v1/bundleIds":
		in.Data.ID = "newbundle"
		f.created = append(f.created, in.Data)
		reply(in.Data)
	case r.Method == "GET" && r.URL.Path == "/v1/profiles":
		reply(f.profiles)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/profiles/"):
		parts := strings.Split(r.URL.Path, "/") // "", v1, profiles, ID, relationships, rel
		reply(f.profRels[parts[3]][parts[5]])
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/profiles/"):
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/v1/profiles/"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "POST" && r.URL.Path == "/v1/profiles":
		in.Data.ID = "newprofile"
		in.Data.Attributes["profileContent"] = base64.StdEncoding.EncodeToString([]byte("new profile"))
		f.created = append(f.created, in.Data)
		reply(in.Data)
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errors":[{"title":"Not found","detail":"no such thing"}]}`))
	}
}

func newFakeClient(t *testing.T, f *fakeASC) *ascClient {
	f.t = t
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	return &ascClient{keyID: "K", issuerID: "I", key: testKey(t), baseURL: ts.URL, hc: ts.Client()}
}

func TestEnsureBundleID(t *testing.T) {
	ctx := context.Background()
	f := &fakeASC{bundles: []resource{
		{Type: "bundleIds", ID: "b1", Attributes: map[string]any{"identifier": "org.example.app.extra"}},
		{Type: "bundleIds", ID: "b2", Attributes: map[string]any{"identifier": "org.example.app"}},
	}}
	c := newFakeClient(t, f)
	if id, err := c.ensureBundleID(ctx, "org.example.app"); err != nil || id != "b2" {
		t.Errorf("existing: got %q, %v; want b2", id, err)
	}
	if id, err := c.ensureBundleID(ctx, "org.example.other"); err != nil || id != "newbundle" {
		t.Errorf("new: got %q, %v; want newbundle", id, err)
	}
	if len(f.created) != 1 || f.created[0].attr("identifier") != "org.example.other" {
		t.Errorf("created %v", f.created)
	}
	if got, want := f.created[0].attr("name"), "Perkeep org example other"; got != want {
		t.Errorf("name = %q; want %q", got, want)
	}
}

func TestEnsureProfile(t *testing.T) {
	ctx := context.Background()
	existing := func() *fakeASC {
		return &fakeASC{
			profiles: []resource{{Type: "profiles", ID: "p1", Attributes: map[string]any{
				"name":           "dev",
				"profileState":   "ACTIVE",
				"profileContent": base64.StdEncoding.EncodeToString([]byte("old profile")),
			}}},
			profRels: map[string]map[string][]ref{"p1": {
				"certificates": {{"certificates", "c1"}},
				"devices":      {{"devices", "d1"}},
			}},
		}
	}

	f := existing()
	got, err := newFakeClient(t, f).ensureProfile(ctx, "dev", "IOS_APP_DEVELOPMENT", "b1", "c1", []string{"d1"})
	if err != nil || string(got) != "old profile" || len(f.deleted) != 0 {
		t.Errorf("matching profile: got %q, %v, deleted %v", got, err, f.deleted)
	}

	f = existing()
	got, err = newFakeClient(t, f).ensureProfile(ctx, "dev", "IOS_APP_DEVELOPMENT", "b1", "c1", []string{"d1", "d2"})
	if err != nil || string(got) != "new profile" || strings.Join(f.deleted, ",") != "p1" {
		t.Errorf("new device: got %q, %v, deleted %v", got, err, f.deleted)
	}
	if len(f.created) != 1 || len(f.created[0].Relationships["devices"].Data.([]any)) != 2 {
		t.Errorf("created %+v", f.created)
	}
}

func TestAPIError(t *testing.T) {
	c := newFakeClient(t, &fakeASC{})
	err := c.do(context.Background(), "GET", "/v1/nope", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Not found: no such thing") {
		t.Errorf("err = %v", err)
	}
}
