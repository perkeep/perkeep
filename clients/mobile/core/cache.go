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

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// A logCache is a string-keyed map persisted as an append-only file of
// JSON lines, each {"k": key, "v": value}, the last for a key winning.
// It's for caches that only grow, like hashes of files: an entry is
// written as soon as it's added, and the file is read once, when the
// cache is opened.
type logCache[V any] struct {
	mu   sync.Mutex
	f    *os.File // nil if in memory only
	m    map[string]V
	path string
}

type logEntry[V any] struct {
	K string `json:"k"`
	V V      `json:"v"`
}

// openLogCache opens the cache in the file at path, creating it if
// needed. If path is empty, the cache is in memory only.
func openLogCache[V any](path string) (*logCache[V], error) {
	c := &logCache[V]{m: map[string]V{}, path: path}
	if path == "" {
		return c, nil
	}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var e logEntry[V]
			// Skip bad lines, such as a last one cut short by a crash.
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				c.m[e.K] = e.V
			}
		}
		err := sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	c.f = f
	return c, nil
}

func (c *logCache[V]) get(k string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *logCache[V]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// put sets k to v, appending the entry to the file.
func (c *logCache[V]) put(k string, v V) error {
	line, err := json.Marshal(logEntry[V]{k, v})
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	if c.f == nil {
		return nil
	}
	_, err = c.f.Write(append(line, '\n'))
	return err
}

// close closes the cache's file. It's still usable in memory.
func (c *logCache[V]) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}
