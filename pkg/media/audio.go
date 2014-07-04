/*
Copyright 2014 The Camlistore Authors.

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

// Package media provides means for querying information about audio and video data.
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"camlistore.org/pkg/types"
)

// ID3v1TagLength is the length of an MP3 ID3v1 tag in bytes.
const ID3v1TagLength = 128

// id3v1Magic is the byte sequence appearing at the beginning of an ID3v1 tag.
var id3v1Magic = []byte("TAG")

// HasID3V1Tag returns true if an ID3v1 tag is present at the end of r.
func HasID3v1Tag(r types.SizeReaderAt) (bool, error) {
	if r.Size() < ID3v1TagLength {
		return false, nil
	}

	buf := make([]byte, len(id3v1Magic), len(id3v1Magic))
	if _, err := r.ReadAt(buf, r.Size()-ID3v1TagLength); err != nil {
		return false, fmt.Errorf("Failed to read ID3v1 data: %v", err)
	}
	if bytes.Equal(buf, id3v1Magic) {
		return true, nil
	}
	return false, nil
}

type mpegVersion int

const (
	mpegVersion1 mpegVersion = iota
	mpegVersion2
	mpegVersion2_5
)

// mpegVersionsById maps from a 2-bit version ID from an MPEG header to the corresponding MPEG audio version.
var mpegVersionsById = map[uint32]mpegVersion{
	0x0: mpegVersion2_5,
	0x2: mpegVersion2,
	0x3: mpegVersion1,
}

type mpegLayer int

const (
	mpegLayer1 mpegLayer = iota
	mpegLayer2
	mpegLayer3
)

// mpegLayersByIndex maps from a 2-bit layer index from an MPEG header to the corresponding MPEG layer.
var mpegLayersByIndex = map[uint32]mpegLayer{
	0x1: mpegLayer3,
	0x2: mpegLayer2,
	0x3: mpegLayer1,
}

// mpegBitrates is indexed by a 4-bit bitrate index from an MPEG header. Values are in kilobits.
var mpegBitrates = map[mpegVersion]map[mpegLayer][16]int{
	mpegVersion1: {
		mpegLayer1: {0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0},
		mpegLayer2: {0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},
		mpegLayer3: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
	},
	mpegVersion2: {
		mpegLayer1: {0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
		mpegLayer2: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		mpegLayer3: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
	},
	mpegVersion2_5: {
		mpegLayer1: {0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
		mpegLayer2: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		mpegLayer3: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
	},
}

// mpegSamplingRates is indexed by a 2-bit sampling rate index from an MPEG header. Values are in hertz.
var mpegSamplingRates = map[mpegVersion][4]int{
	mpegVersion1:   {44100, 48000, 32000, 0},
	mpegVersion2:   {22050, 24000, 16000, 0},
	mpegVersion2_5: {11025, 12000, 8000, 0},
}

var mpegSamplesPerFrame = map[mpegVersion]map[mpegLayer]int{
	mpegVersion1: {
		mpegLayer1: 384,
		mpegLayer2: 1152,
		mpegLayer3: 1152,
	},
	mpegVersion2: {
		mpegLayer1: 384,
		mpegLayer2: 1152,
		mpegLayer3: 576,
	},
	mpegVersion2_5: {
		mpegLayer1: 384,
		mpegLayer2: 1152,
		mpegLayer3: 576,
	},
}

var xingHeaderName = []byte("Xing")
var infoHeaderName = []byte("Info")

// GetMPEGAudioDuration reads the first frame in r and returns the audio length with millisecond precision.
// Format details are at http://www.codeproject.com/Articles/8295/MPEG-Audio-Frame-Header.
func GetMPEGAudioDuration(r types.SizeReaderAt) (time.Duration, error) {
	var header uint32
	if err := binary.Read(io.NewSectionReader(r, 0, r.Size()), binary.BigEndian, &header); err != nil {
		return 0, fmt.Errorf("Failed to read MPEG frame header: %v", err)
	}
	getBits := func(startBit, numBits uint) uint32 {
		return (header << startBit) >> (32 - numBits)
	}

	if getBits(0, 11) != 0x7ff {
		return 0, errors.New("Missing sync bits in MPEG frame header")
	}
	var version mpegVersion
	var ok bool
	if version, ok = mpegVersionsById[getBits(11, 2)]; !ok {
		return 0, errors.New("Invalid MPEG version index")
	}
	var layer mpegLayer
	if layer, ok = mpegLayersByIndex[getBits(13, 2)]; !ok {
		return 0, errors.New("Invalid MPEG layer index")
	}
	bitrate := mpegBitrates[version][layer][getBits(16, 4)]
	if bitrate == 0 {
		return 0, errors.New("Invalid MPEG bitrate")
	}
	samplingRate := mpegSamplingRates[version][getBits(20, 2)]
	if samplingRate == 0 {
		return 0, errors.New("Invalid MPEG sample rate")
	}
	samplesPerFrame := mpegSamplesPerFrame[version][layer]

	var xingHeaderStart int64 = 4
	// Skip "side information".
	if getBits(24, 2) == 0x3 { // Channel mode; 0x3 is mono.
		xingHeaderStart += 17
	} else {
		xingHeaderStart += 32
	}
	// Skip 16-bit CRC if present.
	if getBits(15, 1) == 0x0 { // 0x0 means "has protection".
		xingHeaderStart += 2
	}

	b := make([]byte, 12, 12)
	if _, err := r.ReadAt(b, xingHeaderStart); err != nil {
		return 0, fmt.Errorf("Unable to read Xing header at %d: %v", xingHeaderStart, err)
	}
	var ms int64
	if bytes.Equal(b[0:4], xingHeaderName) || bytes.Equal(b[0:4], infoHeaderName) {
		r := bytes.NewReader(b[4:])
		var xingFlags uint32
		binary.Read(r, binary.BigEndian, &xingFlags)
		if xingFlags&0x1 == 0x0 {
			return 0, fmt.Errorf("Xing header at %d lacks number of frames", xingHeaderStart)
		}
		var numFrames uint32
		binary.Read(r, binary.BigEndian, &numFrames)
		ms = int64(samplesPerFrame) * int64(numFrames) * 1000 / int64(samplingRate)
	} else {
		// Okay, no Xing VBR header. Assume that the file has a constant bitrate.
		// (The other alternative is to read the whole file and examine each frame.)
		ms = r.Size() / int64(bitrate) * 8
	}
	return time.Duration(ms) * time.Millisecond, nil
}
