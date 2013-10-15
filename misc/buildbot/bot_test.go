package main

import (
	"reflect"
	"testing"
)

func TestRingBuffer(t *testing.T) {
	rb := newRingBuffer(4)
	data := []struct {
		in, want string
	}{
		{in: "a", want: "a"},
		{in: "b", want: "ab"},
		{in: "c", want: "abc"},
		{in: "d", want: "abcd"},
		{in: "e", want: "bcde"},
		{in: "f", want: "cdef"},
		// Multibyte writes that wrap the ring buffer.
		{in: "ghi", want: "fghi"},
		{in: "jkl", want: "ijkl"},
		{in: "mno", want: "lmno"},
		// Write larger than ring buffer.
		{in: "pqrstuv", want: "stuv"},
	}
	for i, d := range data {
		in := []byte(d.in)
		n, err := rb.Write(in)
		if err != nil {
			t.Error(err)
		}
		if n != len(in) {
			t.Error(i, "Wrote", n, "bytes, want", len(in))
		}
		got := rb.Bytes()
		want := []byte(d.want)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%d Got %q want %q", i, got, want)
		}
	}
}
