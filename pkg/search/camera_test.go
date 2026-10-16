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

package search_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	. "perkeep.org/pkg/search"
)

func TestQueryCamera(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		galaxy, _ := id.UploadFile("galaxy.jpg", cameraJPEG(t, "Samsung", "Galaxy Nexus"), time.Time{})
		galaxyCopy, _ := id.UploadFile("copy.jpg", cameraJPEG(t, "Samsung", "Galaxy Nexus"), time.Time{})
		iphone, _ := id.UploadFile("iphone.jpg", cameraJPEG(t, "Apple", "iPhone 16 Pro"), time.Time{})
		makeOnly, _ := id.UploadFile("make.jpg", cameraJPEG(t, "Samsung", ""), time.Time{})
		modelOnly, _ := id.UploadFile("model.jpg", cameraJPEG(t, "", "Galaxy Nexus"), time.Time{})
		noCamera, _ := id.UploadFile("plain.jpg", cameraJPEG(t, "", ""), time.Time{})
		id.UploadFile("notes.txt", "Galaxy Nexus", time.Time{})
		nodes := make(map[blob.Ref]blob.Ref)
		for _, file := range []blob.Ref{galaxy, galaxyCopy, iphone, makeOnly, modelOnly, noCamera} {
			pn := id.NewDeterministicPermanodeForTest(file.String())
			id.SetAttribute(pn, "camliContent", file.String())
			nodes[file] = pn
		}
		for _, tt := range []struct {
			expression string
			files      []blob.Ref
		}{
			{`camera:"Galaxy Nexus"`, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{`camera:"galaxy nexus"`, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{`camera:samsung`, []blob.Ref{galaxy, galaxyCopy, makeOnly}},
			{`camera:~NEXUS`, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{`camera:*nexus*`, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{`camera:galaxy*`, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{`camera:*NEXUS`, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{`camera:*`, []blob.Ref{galaxy, galaxyCopy, iphone, makeOnly, modelOnly}},
			{`camera:Samsung camera:~nexus`, []blob.Ref{galaxy, galaxyCopy}},
			{`camera:Apple or camera:Samsung`, []blob.Ref{galaxy, galaxyCopy, iphone, makeOnly}},
			{`camera:* -camera:Samsung`, []blob.Ref{iphone, modelOnly}},
			{`-(camera:Apple or camera:Samsung)`, []blob.Ref{modelOnly, noCamera}},
			{`camera:Nexus`, nil},
			{`camera:Unknown`, nil},
		} {
			t.Logf("query: %s", tt.expression)
			var wanted []blob.Ref
			for _, file := range tt.files {
				wanted = append(wanted, nodes[file])
			}
			qt.wantRes(&SearchQuery{Expression: tt.expression}, wanted...)
		}
		// JSON constraints use the usual StringConstraint semantics, including
		// case sensitivity, regular expressions, and conjunction of fields.
		for _, tt := range []struct {
			sc    *StringConstraint
			files []blob.Ref
		}{
			{&StringConstraint{Equals: "Galaxy Nexus"}, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{&StringConstraint{Equals: "galaxy nexus"}, nil},
			{&StringConstraint{Contains: "NEXUS", CaseInsensitive: true}, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{&StringConstraint{HasPrefix: "Galaxy", HasSuffix: "Nexus"}, []blob.Ref{galaxy, galaxyCopy, modelOnly}},
			{&StringConstraint{Regexp: "^iPhone [0-9]+ Pro$"}, []blob.Ref{iphone}},
			{&StringConstraint{Contains: "Samsung", HasSuffix: "Nexus"}, nil},
			{&StringConstraint{Empty: true}, nil},
		} {
			qt.wantRes(&SearchQuery{Constraint: &Constraint{File: &FileConstraint{Camera: tt.sc}}}, tt.files...)
		}
		var sq SearchQuery
		if err := json.Unmarshal([]byte(`{"constraint":{"file":{"camera":{"contains":"nexus","caseInsensitive":true}}}}`), &sq); err != nil {
			t.Fatal(err)
		}
		qt.wantRes(&sq, galaxy, galaxyCopy, modelOnly)
	})
}

// cameraJPEG builds a small JPEG with optional IFD0 make/model strings.
func cameraJPEG(t *testing.T, make, model string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	x := exifBuf{bo: binary.BigEndian, p: []byte("MM")}
	x.putUint16(42)
	x.putUint32(8)
	type tag struct {
		id    uint16
		value string
		ptr   int
	}
	var tags []tag
	if make != "" {
		tags = append(tags, tag{id: 0x010f, value: make})
	}
	if model != "" {
		tags = append(tags, tag{id: 0x0110, value: model})
	}
	x.putUint16(uint16(len(tags)))
	for i := range tags {
		tag := &tags[i]
		x.putTag(tag.id, 2, uint32(len(tag.value)+1))
		if len(tag.value)+1 <= 4 {
			copy(x.next(4), tag.value)
		} else {
			tag.ptr = x.reservePtr()
		}
	}
	x.putUint32(0) // no thumbnail IFD
	for _, tag := range tags {
		if tag.ptr != 0 {
			x.storePtr(tag.ptr)
			copy(x.next(len(tag.value)+1), tag.value)
		}
	}
	exif := append([]byte("Exif\x00\x00"), x.p...)
	app1 := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(exif)+2))
	result := append([]byte{}, buf.Bytes()[:2]...)
	result = append(result, app1...)
	result = append(result, exif...)
	result = append(result, buf.Bytes()[2:]...)
	return string(result)
}
