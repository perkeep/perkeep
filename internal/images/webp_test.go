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

package images

import (
	"bytes"
	"os"
	"testing"
)

// hithere.webp is the original attachment from issue #850.
func TestDecodeWebPIssue850(t *testing.T) {
	data, err := os.ReadFile("testdata/hithere.webp")
	if err != nil {
		t.Fatal(err)
	}
	conf, err := DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if conf.Format != "webp" || conf.Width != 37 || conf.Height != 12 {
		t.Fatalf("DecodeConfig = %+v; want webp 37x12", conf)
	}
	for _, opts := range []*DecodeOpts{nil, {MaxWidth: 20, MaxHeight: 20}} {
		im, conf, err := Decode(bytes.NewReader(data), opts)
		if err != nil {
			t.Fatal(err)
		}
		wantW, wantH := 37, 12
		if opts != nil {
			wantW, wantH = 20, 6
		}
		if conf.Format != "webp" || im.Bounds().Dx() != wantW || im.Bounds().Dy() != wantH {
			t.Fatalf("Decode(%+v) = %v, %+v; want webp %dx%d", opts, im.Bounds(), conf, wantW, wantH)
		}
	}
}
