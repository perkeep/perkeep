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

package uploadsession

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/test"
)

// dest is a destination recording the sizes of its ReceiveBlobs calls,
// failing the blobs in fail, and blocking calls while gate is non-nil.
type dest struct {
	*test.Fetcher
	mu    sync.Mutex
	calls []int
	fail  map[blob.Ref]bool
	gate  chan struct{}
}

func newDest() *dest { return &dest{Fetcher: new(test.Fetcher)} }

var errDestFail = errors.New("dest failed")

func (d *dest) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	d.mu.Lock()
	d.calls = append(d.calls, len(blobs))
	gate := d.gate
	d.mu.Unlock()
	if gate != nil {
		<-gate
	}
	be := &blobserver.BlobsError{Op: "receive"}
	for _, b := range blobs {
		if d.fail[b.Ref()] {
			be.Add(b.Ref(), errDestFail)
			continue
		}
		r, err := b.ReadAll(ctx)
		if err == nil {
			_, err = d.Fetcher.ReceiveBlob(ctx, b.Ref(), r)
		}
		be.Add(b.Ref(), err)
	}
	return be.OrNil()
}

// hold makes ReceiveBlobs calls block until release.
func (d *dest) hold() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gate = make(chan struct{})
}

// release unblocks the ReceiveBlobs calls blocked by hold.
func (d *dest) release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	close(d.gate)
	d.gate = nil
}

func (d *dest) callSizes() []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

func (d *dest) has(br blob.Ref) bool {
	_, _, err := d.Fetch(context.Background(), br)
	return err == nil
}

// testBlobs returns n distinct blobs of size bytes each.
func testBlobs(n, size int) []*blob.Blob {
	var blobs []*blob.Blob
	for i := range n {
		s := fmt.Sprintf("%d:", i)
		s += strings.Repeat("x", max(size-len(s), 0))
		blobs = append(blobs, (&test.Blob{Contents: s}).Blob())
	}
	return blobs
}

func sizedRefs(blobs []*blob.Blob) []blob.SizedRef {
	var sbs []blob.SizedRef
	for _, b := range blobs {
		sbs = append(sbs, b.SizedRef())
	}
	sort.Sort(blob.SizedByRef(sbs))
	return sbs
}

func refs(sbs []blob.SizedRef) []blob.Ref {
	var brs []blob.Ref
	for _, sb := range sbs {
		brs = append(brs, sb.Ref)
	}
	return brs
}

func open(t *testing.T, m *Manager) *Session {
	t.Helper()
	s, err := m.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func add(t *testing.T, s *Session, blobs []*blob.Blob) {
	t.Helper()
	if err := s.Add(context.Background(), blobs); err != nil {
		t.Fatal(err)
	}
}

// TestFlush checks that blobs are invisible until committed, that blobs
// buffered during a commit are committed together next, and that Flush
// waits for them and reports them durable until acknowledged.
func TestFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		d.hold()
		s := open(t, NewManager(d, Limits{}))
		defer s.Close()
		blobs := testBlobs(10, 100)
		add(t, s, blobs[:1])
		synctest.Wait() // its commit starts at once
		add(t, s, blobs[1:])
		add(t, s, blobs[:3]) // already buffered
		synctest.Wait()
		if got := d.callSizes(); !slices.Equal(got, []int{1}) {
			t.Errorf("ReceiveBlobs calls of sizes %v during the first commit; want [1]", got)
		}
		for _, b := range blobs {
			if d.has(b.Ref()) {
				t.Fatalf("%v visible before a commit", b.Ref())
			}
		}
		flushed := make(chan []blob.SizedRef)
		go func() {
			durable, err := s.Flush(context.Background(), nil)
			if err != nil {
				t.Error(err)
			}
			flushed <- durable
		}()
		synctest.Wait()
		select {
		case <-flushed:
			t.Fatal("Flush returned during a commit")
		default:
		}
		d.release()
		durable := <-flushed
		if want := sizedRefs(blobs); !slices.Equal(durable, want) {
			t.Errorf("Flush = %v; want %v", durable, want)
		}
		if got := d.callSizes(); !slices.Equal(got, []int{1, 9}) {
			t.Errorf("ReceiveBlobs calls of sizes %v; want [1 9]", got)
		}
		for _, b := range blobs {
			if !d.has(b.Ref()) {
				t.Errorf("%v not in destination after Flush", b.Ref())
			}
		}

		// Unacknowledged blobs are still reported; durable ones aren't
		// buffered again.
		add(t, s, blobs[:1])
		durable, err := s.Status(refs(durable[:4]))
		if err != nil {
			t.Fatal(err)
		}
		if want := sizedRefs(blobs)[4:]; !slices.Equal(durable, want) {
			t.Errorf("Status after ack = %v; want %v", durable, want)
		}
		if durable, err = s.Flush(context.Background(), refs(durable)); err != nil || len(durable) != 0 {
			t.Errorf("Flush acknowledging all = %v, %v; want none", durable, err)
		}
		if got := d.callSizes(); len(got) != 2 {
			t.Errorf("ReceiveBlobs calls of sizes %v; want only the first two", got)
		}
	})
}

// TestCommitWithoutFlush checks that blobs are committed without a Flush.
func TestCommitWithoutFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		s := open(t, NewManager(d, Limits{}))
		defer s.Close()
		blobs := testBlobs(4, 10)
		add(t, s, blobs)
		synctest.Wait()
		durable, err := s.Status(nil)
		if want := sizedRefs(blobs); err != nil || !slices.Equal(durable, want) {
			t.Errorf("Status = %v, %v; want %v", durable, err, want)
		}
	})
}

// TestCommitAcrossSessions checks that blobs of different sessions are
// committed together, a ref buffered by both once, and that each session
// learns of its own blobs.
func TestCommitAcrossSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		d.hold()
		m := NewManager(d, Limits{})
		s1, s2 := open(t, m), open(t, m)
		defer s1.Close()
		defer s2.Close()
		blobs := testBlobs(4, 10)
		add(t, s1, blobs[:1])
		synctest.Wait()
		add(t, s1, blobs[1:3])
		add(t, s2, blobs[2:])
		d.release()
		synctest.Wait()
		if got := d.callSizes(); !slices.Equal(got, []int{1, 3}) {
			t.Errorf("ReceiveBlobs calls of sizes %v; want [1 3]", got)
		}
		for _, tt := range []struct {
			s    *Session
			want []*blob.Blob
		}{{s1, blobs[:3]}, {s2, blobs[2:]}} {
			durable, err := tt.s.Status(nil)
			if want := sizedRefs(tt.want); err != nil || !slices.Equal(durable, want) {
				t.Errorf("Status = %v, %v; want %v", durable, err, want)
			}
		}
	})
}

// TestCommitGroups checks that commits are of at most commitBlobs blobs.
func TestCommitGroups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		d.hold()
		s := open(t, NewManager(d, Limits{}))
		defer s.Close()
		blobs := testBlobs(commitBlobs+11, 10)
		add(t, s, blobs[:1])
		synctest.Wait()
		add(t, s, blobs[1:])
		d.release()
		synctest.Wait()
		if got, want := d.callSizes(), []int{1, commitBlobs, 10}; !slices.Equal(got, want) {
			t.Errorf("ReceiveBlobs calls of sizes %v; want %v", got, want)
		}
	})
}

// TestFailures checks that blobs failing their checks or their commit are
// reported, once, and can be added again.
func TestFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		s := open(t, NewManager(d, Limits{}))
		defer s.Close()
		blobs := testBlobs(3, 10)
		bad := blobs[1].Ref()
		d.fail = map[blob.Ref]bool{bad: true}
		corrupt := blob.NewBlob(blob.RefFromString("a"), 1, func(context.Context) ([]byte, error) {
			return []byte("b"), nil
		})
		err := s.Add(context.Background(), append(slices.Clone(blobs), corrupt))
		if !errors.Is(blobserver.BlobErr(err, corrupt.Ref()), blobserver.ErrCorruptBlob) {
			t.Errorf("Add with a corrupt blob = %v; want it to fail with ErrCorruptBlob", err)
		}
		for _, b := range blobs {
			if err := blobserver.BlobErr(err, b.Ref()); err != nil {
				t.Errorf("Add of %v: %v", b.Ref(), err)
			}
		}

		durable, err := s.Flush(context.Background(), nil)
		if want := sizedRefs([]*blob.Blob{blobs[0], blobs[2]}); !slices.Equal(durable, want) {
			t.Errorf("Flush = %v; want %v", durable, want)
		}
		if be, ok := errors.AsType[*blobserver.BlobsError](err); !ok || be.Len() != 1 || !errors.Is(be.Err(bad), errDestFail) {
			t.Errorf("Flush error = %v; want %v failing with %v", err, bad, errDestFail)
		}
		if _, err := s.Status(nil); err != nil {
			t.Errorf("second report of failures: %v", err)
		}

		delete(d.fail, bad)
		add(t, s, blobs[1:2])
		durable, err = s.Flush(context.Background(), refs(durable))
		if want := sizedRefs(blobs[1:2]); err != nil || !slices.Equal(durable, want) {
			t.Errorf("Flush after adding again = %v, %v; want %v", durable, err, want)
		}
	})
}

// TestBackpressure checks that Add blocks while the session's buffer is
// full, until a commit makes room or the context is done.
func TestBackpressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		d.gate = make(chan struct{})
		const size = 1 << 20
		m := NewManager(d, Limits{SessionBytes: 2 * blobserver.MaxBlobSize})
		s := open(t, m)
		defer s.Close()
		blobs := testBlobs(2*blobserver.MaxBlobSize/size+1, size)
		last := blobs[len(blobs)-1]
		blobs = blobs[:len(blobs)-1]
		add(t, s, blobs) // starts a commit, at half the buffer

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Add(ctx, []*blob.Blob{last}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Add to a full session = %v; want %v", err, context.DeadlineExceeded)
		}

		added := make(chan error)
		go func() { added <- s.Add(context.Background(), []*blob.Blob{last}) }()
		synctest.Wait()
		select {
		case err := <-added:
			t.Fatalf("Add to a full session returned %v", err)
		default:
		}
		d.mu.Lock()
		close(d.gate)
		d.gate = nil
		d.mu.Unlock()
		if err := <-added; err != nil {
			t.Fatal(err)
		}
		if durable, err := s.Flush(context.Background(), nil); err != nil || len(durable) != len(blobs)+1 {
			t.Errorf("Flush = %d blobs, %v; want %d", len(durable), err, len(blobs)+1)
		}
		if !m.total.TryAcquire(m.limits.TotalBytes) {
			t.Error("buffer space not all released")
		}
	})
}

// TestFlushWaitsForCommit checks that Flush waits for a commit in progress,
// without the session expiring meanwhile, and returns early if its
// context is done.
func TestFlushWaitsForCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		d.gate = make(chan struct{})
		m := NewManager(d, Limits{})
		s, err := m.Open(time.Second) // raised to the default MinIdle
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		blobs := testBlobs(2, 10)
		add(t, s, blobs)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := s.Flush(ctx, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Flush with a stuck commit = %v; want %v", err, context.DeadlineExceeded)
		}
		if _, err := m.Session(s.ID()); err != nil {
			t.Fatalf("session expired during a Flush: %v", err)
		}
		d.mu.Lock()
		close(d.gate)
		d.gate = nil
		d.mu.Unlock()
		durable, err := s.Flush(context.Background(), nil)
		if want := sizedRefs(blobs); err != nil || !slices.Equal(durable, want) {
			t.Errorf("Flush = %v, %v; want %v", durable, err, want)
		}
	})
}

// TestCloseAndExpiry checks that closed and expired sessions are unknown,
// and still commit their buffered blobs.
func TestCloseAndExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDest()
		d.hold()
		m := NewManager(d, Limits{})
		blobs := testBlobs(4, 10)

		s := open(t, m)
		add(t, s, blobs[:1])
		synctest.Wait()
		add(t, s, blobs[1:2])
		s.Close()
		d.release()
		synctest.Wait()
		for _, b := range blobs[:2] {
			if !d.has(b.Ref()) {
				t.Errorf("%v not committed by Close", b.Ref())
			}
		}
		if _, err := m.Session(s.ID()); !errors.Is(err, ErrUnknownSession) {
			t.Errorf("Session of a closed session = %v; want %v", err, ErrUnknownSession)
		}
		if err := s.Add(context.Background(), blobs[2:]); !errors.Is(err, ErrUnknownSession) {
			t.Errorf("Add to a closed session = %v; want %v", err, ErrUnknownSession)
		}

		s, err := m.Open(time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if s.IdleTimeout() != time.Minute {
			t.Errorf("IdleTimeout = %v; want %v", s.IdleTimeout(), time.Minute)
		}
		add(t, s, blobs[2:])
		time.Sleep(time.Minute / 2)
		if _, err := s.Status(nil); err != nil { // keeps it open
			t.Fatal(err)
		}
		time.Sleep(time.Minute - time.Nanosecond)
		if got, err := m.Session(s.ID()); got != s || err != nil {
			t.Fatalf("Session before the idle timeout = %v, %v", got, err)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if _, err := m.Session(s.ID()); !errors.Is(err, ErrUnknownSession) {
			t.Errorf("Session of an expired session = %v; want %v", err, ErrUnknownSession)
		}
	})
}

// TestLimits checks the limits on sessions and unacknowledged blobs.
func TestLimits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewManager(newDest(), Limits{MaxSessions: 1, MaxUnacked: 2, MinIdle: time.Minute})
		s := open(t, m)
		defer s.Close()
		if s.IdleTimeout() != 5*time.Minute {
			t.Errorf("default IdleTimeout = %v", s.IdleTimeout())
		}
		if _, err := m.Open(0); err == nil {
			t.Error("opened more than MaxSessions sessions")
		}
		blobs := testBlobs(3, 10)
		err := s.Add(context.Background(), blobs)
		if !errors.Is(blobserver.BlobErr(err, blobs[2].Ref()), errTooManyUnacked) {
			t.Errorf("Add over MaxUnacked = %v; want %v", err, errTooManyUnacked)
		}
		durable, err := s.Flush(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		// Durable blobs count until they're acknowledged.
		err = s.Add(context.Background(), blobs[2:])
		if !errors.Is(blobserver.BlobErr(err, blobs[2].Ref()), errTooManyUnacked) {
			t.Errorf("Add over MaxUnacked after Flush = %v; want %v", err, errTooManyUnacked)
		}
		if _, err := s.Status(refs(durable)); err != nil {
			t.Fatal(err)
		}
		add(t, s, blobs[2:])

		s.Close()
		if s, err = m.Open(time.Second); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.IdleTimeout() != time.Minute {
			t.Errorf("IdleTimeout below MinIdle = %v; want %v", s.IdleTimeout(), time.Minute)
		}
	})
}
