/*
Copyright 2012 The Camlistore Authors.

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

package chanworker

import (
	"container/list"
	"sync"
)

type chanWorker struct {
	c chan interface{}

	donec chan bool
	workc chan interface{}
	fn    func(n interface{}, ok bool)
	buf   *list.List
}

// TODO: make it configurable if need be. Although so far in camput it wasn't.
const buffered = 16

// NewWorker starts nWorkers goroutines running fn on incoming
// items sent on the returned channel.  fn may block; writes to the
// channel will buffer.
// If nWorkers is negative, a new goroutine running fn is called for each
// item sent on the returned channel.
// When the returned channel is closed, fn is called with (nil, false)
// after all other calls to fn have completed.
// If nWorkers is zero, NewWorker will panic.
func NewWorker(nWorkers int, fn func(el interface{}, ok bool)) chan<- interface{} {
	if nWorkers == 0 {
		panic("NewChanWorker: invalid value of 0 for nWorkers")
	}
	retc := make(chan interface{}, buffered)
	if nWorkers < 0 {
		// Unbounded number of workers.
		go func() {
			var wg sync.WaitGroup
			for w := range retc {
				wg.Add(1)
				go func(w interface{}) {
					fn(w, true)
					wg.Done()
				}(w)
			}
			wg.Wait()
			fn(nil, false)
		}()
		return retc
	}
	w := &chanWorker{
		c:     retc,
		workc: make(chan interface{}, buffered),
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

func (w *chanWorker) pump() {
	inc := w.c
	for inc != nil || w.buf.Len() > 0 {
		outc := w.workc
		var frontNode interface{}
		if e := w.buf.Front(); e != nil {
			frontNode = e.Value
		} else {
			outc = nil
		}
		select {
		case outc <- frontNode:
			w.buf.Remove(w.buf.Front())
		case el, ok := <-inc:
			if !ok {
				inc = nil
				continue
			}
			w.buf.PushBack(el)
		}
	}
	close(w.workc)
}

func (w *chanWorker) work() {
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
