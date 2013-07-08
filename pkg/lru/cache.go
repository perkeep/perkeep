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

// Package lru implements an LRU cache.
package lru

import (
	"container/list"
	"sync"
)

// Cache is an LRU cache, safe for concurrent access.
type Cache struct {
	maxEntries int

	lk    sync.Mutex
	ll    *list.List
	cache map[string]*list.Element
}

type entry struct {
	key   string
	value interface{}
}

// New returns a new cache with the provided maximum items.
func New(maxEntries int) *Cache {
	return &Cache{
		maxEntries: maxEntries,
		ll:         list.New(),
		cache:      make(map[string]*list.Element),
	}
}

// Add adds the provided key and value to the cache, evicting
// an old item if necessary.
func (c *Cache) Add(key string, value interface{}) {
	c.lk.Lock()
	defer c.lk.Unlock()

	// Already in cache?
	if ee, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ee)
		ee.Value.(*entry).value = value
		return
	}

	// Add to cache if not present
	ele := c.ll.PushFront(&entry{key, value})
	c.cache[key] = ele

	if c.ll.Len() > c.maxEntries {
		c.removeOldest()
	}
}

// Get fetches the key's value from the cache.
// The ok result will be true if the item was found.
func (c *Cache) Get(key string) (value interface{}, ok bool) {
	c.lk.Lock()
	defer c.lk.Unlock()
	if ele, hit := c.cache[key]; hit {
		c.ll.MoveToFront(ele)
		return ele.Value.(*entry).value, true
	}
	return
}

// RemoveOldest removes the oldest item in the cache.
func (c *Cache) RemoveOldest() {
	c.lk.Lock()
	defer c.lk.Unlock()
	c.removeOldest()
}

// note: must hold c.lk
func (c *Cache) removeOldest() {
	ele := c.ll.Back()
	if ele == nil {
		return
	}
	c.ll.Remove(ele)
	delete(c.cache, ele.Value.(*entry).key)
}

// Len returns the number of items in the cache.
func (c *Cache) Len() int {
	c.lk.Lock()
	defer c.lk.Unlock()
	return c.ll.Len()
}
