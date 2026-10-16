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

// Package jsonfile registers the "jsonfile" sorted.KeyValue type, which
// keeps its rows in memory and in a JSON file, an object of string keys
// to string values, that it rewrites after each change.
//
// It's meant for small amounts of state that changes rarely and that
// people may want to read, such as the schedules of a server's health
// checks, not for indexes. Its configuration is:
//
//	{
//	  "type": "jsonfile",
//	  "file": "/path/to/state.json"
//	}
package jsonfile // import "perkeep.org/pkg/sorted/jsonfile"

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/sorted"
)

func init() {
	sorted.RegisterKeyValue("jsonfile", newFromConfig)
}

func newFromConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	file := cfg.RequiredString("file")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return Open(file)
}

// Open returns a KeyValue stored in the JSON file at path, which it
// creates when first changed if it doesn't exist.
func Open(path string) (sorted.KeyValue, error) {
	kv := &keyValue{path: path, mem: sorted.NewMemoryKeyValue()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return kv, nil
	}
	if err != nil {
		return nil, err
	}
	var rows map[string]string
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("jsonfile: %s: %w", path, err)
	}
	for k, v := range rows {
		if err := kv.mem.Set(k, v); err != nil {
			return nil, err
		}
	}
	return kv, nil
}

// keyValue is a sorted.KeyValue whose rows are in mem, and saved to
// path after each change.
type keyValue struct {
	path string
	mem  sorted.KeyValue

	mu sync.Mutex // serializes changes, and saving them
}

var _ sorted.Wiper = (*keyValue)(nil)

func (kv *keyValue) Get(key string) (string, error) { return kv.mem.Get(key) }

func (kv *keyValue) Find(start, end string) sorted.Iterator { return kv.mem.Find(start, end) }

func (kv *keyValue) Set(key, value string) error {
	return kv.change(func() error { return kv.mem.Set(key, value) })
}

func (kv *keyValue) Delete(key string) error {
	return kv.change(func() error { return kv.mem.Delete(key) })
}

func (kv *keyValue) BeginBatch() sorted.BatchMutation { return kv.mem.BeginBatch() }

func (kv *keyValue) CommitBatch(b sorted.BatchMutation) error {
	return kv.change(func() error { return kv.mem.CommitBatch(b) })
}

func (kv *keyValue) Wipe() error {
	return kv.change(func() error { return kv.mem.(sorted.Wiper).Wipe() })
}

func (kv *keyValue) Close() error { return nil }

// change makes a change to the rows with fn, then saves them.
func (kv *keyValue) change(fn func() error) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if err := fn(); err != nil {
		return err
	}
	return kv.saveLocked()
}

// saveLocked writes the rows to kv.path, by writing them to a temporary
// file and renaming it, so a crash can't leave the file half-written.
func (kv *keyValue) saveLocked() error {
	rows := make(map[string]string)
	if err := sorted.Foreach(kv.mem, func(k, v string) error {
		rows[k] = v
		return nil
	}); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rows, "", "\t") // with sorted keys
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(kv.path), "."+filepath.Base(kv.path)+".*")
	if err != nil {
		return fmt.Errorf("jsonfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("jsonfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("jsonfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("jsonfile: %w", err)
	}
	if err := os.Rename(tmp.Name(), kv.path); err != nil {
		return fmt.Errorf("jsonfile: %w", err)
	}
	return nil
}
