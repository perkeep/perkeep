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

package blobref

// TODO: use Generics if/when available
type ChanPeeker struct {
	Ch     <-chan SizedBlobRef

	// A channel should either have a peek value or be closed:
	peek   *SizedBlobRef
	closed bool
}

func (cp *ChanPeeker) Peek() *SizedBlobRef {
	if cp.closed {
		return nil
	}
	if cp.peek != nil {
		return cp.peek
	}
	v, ok := <-cp.Ch
	if !ok {
		cp.closed = true
		return nil
	}
	cp.peek = &v
	return cp.peek
}

func (cp *ChanPeeker) Closed() bool {
	cp.Peek()
	return cp.closed
}

func (cp *ChanPeeker) Take() *SizedBlobRef {
	v := cp.Peek()
	cp.peek = nil
	return v
}

func (cp *ChanPeeker) ConsumeAll() {
	for !cp.Closed() {
		cp.Take()
	}
}

