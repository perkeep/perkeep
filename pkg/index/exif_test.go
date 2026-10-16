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

package index

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

func TestGetEXIF(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%v", corpus), func(t *testing.T) {
			x := NewMemoryIndex()
			t.Cleanup(func() { x.Close() })
			set := func(k, v string) {
				t.Helper()
				if err := x.s.Set(k, v); err != nil {
					t.Fatal(err)
				}
			}
			file, whole := blob.RefFromString("file"), blob.RefFromString("whole")
			copyFile := blob.RefFromString("copy file")
			emptyFile, emptyWhole := blob.RefFromString("empty file"), blob.RefFromString("empty whole")
			noWhole := blob.RefFromString("file without whole")
			dir := blob.RefFromString("directory")
			for _, row := range []struct {
				file, whole blob.Ref
				typ         string
			}{
				{file, whole, "file"}, {copyFile, whole, "file"},
				{emptyFile, emptyWhole, "file"}, {noWhole, blob.Ref{}, "file"},
				{dir, whole, "directory"},
			} {
				set("meta:"+row.file.String(), "100|application/json; camliType="+row.typ)
				wholeString := ""
				if row.whole.Valid() {
					wholeString = row.whole.String()
				}
				set("fileinfo|"+row.file.String(), "1|photo.jpg|image%2Fjpeg|"+wholeString)
			}
			// Whole digests and other blobs are not file schema references.
			set("meta:"+whole.String(), "1|image/jpeg")

			if corpus {
				x.Lock()
				_, err := x.KeepInMemory()
				x.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}

			// Add optional rows after building the corpus, proving GetEXIF
			// reads storage rather than retaining full EXIF in memory.
			for suffix, value := range map[string]string{
				"0|010f": "string|1|A%2BB+%26+C%7CD%2520%2F%E6%97%A5%E6%9C%AC%00",
				"0|0112": "int|1|1",
				"0|c001": "int|3|-1|9007199254740993|4294967295",
				"1|0112": "int|1|6",
				"1|c001": "string|1|", // empty strings are still values
				"2|829a": "rat|2|123456789/987654321|-1/0",
				"2|9003": "string|1|2026:01:02+03:04:05",
				"2|c001": "float|2|1.2345678901234567|1e-100",
				"3|0002": "rat|3|12/1|30/1|0/1",
				"3|010f": "string|1|Not+a+camera+make",
				// Malformed optional rows must not prevent a valid response.
				"0|0001":       "string|1|Invalid%",
				"0|0002":       "int|2|1",
				"0|0003":       "rat|1|1/not-a-number",
				"0|0004":       "float|1|not-a-number",
				"0|0005":       "bytes|1|3",
				"0|0006":       "int|-1|",
				"0|0007":       "string|1|a|b",
				"0|0008":       "string|2|a|b",
				"0|0009":       "int|1|9223372036854775808",
				"0|010F":       "string|1|Uppercase+tag",
				"0|001":        "string|1|Short+tag",
				"0|gggg":       "string|1|Not+hex",
				"0|c002|extra": "int|1|1",
				"4|010f":       "string|1|Unknown+directory",
				"00|010f":      "string|1|Noncanonical+directory",
				"0":            "string|1|Missing+tag",
			} {
				set(keyEXIFTag.Prefix(whole)+suffix, value)
			}
			// Similar prefixes and unrelated whole-file tags cannot leak in.
			set(keyEXIFTag.Key(emptyWhole, exifIFD0, "0001"), "invalid optional row")
			set(keyEXIFTag.Key(blob.RefFromString("another whole"), exifIFD0, "010f"), "string|1|Other")

			wantTags := []camtypes.EXIFTag{
				{Directory: "IFD0", Tag: "010f", Name: "Make", Type: "string", Values: []string{"A+B & C|D%20/日本\x00"}},
				{Directory: "IFD0", Tag: "0112", Name: "Orientation", Type: "int", Values: []string{"1"}},
				{Directory: "IFD0", Tag: "c001", Name: "Unknown tag", Type: "int", Values: []string{"-1", "9007199254740993", "4294967295"}},
				{Directory: "IFD1", Tag: "0112", Name: "Orientation", Type: "int", Values: []string{"6"}},
				{Directory: "IFD1", Tag: "c001", Name: "Unknown tag", Type: "string", Values: []string{""}},
				{Directory: "Exif", Tag: "829a", Name: "ExposureTime", Type: "rat", Values: []string{"123456789/987654321", "-1/0"}},
				{Directory: "Exif", Tag: "9003", Name: "DateTimeOriginal", Type: "string", Values: []string{"2026:01:02 03:04:05"}},
				{Directory: "Exif", Tag: "c001", Name: "Unknown tag", Type: "float", Values: []string{"1.2345678901234567", "1e-100"}},
				{Directory: "GPS", Tag: "0002", Name: "GPSLatitude", Type: "rat", Values: []string{"12/1", "30/1", "0/1"}},
				{Directory: "GPS", Tag: "010f", Name: "Unknown tag", Type: "string", Values: []string{"Not a camera make"}},
			}
			for _, ref := range []blob.Ref{file, copyFile} {
				x.RLock()
				got, err := x.GetEXIF(context.Background(), ref)
				x.RUnlock()
				want := &camtypes.EXIFInfo{FileRef: ref, WholeRef: whole, Tags: wantTags}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("GetEXIF(%s) = %#v, %v; want %#v", ref, got, err, want)
					if got != nil {
						t.Logf("tags: %+v", got.Tags)
					}
				}
			}
			for _, ref := range []blob.Ref{emptyFile, noWhole} {
				x.RLock()
				got, err := x.GetEXIF(context.Background(), ref)
				x.RUnlock()
				if err != nil || got == nil || got.Tags == nil || len(got.Tags) != 0 {
					t.Errorf("GetEXIF(empty %s) = %+v, %v; want non-nil empty tags", ref, got, err)
				}
			}
			for _, ref := range []blob.Ref{dir, whole, blob.RefFromString("missing"), {}} {
				x.RLock()
				_, err := x.GetEXIF(context.Background(), ref)
				x.RUnlock()
				if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("GetEXIF(%v) error = %v; want os.ErrNotExist", ref, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			x.RLock()
			_, err := x.GetEXIF(ctx, file)
			x.RUnlock()
			if !errors.Is(err, context.Canceled) {
				t.Errorf("canceled GetEXIF error = %v", err)
			}
		})
	}
}

func TestParseEXIFTag(t *testing.T) {
	for _, value := range []string{
		"", "string", "string|1", "string|1|Bad%GG", "int|bogus|1",
		"int|4294967296|", "rat|1|1/2/3", "rat|1|bad/2", "int|1|",
	} {
		if got, ok := parseEXIFTag("0|010f", value); ok {
			t.Errorf("parseEXIFTag(%q) = %+v, want malformed", value, got)
		}
	}
	for _, typ := range []string{"int", "rat", "float"} {
		got, ok := parseEXIFTag("0|c001", typ+"|0|")
		if !ok || got.Values == nil || len(got.Values) != 0 {
			t.Errorf("zero-component %s = %+v, %v; want empty values", typ, got, ok)
		}
	}
}
