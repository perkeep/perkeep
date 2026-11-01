/*
Copyright 2026 The Perkeep Authors.

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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/secretstore"
	"perkeep.org/pkg/server/secrets"
	"perkeep.org/pkg/types/camtypes"
)

func TestSecretParse(t *testing.T) {
	for _, args := range [][]string{
		{}, {"unknown"}, {"list", "name"}, {"get"}, {"get", "name", "another"},
		{"set", "name", "do-not-echo-secret"}, {"set", "-revision=-2", "name"},
		{"get", "-file", "file", "name"}, {"delete", "-rotated", "name"},
		{"set", "-unknown=do-not-echo-secret", "name"},
	} {
		c := &secretCmd{revision: -1}
		_, _, err := c.parse(args)
		if err == nil {
			t.Errorf("accepted %q", args)
		} else if strings.Contains(err.Error(), "do-not-echo-secret") {
			t.Error("parse error echoed a value")
		}
	}
	for _, prefix := range []string{"-", "--"} {
		t.Run(prefix, func(t *testing.T) {
			c := &secretCmd{revision: -1}
			verb, name, err := c.parse([]string{
				"set", prefix + "revision", "3", prefix + "rotated",
				prefix + "file", "-", prefix + "json",
				prefix + "server", "https://example.com", "service/token",
			})
			if err != nil || verb != "set" || name != "service/token" || c.revision != 3 || !c.rotated || c.file != "-" || !c.json || c.server != "https://example.com" {
				t.Fatalf("parsed %+v %q %q %v", c, verb, name, err)
			}
		})
	}
}

func TestSecretCLIRoundTrip(t *testing.T) {
	store, err := secretstore.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := &auth.UserPass{Username: "owner", Password: "password"}
	auth.SetMode(owner)
	defer auth.SetMode(auth.None{})
	mux := http.NewServeMux()
	mux.Handle("/secrets/", secrets.New(store))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, `{"blobRoot":"/bs/","secretRoot":"/secrets/"}`)
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	cl, err := client.New(client.OptionNoExternalConfig(), client.OptionServer(s.URL), client.OptionAuthMode(owner))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const name = "importer/sha224-abc/token"
	const legacyValue = "sensitive-legacy-value\n"
	if err := store.Migrate(ctx, []secretstore.LegacyEntry{{Name: name, Value: legacyValue}}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run := func(c *secretCmd, verb, input string) {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if err := c.run(ctx, cl, verb, name, strings.NewReader(input), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
	}
	c := &secretCmd{revision: -1}
	run(c, "get", "")
	if stdout.String() != legacyValue || !strings.Contains(stderr.String(), "⚠") || !strings.Contains(stderr.String(), "immutable blobs") || !strings.Contains(stderr.String(), "pk secret set --rotated") {
		t.Fatal("raw get did not preserve exact value and stderr migration warning")
	}
	run(c, "list", "")
	if strings.Contains(stdout.String(), legacyValue) || !strings.Contains(stdout.String(), "⚠") || !strings.Contains(stdout.String(), "migratedFromPermanode:true") {
		t.Fatal("list must contain metadata warning, not the value")
	}
	c.json = true
	run(c, "list", "")
	var list []camtypes.SecretMetadata
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil || len(list) != 1 || !list[0].MigratedFromPermanode || strings.Contains(stdout.String(), `"value"`) {
		t.Fatal("JSON list missing safe migration metadata")
	}
	run(c, "get", "")
	var entry camtypes.SecretEntry
	if err := json.Unmarshal(stdout.Bytes(), &entry); err != nil || entry.Value != legacyValue || !entry.MigratedFromPermanode || !strings.Contains(stderr.String(), "⚠") {
		t.Fatal("JSON get missing value or warning metadata")
	}
	run(c, "set", "new\nvalue\n")
	if strings.Contains(stdout.String(), "new\\nvalue") || !strings.Contains(stderr.String(), "⚠") {
		t.Fatal("ordinary set exposed value or cleared warning")
	}
	c.rotated = true
	c.file = filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(c.file, []byte("rotated-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(c, "set", "ignored stdin")
	if strings.Contains(stdout.String(), "rotated-value") || strings.Contains(stderr.String(), "⚠") {
		t.Fatal("explicitly rotated set did not clear metadata warning safely")
	}
	c.file = ""
	c.rotated = false
	c.json = false
	run(c, "get", "")
	if stdout.String() != "rotated-value\n" || stderr.Len() != 0 {
		t.Fatal("file input not preserved or stale warning")
	}
	c.revision = 0
	stdout.Reset()
	stderr.Reset()
	if err := c.run(ctx, cl, "set", name, strings.NewReader("not-written"), &stdout, &stderr); err == nil {
		t.Fatal("CLI lost create-only CAS")
	}
	c.revision = -1
	run(c, "delete", "")
	if stdout.Len() != 0 {
		t.Fatal("delete unexpected output")
	}
	if _, err := store.Get(ctx, name); err == nil {
		t.Fatal("delete did not reach store")
	}
}

type countingSecretClient struct{ puts int }

func (*countingSecretClient) ListSecrets(context.Context) ([]camtypes.SecretMetadata, error) {
	return nil, nil
}
func (*countingSecretClient) GetSecret(context.Context, string) (camtypes.SecretEntry, error) {
	return camtypes.SecretEntry{}, nil
}
func (c *countingSecretClient) PutSecret(context.Context, string, string, client.SecretPutOptions) (camtypes.SecretMetadata, error) {
	c.puts++
	return camtypes.SecretMetadata{}, nil
}
func (*countingSecretClient) DeleteSecret(context.Context, string, int64) error { return nil }

func TestSecretCLIInputLimit(t *testing.T) {
	c := &secretCmd{revision: -1}
	cl := new(countingSecretClient)
	if err := c.run(context.Background(), cl, "set", "name", strings.NewReader(strings.Repeat("x", (1<<20)+1)), new(bytes.Buffer), new(bytes.Buffer)); err == nil {
		t.Fatal("oversized stdin accepted")
	}
	if cl.puts != 0 {
		t.Fatal("oversized secret sent to server")
	}
	c.file = filepath.Join(t.TempDir(), "missing")
	if err := c.run(context.Background(), cl, "set", "name", strings.NewReader(""), new(bytes.Buffer), new(bytes.Buffer)); err == nil {
		t.Fatal("missing input file silently accepted")
	}
}

func TestSecretCLIRejectsInvalidUTF8(t *testing.T) {
	for _, fromFile := range []bool{false, true} {
		c := &secretCmd{revision: -1}
		cl := new(countingSecretClient)
		if fromFile {
			c.file = filepath.Join(t.TempDir(), "value")
			if err := os.WriteFile(c.file, []byte{0xff}, 0600); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		err := c.run(context.Background(), cl, "set", "name", bytes.NewReader([]byte{0xff}), &stdout, &stderr)
		if err == nil || err.Error() != "secret value must be valid UTF-8" {
			t.Fatalf("invalid UTF-8: %v", err)
		}
		if cl.puts != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatal("invalid UTF-8 was sent or printed")
		}
	}
}
