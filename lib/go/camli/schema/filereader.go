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

type FileReader struct {
	fetcher blobref.Fetcher
	ss      *Superset
	ci      int    // index into contentparts
	ccon    uint64 // bytes into current chunk already consumed
}

// TODO: make this take a blobref.FetcherAt instead?
func NewFileReader(fetcher blobref.Fetcher, fileBlobRef *blobref.BlobRef) (*FileReader, os.Error) {
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

func (ss *Superset) NewFileReader(fetcher blobref.Fetcher) *FileReader {
	// TODO: return an error if ss isn't a Type "file" ?
	// TODO: return some error if the redundant ss.Size field doesn't match ContentParts?
	return &FileReader{fetcher, ss, 0, 0}
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
		if fr.ccon == cp.Size {
			fr.ci++
			fr.ccon = 0
		}
		skipBytes -= toSkip
	}
}

func (fr *FileReader) Read(p []byte) (n int, err os.Error) {
	var cp *ContentPart
	for {
		if fr.ci >= len(fr.ss.ContentParts) {
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

	br := cp.blobref()
	if br == nil {
		return 0, fmt.Errorf("no blobref in content part %d", fr.ci)
	}
	// TODO: performance: don't re-fetch this on every
	// Read call.  most parts will be large relative to
	// read sizes.  we should stuff the rsc away in fr
	// and re-use it just re-seeking if needed, which
	// could also be tracked.
	log.Printf("filereader: fetching blob %s", br)
	rsc, _, ferr := fr.fetcher.Fetch(br)
	if ferr != nil {
		return 0, fmt.Errorf("schema: FileReader.Read error fetching blob %s: %v", br, ferr)
	}
	defer rsc.Close()

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
	if err == nil || err == os.EOF {
		fr.ccon += uint64(n)
	}
	return
}

func minu64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
