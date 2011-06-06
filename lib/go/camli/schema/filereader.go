/*
Copyright 2011 Google Inc.

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

package schema

import (
	"fmt"
	"json"
	"log"
	"os"

	"camli/blobref"
)

var _ = log.Printf

type FileReader struct {
	fetcher blobref.SeekFetcher
	ss      *Superset
	ci      int    // index into contentparts
	ccon    uint64 // bytes into current chunk already consumed
	remain  int64  // bytes remaining

	cr   blobref.ReadSeekCloser // cached reader
	crbr *blobref.BlobRef       // the blobref that cr is for
}

// TODO: make this take a blobref.FetcherAt instead?
func NewFileReader(fetcher blobref.SeekFetcher, fileBlobRef *blobref.BlobRef) (*FileReader, os.Error) {
	ss := new(Superset)
	rsc, _, err := fetcher.Fetch(fileBlobRef)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: fetching file schema blob: %v", err)
	}
	if err = json.NewDecoder(rsc).Decode(ss); err != nil {
		return nil, fmt.Errorf("schema/filereader: decoding file schema blob: %v", err)
	}
	if ss.Type != "file" {
		return nil, fmt.Errorf("schema/filereader: expected \"file\" schema blob, got %q", ss.Type)
	}
	return ss.NewFileReader(fetcher), nil
}

func (ss *Superset) NewFileReader(fetcher blobref.SeekFetcher) *FileReader {
	// TODO: return an error if ss isn't a Type "file"
	//
	return &FileReader{fetcher: fetcher, ss: ss, remain: int64(ss.Size)}
}

// FileSchema returns the reader's schema superset. Don't mutate it.
func (fr *FileReader) FileSchema() *Superset {
	return fr.ss
}

func (fr *FileReader) Skip(skipBytes uint64) {
	for skipBytes != 0 && fr.ci < len(fr.ss.ContentParts) {
		cp := fr.ss.ContentParts[fr.ci]
		thisChunkSkippable := cp.Size - fr.ccon
		toSkip := minu64(skipBytes, thisChunkSkippable)
		fr.ccon += toSkip
		fr.remain -= int64(toSkip)
		if fr.ccon == cp.Size {
			fr.ci++
			fr.ccon = 0
		}
		skipBytes -= toSkip
	}
}

func (fr *FileReader) closeOpenBlobs() {
	if fr.cr != nil {
		fr.cr.Close()
		fr.cr = nil
		fr.crbr = nil
	}
}

func (fr *FileReader) readerFor(br *blobref.BlobRef) (blobref.ReadSeekCloser, os.Error) {
	if fr.crbr == br {
		return fr.cr, nil
	}
	fr.closeOpenBlobs()
	rsc, _, ferr := fr.fetcher.Fetch(br)
	if ferr != nil {
		return nil, ferr
	}
	fr.crbr = br
	fr.cr = rsc
	return rsc, nil
}

func (fr *FileReader) Read(p []byte) (n int, err os.Error) {
	var cp *ContentPart
	for {
		if fr.ci >= len(fr.ss.ContentParts) {
			fr.closeOpenBlobs()
			if fr.remain > 0 {
				return 0, fmt.Errorf("schema: declared file schema size was larger than sum of content parts")
			}
			return 0, os.EOF
		}
		cp = fr.ss.ContentParts[fr.ci]
		thisChunkReadable := cp.Size - fr.ccon
		if thisChunkReadable == 0 {
			fr.ci++
			fr.ccon = 0
			continue
		}
		break
	}

	if cp.Size == 0 {
		return 0, fmt.Errorf("blobref content part contained illegal size 0")
	}

	br := cp.blobref()
	if br == nil {
		return 0, fmt.Errorf("no blobref in content part %d", fr.ci)
	}

	rsc, ferr := fr.readerFor(br)
	if ferr != nil {
		return 0, fmt.Errorf("schema: FileReader.Read error fetching blob %s: %v", br, ferr)
	}

	seekTo := cp.Offset + fr.ccon
	if seekTo != 0 {
		_, serr := rsc.Seek(int64(seekTo), 0)
		if serr != nil {
			return 0, fmt.Errorf("schema: FileReader.Read seek error on blob %s: %v", br, serr)
		}
	}

	readSize := cp.Size - fr.ccon
	if uint64(len(p)) < readSize {
		readSize = uint64(len(p))
	}

	n, err = rsc.Read(p[:int(readSize)])
	fr.ccon += uint64(n)
	fr.remain -= int64(n)
	if fr.remain < 0 {
		err = fmt.Errorf("schema: file schema was invalid; content parts sum to over declared size")
	}
	return
}

func minu64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
