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

package gceconfig

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
)

// Package variables initialize before init functions. Register testing's flags
// now so env.OnGCE's testenv.InTest check skips metadata even during our init;
// the generated test main normally calls testing.Init only after package init.
// Tests exercise both gate results explicitly through registerHooks instead.
var _ = func() bool {
	testing.Init()
	return true
}()

type hooks struct {
	configDir    func() string
	instanceMeta func(*jsonconfig.ConfigParser, []any) (any, error)
}

// captureHooks supplies every dependency, so exercising registration does not
// depend on pkg/env's cached GCE detection or contact a metadata server.
func captureHooks(t *testing.T, onGCE bool, attribute func(string) (string, error)) hooks {
	t.Helper()
	var h hooks
	gateCalls, dirRegistrations, macroRegistrations := 0, 0, 0
	registerHooks(func() bool {
		gateCalls++
		return onGCE
	}, attribute, func(f func() string) {
		dirRegistrations++
		h.configDir = f
	}, func(name string, f func(*jsonconfig.ConfigParser, []any) (any, error)) {
		macroRegistrations++
		if name != "_gce_instance_meta" {
			t.Errorf("registered macro %q; want _gce_instance_meta", name)
		}
		h.instanceMeta = f
	})
	if gateCalls != 1 {
		t.Errorf("gate called %d times; want 1", gateCalls)
	}
	wantRegistrations := 0
	if onGCE {
		wantRegistrations = 1
	}
	if dirRegistrations != wantRegistrations || macroRegistrations != wantRegistrations {
		t.Fatalf("registrations = (%d, %d); want (%d, %d)",
			dirRegistrations, macroRegistrations, wantRegistrations, wantRegistrations)
	}
	return h
}

func TestRegistrationGate(t *testing.T) {
	for _, onGCE := range []bool{false, true} {
		name := "not-a-perkeep-gce-instance"
		if onGCE {
			name = "perkeep-gce-instance"
		}
		t.Run(name, func(t *testing.T) {
			h := captureHooks(t, onGCE, func(string) (string, error) {
				t.Fatal("registration must not read metadata")
				return "", nil
			})
			if (h.configDir != nil) != onGCE || (h.instanceMeta != nil) != onGCE {
				t.Fatalf("hook presence does not match env.OnGCE result %v", onGCE)
			}
		})
	}
}

func TestConfigDir(t *testing.T) {
	readErr := errors.New("metadata unavailable")
	for _, tt := range []struct {
		name  string
		value string
		err   error
		want  string
	}{
		{name: "empty"},
		{name: "missing", err: readErr},
		{name: "bucket", value: "bucket", want: "/gcs/bucket"},
		{name: "gs-prefix", value: "gs://bucket/config", want: "/gcs/bucket/config"},
		{name: "clean", value: "gs://bucket/a/../config//", want: "/gcs/bucket/config"},
		{name: "leading-slash", value: "/bucket/config", want: "/gcs/bucket/config"},
		{name: "prefix-only", value: "gs://", want: "/gcs"},
		{name: "parent-path", value: "gs://bucket/../../config", want: "/config"},
		{name: "other-prefix", value: "https://bucket/config", want: "/gcs/https:/bucket/config"},
		{name: "value-with-error", value: "gs://bucket", err: readErr, want: "/gcs/bucket"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			h := captureHooks(t, true, func(attr string) (string, error) {
				calls++
				if attr != "camlistore-config-dir" {
					t.Errorf("attribute = %q; want camlistore-config-dir", attr)
				}
				return tt.value, tt.err
			})
			if got := h.configDir(); got != tt.want {
				t.Errorf("configDir() = %q; want %q", got, tt.want)
			}
			if calls != 1 {
				t.Errorf("metadata calls = %d; want 1", calls)
			}
		})
	}
}

func TestInstanceMetaArguments(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []any
		want string
	}{
		{name: "no-arguments", want: "only 1 argument supported after _gce_instance_meta"},
		{name: "too-many", args: []any{"one", "two"}, want: "only 1 argument supported after _gce_instance_meta"},
		{name: "nil", args: []any{nil}, want: "expected argument after _gce_instance_meta to be a string"},
		{name: "number", args: []any{float64(1)}, want: "expected argument after _gce_instance_meta to be a string"},
		{name: "bool", args: []any{true}, want: "expected argument after _gce_instance_meta to be a string"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := captureHooks(t, true, func(string) (string, error) {
				t.Fatal("invalid arguments must not read metadata")
				return "", nil
			})
			value, err := h.instanceMeta(nil, tt.args)
			if value != nil || err == nil || err.Error() != tt.want {
				t.Errorf("instanceMeta() = (%v, %v); want (nil, %q)", value, err, tt.want)
			}
		})
	}
}

func TestInstanceMeta(t *testing.T) {
	readErr := errors.New("metadata unavailable")
	for _, tt := range []struct {
		name  string
		attr  string
		value string
		err   error
	}{
		{name: "value", attr: "camlistore-hostname", value: "example.test"},
		{name: "empty-value", attr: "empty"},
		{name: "empty-attribute", value: "empty attribute accepted"},
		{name: "error", attr: "missing", err: readErr},
		{name: "value-with-error", attr: "missing", value: "ignored", err: readErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			h := captureHooks(t, true, func(attr string) (string, error) {
				calls++
				if attr != tt.attr {
					t.Errorf("attribute = %q; want %q", attr, tt.attr)
				}
				return tt.value, tt.err
			})
			value, err := h.instanceMeta(new(jsonconfig.ConfigParser), []any{tt.attr})
			if tt.err == nil {
				if err != nil || value != tt.value {
					t.Errorf("instanceMeta() = (%v, %v); want (%q, nil)", value, err, tt.value)
				}
			} else {
				const want = `error reading GCE instance attribute "missing": metadata unavailable`
				if value != nil || err == nil || err.Error() != want || !errors.Is(err, readErr) {
					t.Errorf("instanceMeta() = (%v, %v); want (nil, wrapped %q)", value, err, want)
				}
			}
			if calls != 1 {
				t.Errorf("metadata calls = %d; want 1", calls)
			}
		})
	}
}

var parserTestRuns atomic.Uint64

type configFile struct {
	*strings.Reader
	closed bool
}

func (*configFile) Name() string { return "client-config.json" }
func (f *configFile) Close() error {
	f.closed = true
	return nil
}

func TestInstanceMetaConfigParser(t *testing.T) {
	calls := 0
	h := captureHooks(t, true, func(attr string) (string, error) {
		calls++
		if attr != "camlistore-hostname" {
			t.Errorf("attribute = %q; want camlistore-hostname", attr)
		}
		return "https://example.test", nil
	})
	// The real name is asserted by captureHooks. Use a unique alias because
	// jsonconfig has no unregister operation and tests may run with -count.
	name := fmt.Sprintf("_test_gce_instance_meta_%d", parserTestRuns.Add(1))
	jsonconfig.RegisterFunc(name, h.instanceMeta)
	f := &configFile{Reader: strings.NewReader(fmt.Sprintf(
		`{"server": [%q, "camlistore-hostname"]}`, name))}
	parser := &jsonconfig.ConfigParser{
		Open: func(string) (jsonconfig.File, error) { return f, nil },
	}
	conf, err := parser.ReadFile("")
	if err != nil {
		t.Fatal(err)
	}
	if got := conf["server"]; got != "https://example.test" {
		t.Errorf("parsed server = %v; want https://example.test", got)
	}
	if calls != 1 {
		t.Errorf("metadata calls = %d; want 1", calls)
	}
	if !f.closed {
		t.Error("parser did not close configuration file")
	}
}

func TestConfigDirEnvironmentOverride(t *testing.T) {
	calls := 0
	h := captureHooks(t, true, func(string) (string, error) {
		calls++
		return "gs://bucket/config", nil
	})
	// osutil has no unregister operation. Leave an inert hook after this test,
	// including when the tests are repeated in the same process.
	active := true
	t.Cleanup(func() { active = false })
	osutil.RegisterConfigDirFunc(func() string {
		if !active {
			return ""
		}
		return h.configDir()
	})
	const override = "/local/../config"
	t.Setenv("CAMLI_CONFIG_DIR", override)
	if got, err := osutil.PerkeepConfigDir(); err != nil || got != override {
		t.Fatalf("PerkeepConfigDir() = (%q, %v); want (%q, nil)", got, err, override)
	}
	if calls != 0 {
		t.Fatalf("environment override read metadata %d times; want 0", calls)
	}
	t.Setenv("CAMLI_CONFIG_DIR", "")
	if got, err := osutil.PerkeepConfigDir(); err != nil || got != "/gcs/bucket/config" {
		t.Fatalf("PerkeepConfigDir() = (%q, %v); want (/gcs/bucket/config, nil)", got, err)
	}
	if calls != 1 {
		t.Errorf("metadata calls = %d; want 1", calls)
	}
}
