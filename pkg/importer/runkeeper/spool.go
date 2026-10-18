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

package runkeeper

import (
	"bufio"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Sensor samples spill to private disposable files, never an in-memory sample
// collection. The original CSV remains attached to the account. At most 32
// buffered writers and one CSV record are resident, including interleaved IDs.
const (
	maxSpoolBytes          = int64(8 << 30)
	maxActivitySensorBytes = int64(512 << 20)
)

type sensorSpool struct {
	written       int64
	sizes         map[string]int64
	activitySizes map[string]int64
	// Overridable only by in-package tests to exercise limits with small fixtures.
	totalLimit, activityLimit int64
	dir                       string
	open                      map[string]*list.Element
	lru                       list.List
	files                     map[string]string
}
type spoolWriter struct {
	key string
	f   *os.File
	w   *bufio.Writer
}

func newSpool() (*sensorSpool, error) {
	dir, err := os.MkdirTemp("", "perkeep-runkeeper-")
	if err != nil {
		return nil, err
	}
	return &sensorSpool{dir: dir, open: make(map[string]*list.Element), files: make(map[string]string), sizes: make(map[string]int64), activitySizes: make(map[string]int64), totalLimit: maxSpoolBytes, activityLimit: maxActivitySensorBytes}, nil
}
func (s *sensorSpool) closeOne(el *list.Element) error {
	v := el.Value.(*spoolWriter)
	err := v.w.Flush()
	e := v.f.Close()
	if err == nil {
		err = e
	}
	delete(s.open, v.key)
	s.lru.Remove(el)
	return err
}
func (s *sensorSpool) flush() error {
	var errs []error
	for s.lru.Len() > 0 {
		if err := s.closeOne(s.lru.Back()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (s *sensorSpool) close() { s.flush(); os.RemoveAll(s.dir) }
func (s *sensorSpool) add(key string, row rawRow) error {
	if len(key) < 37 || !validUUID(key[:36]) || (key[36:] != "-steps" && key[36:] != "-heartRate") {
		return errors.New("invalid sensor partition key")
	}
	activityID := key[:36]
	// Marshal only one bounded CSV record, and check ACTUAL encoded expansion
	// before opening/writing a partition. Header repetition and escaped values
	// must not turn a small CSV into unbounded scratch disk writes.
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	size := int64(len(data))
	if size > s.totalLimit-s.written || size > s.activityLimit-s.activitySizes[activityID] {
		return fmt.Errorf("runkeeper: encoded sensor spool byte limit exceeded")
	}
	el := s.open[key]
	if el == nil {
		if s.lru.Len() >= 32 {
			if err := s.closeOne(s.lru.Back()); err != nil {
				return err
			}
		}
		name := filepath.Join(s.dir, key+".jsonl")
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		el = s.lru.PushFront(&spoolWriter{key: key, f: f, w: bufio.NewWriterSize(f, 32<<10)})
		s.open[key] = el
		s.files[key] = name
	}
	s.lru.MoveToFront(el)
	n, err := el.Value.(*spoolWriter).w.Write(data)
	s.written += int64(n)
	s.sizes[key] += int64(n)
	s.activitySizes[activityID] += int64(n)
	return err
}
func (s *sensorSpool) read(ctx context.Context, src memberSource, name, kind string, ids map[string]bool) error {
	return readCSV(ctx, src, name, maxSensors, func(row rawRow) error {
		id := strings.ToLower(strings.TrimSpace(row["Activity Id"]))
		if !ids[id] {
			return nil
		} // Unassociated records remain lossless in original CSV/ZIP.
		return s.add(id+"-"+kind, row)
	})
}
func (s *sensorSpool) hash(ctx context.Context, key string) (string, error) {
	name := s.files[key]
	if name == "" {
		return "", nil
	}
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
