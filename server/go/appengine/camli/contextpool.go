// +build appengine

/*
Copyright 2013 Google Inc.

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

package appengine

import (
	"sync"

	"appengine"
)

type ContextPool struct {
	mu sync.Mutex // guards live

	// Live HTTP requests
	live map[appengine.Context]*sync.WaitGroup
}

// HandlerBegin notes that the provided context is beginning and it can be
// shared until HandlerEnd is called.
func (p *ContextPool) HandlerBegin(c appengine.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.live == nil {
		p.live = make(map[appengine.Context]*sync.WaitGroup)
	}
	if _, ok := p.live[c]; ok {
		// dup; ignore.
		return
	}
	p.live[c] = new(sync.WaitGroup)
}

// HandlerEnd notes that the provided context is about to go out of service,
// removes it from the pool of available contexts, and blocks until everybody
// is done using it.
func (p *ContextPool) HandlerEnd(c appengine.Context) {
	p.mu.Lock()
	wg := p.live[c]
	delete(p.live, c)
	p.mu.Unlock()
	if wg != nil {
		wg.Wait()
	}
}

// A ContextLoan is a superset of a Context, so can passed anywhere
// that needs an appengine.Context.
//
// When done, Return it.
type ContextLoan interface {
	appengine.Context

	// Return returns the Context to the pool.
	// Return must be called exactly once.
	Return()
}

// Get returns a valid App Engine context from some active HTTP request
// which is guaranteed to stay valid.  Be sure to return it.
//
// Typical use:
//   ctx := pool.Get()
//   defer ctx.Return()
func (p *ContextPool) Get() ContextLoan {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Pick a random active context.  TODO: pick the "right" one,
	// using some TLS-like-guess/hack from runtume.Stacks.
	var c appengine.Context
	var wg *sync.WaitGroup
	for c, wg = range p.live {
		break
	}
	if c == nil {
		panic("ContextPool.Get called with no live HTTP requests")
	}
	wg.Add(1)
	cl := &contextLoan{Context: c, wg: wg}
	// TODO: set warning finalizer on this?
	return cl
}

type contextLoan struct {
	appengine.Context

	mu sync.Mutex
	wg *sync.WaitGroup
}

func (cl *contextLoan) Return() {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.wg == nil {
		panic("Return called twice")
	}
	cl.wg.Done()
	cl.wg = nil
}
