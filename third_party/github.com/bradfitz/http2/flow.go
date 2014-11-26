// Copyright 2014 The Go Authors.
// See https://code.google.com/p/go/source/browse/CONTRIBUTORS
// Licensed under the same terms as Go itself:
// https://code.google.com/p/go/source/browse/LICENSE

// Flow control

package http2

import "sync"

// flow is the flow control window's counting semaphore.
type flow struct {
	c      *sync.Cond // protects size
	size   int32
	closed bool
}

func newFlow(n int32) *flow {
	return &flow{
		c:    sync.NewCond(new(sync.Mutex)),
		size: n,
	}
}

// cur returns the current number of bytes allow to write.  Obviously
// it's not safe to call this and assume acquiring that number of
// bytes from the acquire method won't be block in the presence of
// concurrent acquisitions.
func (f *flow) cur() int32 {
	f.c.L.Lock()
	defer f.c.L.Unlock()
	return f.size
}

// wait waits for between 1 and n bytes (inclusive) to be available
// and returns the number of quota bytes decremented from the quota
// and allowed to be written. The returned value will be 0 iff the
// stream has been killed.
func (f *flow) wait(n int32) (got int32) {
	if n < 0 {
		panic("negative acquire")
	}
	f.c.L.Lock()
	defer f.c.L.Unlock()
	for {
		if f.closed {
			return 0
		}
		if f.size >= 1 {
			got = f.size
			if got > n {
				got = n
			}
			f.size -= got
			return got
		}
		f.c.Wait()
	}
}

// add adds n bytes (positive or negative) to the flow control window.
// It returns false if the sum would exceed 2^31-1.
func (f *flow) add(n int32) bool {
	f.c.L.Lock()
	defer f.c.L.Unlock()
	remain := (1<<31 - 1) - f.size
	if n > remain {
		return false
	}
	f.size += n
	f.c.Broadcast()
	return true
}

// close marks the flow as closed, meaning everybody gets all the
// tokens they want, because everything else will fail anyway.
func (f *flow) close() {
	f.c.L.Lock()
	defer f.c.L.Unlock()
	f.closed = true
	f.c.Broadcast()
}
