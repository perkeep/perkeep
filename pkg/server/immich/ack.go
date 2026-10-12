/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package immich

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"perkeep.org/pkg/sorted"
)

const (
	immichAckPrefix        = "syncack|"
	immichAckCheckpointKey = "sync|ackCheckpointTime"
	immichAckInterval      = 24 * time.Hour
)

// immichAckState coalesces mobile ACK advances, not asset or account metadata.
// Runtime reads see the latest ACKs; an unclean stop can replay work since the
// last daily checkpoint. Explicit resets are durable barriers, and graceful
// close attempts an early checkpoint. The native KV's durability is unchanged.
//
// All methods, including construction/replacement on a live handler, require
// h.mu. Construction before publishing the handler needs no lock. The timer
// uses that same mutex, never a second lock around native publication.
type immichAckState struct {
	h          *Handler
	values     map[string]string // full native keys, including session and entity
	dirty      map[string]string
	lastFlush  time.Time
	retryAfter time.Time
	timer      *time.Timer
	now        func() time.Time
	closed     bool
}

func newImmichAckState(h *Handler) (*immichAckState, error) {
	a := &immichAckState{
		h: h, values: make(map[string]string), dirty: make(map[string]string),
		now: time.Now, lastFlush: time.Now(),
	}
	it := h.metadata.Find(immichAckPrefix, immichAckPrefix+"\xff")
	for it.Next() {
		a.values[it.Key()] = it.Value()
	}
	if err := it.Close(); err != nil {
		return nil, fmt.Errorf("load immich acknowledgements: %w", err)
	}
	stamp, err := h.metadata.Get(immichAckCheckpointKey)
	if err == nil {
		a.lastFlush, err = time.Parse(time.RFC3339Nano, stamp)
	}
	if err != nil && !errors.Is(err, sorted.ErrNotFound) {
		return nil, fmt.Errorf("load immich acknowledgement checkpoint: %w", err)
	}
	// A cache replacement is not a graceful close: do not publish the state
	// being replaced. Cancel its timer even if a callback is already waiting.
	if old := h.acks; old != nil {
		old.stopTimerLocked()
		old.closed = true
	}
	return a, nil
}

func (a *immichAckState) readyLocked() error {
	if a.closed {
		return errors.New("immich acknowledgements are closed")
	}
	// Do not let the RAM view conceal a poisoned/closed native backend.
	// Resolve metadata on every operation, including fault-injection wrappers.
	_, err := a.h.metadata.Get("sequence")
	if errors.Is(err, sorted.ErrNotFound) {
		return nil
	}
	return err
}

func (a *immichAckState) getLocked(prefix string) (map[string]string, error) {
	if err := a.readyLocked(); err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for key, value := range a.values {
		if strings.HasPrefix(key, prefix) {
			result[strings.TrimPrefix(key, prefix)] = value
		}
	}
	return result, nil
}

// setLocked merges already-validated ACKs. Omitted entities are not deleted.
func (a *immichAckState) setLocked(prefix string, acks map[string]string) error {
	if err := a.readyLocked(); err != nil {
		return err
	}
	for entity, value := range acks {
		if err := sorted.CheckSizes(prefix+entity, value); err != nil {
			return err
		}
	}
	changed := false
	for entity, value := range acks {
		key := prefix + entity
		if previous, ok := a.values[key]; ok && previous == value {
			continue
		}
		a.values[key], a.dirty[key] = value, value
		changed = true
	}
	if changed {
		a.armLocked()
	}
	return nil
}

// deleteLocked publishes the deletion BEFORE changing RAM. Otherwise a crash
// could restore an older, higher persisted ACK and skip replay after a reset.
// nil types resets the session; an empty non-nil slice does nothing.
func (a *immichAckState) deleteLocked(prefix string, types []string) error {
	if err := a.readyLocked(); err != nil {
		return err
	}
	keys := make(map[string]bool)
	if types == nil {
		for key := range a.values {
			if strings.HasPrefix(key, prefix) {
				keys[key] = true
			}
		}
	} else {
		for _, entity := range types {
			key := prefix + entity
			if err := sorted.CheckSizes(key, ""); err != nil {
				return err
			}
			keys[key] = true
		}
	}
	if len(keys) == 0 {
		return nil
	}
	b := a.h.metadata.BeginBatch()
	for key := range keys {
		b.Delete(key)
	}
	if err := a.h.metadata.CommitBatch(b); err != nil {
		return err
	}
	for key := range keys {
		delete(a.values, key)
		delete(a.dirty, key)
	}
	if len(a.dirty) == 0 {
		a.stopTimerLocked()
	}
	return nil
}

func (a *immichAckState) stopTimerLocked() {
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
}

func (a *immichAckState) deadlineLocked() time.Time {
	deadline := a.lastFlush.Add(immichAckInterval)
	if a.retryAfter.After(deadline) {
		deadline = a.retryAfter
	}
	return deadline
}

func (a *immichAckState) armLocked() {
	if a.closed || a.timer != nil || len(a.dirty) == 0 {
		return
	}
	// This is a fixed checkpoint window, not a debounce after the last POST.
	// The mutex prevents the callback from seeing timer before assignment.
	var timer *time.Timer
	timer = time.AfterFunc(a.deadlineLocked().Sub(a.now()), func() {
		a.h.mu.Lock()
		defer a.h.mu.Unlock()
		if a.closed || a.h.acks != a || a.timer != timer {
			return
		}
		a.timer = nil
		if err := a.flushLocked(false); err != nil {
			log.Printf("immich: checkpointing sync acknowledgements: %v", err)
		}
	})
	a.timer = timer
}

func (a *immichAckState) flushLocked(force bool) error {
	if a.closed {
		return errors.New("immich acknowledgements are closed")
	}
	if len(a.dirty) == 0 {
		return nil
	}
	now := a.now()
	if !force && now.Before(a.deadlineLocked()) {
		a.armLocked()
		return nil
	}
	a.stopTimerLocked()
	b := a.h.metadata.BeginBatch()
	for key, value := range a.dirty {
		b.Set(key, value)
	}
	b.Set(immichAckCheckpointKey, now.UTC().Format(time.RFC3339Nano))
	if err := a.h.metadata.CommitBatch(b); err != nil {
		// Keep the RAM state and retry at most daily, not in a hot loop if
		// the previous checkpoint is already overdue.
		a.retryAfter = now.Add(immichAckInterval)
		if !force {
			a.armLocked()
		}
		return err
	}
	clear(a.dirty)
	a.lastFlush = now
	a.retryAfter = time.Time{}
	return nil
}

func (a *immichAckState) closeLocked() error {
	if a.closed {
		return nil
	}
	a.stopTimerLocked()
	err := a.flushLocked(true)
	a.closed = true
	return err
}
