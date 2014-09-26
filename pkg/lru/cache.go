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

	mu    sync.Mutex
	ll    *list.List
	cache map[string]*list.Element
}

// *entry is the type stored in each *list.Element.
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
	c.mu.Lock()
	defer c.mu.Unlock()

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
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, hit := c.cache[key]; hit {
		c.ll.MoveToFront(ele)
		return ele.Value.(*entry).value, true
	}
	return
}

// RemoveOldest removes the oldest item in the cache and returns its key and value.
// If the cache is empty, the empty string and nil are returned.
func (c *Cache) RemoveOldest() (key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeOldest()
}

// note: must hold c.mu
func (c *Cache) removeOldest() (key string, value interface{}) {
	ele := c.ll.Back()
	if ele == nil {
		return
	}
	c.ll.Remove(ele)
	ent := ele.Value.(*entry)
	delete(c.cache, ent.key)
	return ent.key, ent.value

}

// Len returns the number of items in the cache.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
