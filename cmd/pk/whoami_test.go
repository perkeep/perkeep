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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
)

func TestWhoami(t *testing.T) {
	ent, err := jsonsign.EntityFromSecring("2931A67C26F5ABDA", "../../pkg/jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	armorPub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := schema.NewSigner(blob.RefFromString(armorPub), strings.NewReader(armorPub), ent)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		cmd  string
		want string
	}{
		{"identity", "perkeepid:2931A67C26F5ABDA\n"},
		{"fingerprint", "perkeepid:FBB89AA320A2806FE497C0492931A67C26F5ABDA\n"},
	} {
		t.Run(tt.cmd, func(t *testing.T) {
			cmd, ok := whoCmds[tt.cmd]
			if !ok {
				t.Fatalf("whoami subcommand %q is not registered", tt.cmd)
			}
			out, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
			if err != nil {
				t.Fatal(err)
			}
			stdout := os.Stdout
			os.Stdout = out
			t.Cleanup(func() {
				os.Stdout = stdout
				out.Close()
			})
			if err := cmd(nil, signer); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(out.Name())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("output = %q; want %q", got, tt.want)
			}
		})
	}
}
