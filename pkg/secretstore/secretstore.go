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

// Package secretstore stores mutable server-local credentials outside Perkeep's
// immutable blobs and permanodes. Its SQLite file is private but not encrypted:
// protect its directory and backups as carefully as the credentials themselves.
//
// Store does not authorize callers. The server must authorize access before
// calling it, especially Get, which returns plaintext. List returns only
// Metadata. Storage errors never include credential values.
//
// A credential migrated from a permanode may already be present in immutable
// historical claims and backups. Migration preserves its value and sets
// MigratedFromPermanode as a warning; it does not revoke the credential. Ordinary
// writes preserve this warning. ClearMigration is an explicit acknowledgement
// that an administrator has rotated or otherwise dealt with the old credential.
package secretstore // import "perkeep.org/pkg/secretstore"

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"perkeep.org/pkg/types/camtypes"
)

const (
	// AnyRevision explicitly opts out of optimistic concurrency checking.
	// Expected revision zero requires absence; a positive revision must match
	// the current, nondeleted entry. An explicit Put can recreate a tombstoned
	// name, but migration can never do so.
	AnyRevision int64 = -1

	MaxNameSize       = 512
	MaxValueSize      = 1 << 20
	MaxBatchSize      = 1024
	MaxEntries        = 10000 // Includes permanent deletion tombstones.
	MaxTotalValueSize = 64 << 20

	applicationID = 0x504b5353 // "PKSS".
	schemaVersion = 1
)

var (
	ErrNotFound        = errors.New("secretstore: not found")
	ErrConflict        = errors.New("secretstore: revision conflict")
	ErrInvalidName     = errors.New("secretstore: invalid name")
	ErrInvalidValue    = errors.New("secretstore: invalid value")
	ErrInvalidRevision = errors.New("secretstore: invalid expected revision")
	ErrInvalidMutation = errors.New("secretstore: invalid or duplicate mutation")
	ErrTooLarge        = errors.New("secretstore: size limit exceeded")
	ErrClosed          = errors.New("secretstore: closed")
	ErrUnsafePath      = errors.New("secretstore: unsafe path or permissions")
	ErrStorage         = errors.New("secretstore: storage failure")
)

// Metadata intentionally has no Value field. List and mutation results are
// safe to serialize without disclosing credential values.
type Metadata = camtypes.SecretMetadata

// Entry contains plaintext and must only be exposed to authorized callers.
type Entry = camtypes.SecretEntry

// PutOptions controls a write. Its zero value is create-if-absent, not an
// unconditional write: callers must explicitly use AnyRevision for that.
type PutOptions struct {
	// ExpectedRevision is AnyRevision for an unconditional write, zero to
	// require absence, or the current positive revision of a live entry.
	// Zero permits recreating a deleted name without resetting its revision.
	ExpectedRevision int64

	// ClearMigration explicitly acknowledges that historical credential
	// exposure has been addressed and clears MigratedFromPermanode.
	// Ordinary writes preserve the warning.
	ClearMigration bool
}

// Mutation is one member of an atomic Apply.
type Mutation struct {
	// Name identifies the credential and must satisfy ValidateName.
	Name string

	// Value is the replacement plaintext. It must be valid UTF-8 and at most
	// MaxValueSize bytes, and must be empty when Delete is true.
	Value string

	// Delete removes the value and records a permanent deletion tombstone.
	// ClearMigration must be false when Delete is true.
	Delete bool

	// ExpectedRevision has the same semantics as PutOptions.ExpectedRevision
	// unless CompareRecord is true.
	ExpectedRevision int64

	// ClearMigration explicitly acknowledges that historical credential
	// exposure has been addressed and clears MigratedFromPermanode.
	// Ordinary writes preserve the warning.
	ClearMigration bool

	// CompareRecord requires ExpectedRevision to match the stored record's
	// revision exactly, including tombstones. Zero then means never present;
	// AnyRevision is invalid. This is for trusted in-process consumers of a
	// GetState snapshot, not the public API.
	CompareRecord bool `json:"-"`
}

// LegacyEntry is a currently stored credential being moved out of a permanode.
// Migrate never overwrites a different value or revives a deleted credential.
type LegacyEntry struct {
	// Name identifies the credential and must satisfy ValidateName.
	Name string

	// Value is the legacy plaintext, at most MaxValueSize bytes of valid UTF-8.
	Value string
}

// Store is safe for concurrent use by goroutines. Separate Open calls to the
// same file also serialize mutations through SQLite transactions.
type Store struct {
	mu     sync.RWMutex // Keeps Close from racing operations.
	closed bool
	db     *sql.DB
}

// Open opens a durable SQLite credential file, never a blob/index database.
// It creates missing directories with mode 0700 and the file with mode 0600.
// Existing regular files are tightened to 0600. The immediate parent directory
// must not be a symlink or writable by other users; Open does not chmod existing
// directories. The caller must protect the rest of the directory hierarchy.
// Symlink/nonregular files and SQLite sidecars are rejected.
//
// On Windows, private protected DACLs replace Unix modes. New directories and
// files grant access only to the current user, SYSTEM, and Administrators.
// Existing directories must already have a private owner and DACL, including
// private inheritable ACEs for SQLite journals; Open never changes their ACLs.
// Existing files must have a trusted owner and receive a private protected DACL.
// Unsupported or unsafe ACLs fail closed.
//
// The filename is always a local filesystem path, not a SQLite URI. Use
// NewMemory, rather than Open(":memory:"), for explicitly ephemeral storage.
func Open(file string) (*Store, error) {
	if file == "" || file == ":memory:" {
		return nil, ErrUnsafePath
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, ErrUnsafePath
	}
	dir := filepath.Dir(abs)
	if err := preparePrivateDirectory(dir); err != nil {
		return nil, ErrUnsafePath
	}
	if err := privateFile(abs, true); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if err := privateFile(abs+suffix, false); err != nil {
			return nil, err
		}
	}
	path := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && !strings.HasPrefix(path, "/") {
		// SQLite's file URI syntax uses /C:/... for Windows drive paths.
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
	s, err := openDB(u.String())
	if err != nil {
		return nil, err
	}
	// Also check any sidecars SQLite created during recovery/initialization.
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		if err := privateFile(abs+suffix, false); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// NewMemory creates an explicitly ephemeral store for tests and standalone
// importer hosts. Production callers must choose Open: there is no automatic
// fallback from a failed durable store to an in-memory one.
func NewMemory() (*Store, error) {
	return openDB(":memory:")
}

// privateFile rejects links/devices and tightens an existing file's mode.
// All path/OS errors deliberately omit filenames and underlying error text.
func privateFile(file string, create bool) error {
	fi, err := os.Lstat(file)
	if os.IsNotExist(err) {
		if !create {
			return nil
		}
		f, err := openPrivateFile(file, os.O_CREATE|os.O_EXCL|os.O_RDWR)
		if os.IsExist(err) {
			return privateFile(file, false)
		}
		if err != nil {
			return ErrUnsafePath
		}
		if err := protectPrivateFile(f); err != nil {
			f.Close()
			return ErrUnsafePath
		}
		if err := f.Close(); err != nil {
			return ErrUnsafePath
		}
		return nil
	}
	if err != nil || !fi.Mode().IsRegular() {
		return ErrUnsafePath
	}
	f, err := openPrivateFile(file, os.O_RDWR)
	if err != nil {
		return ErrUnsafePath
	}
	defer f.Close()
	got, err := f.Stat()
	if err != nil || !os.SameFile(fi, got) {
		return ErrUnsafePath
	}
	if err := protectPrivateFile(f); err != nil {
		return ErrUnsafePath
	}
	return nil
}

func openDB(source string) (*Store, error) {
	// DELETE mode avoids persistent WAL files. SQLite gives its journal the
	// database file's private permissions. EXTRA synchronous also syncs the
	// directory after deleting the journal, making the commit durable even if
	// power fails immediately afterward. secure_delete overwrites freed
	// credential content in the database (not backups or historical copies).
	q := url.Values{
		"_txlock": {"immediate"},
		"_pragma": {
			"busy_timeout(10000)",
			"journal_mode(DELETE)",
			"synchronous(EXTRA)",
			"secure_delete(ON)",
			"max_page_count(32768)", // Bound the database to ~128 MiB.
		},
	}
	db, err := sql.Open("sqlite", source+"?"+q.Encode())
	if err != nil {
		return nil, storageError(context.Background(), "open")
	}
	// One connection also keeps a :memory: database alive and consistent.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(ctx, "initialize")
	}
	defer tx.Rollback()
	var appID, version int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return storageError(ctx, "read database identity")
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return storageError(ctx, "read schema version")
	}
	if appID == 0 && version != 0 ||
		appID != 0 && (appID != applicationID || version != schemaVersion) {
		return storageError(ctx, "unsupported database")
	}
	if appID == 0 {
		// Do not accidentally turn an index or other SQLite database into the
		// secret store. Only an empty, unclaimed database can be initialized.
		var objects int
		if err := tx.QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").
			Scan(&objects); err != nil {
			return storageError(ctx, "check database identity")
		}
		if objects != 0 {
			return storageError(ctx, "unsupported database")
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS credentials (
		name TEXT PRIMARY KEY NOT NULL,
		value TEXT NOT NULL,
		revision INTEGER NOT NULL CHECK (revision > 0),
		migrated INTEGER NOT NULL CHECK (migrated IN (0, 1)),
		updated INTEGER NOT NULL,
		deleted INTEGER NOT NULL CHECK (deleted IN (0, 1))
	) WITHOUT ROWID`); err != nil {
		return storageError(ctx, "create schema")
	}
	for _, stmt := range []string{
		fmt.Sprintf("PRAGMA application_id = %d", applicationID),
		fmt.Sprintf("PRAGMA user_version = %d", schemaVersion),
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return storageError(ctx, "write schema version")
		}
	}
	if err := tx.Commit(); err != nil {
		return storageError(ctx, "commit schema")
	}
	return nil
}

// ValidateName accepts nonempty slash-separated ASCII components containing
// letters, digits, '.', '_', ':', or '-'. Empty, "." and ".." components are
// forbidden. Names are logical identifiers, never filesystem paths.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > MaxNameSize {
		return ErrInvalidName
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return ErrInvalidName
		}
		for i := range len(part) {
			c := part[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
				c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-' {
				continue
			}
			return ErrInvalidName
		}
	}
	return nil
}

// Close closes the store after its outstanding operations finish. It is
// idempotent. Subsequent operations with valid arguments return ErrClosed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.db.Close(); err != nil {
		return storageError(context.Background(), "close")
	}
	return nil
}

// Get returns an entry including its plaintext value, or ErrNotFound.
func (s *Store) Get(ctx context.Context, name string) (Entry, error) {
	entry, deleted, err := s.GetState(ctx, name)
	if err != nil {
		return Entry{}, err
	}
	if deleted || entry.Revision == 0 {
		return Entry{}, ErrNotFound
	}
	return entry, nil
}

// GetState atomically reads a credential's value and stored revision for a
// trusted in-process consumer. Unlike Get, it exposes deletion state so an
// in-flight operation can pin absence without later undoing an explicit delete.
// Use the returned revision with Mutation.CompareRecord, not an ordinary Put.
//
// A never-present name returns Entry with Name set, Revision zero, empty Value,
// deleted false, and no error. A tombstone returns its positive revision and
// metadata, empty Value, deleted true, and no error. A live entry includes its
// plaintext value. This method must not be exposed through the public API.
func (s *Store) GetState(ctx context.Context, name string) (entry Entry, deleted bool, err error) {
	if err := ValidateName(name); err != nil {
		return Entry{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Entry{}, false, ErrClosed
	}
	rec, err := readRecord(ctx, s.db, name)
	if errors.Is(err, ErrNotFound) {
		return Entry{SecretMetadata: Metadata{Name: name}}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	if rec.deleted {
		rec.Value = ""
	}
	return rec.Entry, rec.deleted, nil
}

// List returns live entries' metadata in name order. Values and deletion
// tombstones are never returned.
func (s *Store) List(ctx context.Context) ([]Metadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT name, revision, migrated, updated FROM credentials WHERE deleted = 0 ORDER BY name")
	if err != nil {
		return nil, storageError(ctx, "list")
	}
	defer rows.Close()
	out := make([]Metadata, 0)
	for rows.Next() {
		var m Metadata
		var ns int64
		if err := rows.Scan(&m.Name, &m.Revision, &m.MigratedFromPermanode, &ns); err != nil {
			return nil, storageError(ctx, "read metadata")
		}
		m.Updated = time.Unix(0, ns).UTC()
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(ctx, "list")
	}
	return out, nil
}

// Put creates or replaces one credential. Ordinary updates preserve migration
// provenance. Values must be valid UTF-8 so JSON clients can round-trip them
// exactly. ExpectedRevision has the semantics documented by AnyRevision.
func (s *Store) Put(ctx context.Context, name, value string, opts PutOptions) (Metadata, error) {
	results, err := s.Apply(ctx, []Mutation{{
		Name: name, Value: value, ExpectedRevision: opts.ExpectedRevision,
		ClearMigration: opts.ClearMigration,
	}})
	if err != nil {
		return Metadata{}, err
	}
	return results[0], nil
}

// Delete removes a value and permanently records a tombstone that prevents
// future legacy migration from resurrecting it. Deleting an absent name with
// AnyRevision or zero also writes a tombstone. It does not revoke the remote
// credential or remove historical permanode claims.
func (s *Store) Delete(ctx context.Context, name string, expectedRevision int64) error {
	_, err := s.Apply(ctx, []Mutation{{
		Name: name, Delete: true, ExpectedRevision: expectedRevision,
	}})
	return err
}

// Apply atomically applies distinct-name mutations, checking all revisions
// within the transaction. On any error no mutations commit. Results correspond
// to input order, including metadata for deletions; deleted metadata is not
// subsequently visible through List or Get.
func (s *Store) Apply(ctx context.Context, mutations []Mutation) ([]Metadata, error) {
	if len(mutations) > MaxBatchSize {
		return nil, ErrTooLarge
	}
	seen := make(map[string]bool, len(mutations))
	var total int64
	for _, m := range mutations {
		if err := ValidateName(m.Name); err != nil {
			return nil, err
		}
		if m.ExpectedRevision < AnyRevision || m.CompareRecord && m.ExpectedRevision == AnyRevision {
			return nil, ErrInvalidRevision
		}
		if len(m.Value) > MaxValueSize {
			return nil, ErrTooLarge
		}
		if !utf8.ValidString(m.Value) {
			return nil, ErrInvalidValue
		}
		total += int64(len(m.Value))
		if total > MaxTotalValueSize {
			return nil, ErrTooLarge
		}
		if seen[m.Name] || m.Delete && (m.Value != "" || m.ClearMigration) {
			return nil, ErrInvalidMutation
		}
		seen[m.Name] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, storageError(ctx, "begin mutation")
	}
	defer tx.Rollback()
	results := make([]Metadata, 0, len(mutations))
	now := time.Now().UTC()
	for _, m := range mutations {
		old, err := readRecord(ctx, tx, m.Name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		exists := err == nil
		live := exists && !old.deleted
		conflict := m.ExpectedRevision == 0 && live ||
			m.ExpectedRevision > 0 && (!live || m.ExpectedRevision != old.Revision)
		if m.CompareRecord {
			conflict = m.ExpectedRevision != old.Revision
		}
		if conflict || old.Revision == math.MaxInt64 {
			return nil, ErrConflict
		}
		rec := record{
			Entry: Entry{
				SecretMetadata: Metadata{
					Name: m.Name, Revision: old.Revision + 1, Updated: now,
					MigratedFromPermanode: old.MigratedFromPermanode && !m.ClearMigration,
				},
				Value: m.Value,
			},
			deleted: m.Delete,
		}
		if err := writeRecord(ctx, tx, rec); err != nil {
			return nil, err
		}
		results = append(results, rec.SecretMetadata)
	}
	if err := checkLimits(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, storageError(ctx, "commit mutation")
	}
	return results, nil
}

// Migrate atomically imports current legacy credential values. A missing name
// is inserted with MigratedFromPermanode true. An identical live value is only
// marked if necessary (advancing its revision); an already marked value is a
// no-op. Different live values and all tombstones are left unchanged.
//
// Migration must only run on current credential attributes, not their immutable
// history. It deliberately does not overwrite a local replacement, even if
// cleanup of the legacy attribute failed on a previous attempt.
func (s *Store) Migrate(ctx context.Context, entries []LegacyEntry) error {
	if len(entries) > MaxBatchSize {
		return ErrTooLarge
	}
	seen := make(map[string]bool, len(entries))
	var total int64
	for _, e := range entries {
		if err := ValidateName(e.Name); err != nil {
			return err
		}
		if len(e.Value) > MaxValueSize {
			return ErrTooLarge
		}
		if !utf8.ValidString(e.Value) {
			return ErrInvalidValue
		}
		total += int64(len(e.Value))
		if total > MaxTotalValueSize {
			return ErrTooLarge
		}
		if seen[e.Name] {
			return ErrInvalidMutation
		}
		seen[e.Name] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(ctx, "begin migration")
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, e := range entries {
		old, err := readRecord(ctx, tx, e.Name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && (old.deleted || old.Value != e.Value || old.MigratedFromPermanode) {
			continue
		}
		if old.Revision == math.MaxInt64 {
			return ErrConflict
		}
		rec := record{Entry: Entry{
			SecretMetadata: Metadata{
				Name: e.Name, Revision: old.Revision + 1,
				MigratedFromPermanode: true, Updated: now,
			},
			Value: e.Value,
		}}
		if err := writeRecord(ctx, tx, rec); err != nil {
			return err
		}
	}
	if err := checkLimits(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return storageError(ctx, "commit migration")
	}
	return nil
}

type record struct {
	Entry
	deleted bool
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readRecord(ctx context.Context, q queryer, name string) (record, error) {
	var r record
	var ns int64
	err := q.QueryRowContext(ctx,
		"SELECT name, value, revision, migrated, updated, deleted FROM credentials WHERE name = ?", name).
		Scan(&r.Name, &r.Value, &r.Revision, &r.MigratedFromPermanode, &ns, &r.deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return record{}, ErrNotFound
	}
	if err != nil {
		return record{}, storageError(ctx, "read")
	}
	r.Updated = time.Unix(0, ns).UTC()
	return r, nil
}

func writeRecord(ctx context.Context, tx *sql.Tx, r record) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO credentials
		(name, value, revision, migrated, updated, deleted) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value,
		revision = excluded.revision, migrated = excluded.migrated,
		updated = excluded.updated, deleted = excluded.deleted`,
		r.Name, r.Value, r.Revision, r.MigratedFromPermanode, r.Updated.UnixNano(), r.deleted)
	if err != nil {
		return storageError(ctx, "write")
	}
	return nil
}

func checkLimits(ctx context.Context, tx *sql.Tx) error {
	var count, size int64
	if err := tx.QueryRowContext(ctx,
		"SELECT count(*), coalesce(sum(length(CAST(value AS BLOB))), 0) FROM credentials").
		Scan(&count, &size); err != nil {
		return storageError(ctx, "check size")
	}
	if count > MaxEntries || size > MaxTotalValueSize {
		return ErrTooLarge
	}
	return nil
}

// Never return driver errors: they can include SQL parameters or database
// content. Preserve cancellation without disclosing any plaintext.
func storageError(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w (%s)", ErrStorage, operation)
}
