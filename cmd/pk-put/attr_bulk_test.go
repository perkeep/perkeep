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
	"fmt"
	"io"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
)

const attrBulkPN = "sha224-0808f64e60d58979fcb676c96ec938270dea42445aeefcd3a4e6f8db"

type attrBulkTestOperation struct {
	ClaimType string `json:"claimType"`
	Permanode string `json:"permaNode"`
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
}

func attrBulkTestClaims(t *testing.T, bb *schema.Builder) []attrBulkTestOperation {
	t.Helper()
	data, err := bb.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > attrBulkMaxUnsignedBytes {
		t.Fatalf("unsigned batch is too large: %d bytes", len(data))
	}
	var batch struct {
		Type      string                  `json:"camliType"`
		ClaimType string                  `json:"claimType"`
		Claims    []attrBulkTestOperation `json:"claims"`
	}
	if err := json.Unmarshal([]byte(data), &batch); err != nil {
		t.Fatal(err)
	}
	if batch.Type != "claim" || batch.ClaimType != "multi" || len(batch.Claims) == 0 || len(batch.Claims) > attrBulkBatchSize {
		t.Fatalf("unexpected batch: %+v", batch)
	}
	return batch.Claims
}

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
				for _, claim := range attrBulkTestClaims(t, bb) {
					if claim.ClaimType != tt.claimType || claim.Permanode != attrBulkPN || claim.Attribute != "title" {
						t.Errorf("unexpected claim: %+v", claim)
					}
					values = append(values, claim.Value)
				}
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

func TestAttrBulkBatches(t *testing.T) {
	for _, n := range []int{0, 1, attrBulkBatchSize - 1, attrBulkBatchSize, attrBulkBatchSize + 1, 2 * attrBulkBatchSize, 2*attrBulkBatchSize + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			for _, finalNewline := range []bool{false, true} {
				var input strings.Builder
				for i := range n {
					pn := blob.RefFromString(fmt.Sprintf("permanode %d", i))
					fmt.Fprintf(&input, "%s\ttitle\tvalue %d", pn, i)
					if i+1 < n || finalNewline {
						input.WriteByte('\n')
					}
				}
				batches, operations := 0, 0
				err := new(attrBulkCmd).run(strings.NewReader(input.String()), func(bb *schema.Builder) error {
					claims := attrBulkTestClaims(t, bb)
					want := min(attrBulkBatchSize, n-operations)
					if len(claims) != want {
						t.Fatalf("batch %d has %d operations; want %d", batches, len(claims), want)
					}
					for _, claim := range claims {
						pn := blob.RefFromString(fmt.Sprintf("permanode %d", operations))
						if claim.Permanode != pn.String() || claim.Value != fmt.Sprintf("value %d", operations) {
							t.Fatalf("operation %d is out of order: %+v", operations, claim)
						}
						operations++
					}
					batches++
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if want := (n + attrBulkBatchSize - 1) / attrBulkBatchSize; batches != want || operations != n {
					t.Fatalf("uploaded %d batches and %d operations; want %d and %d", batches, operations, want, n)
				}
			}
		})
	}
}

func TestAttrBulkBatchSizeLimit(t *testing.T) {
	for _, value := range []string{strings.Repeat("x", 400<<10), strings.Repeat("\x00", 80<<10)} {
		var input strings.Builder
		for i := range 3 {
			fmt.Fprintf(&input, "%s\ttitle\t%d%s\n", attrBulkPN, i, value)
		}
		batches, operations := 0, 0
		err := new(attrBulkCmd).run(strings.NewReader(input.String()), func(bb *schema.Builder) error {
			for _, claim := range attrBulkTestClaims(t, bb) {
				if claim.Value != fmt.Sprint(operations)+value {
					t.Fatalf("operation %d is out of order or corrupted", operations)
				}
				operations++
			}
			batches++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if batches < 2 || operations != 3 {
			t.Fatalf("uploaded %d batches and %d operations; want multiple batches and 3 operations", batches, operations)
		}
	}
}

func TestAttrBulkOversizedOperation(t *testing.T) {
	input := attrBulkPN + "\ttitle\t" + strings.Repeat("x", schema.MaxSchemaBlobSize) + "\n"
	err := new(attrBulkCmd).run(strings.NewReader(input), func(*schema.Builder) error {
		t.Fatal("oversized claim was uploaded")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "line 1: attribute claim exceeds") {
		t.Fatalf("error = %v; want oversized operation error on line 1", err)
	}
}

func TestAttrBulkSignedSize(t *testing.T) {
	entity, err := jsonsign.EntityFromSecring("2931A67C26F5ABDA", "../../pkg/jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := schema.NewSigner(blob.RefFromString(publicKey), strings.NewReader(publicKey), entity)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := schema.NewMultiClaim(schema.NewSetAttributeClaim(blob.MustParse(attrBulkPN), "title", ""))
	if err != nil {
		t.Fatal(err)
	}
	data, err := empty.JSON()
	if err != nil {
		t.Fatal(err)
	}
	// Fill almost the entire unsigned budget, allowing a little variation in
	// the claim date's serialized length.
	value := strings.Repeat("x", attrBulkMaxUnsignedBytes-len(data)-128)
	input := attrBulkPN + "\ttitle\t" + value + "\n"
	calls := 0
	err = new(attrBulkCmd).run(strings.NewReader(input), func(bb *schema.Builder) error {
		calls++
		attrBulkTestClaims(t, bb)
		signed, err := bb.Sign(ctxbg, signer)
		if err != nil {
			return err
		}
		if len(signed) > schema.MaxSchemaBlobSize {
			t.Fatalf("signed batch exceeds the schema size limit: %d bytes", len(signed))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("uploaded %d batches; want 1", calls)
	}
}

func TestAttrBulkSplitUploadError(t *testing.T) {
	uploadErr := errors.New("split upload failed")
	input := strings.Repeat(attrBulkPN+"\ttitle\t"+strings.Repeat("x", 400<<10)+"\n", 5)
	calls := 0
	err := new(attrBulkCmd).run(strings.NewReader(input), func(bb *schema.Builder) error {
		calls++
		attrBulkTestClaims(t, bb)
		if calls == 2 {
			return uploadErr
		}
		return nil
	})
	// Splitting five large operations produces batches of two, one, and two.
	// A failure on the second upload must not attempt the last batch.
	if !errors.Is(err, uploadErr) || !strings.Contains(err.Error(), "lines 3-3: uploading multi claim") {
		t.Fatalf("error = %v; want upload error on line 3", err)
	}
	if calls != 2 {
		t.Fatalf("attempted %d uploads; want 2", calls)
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
			// One full batch is committed; a pending record before the bad
			// line is discarded rather than flushed on the error.
			input := strings.Repeat(attrBulkPN+"\ttag\tfirst\n", attrBulkBatchSize+1) + tt.record + "\n" + attrBulkPN + "\ttag\tlast\n"
			calls := 0
			err := tt.cmd.run(strings.NewReader(input), func(bb *schema.Builder) error {
				calls++
				if got := len(attrBulkTestClaims(t, bb)); got != attrBulkBatchSize {
					t.Errorf("uploaded %d operations; want %d", got, attrBulkBatchSize)
				}
				return nil
			})
			line := fmt.Sprintf("line %d:", attrBulkBatchSize+2)
			if err == nil || !strings.Contains(err.Error(), line) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want %s and %q", err, line, tt.want)
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
	for _, complete := range []int{0, 1, attrBulkBatchSize, attrBulkBatchSize + 1} {
		for _, partial := range []string{"", attrBulkPN + "\ttag\tpartial"} {
			input := io.MultiReader(strings.NewReader(strings.Repeat(attrBulkPN+"\ttag\tfirst\n", complete)+partial), attrBulkErrorReader{readErr})
			calls := 0
			err := new(attrBulkCmd).run(input, func(*schema.Builder) error { calls++; return nil })
			line := fmt.Sprintf("line %d: reading stdin", complete+1)
			if !errors.Is(err, readErr) || !strings.Contains(err.Error(), line) {
				t.Fatalf("error = %v; want %s error", err, line)
			}
			if want := complete / attrBulkBatchSize; calls != want {
				t.Fatalf("uploaded %d batches; want %d", calls, want)
			}
		}
	}
}

func TestAttrBulkUploadError(t *testing.T) {
	uploadErr := errors.New("upload failed")
	for _, n := range []int{attrBulkBatchSize + 1, 3 * attrBulkBatchSize} {
		input := strings.Repeat(attrBulkPN+"\ttag\tfoo\n", n)
		calls := 0
		err := new(attrBulkCmd).run(strings.NewReader(input), func(*schema.Builder) error {
			calls++
			if calls == 2 {
				return uploadErr
			}
			return nil
		})
		lineRange := fmt.Sprintf("lines %d-%d: uploading multi claim", attrBulkBatchSize+1, min(n, 2*attrBulkBatchSize))
		if !errors.Is(err, uploadErr) || !strings.Contains(err.Error(), lineRange) {
			t.Fatalf("error = %v; want %s error", err, lineRange)
		}
		if calls != 2 {
			t.Fatalf("attempted %d uploads; want 2", calls)
		}
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
