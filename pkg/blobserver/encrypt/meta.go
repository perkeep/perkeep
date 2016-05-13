/*
Copyright 2016 The Camlistore Authors

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

package encrypt

import (
	"bytes"
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/sorted"
)

// Encrypted meta format:
//    #camlistore/encmeta=2
// Then sorted lines, each ending in a newline, like:
//    sha1-plain/<plain size>/sha1-encrypted

const (
	// FullMetaBlobSize is the size at which we stop compacting a meta blob.
	FullMetaBlobSize = 512 << 10
)

type metaEntry struct {
	plain     blob.SizedRef
	encrypted blob.Ref
}

type metaBlob struct {
	br      blob.Ref // of meta blob
	entries []metaEntry
}

// metaBlobHeap is a heap of metaBlobs.
// heap.Pop returns the metaBlob with the LEAST entries.
type metaBlobHeap []*metaBlob

var _ heap.Interface = (*metaBlobHeap)(nil)

func (s *metaBlobHeap) Push(x interface{}) {
	*s = append(*s, x.(*metaBlob))
}

func (s *metaBlobHeap) Pop() interface{} {
	l := s.Len()
	v := (*s)[l]
	*s = (*s)[:l-1]
	return v
}

func (s *metaBlobHeap) Less(i, j int) bool {
	sl := *s
	v := len(sl[i].entries) < len(sl[j].entries)
	if len(sl[i].entries) == len(sl[j].entries) {
		v = sl[i].br.String() < sl[j].br.String()
	}
	return v
}

func (s *metaBlobHeap) Len() int      { return len(*s) }
func (s *metaBlobHeap) Swap(i, j int) { (*s)[i], (*s)[j] = (*s)[j], (*s)[i] }

// makeSingleMetaBlob makes and encrypts a metaBlob with one entry.
func (s *storage) makeSingleMetaBlob(plainBR, encBR blob.Ref, plainSize uint32) []byte {
	plain := fmt.Sprintf("#camlistore/encmeta=2\n%s/%d/%s\n", plainBR, plainSize, encBR)
	return s.encryptBlob(nil, []byte(plain))
}

func packIndexEntry(plainSize uint32, encBR blob.Ref) string {
	return fmt.Sprintf("%d/%s", plainSize, encBR)
}

func unpackIndexEntry(s string) (plainSize uint32, encBR blob.Ref, err error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		err = fmt.Errorf("malformed index entry %q", s)
		return
	}
	size, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		err = fmt.Errorf("malformed index entry %q: %s", s, err)
		return
	}
	plainSize = uint32(size)
	encBR = blob.ParseOrZero(parts[1])
	if !encBR.Valid() {
		err = fmt.Errorf("malformed index entry %q: %s", s, err)
	}
	return
}

// fetchMeta returns os.ErrNotExist if the plaintext blob is not in the index.
func (s *storage) fetchMeta(b blob.Ref) (plainSize uint32, encBR blob.Ref, err error) {
	v, err := s.index.Get(b.String())
	if err == sorted.ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return 0, blob.Ref{}, err
	}
	return unpackIndexEntry(v)
}

// processEncryptedMetaBlob decrypts dat (the data for the br meta blob) and parses
// its meta lines, updating the index.
//
// processEncryptedMetaBlob is not thread-safe.
func (s *storage) processEncryptedMetaBlob(br blob.Ref, dat []byte) error {
	plain, err := s.decryptBlob(nil, dat)
	if err != nil {
		return err
	}
	p := bytes.NewBuffer(plain)

	header, err := p.ReadString('\n')
	if err != nil {
		return errors.New("No first line")
	}
	if header != "#camlistore/encmeta=2\n" {
		if len(header) > 80 {
			header = header[:80]
		}
		return fmt.Errorf("unsupported first line %q", header)
	}
	for {
		line, err := p.ReadString('\n')
		if err != nil && len(line) != 0 {
			return io.ErrUnexpectedEOF
		} else if err != nil {
			return nil
		}
		parts := strings.Split(strings.TrimRight(line, "\n"), "/")
		if len(parts) != 3 {
			if len(line) > 80 {
				line = line[:80]
			}
			return fmt.Errorf("malformed line %q", line)
		}
		// We do very limited checking here, as we signed the blob and we check
		// the value anyway on s.index.Get.
		if _, ok := blob.ParseKnown(parts[0]); !ok {
			if len(line) > 80 {
				line = line[:80]
			}
			return fmt.Errorf("malformed line %q", line)
		}
		if err := s.index.Set(parts[0], parts[1]+"/"+parts[2]); err != nil {
			return err
		}
	}
}

func (s *storage) readAllMetaBlobs() error {
	type encMB struct {
		br  blob.Ref
		dat []byte // encrypted blob
		err error
	}
	metac := make(chan encMB, 16)

	const maxInFlight = 50
	var gate = make(chan bool, maxInFlight)

	var stopEnumerate = make(chan bool) // closed on error
	enumErrc := make(chan error, 1)
	go func() {
		var wg sync.WaitGroup
		enumErrc <- blobserver.EnumerateAll(context.TODO(), s.meta, func(sb blob.SizedRef) error {
			select {
			case <-stopEnumerate:
				return errors.New("enumeration stopped")
			default:
			}

			wg.Add(1)
			gate <- true
			go func() {
				defer wg.Done()
				defer func() { <-gate }()
				rc, _, err := s.meta.Fetch(sb.Ref)
				var all []byte
				if err == nil {
					all, err = ioutil.ReadAll(rc)
					rc.Close()
				}
				metac <- encMB{sb.Ref, all, err}
			}()
			return nil
		})
		wg.Wait()
		close(metac)
	}()

	for mi := range metac {
		err := mi.err
		if err == nil {
			err = s.processEncryptedMetaBlob(mi.br, mi.dat)
		}
		if err != nil {
			close(stopEnumerate)
			go func() {
				for range metac {
				}
			}()
			// TODO: advertise in this error message a new option or environment variable
			// to skip a certain or all meta blobs, to allow partial recovery, if some
			// are corrupt. For now, require all to be correct.
			return fmt.Errorf("Error with meta blob %v: %v", mi.br, err)
		}
	}

	return <-enumErrc
}
