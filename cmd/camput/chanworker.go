/*
Copyright 2012 Google Inc.

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

// Worker pools of functions processing channel input.
// TODO(brafitz): move this to a common library, making it operate on interface{} instead?

package main

import (
	"container/list"
	"sync"
)

type nodeWorker struct {
	c chan *node

	donec chan bool
	workc chan *node
	fn    func(n *node, ok bool)
	buf   *list.List
}

// NewNodeWorker starts nWorkers goroutines running fn on incoming
// nodes sent on the returned channel.  fn may block; writes to the
// channel will buffer.
// If nWorkers is negative, a new goroutine running fn is called for each
// item sent on the returned channel.
// When the returned channel is closed, fn is called with (nil, false)
// after all other calls to fn have completed.
func NewNodeWorker(nWorkers int, fn func(n *node, ok bool)) chan<- *node {
	if nWorkers == 0 {
		panic("invalid nWorkers valid of 0")
	}
	retc := make(chan *node, buffered)
	if nWorkers < 0 {
		// Unbounded number of workers.
		go func() {
			var wg sync.WaitGroup
			for w := range retc {
				wg.Add(1)
				go func(w *node) {
					fn(w, true)
					wg.Done()
				}(w)
			}
			wg.Wait()
			fn(nil, false)
		}()
		return retc
	}
	w := &nodeWorker{
		c:     retc,
		workc: make(chan *node, buffered),
		donec: make(chan bool), // when workers finish
		fn:    fn,
		buf:   list.New(),
	}
	go w.pump()
	for i := 0; i < nWorkers; i++ {
		go w.work()
	}
	go func() {
		for i := 0; i < nWorkers; i++ {
			<-w.donec
		}
		fn(nil, false) // final sentinel
	}()
	return retc
}

func (w *nodeWorker) pump() {
	inc := w.c
	for inc != nil || w.buf.Len() > 0 {
		outc := w.workc
		var frontNode *node
		if e := w.buf.Front(); e != nil {
			frontNode = e.Value.(*node)
		} else {
			outc = nil
		}
		select {
		case outc <- frontNode:
			w.buf.Remove(w.buf.Front())
		case n, ok := <-inc:
			if !ok {
				inc = nil
				continue
			}
			w.buf.PushBack(n)
		}
	}
	close(w.workc)
}

func (w *nodeWorker) work() {
	for {
		select {
		case n, ok := <-w.workc:
			if !ok {
				w.donec <- true
				return
			}
			w.fn(n, true)
		}
	}
}
