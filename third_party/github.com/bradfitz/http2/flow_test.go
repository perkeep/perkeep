// Copyright 2014 The Go Authors.
// See https://code.google.com/p/go/source/browse/CONTRIBUTORS
// Licensed under the same terms as Go itself:
// https://code.google.com/p/go/source/browse/LICENSE

package http2

import (
	"testing"
	"time"
)

func TestFlow(t *testing.T) {
	f := newFlow(10)
	if got, want := f.cur(), int32(10); got != want {
		t.Fatalf("size = %d; want %d", got, want)
	}
	if got, want := f.wait(1), int32(1); got != want {
		t.Errorf("wait = %d; want %d", got, want)
	}
	if got, want := f.cur(), int32(9); got != want {
		t.Fatalf("size = %d; want %d", got, want)
	}
	if got, want := f.wait(20), int32(9); got != want {
		t.Errorf("wait = %d; want %d", got, want)
	}
	if got, want := f.cur(), int32(0); got != want {
		t.Fatalf("size = %d; want %d", got, want)
	}

	// Wait for 10, which should block, so start a background goroutine
	// to refill it.
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.add(50)
	}()
	if got, want := f.wait(1), int32(1); got != want {
		t.Errorf("after block, got %d; want %d", got, want)
	}

	if got, want := f.cur(), int32(49); got != want {
		t.Fatalf("size = %d; want %d", got, want)
	}
}

func TestFlowAdd(t *testing.T) {
	f := newFlow(0)
	if !f.add(1) {
		t.Fatal("failed to add 1")
	}
	if !f.add(-1) {
		t.Fatal("failed to add -1")
	}
	if got, want := f.cur(), int32(0); got != want {
		t.Fatalf("size = %d; want %d", got, want)
	}
	if !f.add(1<<31 - 1) {
		t.Fatal("failed to add 2^31-1")
	}
	if got, want := f.cur(), int32(1<<31-1); got != want {
		t.Fatalf("size = %d; want %d", got, want)
	}
	if f.add(1) {
		t.Fatal("adding 1 to max shouldn't be allowed")
	}

}

func TestFlowClose(t *testing.T) {
	f := newFlow(0)

	// Wait for 10, which should block, so start a background goroutine
	// to refill it.
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.close()
	}()
	gotc := make(chan int32)
	go func() {
		gotc <- f.wait(1)
	}()
	select {
	case got := <-gotc:
		if got != 0 {
			t.Errorf("got %d; want 0", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout")
	}
}
