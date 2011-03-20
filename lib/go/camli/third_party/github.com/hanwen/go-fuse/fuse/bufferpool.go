package fuse

import (
	"sync"
	"fmt"
	"unsafe"
)

// This implements a pool of buffers that returns slices with capacity
// (2^e * PAGESIZE) for e=0,1,...  which have possibly been used, and
// may contain random contents.
type BufferPool struct {
	lock sync.Mutex

	// For each exponent a list of slice pointers.
	buffersByExponent [][][]byte

	// start of slice -> exponent.
	outstandingBuffers map[uintptr]uint
}

// Returns the smallest E such that 2^E >= Z.
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

func NewBufferPool() *BufferPool {
	bp := new(BufferPool)
	bp.buffersByExponent = make([][][]byte, 0, 8)
	bp.outstandingBuffers = make(map[uintptr]uint)
	return bp
}

func (me *BufferPool) String() string {
	s := ""
	for exp, bufs := range me.buffersByExponent {
		s = s + fmt.Sprintf("%d = %d\n", exp, len(bufs))
	}
	return s
}

func (me *BufferPool) getBuffer(exponent uint) []byte {
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

func (me *BufferPool) addBuffer(slice []byte, exp uint) {
	for len(me.buffersByExponent) <= int(exp) {
		me.buffersByExponent = append(me.buffersByExponent, make([][]byte, 0))
	}
	me.buffersByExponent[exp] = append(me.buffersByExponent[exp], slice)
}


func (me *BufferPool) AllocBuffer(size uint32) []byte {
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

	if b != nil {
		b = b[:size]
		return b
	}

	b = make([]byte, size, rounded)
	me.outstandingBuffers[uintptr(unsafe.Pointer(&b[0]))] = exp
	return b
}

// Takes back a buffer if it was allocated through AllocBuffer.  It is
// not an error to call FreeBuffer() on a slice obtained elsewhere.
func (me *BufferPool) FreeBuffer(slice []byte) {
	if cap(slice) < PAGESIZE {
		return
	}
	slice = slice[:cap(slice)] 
	key := uintptr(unsafe.Pointer(&slice[0]))
	
	me.lock.Lock()
	defer me.lock.Unlock()
	exp, ok := me.outstandingBuffers[key]
	if ok {
		me.addBuffer(slice, exp)
		me.outstandingBuffers[key] = 0, false
	}
}
