/*
Copyright 2011 Google Inc.

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

package blob

// TODO: use Generics if/when available
type ChanPeeker struct {
	Ch <-chan SizedRef

	// A channel should either have a peek value or be closed:
	peek   *SizedRef
	closed bool
}

func (cp *ChanPeeker) MustPeek() SizedRef {
	sr, ok := cp.Peek()
	if !ok {
		panic("No Peek value available")
	}
	return sr
}

func (cp *ChanPeeker) Peek() (sr SizedRef, ok bool) {
	if cp.closed {
		return
	}
	if cp.peek != nil {
		return *cp.peek, true
	}
	v, ok := <-cp.Ch
	if !ok {
		cp.closed = true
		return
	}
	cp.peek = &v
	return *cp.peek, true
}

func (cp *ChanPeeker) Closed() bool {
	cp.Peek()
	return cp.closed
}

func (cp *ChanPeeker) MustTake() SizedRef {
	sr, ok := cp.Take()
	if !ok {
		panic("MustTake called on empty channel")
	}
	return sr
}

func (cp *ChanPeeker) Take() (sr SizedRef, ok bool) {
	v, ok := cp.Peek()
	if !ok {
		return
	}
	cp.peek = nil
	return v, true
}

func (cp *ChanPeeker) ConsumeAll() {
	for !cp.Closed() {
		cp.Take()
	}
}
