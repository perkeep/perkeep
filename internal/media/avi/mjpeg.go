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

package avi

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"sync"
)

// DecodeMJPEG decodes a Motion JPEG video frame. Motion JPEG frames, such
// as those of older cameras' AVI videos, often omit their Huffman tables
// (DHT segments) and use the standard ones of the JPEG specification,
// Annex K.3, which DecodeMJPEG adds if needed.
func DecodeMJPEG(frame []byte) (image.Image, error) {
	if len(frame) >= 2 && !hasHuffmanTables(frame) {
		withDHT := make([]byte, 0, len(frame)+len(standardDHT()))
		withDHT = append(withDHT, frame[:2]...) // SOI
		withDHT = append(withDHT, standardDHT()...)
		frame = append(withDHT, frame[2:]...)
	}
	return jpeg.Decode(bytes.NewReader(frame))
}

// hasHuffmanTables reports whether the JPEG frame has a DHT segment
// before its start of scan.
func hasHuffmanTables(frame []byte) bool {
	for b := frame[2:]; len(b) >= 4 && b[0] == 0xff; {
		switch b[1] {
		case 0xc4: // DHT
			return true
		case 0xda: // SOS
			return false
		}
		n := int(binary.BigEndian.Uint16(b[2:]))
		if n < 2 || 2+n > len(b) {
			return false
		}
		b = b[2+n:]
	}
	return false
}

// standardDHT returns a DHT segment of the JPEG specification's standard
// Huffman tables, which Go's image/jpeg encoder writes.
var standardDHT = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	// Color, for the chrominance tables too.
	if err := jpeg.Encode(&buf, image.NewYCbCr(image.Rect(0, 0, 8, 8), image.YCbCrSubsampleRatio444), nil); err != nil {
		panic(err)
	}
	b := buf.Bytes()
	i := bytes.Index(b, []byte{0xff, 0xc4})
	if i < 0 {
		panic("avi: image/jpeg wrote no DHT segment")
	}
	n := int(binary.BigEndian.Uint16(b[i+2:]))
	return bytes.Clone(b[i : i+2+n])
})
