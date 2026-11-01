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
	"os"
	"path/filepath"
	"testing"
)

// TestPrepareBuild prepares an unsigned device build (from
// clients/mobile/ios/build.sh device) named by $IOSSIGN_TEST_APP, as
// signing does, and signs its bundles ad hoc in the same order, on macOS.
func TestPrepareBuild(t *testing.T) {
	src := os.Getenv("IOSSIGN_TEST_APP")
	if src == "" {
		t.Skip("set IOSSIGN_TEST_APP to an unsigned device build of the app")
	}
	cfg := &config{TeamID: "TEAM123456", BundleID: "com.example.pk"}
	j := &job{cfg: cfg, src: src, development: true, buildNumber: "42"}
	defer j.cleanup()
	if err := j.prepare(); err != nil {
		t.Fatal(err)
	}
	if len(j.bundles) != 2 || j.bundles[0].id != "com.example.pk.share" || j.bundles[1].id != "com.example.pk" {
		t.Fatalf("bundles = %+v; want the share extension, then the app", j.bundles)
	}
	ext := filepath.Join(j.bundles[0].dir, "Info.plist")
	for k, want := range map[string]string{"CFBundleIdentifier": "com.example.pk.share", "CFBundleVersion": "42"} {
		if got, err := plistGet(ext, k); err != nil || got != want {
			t.Errorf("extension's %s = %q, %v; want %q", k, got, err, want)
		}
	}
	appVersion, _ := plistGet(filepath.Join(j.app, "Info.plist"), "CFBundleShortVersionString")
	if got, _ := plistGet(ext, "CFBundleShortVersionString"); got != appVersion {
		t.Errorf("extension's version = %q; want the app's, %q", got, appVersion)
	}
	for _, b := range j.bundles {
		ent := filepath.Join(j.work, b.id+".entitlements.plist")
		if err := os.WriteFile(ent, entitlementsPlist(cfg.TeamID, b.id, j.keychainGroup(), true), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := run("codesign", "--force", "--sign", "-", "--entitlements", ent, "--timestamp=none", b.dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := run("codesign", "--verify", "--strict", "--deep", "--verbose=1", j.app); err != nil {
		t.Fatal(err)
	}
}
