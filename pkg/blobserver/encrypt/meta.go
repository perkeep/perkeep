/*
Copyright 2016 The Perkeep Authors

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
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"perkeep.org/internal/pools"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/sorted"
)

// Encrypted meta format:
//    #camlistore/encmeta=2
// Then sorted lines, each ending in a newline, like:
//    sha1-plain/<plain size>/sha1-encrypted

const (
	// FullMetaBlobSize is the number of lines at which we stop compacting a meta blob.
	FullMetaBlobSize = 10 * 1000 // ~ 512kB
	// SmallMetaCountLimit is the number of small meta that triggers compaction.
	SmallMetaCountLimit = 100 // 100 rounds to make a full = ~ 26MB bw waste
)

type metaBlob struct {
	br     blob.Ref // of meta blob
	plains []blob.Ref
}

// metaBlobHeap is a heap of metaBlobs.
// heap.Pop returns the metaBlob with the LEAST entries.
type metaBlobHeap struct {
	sync.Mutex
	s []*metaBlob
}

var _ heap.Interface = (*metaBlobHeap)(nil)

func (h *metaBlobHeap) Push(x any) {
	h.s = append(h.s, x.(*metaBlob))
}

func (h *metaBlobHeap) Pop() any {
	l := len(h.s)
	v := h.s[l-1]
	h.s = h.s[:l-1]
	return v
}

func (h *metaBlobHeap) Less(i, j int) bool {
	return len(h.s[i].plains) < len(h.s[j].plains)
}

func (h *metaBlobHeap) Len() int      { return len(h.s) }
func (h *metaBlobHeap) Swap(i, j int) { h.s[i], h.s[j] = h.s[j], h.s[i] }

func (s *storage) recordMeta(b *metaBlob) {
	if len(b.plains) > FullMetaBlobSize {
		return
	}

	s.smallMeta.Lock()
	defer s.smallMeta.Unlock()
	heap.Push(s.smallMeta, b)

	// If the heap is full, pop and group the entries under the lock,
	// then schedule upload, deletion and reinserion in parallel.
	if s.smallMeta.Len() > SmallMetaCountLimit {
		var plains, toDelete []blob.Ref
		for s.smallMeta.Len() > 0 {
			meta := heap.Pop(s.smallMeta).(*metaBlob)
			plains = append(plains, meta.plains...)
			toDelete = append(toDelete, meta.br)
			if len(plains) > FullMetaBlobSize {
				go s.makePackedMetaBlob(plains, toDelete)
				plains, toDelete = nil, nil
			}
		}
		if len(toDelete) == 1 {
			heap.Push(s.smallMeta, &metaBlob{br: toDelete[0], plains: plains})
		} else if len(toDelete) > 1 {
			go s.makePackedMetaBlob(plains, toDelete)
		}
	}
}

func (s *storage) makePackedMetaBlob(plains, toDelete []blob.Ref) {
	ctx := context.Background() // TODO
	// We lose track of the small blobs in case of error, but they will be packed at next start.
	sort.Sort(blob.ByRef(plains))

	metaBytes := pools.BytesBuffer()
	defer pools.PutBuffer(metaBytes)

	metaBytes.WriteString("#camlistore/encmeta=2\n")
	metaBytes.Grow(len(plains[0].String()) * len(plains) * 2)
	for _, plain := range plains {
		p := plain.String()
		metaBytes.WriteString(p)
		metaBytes.WriteString("/")
		v, err := s.index.Get(p)
		if err != nil {
			log.Printf("encrypt: failed to find the index entry for %s while packing: %v", p, err)
			return
		}
		metaBytes.WriteString(v)
		metaBytes.WriteString("\n")
	}

	encBytes := pools.BytesBuffer()
	defer pools.PutBuffer(encBytes)

	if err := s.encryptBlob(encBytes, metaBytes); err != nil {
		log.Printf("encrypt: failed to encrypt meta: %v", err)
		return
	}

	metaBR := blob.RefFromBytes(encBytes.Bytes())
	metaSB, err := blobserver.ReceiveNoHash(ctx, s.meta, metaBR, encBytes)
	if err != nil {
		log.Printf("encrypt: failed to upload a packed meta: %v", err)
		return
	}
	if len(plains) < FullMetaBlobSize {
		s.recordMeta(&metaBlob{br: metaSB.Ref, plains: plains})
	}
	if err := s.meta.RemoveBlobs(ctx, toDelete); err != nil {
		log.Printf("encrypt: failed to delete small meta blobs: %v", err)
	}
	log.Printf("encrypt: packed %d small meta blobs into one (%d refs)", len(toDelete), len(plains))
}

// makeSingleMetaBlob makes and encrypts a metaBlob with one entry.
func (s *storage) makeSingleMetaBlob(plainBR, encBR blob.Ref, plainSize uint32) ([]byte, error) {
	// would be racy to use the pool and return Bytes() here, just use a fresh buffer
	encBytes := bytes.NewBuffer(nil)

	plain := fmt.Sprintf("#camlistore/encmeta=2\n%s/%d/%s\n", plainBR, plainSize, encBR)
	if err := s.encryptBlob(encBytes, bytes.NewBufferString(plain)); err != nil {
		return nil, err
	}

	return encBytes.Bytes(), nil
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
func (s *storage) fetchMeta(ctx context.Context, b blob.Ref) (plainSize uint32, encBR blob.Ref, err error) {
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
	plainBytes := pools.BytesBuffer()
	defer pools.PutBuffer(plainBytes)

	if err := s.decryptBlob(plainBytes, bytes.NewBuffer(dat)); err != nil {
		return err
	}

	header, err := plainBytes.ReadString('\n')
	if err != nil {
		return errors.New("No first line")
	}
	if header != "#camlistore/encmeta=2\n" {
		if len(header) > 80 {
			header = header[:80]
		}
		return fmt.Errorf("unsupported first line %q", header)
	}
	var plains []blob.Ref
	for {
		line, err := plainBytes.ReadString('\n')
		if err != nil && len(line) != 0 {
			return io.ErrUnexpectedEOF
		} else if err != nil {
			break
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
		plainBR, ok := blob.ParseKnown(parts[0])
		if !ok {
			if len(line) > 80 {
				line = line[:80]
			}
			return fmt.Errorf("malformed line %q", line)
		}
		plains = append(plains, plainBR)
		if err := s.index.Set(parts[0], parts[1]+"/"+parts[2]); err != nil {
			return err
		}
	}
	s.recordMeta(&metaBlob{br: br, plains: plains})
	return nil
}

func (s *storage) readAllMetaBlobs() error {
	type encMB struct {
		br  blob.Ref
		dat []byte // encrypted blob
		err error
	}
	metac := make(chan encMB, 16)

	const maxInFlight = 5 // arbitrary
	var gate = make(chan bool, maxInFlight)

	var stopEnumerate = make(chan bool) // closed on error
	enumErrc := make(chan error, 1)
	go func() {
		var wg sync.WaitGroup
		ctx := context.TODO()
		enumErrc <- blobserver.EnumerateAll(ctx, s.meta, func(sb blob.SizedRef) error {
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
				rc, _, err := s.meta.Fetch(ctx, sb.Ref)
				if err != nil {
					metac <- encMB{sb.Ref, nil, fmt.Errorf("fetch failed: %v", err)}
					return
				}
				defer rc.Close()
				all, err := io.ReadAll(rc)
				if err != nil {
					metac <- encMB{sb.Ref, nil, fmt.Errorf("read failed: %v", err)}
					return
				}
				metac <- encMB{sb.Ref, all, nil}
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
			return fmt.Errorf("error with meta blob %v: %v", mi.br, err)
		}
	}

	return <-enumErrc
}
