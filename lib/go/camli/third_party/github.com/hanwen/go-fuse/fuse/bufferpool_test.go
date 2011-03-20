package fuse

import (
	"testing"
	"fmt"
)

var _ = fmt.Println

func TestIntToExponent(t *testing.T) {
	e := IntToExponent(1)
	if e != 0 {
		t.Error("1", e)
	}
	e = IntToExponent(2)
	if e != 1 {
		t.Error("2", e)
	}
	e = IntToExponent(3)
	if e != 2 {
		t.Error("3", e)
	}
	e = IntToExponent(4)
	if e != 2 {
		t.Error("4", e)
	}
}

func TestBufferPool(t *testing.T) {
	bp := NewBufferPool()

	b1 := bp.AllocBuffer(PAGESIZE)
	_ = bp.AllocBuffer(2 * PAGESIZE)
	bp.FreeBuffer(b1)

	b1_2 := bp.AllocBuffer(PAGESIZE)
	if &b1[0] != &b1_2[0] {
		t.Error("bp 0")
	}

}

func TestFreeBufferEmpty(t *testing.T) {
	bp := NewBufferPool()
	c := make([]byte, 0, 2*PAGESIZE)
	bp.FreeBuffer(c)
}
