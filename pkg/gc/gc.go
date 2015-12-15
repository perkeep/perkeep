/*
Copyright 2014 The Camlistore Authors

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

// Package gc defines a generic garbage collector.
package gc

import (
	"errors"
	"fmt"

	"golang.org/x/net/context"

	"go4.org/syncutil"
)

const buffered = 32 // arbitrary

// Item is something that exists that may or may not survive a GC collection.
type Item interface{}

// A Collector performs a garbage collection.
type Collector struct {
	// World specifies a World that should be stopped before a
	// collection and started again after.
	World World

	Marker         Marker
	Roots          Enumerator
	Sweeper        Enumerator
	ItemEnumerator ItemEnumerator
	Deleter        Deleter
}

type Marker interface {
	// Mark marks that an item should exist.
	// It must be safe for calls from concurrent goroutines.
	Mark(Item) error

	// IsMarked returns whether the item is marked.
	// It must be safe for calls from concurrent goroutines.
	IsMarked(Item) (bool, error)
}

// World defines the thing that should be stopped before GC and started after.
type World interface {
	Stop() error
	Start() error
}

type Deleter interface {
	// Delete deletes an item that was deemed unreachable via
	// the garbage collector.
	// It must be safe for calls from concurrent goroutines.
	Delete(Item) error
}

// Enumerator enumerates items.
type Enumerator interface {
	// Enumerate enumerates items (which items depends on usage)
	// and sends them to the provided channel. Regardless of return
	// value, the channel should be closed.
	//
	// If the provided context is closed, Enumerate should return
	// with an error (typically context.Canceled)
	Enumerate(context.Context, chan<- Item) error
}

// ItemEnumerator enumerates all the edges out from an item.
type ItemEnumerator interface {
	// EnumerateItme is like Enuerator's Enumerate, but specific
	// to the provided item.
	EnumerateItem(context.Context, Item, chan<- Item) error
}

// ctx will be canceled on failure
func (c *Collector) markItem(ctx context.Context, it Item, isRoot bool) error {
	if !isRoot {
		marked, err := c.Marker.IsMarked(it)
		if err != nil {
			return err
		}
		if marked {
			return nil
		}
	}
	if err := c.Marker.Mark(it); err != nil {
		return err
	}

	// FIXME(tgulacsi): is it a problem that we cannot cancel the parent?
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan Item, buffered)
	var grp syncutil.Group
	grp.Go(func() error {
		return c.ItemEnumerator.EnumerateItem(ctx, it, ch)
	})
	grp.Go(func() error {
		for it := range ch {
			if err := c.markItem(ctx, it, false); err != nil {
				return err
			}
		}
		return nil
	})
	if err := grp.Err(); err != nil {
		cancel()
		return err
	}
	return nil
}

// Collect performs a garbage collection.
func (c *Collector) Collect(ctx context.Context) (err error) {
	if c.World == nil {
		return errors.New("no World")
	}
	if c.Marker == nil {
		return errors.New("no Marker")
	}
	if c.Roots == nil {
		return errors.New("no Roots")
	}
	if c.Sweeper == nil {
		return errors.New("no Sweeper")
	}
	if c.ItemEnumerator == nil {
		return errors.New("no ItemEnumerator")
	}
	if c.Deleter == nil {
		return errors.New("no Deleter")
	}
	if err := c.World.Stop(); err != nil {
		return err
	}
	defer func() {
		startErr := c.World.Start()
		if err == nil {
			err = startErr
		}
	}()

	// Mark.
	roots := make(chan Item, buffered)
	markCtx, cancelMark := context.WithCancel(ctx)
	var marker syncutil.Group
	marker.Go(func() error {
		defer cancelMark()
		for it := range roots {
			if err := c.markItem(markCtx, it, true); err != nil {
				return err
			}
		}
		return nil
	})
	marker.Go(func() error {
		return c.Roots.Enumerate(markCtx, roots)
	})
	if err := marker.Err(); err != nil {
		return fmt.Errorf("Mark failure: %v", err)
	}

	// Sweep.
	all := make(chan Item, buffered)
	sweepCtx, _ := context.WithCancel(ctx)
	var sweeper syncutil.Group
	sweeper.Go(func() error {
		return c.Sweeper.Enumerate(sweepCtx, all)
	})
	sweeper.Go(func() error {
		defer sweepCtx.Done()
		for it := range all {
			ok, err := c.Marker.IsMarked(it)
			if err != nil {
				return err
			}
			if !ok {
				if err := c.Deleter.Delete(it); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err := sweeper.Err(); err != nil {
		return fmt.Errorf("Sweep failure: %v", err)
	}
	return nil
}
