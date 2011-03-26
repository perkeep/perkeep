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

package lru

import (
	"container/list"
	"sync"
)

type Cache struct {
	maxEntries int

	lk    sync.Mutex
	ll    *list.List
	cache map[string]*list.Element
}

func New(maxEntries int) *Cache {
	return &Cache{
		maxEntries: maxEntries,
		cache:      make(map[string]*list.Element),
	}
}

func (c *Cache) Add(key string, value interface{}) {
	c.lk.Lock()
	defer c.lk.Unlock()
	if ee, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ee)
		ee.Value = value
		return
	}
	// TODO: check size, add new element, etc
}
