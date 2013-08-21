/*
Copyright 2012 Google Inc.

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
	"io"
	"io/ioutil"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"camlistore.org/pkg/blob"
)

func TestWriteFileMap(t *testing.T) {
	m := NewFileMap("test-file")
	r := &randReader{seed: 123, length: 5 << 20}
	sr := new(statsStatReceiver)
	br, err := WriteFileMap(sr, m, r)
	if err != nil {
		t.Fatal(err)
	}
	sizes := []int{}
	for _, size := range sr.have {
		sizes = append(sizes, int(size))
	}
	sort.Ints(sizes)
	t.Logf("Got root file %v; %d blobs, %d bytes", br, sr.numBlobs(), sr.sumBlobSize())
	t.Logf("Sizes are %v", sizes)

	// TODO(bradfitz): these are fragile tests and mostly just a placeholder.
	// Real tests to add:
	//   -- no "bytes" schema with a single "blobref"
	//   -- more seeds (including some that tickle the above)
	//   -- file reader reading back the root gets the same sha1 content back
	//      (will require keeping the full data in our stats receiver, not
	//       just the size)
	//   -- well-balanced tree
	//   -- nothing too big, nothing too small.
	if g, w := br.String(), "sha1-95a5d2686b239e36dff3aeb5a45ed18153121835"; g != w {
		t.Errorf("root blobref = %v; want %v", g, w)
	}
	if g, w := sr.numBlobs(), 88; g != w {
		t.Errorf("num blobs = %v; want %v", g, w)
	}
	if g, w := sr.sumBlobSize(), int64(5252655); g != w {
		t.Errorf("sum blob size = %v; want %v", g, w)
	}
	if g, w := sizes[len(sizes)-1], 262144; g != w {
		t.Errorf("biggest blob is %d; want %d", g, w)
	}
}

type randReader struct {
	seed   int64
	length int
	rnd    *rand.Rand // lazy init
	remain int        // lazy init
}

func (r *randReader) Read(p []byte) (n int, err error) {
	if r.rnd == nil {
		r.rnd = rand.New(rand.NewSource(r.seed))
		r.remain = r.length
	}
	if r.remain == 0 {
		return 0, io.EOF
	}
	if len(p) > r.remain {
		p = p[:r.remain]
	}
	for i := range p {
		p[i] = byte(r.rnd.Intn(256))
	}
	r.remain -= len(p)
	return len(p), nil
}

// statsStatReceiver is a dummy blobserver.StatReceiver that doesn't
// store anything; it just collects statistics.
//
// TODO: we have another copy of this same type in
// camput/files.go. move them to a common place?  well, the camput one
// is probably going away at some point.
type statsStatReceiver struct {
	mu   sync.Mutex
	have map[blob.Ref]int64
}

func (sr *statsStatReceiver) numBlobs() int {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return len(sr.have)
}

func (sr *statsStatReceiver) sumBlobSize() int64 {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	var sum int64
	for _, v := range sr.have {
		sum += v
	}
	return sum
}

func (sr *statsStatReceiver) ReceiveBlob(br blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
	n, err := io.Copy(ioutil.Discard, source)
	if err != nil {
		return
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.have == nil {
		sr.have = make(map[blob.Ref]int64)
	}
	sr.have[br] = n
	return blob.SizedRef{br, n}, nil
}

func (sr *statsStatReceiver) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	for _, br := range blobs {
		if size, ok := sr.have[br]; ok {
			dest <- blob.SizedRef{br, size}
		}
	}
	return nil
}
