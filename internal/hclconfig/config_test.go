package hclconfig_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"

	"perkeep.org/internal/hclconfig"
)

func Test(t *testing.T) {
	testData, err := filepath.Glob("testdata/*.hcl")
	if err != nil {
		t.Fatalf("unable to load testfiles: %s", err)
	}

	// set a few environment variables for testing
	os.Setenv("PERKEEP_TEST_USERNAME", "foo")
	os.Setenv("PERKEEP_TEST_PASSWORD", "bar")
	os.Setenv("PERKEEP_TEST_LISTEN_PORT", "4443")
	os.Setenv("PERKEEP_TEST_HOME", "/var/perkeep/home")

	for _, td := range testData {
		t.Run(td, func(t *testing.T) {
			testIn, err := os.ReadFile(td)
			if err != nil {
				t.Fatalf("unable to load test input %q: %s", td, err)
			}
			testExpect, err := os.ReadFile(td + ".expect.json")
			if err != nil {
				t.Fatalf("unable to load test expectation %q: %s", td+".expect.json", err)
			}

			hclfile, diags := hclparse.NewParser().ParseHCL(testIn, td)
			if diags.HasErrors() {
				t.Fatalf("unable to parse test input %q: %s", td, diags.Error())
			}

			lowLevelCfg, err := hclconfig.ParseHCLBodyToLowLevelConfig(hclfile.Body)
			if err != nil {
				t.Fatalf("unable to generate low level config %q: %s", td, err)
			}

			var lowLevelCfgExpect map[string]interface{}
			if err = json.Unmarshal(testExpect, &lowLevelCfgExpect); err != nil {
				t.Fatalf("unable to unmarshal low level config expectation %q: %s", td, err)
			}

			if !reflect.DeepEqual(lowLevelCfgExpect, lowLevelCfg) {
				t.Errorf("expected %s and %s to be equal", mustJsonPretty(lowLevelCfg), mustJsonPretty(lowLevelCfgExpect))
			}
		})
	}
}

func mustJsonPretty(payload map[string]interface{}) []byte {
	res, err := json.MarshalIndent(payload, "", "\t")
	if err != nil {
		panic(err)
	}
	return res
}
