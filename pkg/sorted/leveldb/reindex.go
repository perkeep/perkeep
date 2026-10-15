/*
Copyright 2026 The Perkeep Authors.

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

package leveldb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"perkeep.org/pkg/sorted"
)

var (
	_ sorted.Reindexer      = (*kvis)(nil)
	_ sorted.ReindexSession = (*reindexSession)(nil)
)

// incompleteMarker is the name of a file in the database directory whose
// presence means the database's contents are incomplete and disposable: a
// reindex or Wipe began but didn't finish. goleveldb ignores files with
// names it doesn't use.
const incompleteMarker = "PERKEEP-INCOMPLETE"

var errSessionDone = errors.New("leveldb: reindex session is finished")

// BeginReindex durably marks the database incomplete, then replaces it
// with an empty one. The session's writes aren't synced to disk: until
// Finish removes the marker, the index is incomplete anyway, and an
// interrupted rebuild must start over, so there's no point waiting for
// each write to reach the disk. Finish makes the rebuild durable before it
// removes the marker.
func (is *kvis) BeginReindex() (sorted.ReindexSession, error) {
	if err := is.resetIncomplete(); err != nil {
		return nil, fmt.Errorf("leveldb: beginning reindex: %w", err)
	}
	return &reindexSession{
		kv:        is,
		writeOpts: &opt.WriteOptions{Sync: false},
	}, nil
}

// resetIncomplete closes the database, durably marks it incomplete,
// removes its files, and opens a new empty database in their place. If it
// fails, is.db may be nil, and only Close is permitted.
func (is *kvis) resetIncomplete() error {
	err := is.db.Close()
	is.db = nil
	if err != nil {
		return err
	}
	if err := writeIncompleteMarker(is.path); err != nil {
		return err
	}
	if err := clearExceptMarker(is.path); err != nil {
		return err
	}
	db, err := leveldb.OpenFile(is.path, is.opts)
	if err != nil {
		return fmt.Errorf("error creating %s: %w", is.path, err)
	}
	is.db = db
	return nil
}

func hasIncompleteMarker(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, incompleteMarker))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func writeIncompleteMarker(dir string) error {
	f, err := os.Create(filepath.Join(dir, incompleteMarker))
	if err != nil {
		return err
	}
	_, err = f.WriteString("This Perkeep index is incomplete: a reindex or wipe didn't finish.\n")
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return syncDir(dir)
}

func removeIncompleteMarker(dir string) error {
	if err := os.Remove(filepath.Join(dir, incompleteMarker)); err != nil {
		return err
	}
	return syncDir(dir)
}

// clearExceptMarker removes everything in dir except the incomplete marker.
func clearExceptMarker(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		if ent.Name() == incompleteMarker {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, ent.Name())); err != nil {
			return err
		}
	}
	return syncDir(dir)
}

// syncDir makes dir's entries durable. Like goleveldb, it does nothing on
// Windows, where directories can't be synced this way.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// reindexSession uses its parent's database, writing without syncing.
// It deliberately does not embed kvis, so it doesn't implement
// sorted.TransactionalReader, sorted.Wiper, or sorted.Reindexer.
type reindexSession struct {
	kv        *kvis
	writeOpts *opt.WriteOptions

	// mu is held for reading by operations on kv.db, and for writing
	// by Finish, which replaces kv.db.
	mu              sync.RWMutex
	done            bool
	finishedSuccess bool
	finishErr       error

	errMu sync.Mutex
	err   error // first write error, even if the caller ignores it
}

// begin locks s for an operation on its database, returning an error if
// s can't be used. The caller must call s.mu.RUnlock if it returns nil.
func (s *reindexSession) begin() error {
	s.mu.RLock()
	if s.done {
		s.mu.RUnlock()
		return errSessionDone
	}
	return nil
}

// fail records err as s's first write error, if it's the first.
func (s *reindexSession) fail(err error) error {
	if err == nil {
		return nil
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		s.err = err
	}
	return err
}

func (s *reindexSession) Get(key string) (string, error) {
	if err := s.begin(); err != nil {
		return "", err
	}
	defer s.mu.RUnlock()
	return get(s.kv.db, s.kv.readOpts, key)
}

func (s *reindexSession) Find(start, end string) sorted.Iterator {
	if err := s.begin(); err != nil {
		return errIter{err: err}
	}
	defer s.mu.RUnlock()
	return find(s.kv.db, s.kv.readOpts, start, end)
}

func (s *reindexSession) Set(key, value string) error {
	if err := s.begin(); err != nil {
		return err
	}
	defer s.mu.RUnlock()
	// Match kvis: oversize sets are skipped.
	if sorted.CheckSizes(key, value) != nil {
		return nil
	}
	return s.fail(s.kv.db.Put([]byte(key), []byte(value), s.writeOpts))
}

func (s *reindexSession) Delete(key string) error {
	if err := s.begin(); err != nil {
		return err
	}
	defer s.mu.RUnlock()
	return s.fail(s.kv.db.Delete([]byte(key), s.writeOpts))
}

func (s *reindexSession) BeginBatch() sorted.BatchMutation {
	return &lvbatch{batch: new(leveldb.Batch)}
}

func (s *reindexSession) CommitBatch(bm sorted.BatchMutation) error {
	b, ok := bm.(*lvbatch)
	if !ok {
		return errors.New("invalid batch type")
	}
	if err := s.begin(); err != nil {
		return err
	}
	defer s.mu.RUnlock()
	b.errMu.Lock()
	defer b.errMu.Unlock()
	if b.err != nil {
		return b.err
	}
	return s.fail(s.kv.db.Write(b.batch, s.writeOpts))
}

// Finish is idempotent; the first call determines the outcome. Finish(true)
// closes and reopens the database: opening it moves everything in its
// journals, which unsynced writes leave unsynced, into synced tables. Only
// then does it remove the incomplete marker. Failed or aborted sessions
// leave the marker, and require a fresh rebuild or Wipe. If reopening the
// database fails, only Close is permitted on the parent.
func (s *reindexSession) Finish(success bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		if success && !s.finishedSuccess && s.finishErr == nil {
			return errSessionDone
		}
		return s.finishErr
	}
	s.done = true
	s.errMu.Lock()
	err := s.err
	s.errMu.Unlock()
	if success && err == nil {
		err = s.kv.reopen()
		if err == nil {
			err = removeIncompleteMarker(s.kv.path)
		}
	}
	s.finishedSuccess = success && err == nil
	s.finishErr = err
	return s.finishErr
}

// Close aborts only this session, not the parent database.
func (s *reindexSession) Close() error { return s.Finish(false) }

// reopen closes and reopens is.db. If it fails, is.db may be nil.
func (is *kvis) reopen() error {
	err := is.db.Close()
	is.db = nil
	if err != nil {
		return err
	}
	db, err := leveldb.OpenFile(is.path, is.opts)
	if err != nil {
		return fmt.Errorf("reopening %s: %w", is.path, err)
	}
	is.db = db
	return nil
}
