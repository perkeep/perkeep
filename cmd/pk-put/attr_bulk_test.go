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
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"perkeep.org/pkg/schema"
)

const attrBulkPN = "sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db"

func TestAttrBulkClaims(t *testing.T) {
	longValue := strings.Repeat("x", 128<<10)
	for _, tt := range []struct {
		name      string
		cmd       attrBulkCmd
		input     string
		claimType string
		values    []string
	}{
		{"set", attrBulkCmd{}, attrBulkPN + "\ttitle\t hello 世界 \n" + attrBulkPN + "\ttitle\tsecond\n", "set-attribute", []string{" hello 世界 ", "second"}},
		{"add", attrBulkCmd{add: true}, attrBulkPN + "\ttitle\tfoo\n", "add-attribute", []string{"foo"}},
		{"delete value", attrBulkCmd{del: true}, attrBulkPN + "\ttitle\tfoo\n", "del-attribute", []string{"foo"}},
		{"delete all", attrBulkCmd{del: true}, attrBulkPN + "\ttitle\n", "del-attribute", []string{""}},
		{"empty value", attrBulkCmd{}, attrBulkPN + "\ttitle\t\n", "set-attribute", []string{""}},
		{"delete empty value", attrBulkCmd{del: true}, attrBulkPN + "\ttitle\t\n", "del-attribute", []string{""}},
		{"no final newline", attrBulkCmd{}, attrBulkPN + "\ttitle\tlast", "set-attribute", []string{"last"}},
		{"CRLF", attrBulkCmd{}, attrBulkPN + "\ttitle\tfoo\r\n", "set-attribute", []string{"foo"}},
		{"final CR without LF", attrBulkCmd{}, attrBulkPN + "\ttitle\tfoo\r", "set-attribute", []string{"foo\r"}},
		{"long line", attrBulkCmd{}, attrBulkPN + "\ttitle\t" + longValue + "\n", "set-attribute", []string{longValue}},
		{"empty input", attrBulkCmd{}, "", "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var values []string
			err := tt.cmd.run(strings.NewReader(tt.input), func(bb *schema.Builder) error {
				data, err := bb.JSON()
				if err != nil {
					return err
				}
				var claim struct {
					Type      string `json:"camliType"`
					ClaimType string `json:"claimType"`
					Permanode string `json:"permaNode"`
					Attribute string `json:"attribute"`
					Value     string `json:"value"`
				}
				if err := json.Unmarshal([]byte(data), &claim); err != nil {
					return err
				}
				if claim.Type != "claim" || claim.ClaimType != tt.claimType || claim.Permanode != attrBulkPN || claim.Attribute != "title" {
					t.Errorf("unexpected claim: %+v", claim)
				}
				values = append(values, claim.Value)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(values) != len(tt.values) {
				t.Fatalf("uploaded %d claims; want %d", len(values), len(tt.values))
			}
			for i, want := range tt.values {
				if values[i] != want {
					t.Errorf("claim %d value differs (got %d bytes, want %d)", i, len(values[i]), len(want))
				}
			}
		})
	}
}

func TestAttrBulkInvalidRecords(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cmd    attrBulkCmd
		record string
		want   string
	}{
		{"blank", attrBulkCmd{}, "", "expected"},
		{"missing value", attrBulkCmd{}, attrBulkPN + "\ttag", "expected"},
		{"missing add value", attrBulkCmd{add: true}, attrBulkPN + "\ttag", "expected"},
		{"missing name", attrBulkCmd{del: true}, attrBulkPN, "expected"},
		{"extra fields", attrBulkCmd{}, attrBulkPN + "\ttag\tfoo\textra", "expected"},
		{"extra delete fields", attrBulkCmd{del: true}, attrBulkPN + "\ttag\tfoo\textra", "expected"},
		{"invalid ref", attrBulkCmd{}, "bad-ref\ttag\tfoo", "invalid permanode blobref"},
		{"empty name", attrBulkCmd{}, attrBulkPN + "\t\tfoo", "attribute name must not be empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := attrBulkPN + "\ttag\tfirst\n" + tt.record + "\n" + attrBulkPN + "\ttag\tlast\n"
			calls := 0
			err := tt.cmd.run(strings.NewReader(input), func(*schema.Builder) error { calls++; return nil })
			if err == nil || !strings.Contains(err.Error(), "line 2:") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want line 2 and %q", err, tt.want)
			}
			if calls != 1 {
				t.Fatalf("uploaded %d claims; want 1", calls)
			}
		})
	}
}

type attrBulkErrorReader struct{ err error }

func (r attrBulkErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestAttrBulkReadError(t *testing.T) {
	readErr := errors.New("stdin failed")
	for _, partial := range []string{"", attrBulkPN + "\ttag\tpartial"} {
		input := io.MultiReader(strings.NewReader(attrBulkPN+"\ttag\tfirst\n"+partial), attrBulkErrorReader{readErr})
		calls := 0
		err := new(attrBulkCmd).run(input, func(*schema.Builder) error { calls++; return nil })
		if !errors.Is(err, readErr) || !strings.Contains(err.Error(), "line 2: reading stdin") {
			t.Fatalf("error = %v; want line 2 read error", err)
		}
		if calls != 1 {
			t.Fatalf("uploaded %d claims; want 1", calls)
		}
	}
}

func TestAttrBulkUploadError(t *testing.T) {
	uploadErr := errors.New("upload failed")
	input := strings.Repeat(attrBulkPN+"\ttag\tfoo\n", 3)
	calls := 0
	err := new(attrBulkCmd).run(strings.NewReader(input), func(*schema.Builder) error {
		calls++
		if calls == 2 {
			return uploadErr
		}
		return nil
	})
	if !errors.Is(err, uploadErr) || !strings.Contains(err.Error(), "line 2: uploading claim") {
		t.Fatalf("error = %v; want line 2 upload error", err)
	}
	if calls != 2 {
		t.Fatalf("attempted %d uploads; want 2", calls)
	}
}

func TestAttrBulkCommand(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  []string
		input string
		code  int
		want  string
	}{
		{"empty", nil, "", 0, ""},
		{"positional argument", []string{"unexpected"}, "", 1, "takes no positional arguments"},
		{"exclusive flags", []string{"--add", "--del"}, "", 1, "Add and del options are exclusive"},
		{"stdin", nil, "invalid\ttag\tfoo\n", 2, "line 1: invalid permanode blobref"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := &env{stdin: strings.NewReader(tt.input)}
			// cmdmain keeps registered command flags across invocations.
			args := []string{"attr-bulk", "--add=false", "--del=false"}
			stdout, stderr, code := e.Run(append(args, tt.args...)...)
			if code != tt.code || !strings.Contains(string(stderr), tt.want) {
				t.Fatalf("exit %d, stdout=%s stderr=%s; want exit %d and %q", code, stdout, stderr, tt.code, tt.want)
			}
			if len(stdout) != 0 {
				t.Errorf("unexpected stdout: %s", stdout)
			}
		})
	}
}
