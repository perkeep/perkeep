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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
)

func TestAdminReindexDiskpackedArguments(t *testing.T) {
	// osutil panics on unconfigured host-directory lookups in tests. Argument
	// handling must finish before even looking up the default configuration.
	t.Setenv("CAMLI_CONFIG_DIR", "")
	t.Setenv("CAMLI_INCLUDE_PATH", "")
	for _, tt := range []struct {
		args string
		code int
		want string
	}{
		{"-h", 0, "Usage: perkeepd admin reindex-diskpacked"},
		{"-help", 0, "Usage: perkeepd admin reindex-diskpacked"},
		{"--help", 0, "Usage: perkeepd admin reindex-diskpacked"},
		{"--unknown", 2, "flag provided but not defined"},
		{"--listen=:0", 2, "flag provided but not defined"},
		{"--reindex", 2, "flag provided but not defined"},
		{"--version", 2, "flag provided but not defined"},
		{"--configfile=other.json", 2, "flag provided but not defined"},
		{"--overwrite=invalid", 2, "invalid boolean value"},
		{"one two", 2, "at most one path"},
		{"--overwrite one two", 2, "at most one path"},
	} {
		t.Run(tt.args, func(t *testing.T) {
			checkAdminReindexDiskpacked(t, tt.code, tt.want, strings.Fields(tt.args)...)
		})
	}
}

func checkAdminReindexDiskpacked(t *testing.T, status int, want string, args ...string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if got := runAdminReindexDiskpacked(args, &out, &errOut); got != status {
		t.Fatalf("args %q: status = %d, want %d; stderr: %s", args, got, status, &errOut)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), want) {
		t.Fatalf("args %q: stdout = %q, stderr = %q; want only stderr containing %q", args, &out, &errOut, want)
	}
}

// writeAdminDiskpackedPack writes the real pack format without opening a store
// for writes. Tests can start with missing or incorrect index rows.
func writeAdminDiskpackedPack(t *testing.T, root string) map[string]string {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	var pack bytes.Buffer
	rows := make(map[string]string)
	for _, contents := range []string{"first packed blob", "another packed blob"} {
		ref := blob.RefFromString(contents).String()
		fmt.Fprintf(&pack, "[%s %d]", ref, len(contents))
		rows[ref] = fmt.Sprintf("0 %d %d", pack.Len(), len(contents))
		pack.WriteString(contents)
	}
	if err := os.WriteFile(filepath.Join(root, "pack-00000.blobs"), pack.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return rows
}

func adminDiskpackedHandler(path string, metaIndex any) map[string]any {
	args := map[string]any{"path": path}
	if metaIndex != nil {
		args["metaIndex"] = metaIndex
	}
	return map[string]any{"handler": "storage-diskpacked", "handlerArgs": args}
}

func writeAdminDiskpackedConfig(t *testing.T, file string, prefixes map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"handlerConfig": true, "prefixes": prefixes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func adminDiskpackedIndexRows(t *testing.T, file string, set map[string]string) map[string]string {
	t.Helper()
	kv, err := sorted.NewKeyValue(jsonconfig.Obj{"type": sorted.DefaultKVFileType, "file": file})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := kv.Close(); err != nil {
			t.Error(err)
		}
	}()
	for k, v := range set {
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	rows := make(map[string]string)
	if err := sorted.Foreach(kv, func(k, v string) error {
		rows[k] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAdminReindexDiskpackedCheckAndOverwrite(t *testing.T) {
	dir := adminConfigDir(t)
	root := filepath.Join(dir, "packs")
	want := writeAdminDiskpackedPack(t, root)
	index := filepath.Join(root, "index."+sorted.DefaultKVFileType)
	stale := blob.RefFromString("no longer in the pack").String()
	initial := map[string]string{stale: "0 123 4"}
	for ref := range want {
		initial[ref] = "0 0 0"
		break // One wrong row and one missing row.
	}
	adminDiskpackedIndexRows(t, index, initial)
	packFile := filepath.Join(root, "pack-00000.blobs")
	packBefore, err := os.ReadFile(packFile)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dir, "must-not-open")
	writeAdminDiskpackedConfig(t, filepath.Join(dir, "server-config.json"), map[string]any{
		"/packs/":  adminDiskpackedHandler(root, nil),
		"/bad/":    map[string]any{"handler": "does-not-exist"},
		"/unused/": map[string]any{"handler": "storage-filesystem", "handlerArgs": map[string]any{"path": unrelated}},
	})
	checkAdminReindexDiskpacked(t, 1, "index does not match data")
	// Checking must not repair logical rows. Opening an index can still write
	// backend housekeeping files, so comparing its bytes would be incorrect.
	if got := adminDiskpackedIndexRows(t, index, nil); !reflect.DeepEqual(got, initial) {
		t.Fatalf("check changed index rows: %v; want %v", got, initial)
	}
	checkAdminReindexDiskpacked(t, 0, "indexConf:", "--overwrite")
	// Reindex updates packed rows, but does not wipe stale entries.
	want[stale] = initial[stale]
	if got := adminDiskpackedIndexRows(t, index, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("rebuilt rows = %v; want %v", got, want)
	}
	checkAdminReindexDiskpacked(t, 0, "indexConf:")
	if after, err := os.ReadFile(packFile); err != nil || !bytes.Equal(after, packBefore) {
		t.Errorf("pack changed: %v", err)
	}
	if _, err := os.Stat(unrelated); !os.IsNotExist(err) {
		t.Errorf("unrelated handler path was touched: %v", err)
	}
}

func TestAdminReindexDiskpackedPathAndMetaIndex(t *testing.T) {
	for _, kind := range []string{"custom", "empty-object", "non-object"} {
		t.Run(kind, func(t *testing.T) {
			dir := adminConfigDir(t)
			root := filepath.Join(dir, "selected")
			want := writeAdminDiskpackedPack(t, root)
			defaultIndex := filepath.Join(root, "index."+sorted.DefaultKVFileType)
			index := defaultIndex
			var metaIndex any
			switch kind {
			case "custom":
				index = filepath.Join(dir, "custom-index")
				metaIndex = map[string]any{"type": sorted.DefaultKVFileType, "file": index}
			case "empty-object":
				metaIndex = map[string]any{}
			case "non-object":
				metaIndex = "ignored, as in the old recovery command"
			}
			initial := adminDiskpackedIndexRows(t, index, nil)
			writeAdminDiskpackedConfig(t, filepath.Join(dir, "server-config.json"), map[string]any{
				"/selected/": adminDiskpackedHandler(root, metaIndex),
				// An unselected handler must not open its broken index.
				"/other/": adminDiskpackedHandler(filepath.Join(dir, "other"), map[string]any{"type": "does-not-exist"}),
			})
			checkAdminReindexDiskpacked(t, 1, "Ambiguity")
			checkAdminReindexDiskpacked(t, 1, "doesn't specify a disk-packed", "--overwrite", filepath.Join(dir, "unconfigured"))
			checkAdminReindexDiskpacked(t, 1, "doesn't specify a disk-packed", root+string(filepath.Separator))
			if got := adminDiskpackedIndexRows(t, index, nil); !reflect.DeepEqual(got, initial) {
				t.Fatal("selection errors changed the index")
			}
			checkAdminReindexDiskpacked(t, 0, "indexConf:", "--overwrite", root)
			if got := adminDiskpackedIndexRows(t, index, nil); !reflect.DeepEqual(got, want) {
				t.Fatalf("selected index = %v; want %v", got, want)
			}
			if kind == "custom" {
				if _, err := os.Stat(defaultIndex); !os.IsNotExist(err) {
					t.Errorf("custom metaIndex fell back to default index: %v", err)
				}
			}
			for _, name := range []string{"other", "unconfigured"} {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Errorf("unselected path %s was touched: %v", name, err)
				}
			}
		})
	}
}

func TestAdminReindexDiskpackedConfigErrors(t *testing.T) {
	for _, tt := range []struct {
		name, config, want string
	}{
		{"missing", "", "server-config.json"},
		{"malformed", "{", "perkeepd admin reindex-diskpacked:"},
		{"no-prefixes", `{"handlerConfig":true}`, "No 'prefixes' object"},
		{"empty-prefixes", `{"handlerConfig":true,"prefixes":{}}`, "doesn't specify a disk-packed"},
		{"not-diskpacked", `{"handlerConfig":true,"prefixes":{"/bs/":{"handler":"storage-filesystem"}}}`, "doesn't specify a disk-packed"},
		{"bad-prefix", `{"handlerConfig":true,"prefixes":{"/bs/":false}}`, "doesn't specify a disk-packed"},
		{"missing-args", `{"handlerConfig":true,"prefixes":{"/bs/":{"handler":"storage-diskpacked"}}}`, "doesn't specify a disk-packed"},
		{"missing-path", `{"handlerConfig":true,"prefixes":{"/bs/":{"handler":"storage-diskpacked","handlerArgs":{}}}}`, "doesn't specify a disk-packed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := adminConfigDir(t)
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "server-config.json"), []byte(tt.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			checkAdminReindexDiskpacked(t, 1, tt.want)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			wantEntries := 1
			if tt.config == "" {
				wantEntries = 0
			}
			if len(entries) != wantEntries {
				t.Errorf("configuration failure created files: %v", entries)
			}
		})
	}
}

func TestAdminReindexDiskpackedInvalidMetaIndex(t *testing.T) {
	dir := adminConfigDir(t)
	root := filepath.Join(dir, "packs")
	want := writeAdminDiskpackedPack(t, root)
	index := filepath.Join(root, "index."+sorted.DefaultKVFileType)
	adminDiskpackedIndexRows(t, index, want)
	writeAdminDiskpackedConfig(t, filepath.Join(dir, "server-config.json"), map[string]any{
		"/packs/": adminDiskpackedHandler(root, map[string]any{"type": "does-not-exist"}),
	})
	// A configured but invalid backend must fail, not silently fall back to
	// the valid default index.
	checkAdminReindexDiskpacked(t, 1, `Invalid sorted.KeyValue type "does-not-exist"`, "--overwrite")
	if got := adminDiskpackedIndexRows(t, index, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid metaIndex changed default index: %v; want %v", got, want)
	}
}
