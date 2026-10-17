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

// Package uploadsession implements upload sessions: buffers on the server
// that collect blobs uploaded without waiting for them to be durable, and
// commit them to storage in groups, with blobserver.ReceiveBlobs.
//
// A client adds blobs to its session, and later flushes the session to
// learn which blobs are durable. Committing many blobs at once lets
// storage share the cost of making them durable, like syncs to disk,
// among them, which uploads of a few blobs each can't.
//
// A Manager commits one group at a time, of the blobs all its sessions
// have buffered, starting as soon as there are any: blobs added while a
// group is committed wait for the next. So groups grow with the rate
// blobs arrive at, and storage isn't left idle while blobs wait.
//
// Storage doesn't know about sessions: a buffered blob isn't in storage,
// so it isn't visible to anyone until it's committed, and so durable.
//
// Sessions are kept only in memory. When the server restarts, it loses
// them and the blobs they buffered, none of which a client was told were
// durable; operations on a lost session fail with ErrUnknownSession.
package uploadsession // import "perkeep.org/pkg/blobserver/uploadsession"

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

// ErrUnknownSession is the error for an operation on a session that's
// closed, expired, or was lost when the server restarted.
var ErrUnknownSession = errors.New("uploadsession: unknown upload session")

// errTooManyUnacked is the error for a blob added to a session that has
// Limits.MaxUnacked blobs buffered or durable and not acknowledged.
var errTooManyUnacked = errors.New("uploadsession: too many blobs not acknowledged; flush the session")

// A Manager commits buffered blobs in groups of up to commitBlobs blobs,
// or of about commitBytes bytes, whichever is reached first.
const (
	commitBlobs = 256
	commitBytes = 32 << 20
)

// Limits are the limits on a Manager's sessions. Zero fields get the
// defaults given.
type Limits struct {
	// MaxSessions is how many sessions can be open at once.
	// The default is 1000.
	MaxSessions int

	// SessionBytes is how many bytes of blobs a session can buffer.
	// The default is 64 MB.
	SessionBytes int64

	// TotalBytes is how many bytes of blobs all sessions together can
	// buffer. The default is 256 MB.
	TotalBytes int64

	// MaxUnacked is how many blobs a session can have buffered, or
	// durable and not acknowledged. The default is 100,000.
	MaxUnacked int

	// DefaultIdle is the idle timeout of a session for which none is
	// asked. MinIdle and MaxIdle bound the timeouts asked for. The
	// defaults are 5 minutes, 10 seconds, and 1 hour.
	DefaultIdle, MinIdle, MaxIdle time.Duration
}

// withDefaults returns l with zero fields set to their defaults, and the
// byte limits raised to hold at least a blob of the maximum size.
func (l Limits) withDefaults() Limits {
	def := func(v *int64, d int64) {
		if *v == 0 {
			*v = d
		}
		*v = max(*v, blobserver.MaxBlobSize)
	}
	def(&l.SessionBytes, 64<<20)
	def(&l.TotalBytes, 256<<20)
	if l.MaxSessions == 0 {
		l.MaxSessions = 1000
	}
	if l.MaxUnacked == 0 {
		l.MaxUnacked = 100_000
	}
	if l.DefaultIdle == 0 {
		l.DefaultIdle = 5 * time.Minute
	}
	if l.MinIdle == 0 {
		l.MinIdle = 10 * time.Second
	}
	if l.MaxIdle == 0 {
		l.MaxIdle = time.Hour
	}
	return l
}

// A Manager has the upload sessions committing blobs to one destination.
type Manager struct {
	dst    blobserver.BlobReceiver
	limits Limits
	total  *semaphore.Weighted // all sessions' buffered bytes

	mu         sync.Mutex
	sessions   map[string]*Session
	queue      []queued // waiting for a commit, in the order added
	committing bool     // whether commit is running
}

// queued is a blob buffered by a session.
type queued struct {
	s *Session
	b *blob.Blob
}

// NewManager returns a Manager of sessions that commit blobs to dst, with
// blobserver.ReceiveBlobsNoHash.
func NewManager(dst blobserver.BlobReceiver, limits Limits) *Manager {
	limits = limits.withDefaults()
	return &Manager{
		dst:      dst,
		limits:   limits,
		total:    semaphore.NewWeighted(limits.TotalBytes),
		sessions: make(map[string]*Session),
	}
}

// Limits returns m's limits, with defaults filled in.
func (m *Manager) Limits() Limits { return m.limits }

// Open opens a new session, which closes once it has been idle, with no
// operations in progress, for the idle timeout: idle, bounded by the
// Limits, or the default if idle is zero.
func (m *Manager) Open(idle time.Duration) (*Session, error) {
	if idle == 0 {
		idle = m.limits.DefaultIdle
	}
	s := &Session{
		m:          m,
		id:         rand.Text(),
		idle:       min(max(idle, m.limits.MinIdle), m.limits.MaxIdle),
		bytes:      semaphore.NewWeighted(m.limits.SessionBytes),
		lastActive: time.Now(),
		pending:    make(map[blob.Ref]bool),
		durable:    make(map[blob.Ref]uint32),
		failed:     make(map[blob.Ref]error),
		progress:   make(chan struct{}),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= m.limits.MaxSessions {
		return nil, fmt.Errorf("uploadsession: too many open sessions (%d)", len(m.sessions))
	}
	m.sessions[s.id] = s
	s.expiry = time.AfterFunc(s.idle, s.expire)
	return s, nil
}

// Session returns the open session with the given ID, or an error
// matching ErrUnknownSession.
func (m *Manager) Session(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrUnknownSession
	}
	return s, nil
}

// remove removes s from m's open sessions.
func (m *Manager) remove(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, s.id)
}

// enqueue queues b, buffered by s, starting commit if it's not running.
func (m *Manager) enqueue(s *Session, b *blob.Blob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, queued{s, b})
	if !m.committing {
		m.committing = true
		go m.commit()
	}
}

// commit commits the queued blobs in groups, until there are none.
func (m *Manager) commit() {
	for {
		m.mu.Lock()
		if len(m.queue) == 0 {
			m.committing = false
			m.mu.Unlock()
			return
		}
		var group []queued
		var groupBytes int64
		for len(m.queue) > 0 && len(group) < commitBlobs && groupBytes < commitBytes {
			q := m.queue[0]
			m.queue[0] = queued{}
			m.queue = m.queue[1:]
			group = append(group, q)
			groupBytes += int64(q.b.Size())
		}
		m.mu.Unlock()

		blobs := make([]*blob.Blob, len(group))
		for i, q := range group {
			blobs[i] = q.b
		}
		// Blobs of the same ref from different sessions are received
		// once, and get the same result.
		err := blobserver.ReceiveBlobsNoHash(context.Background(), m.dst, blobs)

		bySession := make(map[*Session][]*blob.Blob)
		for _, q := range group {
			bySession[q.s] = append(bySession[q.s], q.b)
		}
		for s, blobs := range bySession {
			s.finish(blobs, err)
		}
		m.total.Release(groupBytes)
	}
}

// A Session buffers blobs for its Manager to commit. Its methods are safe
// for concurrent use.
type Session struct {
	m     *Manager
	id    string
	idle  time.Duration
	bytes *semaphore.Weighted // this session's buffered bytes

	mu         sync.Mutex
	closed     bool
	active     int // Add, Flush and Status calls in progress
	lastActive time.Time
	expiry     *time.Timer
	pending    map[blob.Ref]bool // buffered, or being committed
	added      int64             // how many blobs have been buffered
	done       int64             // how many of the buffered blobs commits have finished
	durable    map[blob.Ref]uint32
	failed     map[blob.Ref]error
	progress   chan struct{} // closed and replaced when done grows
}

// ID returns the session's ID: a random string, unguessable by other
// clients.
func (s *Session) ID() string { return s.id }

// IdleTimeout returns how long the session can be idle before it closes.
func (s *Session) IdleTimeout() time.Duration { return s.idle }

// begin starts an operation on s, which mustn't expire while it's in
// progress. If it returns nil, the caller must call end.
func (s *Session) begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnknownSession
	}
	s.active++
	return nil
}

// end ends an operation started by begin.
func (s *Session) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	s.lastActive = time.Now()
	if s.active == 0 && !s.closed {
		s.expiry.Reset(s.idle)
	}
}

// expire closes s if it's been idle for its idle timeout.
func (s *Session) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// An operation can begin and end while this waits for the lock;
	// its end resets the timer, to call expire again later.
	if s.closed || s.active > 0 || time.Since(s.lastActive) < s.idle {
		return
	}
	s.closeLocked()
}

// Close closes the session. The blobs it has buffered are still
// committed, but nobody is told whether they're durable.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closeLocked()
	}
}

func (s *Session) closeLocked() {
	s.closed = true
	s.expiry.Stop()
	s.m.remove(s)
}

// Add checks blobs (as blobserver.CheckBlob does) and buffers them to
// commit. Blobs already buffered or durable, and not acknowledged, are
// skipped. Add blocks while the session, or all the sessions together,
// have as many bytes buffered as their Limits allow, until commits make
// room, or ctx is done.
//
// It returns nil if every blob was buffered, a *blobserver.BlobsError
// listing those that weren't, or an error matching ErrUnknownSession if
// the session isn't open.
func (s *Session) Add(ctx context.Context, blobs []*blob.Blob) error {
	if err := s.begin(); err != nil {
		return err
	}
	defer s.end()
	be := &blobserver.BlobsError{Op: "upload session add"}
	for _, b := range blobs {
		err := blobserver.CheckBlob(ctx, b)
		if err == nil {
			err = s.add(ctx, b)
		}
		be.Add(b.Ref(), err)
	}
	return be.OrNil()
}

// add buffers b, which passed its checks.
func (s *Session) add(ctx context.Context, b *blob.Blob) error {
	br := b.Ref()
	if skip, err := s.skipAdd(br); skip || err != nil {
		return err
	}
	n := int64(b.Size())
	if err := s.bytes.Acquire(ctx, n); err != nil {
		return err
	}
	if err := s.m.total.Acquire(ctx, n); err != nil {
		s.bytes.Release(n)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Check again, as another Add or Close can come between.
	skip, err := s.skipAddLocked(br)
	if s.closed {
		err = ErrUnknownSession
	}
	if skip || err != nil {
		s.m.total.Release(n)
		s.bytes.Release(n)
		return err
	}
	delete(s.failed, br)
	s.pending[br] = true
	s.added++
	s.m.enqueue(s, b)
	return nil
}

func (s *Session) skipAdd(br blob.Ref) (skip bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipAddLocked(br)
}

// skipAddLocked reports whether adding br would add nothing, or returns
// an error if br can't be added.
func (s *Session) skipAddLocked(br blob.Ref) (skip bool, err error) {
	if _, ok := s.durable[br]; ok || s.pending[br] {
		return true, nil
	}
	if len(s.pending)+len(s.durable) >= s.m.limits.MaxUnacked {
		return false, errTooManyUnacked
	}
	return false, nil
}

// finish records the results of committing blobs that s buffered: err,
// from blobserver.ReceiveBlobsNoHash.
func (s *Session) finish(blobs []*blob.Blob, err error) {
	var n int64
	s.mu.Lock()
	for _, b := range blobs {
		br := b.Ref()
		delete(s.pending, br)
		if err := blobserver.BlobErr(err, br); err != nil {
			s.failed[br] = err
		} else {
			s.durable[br] = b.Size()
		}
		n += int64(b.Size())
	}
	s.done += int64(len(blobs))
	close(s.progress)
	s.progress = make(chan struct{})
	s.mu.Unlock()
	s.bytes.Release(n)
}

// Flush waits for the blobs buffered so far to be committed (or for ctx
// to be done). It first forgets the refs in ack, which the caller already
// knows are durable. It returns, sorted, the durable blobs the caller
// hasn't acknowledged. Blobs that failed to commit since the last Flush
// or Status are listed in a returned *blobserver.BlobsError; they're no
// longer in the session, and can be added again.
//
// It returns an error matching ErrUnknownSession if the session isn't
// open.
func (s *Session) Flush(ctx context.Context, ack []blob.Ref) ([]blob.SizedRef, error) {
	if err := s.begin(); err != nil {
		return nil, err
	}
	defer s.end()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ackLocked(ack)
	target := s.added
	for s.done < target {
		progress := s.progress
		s.mu.Unlock()
		select {
		case <-progress:
			s.mu.Lock()
		case <-ctx.Done():
			s.mu.Lock()
			return nil, ctx.Err()
		}
	}
	return s.statusLocked()
}

// Status is like Flush, but doesn't wait for anything.
func (s *Session) Status(ack []blob.Ref) ([]blob.SizedRef, error) {
	if err := s.begin(); err != nil {
		return nil, err
	}
	defer s.end()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ackLocked(ack)
	return s.statusLocked()
}

func (s *Session) ackLocked(ack []blob.Ref) {
	for _, br := range ack {
		delete(s.durable, br)
	}
}

// statusLocked returns the durable blobs, and the failed ones, which it
// forgets.
func (s *Session) statusLocked() ([]blob.SizedRef, error) {
	durable := make([]blob.SizedRef, 0, len(s.durable))
	for br, size := range s.durable {
		durable = append(durable, blob.SizedRef{Ref: br, Size: size})
	}
	sort.Sort(blob.SizedByRef(durable))
	be := &blobserver.BlobsError{Op: "upload session commit"}
	for br, err := range s.failed {
		be.Add(br, err)
	}
	clear(s.failed)
	return durable, be.OrNil()
}
