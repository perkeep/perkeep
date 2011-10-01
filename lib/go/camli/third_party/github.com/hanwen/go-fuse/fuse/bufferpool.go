package fuse

import (
	"sync"
	"fmt"
	"unsafe"
	"log"
)

var _ = log.Println

type BufferPool interface {
	AllocBuffer(size uint32) []byte
	FreeBuffer(slice []byte)
}

type GcBufferPool struct {

}

// NewGcBufferPool is just a fallback to the standard allocation routines. 
func NewGcBufferPool() *GcBufferPool {
	return &GcBufferPool{}
}

func (me *GcBufferPool) AllocBuffer(size uint32) []byte {
	return make([]byte, size)
}

func (me *GcBufferPool) FreeBuffer(slice []byte) {
}

// BufferPool implements a pool of buffers that returns slices with
// capacity (2^e * PAGESIZE) for e=0,1,...  which have possibly been
// used, and may contain random contents.
type BufferPoolImpl struct {
	lock sync.Mutex

	// For each exponent a list of slice pointers.
	buffersByExponent [][][]byte

	// start of slice -> exponent.
	outstandingBuffers map[uintptr]uint

	// Total count of created buffers.  Handy for finding memory
	// leaks.
	createdBuffers int
}

// IntToExponent the smallest E such that 2^E >= Z.
func IntToExponent(z int) uint {
	x := z
	var exp uint = 0
	for x > 1 {
		exp++
		x >>= 1
	}

	if z > (1 << exp) {
		exp++
	}
	return exp
}

func NewBufferPool() *BufferPoolImpl {
	bp := new(BufferPoolImpl)
	bp.buffersByExponent = make([][][]byte, 0, 8)
	bp.outstandingBuffers = make(map[uintptr]uint)
	return bp
}

func (me *BufferPoolImpl) String() string {
	me.lock.Lock()
	defer me.lock.Unlock()
	s := fmt.Sprintf("created: %v\noutstanding %v\n",
		me.createdBuffers, len(me.outstandingBuffers))
	for exp, bufs := range me.buffersByExponent {
		s = s + fmt.Sprintf("%d = %d\n", exp, len(bufs))
	}
	return s
}

func (me *BufferPoolImpl) getBuffer(exponent uint) []byte {
	if len(me.buffersByExponent) <= int(exponent) {
		return nil
	}
	bufferList := me.buffersByExponent[exponent]
	if len(bufferList) == 0 {
		return nil
	}

	result := bufferList[len(bufferList)-1]
	me.buffersByExponent[exponent] = me.buffersByExponent[exponent][:len(bufferList)-1]
	return result
}

func (me *BufferPoolImpl) addBuffer(slice []byte, exp uint) {
	for len(me.buffersByExponent) <= int(exp) {
		me.buffersByExponent = append(me.buffersByExponent, make([][]byte, 0))
	}
	me.buffersByExponent[exp] = append(me.buffersByExponent[exp], slice)
}

// AllocBuffer creates a buffer of at least the given size. After use,
// it should be deallocated with FreeBuffer().
func (me *BufferPoolImpl) AllocBuffer(size uint32) []byte {
	sz := int(size)
	if sz < PAGESIZE {
		sz = PAGESIZE
	}

	exp := IntToExponent(sz)
	rounded := 1 << exp

	exp -= IntToExponent(PAGESIZE)

	me.lock.Lock()
	defer me.lock.Unlock()

	b := me.getBuffer(exp)

	if b == nil {
		me.createdBuffers++
		b = make([]byte, size, rounded)
	} else {
		b = b[:size]
	}

	me.outstandingBuffers[uintptr(unsafe.Pointer(&b[0]))] = exp

	// FUSE throttles to ~10 outstanding requests, no normally,
	// should not have more than 20 buffers outstanding.
	if paranoia && (me.createdBuffers > 50 || len(me.outstandingBuffers) > 50) {
		panic("Leaking buffers")
	}

	return b
}

// FreeBuffer takes back a buffer if it was allocated through
// AllocBuffer.  It is not an error to call FreeBuffer() on a slice
// obtained elsewhere.
func (me *BufferPoolImpl) FreeBuffer(slice []byte) {
	if slice == nil {
		return
	}
	sz := cap(slice)
	if sz < PAGESIZE {
		return
	}
	exp := IntToExponent(sz)
	rounded := 1 << exp
	if rounded != sz {
		return
	}
	slice = slice[:sz]
	key := uintptr(unsafe.Pointer(&slice[0]))

	me.lock.Lock()
	defer me.lock.Unlock()
	exp, ok := me.outstandingBuffers[key]
	if ok {
		me.addBuffer(slice, exp)
		me.outstandingBuffers[key] = 0, false
	}
}
