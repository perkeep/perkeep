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
	"image"
	"image/jpeg"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	. "perkeep.org/pkg/search"
)

func TestQueryLivePhoto(t *testing.T) {
	testQueryTypes(t, memIndexTypes, func(qt *queryTest) {
		id := qt.id
		const idA, idB, idC = "AAAAAAAA-0000-4000-8000-000000000001", "BBBBBBBB-0000-4000-8000-000000000002", "CCCCCCCC-0000-4000-8000-000000000003"
		stillA, _ := id.UploadFile("IMG_0001.JPG", livePhotoJPEG(t, idA), time.Time{})
		videoA, _ := id.UploadFile("IMG_0001.MOV", livePhotoMOV(idA, "Apple", "iPhone 16 Pro"), time.Time{})
		stillB, _ := id.UploadFile("IMG_0002.JPG", livePhotoJPEG(t, idB), time.Time{})     // video not uploaded
		videoC, _ := id.UploadFile("IMG_0003.MOV", livePhotoMOV(idC, "", ""), time.Time{}) // still not uploaded
		plain, _ := id.UploadFile("plain.jpg", cameraJPEG(t, "", ""), time.Time{})
		nodes := make(map[blob.Ref]blob.Ref)
		for _, file := range []blob.Ref{stillA, videoA, stillB, videoC, plain} {
			pn := id.NewDeterministicPermanodeForTest(file.String())
			id.SetAttribute(pn, "camliContent", file.String())
			nodes[file] = pn
		}
		for _, tt := range []struct {
			expression string
			files      []blob.Ref
		}{
			{`is:livephoto`, []blob.Ref{stillA}},
			{`is:livephotovideo`, []blob.Ref{videoA}},
			{`livephoto:aaaaaaaa-0000-4000-8000-000000000001`, []blob.Ref{stillA, videoA}},
			{`livephoto:` + idB, []blob.Ref{stillB}},
			{`livephoto:unknown`, nil},
			{`-is:livephotovideo`, []blob.Ref{stillA, stillB, videoC, plain}},
			{`camera:"iPhone 16 Pro"`, []blob.Ref{videoA}},
		} {
			t.Logf("query: %s", tt.expression)
			var wanted []blob.Ref
			for _, file := range tt.files {
				wanted = append(wanted, nodes[file])
			}
			qt.wantRes(&SearchQuery{Expression: tt.expression}, wanted...)
		}
		qt.wantRes(&SearchQuery{Constraint: &Constraint{File: &FileConstraint{LivePhoto: "any"}}}, stillA, videoA)
		if _, err := qt.Handler().Query(ctxbg, &SearchQuery{Constraint: &Constraint{File: &FileConstraint{LivePhoto: "bogus"}}}); err == nil {
			t.Error("invalid livePhoto constraint value accepted")
		}

		res, err := qt.Handler().Describe(ctxbg, &DescribeRequest{BlobRefs: []blob.Ref{stillA, videoC}})
		if err != nil {
			t.Fatal(err)
		}
		if lp := res.Meta.Get(stillA).LivePhoto; lp == nil || lp.ContentIdentifier != idA || lp.Video != videoA || lp.Still.Valid() {
			t.Errorf("still LivePhoto = %+v; want video %v", lp, videoA)
		}
		if db := res.Meta.Get(videoA); db == nil || db.File == nil || db.File.FileName != "IMG_0001.MOV" ||
			db.LivePhoto == nil || db.LivePhoto.Still != stillA || db.MediaTags[index.MediaTagDurationMS] != "740" {
			t.Errorf("video not described with its Live Photo still: %+v", db)
		}
		if db := res.Meta.Get(videoC); db.LivePhoto != nil || db.MediaTags[index.MediaTagContentIdentifier] != idC {
			t.Errorf("unpaired video described as %+v", db)
		}
	})
}

// livePhotoJPEG returns a small JPEG whose EXIF has an Apple MakerNote
// with the given content identifier.
func livePhotoJPEG(t *testing.T, contentID string) string {
	t.Helper()
	be := binary.BigEndian
	// Apple MakerNote: header, one ASCII entry, its value.
	mn := append([]byte("Apple iOS\x00\x00\x01MM"), 0, 1)
	mn = be.AppendUint16(mn, 0x0011)
	mn = be.AppendUint16(mn, 2)
	mn = be.AppendUint32(mn, uint32(len(contentID)+1))
	mn = be.AppendUint32(mn, uint32(len(mn)+4))
	mn = append(append(mn, contentID...), 0)
	// TIFF: IFD0 at 8 pointing to the Exif IFD at 26, holding the
	// MakerNote, whose value is at 44.
	x := []byte("MM\x00\x2a")
	x = be.AppendUint32(x, 8)
	x = be.AppendUint16(x, 1)
	x = be.AppendUint16(x, 0x8769)
	x = be.AppendUint16(x, 4)
	x = be.AppendUint32(x, 1)
	x = be.AppendUint32(x, 26)
	x = be.AppendUint32(x, 0)
	x = be.AppendUint16(x, 1)
	x = be.AppendUint16(x, 0x927c)
	x = be.AppendUint16(x, 7)
	x = be.AppendUint32(x, uint32(len(mn)))
	x = be.AppendUint32(x, 44)
	x = be.AppendUint32(x, 0)
	x = append(x, mn...)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	exif := append([]byte("Exif\x00\x00"), x...)
	app1 := be.AppendUint16([]byte{0xff, 0xe1}, uint16(len(exif)+2))
	result := append([]byte{}, buf.Bytes()[:2]...)
	result = append(result, app1...)
	result = append(result, exif...)
	result = append(result, buf.Bytes()[2:]...)
	return string(result)
}

func qtBox(typ string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	return append(append(binary.BigEndian.AppendUint32(nil, uint32(8+len(b))), typ...), b...)
}

// livePhotoMOV returns a 740 ms QuickTime movie with the given Apple
// metadata, omitting empty values.
func livePhotoMOV(contentID, cameraMake, cameraModel string) string {
	be := binary.BigEndian
	var keys, ilst [][]byte
	n := uint32(0)
	for _, kv := range [][2]string{
		{"com.apple.quicktime.content.identifier", contentID},
		{"com.apple.quicktime.make", cameraMake},
		{"com.apple.quicktime.model", cameraModel},
	} {
		if kv[1] == "" {
			continue
		}
		n++
		keys = append(keys, be.AppendUint32(nil, uint32(8+len(kv[0]))), []byte("mdta"+kv[0]))
		ilst = append(ilst, qtBox(string(be.AppendUint32(nil, n)), qtBox("data", be.AppendUint32(nil, 1), make([]byte, 4), []byte(kv[1]))))
	}
	return string(bytes.Join([][]byte{
		qtBox("ftyp", []byte("qt  "), make([]byte, 4), []byte("qt  ")),
		qtBox("mdat", make([]byte, 100)),
		qtBox("moov",
			qtBox("mvhd", make([]byte, 12), be.AppendUint32(nil, 600), be.AppendUint32(nil, 444), make([]byte, 80)),
			qtBox("meta",
				qtBox("hdlr", make([]byte, 8), []byte("mdta"), make([]byte, 12)),
				qtBox("keys", make([]byte, 4), be.AppendUint32(nil, n), bytes.Join(keys, nil)),
				qtBox("ilst", ilst...))),
	}, nil))
}
