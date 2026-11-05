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

package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStorageJSON(t *testing.T) {
	var c Config
	in := `{"storage": {"nas": {"type": "blobpacked", "dir": "/mnt/nas"}}}`
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	s := c.Storage["nas"]
	if s == nil || s.Type != "blobpacked" || s.Local == nil || s.Local.Dir != "/mnt/nas" {
		t.Fatalf("decoded %+v", s)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), `{"type":"blobpacked","dir":"/mnt/nas"}`; got != want {
		t.Errorf("encoded %s; want %s", got, want)
	}

	in = `{"type": "s3", "bucket": "b/dir", "accessKey": "k", "secretKey": "@/etc/s3-secret", "packed": false}`
	var s3 Storage
	if err := json.Unmarshal([]byte(in), &s3); err != nil {
		t.Fatal(err)
	}
	if s3.Local != nil || s3.S3 == nil || s3.S3.Bucket != "b/dir" || s3.S3.AccessKey != "k" || s3.S3.SecretKey != "@/etc/s3-secret" || s3.S3.Packed == nil || *s3.S3.Packed {
		t.Fatalf("decoded %+v, %+v", s3, s3.S3)
	}
	out, err = json.Marshal(s3)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), `{"type":"s3","bucket":"b/dir","accessKey":"k","secretKey":"@/etc/s3-secret","packed":false}`; got != want {
		t.Errorf("encoded %s; want %s", got, want)
	}

	for _, tt := range []struct{ in, wantErr string }{
		{`{"dir": "/mnt/nas"}`, "no type"},
		{`{"type": "tape", "dir": "/mnt/nas"}`, `unknown type "tape"`},
		// A typo, or a field of another type.
		{`{"type": "blobpacked", "dri": "/mnt/nas"}`, `unknown field "dri"`},
		{`{"type": "filesystem", "dir": "/mnt/nas", "bucket": "b"}`, `unknown field "bucket"`},
		{`{"type": "s3", "bucket": "b", "dir": "/mnt/nas"}`, `unknown field "dir"`},
	} {
		var s Storage
		err := json.Unmarshal([]byte(tt.in), &s)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("Unmarshal(%s) = %v; want an error with %q", tt.in, err, tt.wantErr)
		}
	}
}
