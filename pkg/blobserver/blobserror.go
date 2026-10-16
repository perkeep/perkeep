/*
Copyright 2026 The Perkeep Authors

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

package blobserver

import (
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"perkeep.org/pkg/blob"
)

// A BlobsError is the error from an operation on several blobs, such as
// ReceiveBlobs, that failed for some of the blobs, not necessarily all.
// It records each failed blob's error; the operation's other blobs
// succeeded.
//
// The zero value is an empty BlobsError, ready to use. It's not safe for
// concurrent use.
type BlobsError struct {
	Op string // the operation, such as "receive", for Error messages

	errs map[blob.Ref]error // only failed blobs
}

// Add records err as br's error, unless br already has one. A nil err is
// ignored.
func (e *BlobsError) Add(br blob.Ref, err error) {
	if err == nil {
		return
	}
	if e.errs == nil {
		e.errs = make(map[blob.Ref]error)
	}
	if _, ok := e.errs[br]; !ok {
		e.errs[br] = err
	}
}

// AddResult records the result of an operation on refs: err is what that
// operation returned. If it's nil, nothing is recorded. If it is (or
// wraps) a *BlobsError, its errors are copied. Any other error is recorded
// for every ref. Wrappers use AddResult to combine the results of
// operations on their children.
func (e *BlobsError) AddResult(refs []blob.Ref, err error) {
	if err == nil {
		return
	}
	if be, ok := errors.AsType[*BlobsError](err); ok {
		for br, err := range be.errs {
			e.Add(br, err)
		}
		return
	}
	for _, br := range refs {
		e.Add(br, err)
	}
}

// Err returns br's error, or nil if br succeeded (or wasn't part of the
// operation).
func (e *BlobsError) Err(br blob.Ref) error {
	if e == nil {
		return nil
	}
	return e.errs[br]
}

// Len returns the number of failed blobs.
func (e *BlobsError) Len() int {
	if e == nil {
		return 0
	}
	return len(e.errs)
}

// failedRefs returns the failed blobs' refs, sorted.
func (e *BlobsError) failedRefs() []blob.Ref {
	return slices.SortedFunc(maps.Keys(e.errs), func(a, b blob.Ref) int {
		switch {
		case a.Less(b):
			return -1
		case b.Less(a):
			return 1
		}
		return 0
	})
}

// All returns the failed blobs and their errors, in order of their refs.
func (e *BlobsError) All() iter.Seq2[blob.Ref, error] {
	return func(yield func(blob.Ref, error) bool) {
		if e == nil {
			return
		}
		for _, br := range e.failedRefs() {
			if !yield(br, e.errs[br]) {
				return
			}
		}
	}
}

// OrNil returns e, or nil if no blob failed, for returning from an
// operation:
//
//	return be.OrNil()
func (e *BlobsError) OrNil() error {
	if e.Len() == 0 {
		return nil
	}
	return e
}

// maxErrorMessages is how many distinct messages Error includes.
const maxErrorMessages = 3

// Error summarizes the failures: the Op, how many blobs failed, and up to
// a few distinct error messages, each with the number of blobs that failed
// with it and one of their refs.
func (e *BlobsError) Error() string {
	op := e.Op
	if op == "" {
		op = "blobs operation"
	}
	refs := e.failedRefs()
	if len(refs) == 1 {
		return fmt.Sprintf("%s %v: %v", op, refs[0], e.errs[refs[0]])
	}
	// Group by message, not by error value: errors aren't necessarily
	// comparable.
	type group struct {
		example blob.Ref
		n       int
	}
	var msgs []string
	groups := map[string]*group{}
	for _, br := range refs {
		msg := e.errs[br].Error()
		g, ok := groups[msg]
		if !ok {
			g = &group{example: br}
			groups[msg] = g
			msgs = append(msgs, msg)
		}
		g.n++
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %d blobs failed: ", op, len(refs))
	for i, msg := range msgs {
		if i == maxErrorMessages {
			fmt.Fprintf(&sb, "; and %d more distinct errors", len(msgs)-i)
			break
		}
		if i > 0 {
			sb.WriteString("; ")
		}
		g := groups[msg]
		if g.n == 1 {
			fmt.Fprintf(&sb, "%v: %s", g.example, msg)
		} else {
			fmt.Fprintf(&sb, "%s (%d blobs, including %v)", msg, g.n, g.example)
		}
	}
	return sb.String()
}

// Unwrap returns the failed blobs' errors, in order of their refs. So
// errors.Is and errors.As report whether some blob failed with a given
// error, such as ErrCorruptBlob, not whether the whole operation did; use
// BlobErr to ask about a particular blob.
func (e *BlobsError) Unwrap() []error {
	var errs []error
	for _, err := range e.All() {
		errs = append(errs, err)
	}
	return errs
}

// BlobErr returns br's error from err, the error returned by an operation
// on several blobs including br: nil if err is nil; br's error if err is
// (or wraps) a *BlobsError; and otherwise err itself, as the operation
// failed as a whole.
func BlobErr(err error, br blob.Ref) error {
	if err == nil {
		return nil
	}
	if be, ok := errors.AsType[*BlobsError](err); ok {
		return be.Err(br)
	}
	return err
}
