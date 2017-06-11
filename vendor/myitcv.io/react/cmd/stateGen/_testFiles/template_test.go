package banana_test

import (
	"bytes"
	"testing"

	"myitcv.io/react/cmd/stateGen/_testFiles"
)

func TestBasic(t *testing.T) {
	r := banana.NewRoot()

	if v := r.Model().Get(); v != nil {
		t.Fatalf("expected nil as initial value; got %v", v)
	}

	b := bytes.NewBuffer(nil)
	r.Model().Set(b)

	if v := r.Model().Get(); v != b {
		t.Fatalf("expected value %v; got %v", b, v)
	}

	cb1Count := 0
	cb2Count := 0

	cb1 := func() {
		cb1Count++
	}

	cb2 := func() {
		cb2Count++
	}

	sub1 := r.TaggingScreen().Subscribe(cb1)
	sub2 := r.TaggingScreen().Name().Subscribe(cb2)

	if v := r.TaggingScreen().Name().Get(); v != "" {
		t.Fatalf("expected empty string initial value; got %q", v)
	}

	setCount1 := 1
	setCount2 := 1

	r.TaggingScreen().Name().Set("hello")

	if cb1Count != setCount1 {
		t.Fatalf("expected cb1Count to be %v; got %v", setCount1, cb1Count)
	}
	if cb2Count != setCount2 {
		t.Fatalf("expected cb2Count to be %v; got %v", setCount2, cb2Count)
	}

	// leave the value the same... shouldn't get callback
	r.TaggingScreen().Name().Set("hello")

	if cb1Count != setCount1 {
		t.Fatalf("expected cb1Count to be %v; got %v", setCount1, cb1Count)
	}
	if cb2Count != setCount2 {
		t.Fatalf("expected cb2Count to be %v; got %v", setCount2, cb2Count)
	}

	sub1.Clear()

	setCount2++
	r.TaggingScreen().Name().Set("again")

	if cb1Count != setCount1 {
		t.Fatalf("expected cb1Count to be %v; got %v", setCount1, cb1Count)
	}
	if cb2Count != setCount2 {
		t.Fatalf("expected cb2Count to be %v; got %v", setCount2, cb2Count)
	}

	sub2.Clear()

	r.TaggingScreen().Name().Set("hello")

	if cb1Count != setCount1 {
		t.Fatalf("expected cb1Count to be %v; got %v", setCount1, cb1Count)
	}
	if cb2Count != setCount2 {
		t.Fatalf("expected cb2Count to be %v; got %v", setCount2, cb2Count)
	}
}
