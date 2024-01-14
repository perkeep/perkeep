/*
Copyright 2012 The Perkeep Authors

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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/jsonsign/signhandler"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/server"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/clientconfig"
	"perkeep.org/pkg/types/serverconfig"

	// For registering all the handler constructors needed in TestInstallHandlers
	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/replica"
	_ "perkeep.org/pkg/importer/allimporters"
	_ "perkeep.org/pkg/search"
	_ "perkeep.org/pkg/server"
)

var (
	updateGolden = flag.Bool("update_golden", false, "Update golden *.want files")
	flagOnly     = flag.String("only", "", "If non-empty, substring of foo.json input file to match.")
)

const (
	// relativeRing points to a real secret ring, but serverinit
	// rewrites it to be an absolute path.  We then canonicalize
	// it to secringPlaceholder in the golden files.
	relativeRing       = "../jsonsign/testdata/test-secring.gpg"
	secringPlaceholder = "/path/to/secring"
)

func init() {
	// Avoid Linux vs. OS X differences in tests.
	serverinit.SetTempDirFunc(func() string { return "/tmp" })
	serverinit.SetNoMkdir(true)
}

func prettyPrint(t *testing.T, w io.Writer, v interface{}) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	w.Write(out)
}

func TestConfigs(t *testing.T) {
	dir, err := os.Open("testdata")
	if err != nil {
		t.Fatal(err)
	}
	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasPrefix(name, ".#") {
			// Emacs noise.
			continue
		}
		if *flagOnly != "" && !strings.Contains(name, *flagOnly) {
			continue
		}
		if strings.HasSuffix(name, ".json") {
			if strings.HasSuffix(name, "-want.json") {
				continue
			}
			testConfig(filepath.Join("testdata", name), t)
		}
	}
}

type namedReadSeeker struct {
	name string
	io.ReadSeeker
}

func (n namedReadSeeker) Name() string { return n.name }
func (n namedReadSeeker) Close() error { return nil }

// configParser returns a custom jsonconfig ConfigParser whose reader rewrites
// "/path/to/secring" to the absolute path of the jsonconfig test-secring.gpg file.
// On windows, it also fixes the slash separated paths.
func configParser() *jsonconfig.ConfigParser {
	return &jsonconfig.ConfigParser{
		Open: func(path string) (jsonconfig.File, error) {
			slurp, err := replaceRingPath(path)
			if err != nil {
				return nil, err
			}
			slurp = backslashEscape(slurp)
			return namedReadSeeker{path, bytes.NewReader(slurp)}, nil
		},
	}
}

// replaceRingPath returns the contents of the file at path with secringPlaceholder replaced with the absolute path of relativeRing.
func replaceRingPath(path string) ([]byte, error) {
	secRing, err := filepath.Abs(relativeRing)
	if err != nil {
		return nil, fmt.Errorf("Could not get absolute path of %v: %v", relativeRing, err)
	}
	secRing = strings.Replace(secRing, `\`, `\\`, -1)
	slurpBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// twice: once in search owner, and once in sighelper.
	return bytes.Replace(slurpBytes, []byte(secringPlaceholder), []byte(secRing), 2), nil
}

// We just need to make sure that we don't match the prefix handlers too.
var unixPathPattern = regexp.MustCompile(`"/.*/.+"`)

// backslashEscape, on windows, changes all the slash separated paths (which
// match unixPathPattern, to omit the prefix handler paths) with escaped
// backslashes.
func backslashEscape(b []byte) []byte {
	if runtime.GOOS != "windows" {
		return b
	}
	unixPaths := unixPathPattern.FindAll(b, -1)
	if unixPaths == nil {
		return b
	}
	var oldNew []string
	for _, v := range unixPaths {
		bStr := string(v)
		oldNew = append(oldNew, bStr, strings.Replace(bStr, `/`, `\\`, -1))
	}
	r := strings.NewReplacer(oldNew...)
	return []byte(r.Replace(string(b)))
}

func testConfig(name string, t *testing.T) {
	wantedError := func() error {
		slurp, err := os.ReadFile(strings.Replace(name, ".json", ".err", 1))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("Error reading .err file: %v", err)
		}
		return errors.New(string(slurp))
	}
	b, err := replaceRingPath(name)
	if err != nil {
		t.Fatalf("Could not read %s: %v", name, err)
	}
	b = backslashEscape(b)
	var hiLevelConf serverconfig.Config
	if err := json.Unmarshal(b, &hiLevelConf); err != nil {
		t.Fatalf("Could not unmarshal %s into a serverconfig.Config: %v", name, err)
	}

	lowLevelConf, err := serverinit.GenLowLevelConfig(&hiLevelConf)
	if g, w := strings.TrimSpace(fmt.Sprint(err)), strings.TrimSpace(fmt.Sprint(wantedError())); g != w {
		t.Fatalf("test %s: got GenLowLevelConfig error %q; want %q", name, g, w)
	}
	if err != nil {
		return
	}
	if err := (&jsonconfig.ConfigParser{}).CheckTypes(lowLevelConf.Export_Obj()); err != nil {
		t.Fatalf("Error while parsing low-level conf generated from %v: %v", name, err)
	}

	// TODO(mpl): should we stop execution (and not update golden files)
	// if the comparison fails? Currently this is not the case.
	wantFile := strings.Replace(name, ".json", "-want.json", 1)
	wantConf, err := configParser().ReadFile(wantFile)
	if err != nil {
		t.Fatalf("test %s: ReadFile: %v", name, err)
	}
	if *updateGolden {
		contents, err := json.MarshalIndent(lowLevelConf.Export_Obj(), "", "\t")
		if err != nil {
			t.Fatal(err)
		}
		contents = canonicalizeGolden(t, contents)
		if err := os.WriteFile(wantFile, contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
	compareConfigurations(t, name, lowLevelConf.Export_Obj(), wantConf)
}

func compareConfigurations(t *testing.T, name, g interface{}, w interface{}) {
	var got, want bytes.Buffer
	prettyPrint(t, &got, g)
	prettyPrint(t, &want, w)

	if got.String() != want.String() {
		t.Errorf("test %s configurations differ.\nGot:\n%s\nWant:\n%s\nDiff (want -> got), %s:\n%s",
			name, &got, &want, name, test.Diff(want.Bytes(), got.Bytes()))
	}
}

func canonicalizeGolden(t *testing.T, v []byte) []byte {
	localPath, err := filepath.Abs(relativeRing)
	if err != nil {
		t.Fatal(err)
	}
	// twice: once in search owner, and once in sighelper.
	v = bytes.Replace(v, []byte(localPath), []byte(secringPlaceholder), 2)
	if !bytes.HasSuffix(v, []byte("\n")) {
		v = append(v, '\n')
	}
	return v
}

func TestExpansionsInHighlevelConfig(t *testing.T) {
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatalf("source root folder not found: %v", err)
	}
	const keyID = "26F5ABDA"
	t.Setenv("TMP_EXPANSION_TEST", keyID)
	t.Setenv("TMP_EXPANSION_SECRING", filepath.Join(srcRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg")))
	// Setting CAMLI_CONFIG_DIR to avoid triggering failInTests in osutil.PerkeepConfigDir
	t.Setenv("CAMLI_CONFIG_DIR", "whatever")
	conf, err := serverinit.Load([]byte(`
{
    "auth": "localhost",
    "listen": ":4430",
    "https": false,
    "identity": ["_env", "${TMP_EXPANSION_TEST}"],
    "identitySecretRing": ["_env", "${TMP_EXPANSION_SECRING}"],
    "googlecloudstorage": ":camlistore-dev-blobs",
    "kvIndexFile": "/tmp/camli-index.kvdb"
}
`))
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%#v", conf)
	if !strings.Contains(got, keyID) {
		t.Errorf("Expected key %s in resulting low-level config. Got: %s", keyID, got)
	}
}

func TestInstallHandlers(t *testing.T) {
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatalf("source root folder not found: %v", err)
	}
	conf := serverinit.DefaultBaseConfig
	conf.Identity = "26F5ABDA"
	conf.IdentitySecretRing = filepath.Join(srcRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg"))
	conf.MemoryStorage = true
	conf.MemoryIndex = true

	confData, err := json.MarshalIndent(conf, "", "    ")
	if err != nil {
		t.Fatalf("Could not json encode config: %v", err)
	}

	// Setting CAMLI_CONFIG_DIR to avoid triggering failInTests in osutil.PerkeepConfigDir
	t.Setenv("CAMLI_CONFIG_DIR", "whatever")
	lowConf, err := serverinit.Load(confData)
	if err != nil {
		t.Fatal(err)
	}

	hi := http.NewServeMux()
	address := "http://" + conf.Listen
	_, err = lowConf.InstallHandlers(hi, address)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		prefix        string
		authWrapped   bool
		prefixWrapped bool
		handlerType   reflect.Type
	}{
		{
			prefix:        "/",
			handlerType:   reflect.TypeOf(&server.RootHandler{}),
			prefixWrapped: true,
		},

		{
			prefix:        "/sync/",
			handlerType:   reflect.TypeOf(&server.SyncHandler{}),
			prefixWrapped: true,
			authWrapped:   true,
		},

		{
			prefix:        "/my-search/",
			handlerType:   reflect.TypeOf(&search.Handler{}),
			prefixWrapped: true,
			authWrapped:   true,
		},

		{
			prefix:        "/ui/",
			handlerType:   reflect.TypeOf(&server.UIHandler{}),
			prefixWrapped: true,
			authWrapped:   true,
		},

		{
			prefix:        "/importer/",
			handlerType:   reflect.TypeOf(&importer.Host{}),
			prefixWrapped: true,
			authWrapped:   true,
		},

		{
			prefix:        "/sighelper/",
			handlerType:   reflect.TypeOf(&signhandler.Handler{}),
			prefixWrapped: true,
			authWrapped:   true,
		},

		{
			prefix:        "/status/",
			handlerType:   reflect.TypeOf(&server.StatusHandler{}),
			prefixWrapped: true,
			authWrapped:   true,
		},

		{
			prefix:        "/help/",
			handlerType:   reflect.TypeOf(&server.HelpHandler{}),
			prefixWrapped: true,
			authWrapped:   true,
		},

		{
			prefix:        "/setup/",
			handlerType:   reflect.TypeOf(&server.SetupHandler{}),
			prefixWrapped: true,
		},

		{
			prefix:      "/bs/camli/",
			handlerType: reflect.TypeOf(http.HandlerFunc(nil)),
		},

		{
			prefix:      "/index/camli/",
			handlerType: reflect.TypeOf(http.HandlerFunc(nil)),
		},

		{
			prefix:      "/bs-and-index/camli/",
			handlerType: reflect.TypeOf(http.HandlerFunc(nil)),
		},

		{
			prefix:      "/bs-and-maybe-also-index/camli/",
			handlerType: reflect.TypeOf(http.HandlerFunc(nil)),
		},

		{
			prefix:      "/cache/camli/",
			handlerType: reflect.TypeOf(http.HandlerFunc(nil)),
		},
	}
	for _, v := range tests {
		req, err := http.NewRequest("GET", address+v.prefix, nil)
		if err != nil {
			t.Error(err)
			continue
		}
		h, _ := hi.Handler(req)
		if v.authWrapped {
			ah, ok := h.(auth.Handler)
			if !ok {
				t.Errorf("handler for %v should be auth wrapped", v.prefix)
				continue
			}
			h = ah.Handler
		}
		if v.prefixWrapped {
			ph, ok := h.(*httputil.PrefixHandler)
			if !ok {
				t.Errorf("handler for %v should be prefix wrapped", v.prefix)
				continue
			}
			h = ph.Handler
		}
		if reflect.TypeOf(h) != v.handlerType {
			t.Errorf("for %v: want %v, got %v", v.prefix, v.handlerType, reflect.TypeOf(h))
		}
	}
}

// TestGenerateClientConfig validates the client config generated for display
// by the HelpHandler.
func TestGenerateClientConfig(t *testing.T) {
	inName := filepath.Join("testdata", "gen_client_config.in")
	wantName := strings.Replace(inName, ".in", ".out", 1)

	b, err := replaceRingPath(inName)
	if err != nil {
		t.Fatalf("Failed to read high-level server config file: %v", err)
	}
	b = backslashEscape(b)
	var hiLevelConf serverconfig.Config
	if err := json.Unmarshal(b, &hiLevelConf); err != nil {
		t.Fatalf("Failed to unmarshal server config: %v", err)
	}
	lowLevelConf, err := serverinit.GenLowLevelConfig(&hiLevelConf)
	if err != nil {
		t.Fatalf("Failed to generate low-level config: %v", err)
	}
	generatedConf, err := clientconfig.GenerateClientConfig(lowLevelConf.Export_Obj())
	if err != nil {
		t.Fatalf("Failed to generate client config: %v", err)
	}

	wb, err := replaceRingPath(wantName)
	if err != nil {
		t.Fatalf("Failed to read want config file: %v", err)
	}
	wb = backslashEscape(wb)
	var wantConf clientconfig.Config
	if err := json.Unmarshal(wb, &wantConf); err != nil {
		t.Fatalf("Failed to unmarshall want config: %v", err)
	}

	compareConfigurations(t, inName, generatedConf, wantConf)
}

// TestConfigHandlerRedaction validates that configHandler redacts sensitive
// values, still resulting in a valid JSON document.
func TestConfigHandlerRedaction(t *testing.T) {
	config := serverinit.ExportNewConfigFromObj(jsonconfig.Obj{
		"auth":                  "secret",
		"aws_secret_access_key": "secret",
		"password":              "secret",
		"client_secret":         "secret",
	})

	rr := httptest.NewRecorder()
	serverinit.ConfigHandler(config).ServeHTTP(rr, nil)
	got := make(map[string]string)
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("Failed to unmarshal configHandler response: %v", err)
	}
	want := map[string]string{
		"auth":                  "REDACTED",
		"aws_secret_access_key": "REDACTED",
		"password":              "REDACTED",
		"client_secret":         "REDACTED",
	}

	compareConfigurations(t, "configHandlerRedaction", got, want)
}

// TestConfigHandlerRemoveKnownKeys validates that configHandler removes
// "_knownkeys" keys properly, still resulting in a valid JSON document.
func TestConfigHandlerRemoveKnownKeys(t *testing.T) {
	config := serverinit.ExportNewConfigFromObj(jsonconfig.Obj{
		"/ui/": "",
		"_knownkeys": map[string]string{
			"key": "value",
		},
	})

	rr := httptest.NewRecorder()
	serverinit.ConfigHandler(config).ServeHTTP(rr, nil)
	got := make(map[string]string)
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("Failed to unmarshal configHandler response: %v", err)
	}
	want := map[string]string{
		"/ui/": "",
	}

	compareConfigurations(t, "configHandlerRemoveKnownKeys", got, want)
}
