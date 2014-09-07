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
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver/stats"
	"camlistore.org/pkg/test"
)

func TestWriteFileMap(t *testing.T) {
	m := NewFileMap("test-file")
	r := &randReader{seed: 123, length: 5 << 20}
	sr := new(stats.Receiver)
	var buf bytes.Buffer
	br, err := WriteFileMap(sr, m, io.TeeReader(r, &buf))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Got root file %v; %d blobs, %d bytes", br, sr.NumBlobs(), sr.SumBlobSize())
	sizes := sr.Sizes()
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
	if g, w := sr.NumBlobs(), 88; g != w {
		t.Errorf("num blobs = %v; want %v", g, w)
	}
	if g, w := sr.SumBlobSize(), int64(5252655); g != w {
		t.Errorf("sum blob size = %v; want %v", g, w)
	}
	if g, w := sizes[len(sizes)-1], 262144; g != w {
		t.Errorf("biggest blob is %d; want %d", g, w)
	}
}

func TestWriteThenRead(t *testing.T) {
	m := NewFileMap("test-file")
	const size = 5 << 20
	r := &randReader{seed: 123, length: size}
	sto := new(test.Fetcher)
	var buf bytes.Buffer
	br, err := WriteFileMap(sto, m, io.TeeReader(r, &buf))
	if err != nil {
		t.Fatal(err)
	}

	var got bytes.Buffer
	fr, err := NewFileReader(sto, br)
	if err != nil {
		t.Fatal(err)
	}

	n, err := io.Copy(&got, fr)
	if err != nil {
		t.Fatal(err)
	}
	if n != size {
		t.Errorf("read back %d bytes; want %d", n, size)
	}
	if !bytes.Equal(buf.Bytes(), got.Bytes()) {
		t.Error("bytes differ")
	}

	var offs []int

	getOffsets := func() error {
		offs = offs[:0]
		var off int
		return fr.ForeachChunk(func(_ []blob.Ref, p BytesPart) error {
			offs = append(offs, off)
			off += int(p.Size)
			return err
		})
	}

	if err := getOffsets(); err != nil {
		t.Fatal(err)
	}
	sort.Ints(offs)
	wantOffs := "[0 262144 358150 433428 525437 602690 675039 748088 816210 898743 980993 1053410 1120438 1188662 1265192 1332541 1398316 1463899 1530446 1596700 1668839 1738909 1817065 1891025 1961646 2031127 2099232 2170640 2238692 2304743 2374317 2440449 2514327 2582670 2653257 2753975 2827518 2905783 2975426 3053820 3134057 3204879 3271019 3346750 3421351 3487420 3557939 3624006 3701093 3768863 3842013 3918267 4001933 4069157 4139132 4208109 4281390 4348801 4422695 4490535 4568111 4642769 4709005 4785526 4866313 4933575 5005564 5071633 5152695 5227716]"
	gotOffs := fmt.Sprintf("%v", offs)
	if wantOffs != gotOffs {
		t.Errorf("Got chunk offsets %v; want %v", gotOffs, wantOffs)
	}

	// Now force a fetch failure on one of the filereader schema chunks, to
	// force a failure of GetChunkOffsets
	errFetch := errors.New("fake fetch error")
	var fetches struct {
		sync.Mutex
		n int
	}
	sto.FetchErr = func() error {
		fetches.Lock()
		defer fetches.Unlock()
		fetches.n++
		if fetches.n == 1 {
			return nil
		}
		return errFetch
	}

	fr, err = NewFileReader(sto, br)
	if err != nil {
		t.Fatal(err)
	}
	if err := getOffsets(); fmt.Sprint(err) != "schema/filereader: fetching file schema blob: fake fetch error" {
		t.Errorf("expected second call of GetChunkOffsets to return wrapped errFetch; got %v", err)
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
