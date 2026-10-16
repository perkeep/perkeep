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
	"fmt"
	"testing"
	"unsafe"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
)

func TestCorpusFileCamera(t *testing.T) {
	type tag struct {
		dir   int
		id    string
		value string
	}
	makeTag := tag{exifIFD0, "010f", "string|1|Camera+Maker"}
	modelTag := tag{exifIFD0, "0110", "string|1|Camera+Model"}
	tests := []struct {
		name        string
		tags        []tag
		make, model string
	}{
		{"both", []tag{makeTag, modelTag}, "Camera Maker", "Camera Model"},
		{"make", []tag{makeTag}, "Camera Maker", ""},
		{"model", []tag{modelTag}, "", "Camera Model"},
		{"missing", nil, "", ""},
		{"empty", []tag{{exifIFD0, "010f", "string|1|"}, {exifIFD0, "0110", "string|1|"}}, "", ""},
		{"padding", []tag{
			{exifIFD0, "010f", "string|1|" + urle("\x00 \tCamera Maker \x00\n")},
			{exifIFD0, "0110", "string|1|" + urle("\u2003\x00 Camera Model\x00 \r")},
		}, "Camera Maker", "Camera Model"},
		{"onlyPadding", []tag{{exifIFD0, "010f", "string|1|%00+%09%00"}, {exifIFD0, "0110", "string|1|+%00"}}, "", ""},
		{"escaped", []tag{
			{exifIFD0, "010f", "string|1|" + urle("A+B & C|D%20/日本")},
			{exifIFD0, "0110", "string|1|" + urle("Model +|%")},
		}, "A+B & C|D%20/日本", "Model +|%"},
		{"otherTags", []tag{{exifIFD0, "010e", "string|1|Description"}, {exifIFD0, "a434", "string|1|Lens"}}, "", ""},
		{"ignoreOtherDirectories", []tag{
			makeTag, modelTag,
			{exifIFD1, "010f", "string|1|Thumbnail+Maker"},
			{exifIFD1, "0110", "string|1|Thumbnail+Model"},
			{exifIFD, "010f", "string|1|Exif+Maker"},
			{exifIFDGPS, "0110", "string|1|GPS+Model"},
			{8, "010f", "string|1|Legacy+Offset"},
		}, "Camera Maker", "Camera Model"},
	}
	for _, dir := range []int{exifIFD1, exifIFD, exifIFDGPS, 8, 123456} {
		tests = append(tests, struct {
			name        string
			tags        []tag
			make, model string
		}{
			name: fmt.Sprintf("directory%d", dir),
			tags: []tag{{dir, "010f", makeTag.value}, {dir, "0110", modelTag.value}},
		})
	}
	for _, value := range []string{
		"", "string", "string|1", "string|0|Camera", "string|2|A|B",
		"int|1|42", "rat|1|1/2", "float|1|1.5", "unknown|1|Camera",
		"string|1|Bad%", "string|1|Bad%GG",
	} {
		tests = append(tests, struct {
			name        string
			tags        []tag
			make, model string
		}{
			name: "value=" + value,
			tags: []tag{{exifIFD0, "010f", value}, {exifIFD0, "0110", value}},
		})
	}

	for _, mode := range []string{"startup", "incremental"} {
		t.Run(mode, func(t *testing.T) {
			rows := make(map[string]string)
			for _, tt := range tests {
				file := blob.RefFromString(tt.name + " file")
				whole := blob.RefFromString(tt.name + " whole")
				rows[keyWholeToFileRef.Key(whole, file)] = "1"
				for _, tag := range tt.tags {
					rows[keyEXIFTag.Key(whole, tag.dir, tag.id)] = tag.value
				}
			}
			sharedFile := blob.RefFromString("shared file")
			rows[keyWholeToFileRef.Key(blob.RefFromString("both whole"), sharedFile)] = "1"

			var c *Corpus
			if mode == "startup" {
				kv := sorted.NewMemoryKeyValue()
				t.Cleanup(func() { kv.Close() })
				for k, v := range rows {
					if err := kv.Set(k, v); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				c, err = NewCorpusFromStorage(kv)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				c = newCorpus()
				if err := c.addBlob(context.Background(), blob.RefFromString("received"), &mutationMap{kv: rows}); err != nil {
					t.Fatal(err)
				}
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					file := blob.RefFromString(tt.name + " file")
					gotMake, gotModel, ok := c.FileCamera(file)
					wantOK := tt.make != "" || tt.model != ""
					if gotMake != tt.make || gotModel != tt.model || ok != wantOK {
						t.Errorf("FileCamera = (%q, %q, %v); want (%q, %q, %v)",
							gotMake, gotModel, ok, tt.make, tt.model, wantOK)
					}
				})
			}
			for _, file := range []blob.Ref{{}, blob.RefFromString("unknown"), blob.RefFromString("both whole")} {
				if make, model, ok := c.FileCamera(file); make != "" || model != "" || ok {
					t.Errorf("unmapped FileCamera(%v) = (%q, %q, %v)", file, make, model, ok)
				}
			}
			if make, model, ok := c.FileCamera(sharedFile); make != "Camera Maker" || model != "Camera Model" || !ok {
				t.Errorf("shared FileCamera = (%q, %q, %v)", make, model, ok)
			}
			make1, model1, _ := c.FileCamera(blob.RefFromString("both file"))
			make2, model2, _ := c.FileCamera(blob.RefFromString("padding file"))
			if unsafe.StringData(make1) != unsafe.StringData(make2) || unsafe.StringData(model1) != unsafe.StringData(model2) {
				t.Error("camera strings are not interned")
			}
		})
	}
}

func TestCorpusFileCameraIncrementalParts(t *testing.T) {
	c := newCorpus()
	file, whole := blob.RefFromString("file"), blob.RefFromString("whole")
	add := func(k, v string) {
		t.Helper()
		if err := c.addBlob(context.Background(), blob.RefFromString(k+v), &mutationMap{kv: map[string]string{k: v}}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(wantMake, wantModel string, wantOK bool) {
		t.Helper()
		if make, model, ok := c.FileCamera(file); make != wantMake || model != wantModel || ok != wantOK {
			t.Errorf("FileCamera = (%q, %q, %v); want (%q, %q, %v)", make, model, ok, wantMake, wantModel, wantOK)
		}
	}
	add(keyEXIFTag.Key(whole, exifIFD0, "010f"), "string|1|Maker")
	check("", "", false) // The whole-to-file row can arrive after the EXIF row.
	add(keyWholeToFileRef.Key(whole, file), "1")
	check("Maker", "", true)
	add(keyEXIFTag.Key(whole, exifIFD0, "0110"), "string|1|Model")
	check("Maker", "Model", true)
	add(keyEXIFTag.Key(whole, exifIFD0, "010f"), "string|1|Invalid%")
	check("Maker", "Model", true) // Bad optional data does not replace valid data.
	add(keyEXIFTag.Key(whole, exifIFD0, "010f"), "string|1|%00")
	check("", "Model", true)
	add(keyEXIFTag.Key(whole, exifIFD0, "0110"), "string|1|+")
	check("", "", false)
	if len(c.camera) != 0 {
		t.Error("retained an empty camera entry")
	}
}

func TestCorpusCameraMalformedKeys(t *testing.T) {
	whole := blob.RefFromString("whole")
	for _, key := range []string{
		"exiftag|", "exiftag|" + whole.String(), fmt.Sprintf("exiftag|%s|0", whole),
		"exiftag|not-a-ref|0|010f", fmt.Sprintf("exiftag|%s|0|010f|extra", whole),
	} {
		t.Run(key, func(t *testing.T) {
			kv := sorted.NewMemoryKeyValue()
			t.Cleanup(func() { kv.Close() })
			if err := kv.Set(key, "string|1|Maker"); err != nil {
				t.Fatal(err)
			}
			if _, err := NewCorpusFromStorage(kv); err == nil {
				t.Error("startup accepted a malformed key")
			}
			c := newCorpus()
			if err := c.addBlob(context.Background(), blob.RefFromString("received"),
				&mutationMap{kv: map[string]string{key: "string|1|Maker"}}); err == nil {
				t.Error("incremental merge accepted a malformed key")
			}
			if len(c.camera) != 0 {
				t.Error("malformed key added camera metadata")
			}
		})
	}
}

func TestCorpusCameraIgnoredRows(t *testing.T) {
	whole := blob.RefFromString("whole")
	for _, row := range []struct{ key, value string }{
		{keyEXIFTag.Key(whole, exifIFD1, "010f"), "string|1|Thumbnail"},
		{keyEXIFTag.Key(whole, exifIFD, "010f"), "string|1|Exif"},
		{keyEXIFTag.Key(whole, exifIFDGPS, "0110"), "string|1|GPS"},
		{keyEXIFTag.Key(whole, 8, "010f"), "string|1|Legacy"},
		{keyEXIFTag.Key(whole, exifIFD0, "010e"), "string|1|Description"},
		{keyEXIFTag.Key(whole, exifIFD0, "010f"), "int|1|42"},
		{keyEXIFTag.Key(whole, exifIFD0, "0110"), "string|2|A|B"},
	} {
		t.Run(row.key+"="+row.value, func(t *testing.T) {
			c := newCorpus()
			if err := c.mergeEXIFTagRow([]byte(row.key), []byte(row.value)); err != nil {
				t.Fatal(err)
			}
			if len(c.brOfStr) != 0 || len(c.camera) != 0 || len(c.strs) != 0 {
				t.Error("ignored row cached a blobref or camera metadata")
			}
		})
	}
}
