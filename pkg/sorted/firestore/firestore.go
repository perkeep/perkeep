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

// Package firestore provides an implementation of sorted.KeyValue on top
// of Google Cloud Firestore (in native mode).
//
// Each key/value pair is one document in a single collection: the key is
// the document ID and the value is the document's "Value" string field.
// Keys enumerate in Firestore's document ID order, which is the byte order
// of the IDs, so Find behaves like the file based implementations.
//
// The JSON config is:
//
//	{
//	    "type": "firestore",
//	    "project": "my-gcp-project",   // optional; see below
//	    "database": "(default)",       // optional; the Firestore database ID
//	    "collection": "pk_index"       // required; must not be shared
//	}
//
// If project is empty, the GOOGLE_CLOUD_PROJECT environment variable is
// used, and failing that the project of the Application Default
// Credentials. Credentials always come from the Application Default
// Credentials (GOOGLE_APPLICATION_CREDENTIALS, the metadata server, etc).
// Setting FIRESTORE_EMULATOR_HOST makes the client talk to the emulator.
//
// Each store needs its own collection: Wipe deletes every document in the
// collection, so two stores sharing one would wipe each other.
//
// Firestore document IDs cannot be empty, cannot contain a slash, cannot
// be "." or "..", and cannot be of the form "__name__". Keys that would be
// invalid IDs are rejected by Set and are never found by Get.
package firestore // import "perkeep.org/pkg/sorted/firestore"

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"go4.org/jsonconfig"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"perkeep.org/pkg/sorted"
)

func init() {
	sorted.RegisterKeyValue("firestore", newKeyValueFromJSONConfig)
}

// document is the shape of every document in the collection. The field
// name was chosen by the first version of this package and must not
// change: existing collections use it.
type document struct {
	Value string `firestore:"Value"`
}

var (
	_ sorted.Wiper = (*keyValue)(nil)

	errInvalidKey = errors.New("firestore: key is not a valid document ID")
)

// maxIteratorResumes bounds how many times in a row an iterator reopens
// its query stream after a transient error, before giving up.
const maxIteratorResumes = 5

// newKeyValueFromJSONConfig returns a sorted.KeyValue on top of the
// Firestore collection described by cfg.
func newKeyValueFromJSONConfig(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
	var (
		project    = cfg.OptionalString("project", "")
		database   = cfg.OptionalString("database", firestore.DefaultDatabaseID)
		collection = cfg.RequiredString("collection")
	)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if strings.Contains(collection, "/") {
		return nil, fmt.Errorf("firestore: collection %q must be a top-level collection ID, not a path", collection)
	}
	if project == "" {
		project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if project == "" {
		project = firestore.DetectProjectID
	}
	ctx := context.Background()
	client, err := firestore.NewClientWithDatabase(ctx, project, database)
	if err != nil {
		return nil, fmt.Errorf("firestore: creating client: %w", err)
	}
	return &keyValue{
		client: client,
		coll:   client.Collection(collection),
	}, nil
}

type keyValue struct {
	client *firestore.Client
	coll   *firestore.CollectionRef
}

// validKey reports whether key can be a Firestore document ID.
func validKey(key string) bool {
	switch {
	case key == "", key == ".", key == "..":
		return false
	case strings.Contains(key, "/"):
		return false
	case len(key) >= 4 && strings.HasPrefix(key, "__") && strings.HasSuffix(key, "__"):
		return false
	}
	return true
}

func (kv *keyValue) Get(key string) (string, error) {
	if !validKey(key) {
		return "", sorted.ErrNotFound
	}
	snap, err := kv.coll.Doc(key).Get(context.Background())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", sorted.ErrNotFound
		}
		return "", fmt.Errorf("firestore: get %q: %w", key, err)
	}
	var doc document
	if err := snap.DataTo(&doc); err != nil {
		return "", fmt.Errorf("firestore: decoding %q: %w", key, err)
	}
	return doc.Value, nil
}

func (kv *keyValue) Set(key, value string) error {
	if err := sorted.CheckSizes(key, value); err != nil {
		// Like the other implementations, skip oversized pairs
		// (CheckSizes logged them) instead of failing.
		return nil
	}
	if !validKey(key) {
		return fmt.Errorf("%w: %q", errInvalidKey, key)
	}
	if _, err := kv.coll.Doc(key).Set(context.Background(), document{Value: value}); err != nil {
		return fmt.Errorf("firestore: set %q: %w", key, err)
	}
	return nil
}

func (kv *keyValue) Delete(key string) error {
	if !validKey(key) {
		// Such a key can't exist, and deleting a missing key is not an error.
		return nil
	}
	if _, err := kv.coll.Doc(key).Delete(context.Background()); err != nil {
		return fmt.Errorf("firestore: delete %q: %w", key, err)
	}
	return nil
}

func (kv *keyValue) BeginBatch() sorted.BatchMutation {
	return sorted.NewBatchMutation()
}

// CommitBatch applies the batch with a BulkWriter. The writes are not
// atomic as a whole, but every write either completes or CommitBatch
// returns an error.
func (kv *keyValue) CommitBatch(bm sorted.BatchMutation) error {
	b, ok := bm.(interface{ Mutations() []sorted.Mutation })
	if !ok {
		return errors.New("firestore: invalid batch type")
	}
	// A BulkWriter rejects a second write to the same document, so only
	// keep the last mutation of each key. The batch's final state is the
	// same, and the writes are independent anyway.
	var (
		order []string
		last  = map[string]sorted.Mutation{}
	)
	for _, m := range b.Mutations() {
		key := m.Key()
		if !m.IsDelete() {
			if err := sorted.CheckSizes(key, m.Value()); err != nil {
				continue
			}
		}
		if !validKey(key) {
			if m.IsDelete() {
				continue
			}
			return fmt.Errorf("%w: %q", errInvalidKey, key)
		}
		if _, dup := last[key]; !dup {
			order = append(order, key)
		}
		last[key] = m
	}
	if len(order) == 0 {
		return nil
	}

	bw := kv.client.BulkWriter(context.Background())
	jobs := make([]*firestore.BulkWriterJob, 0, len(order))
	for _, key := range order {
		m := last[key]
		var (
			job *firestore.BulkWriterJob
			err error
		)
		if m.IsDelete() {
			job, err = bw.Delete(kv.coll.Doc(key))
		} else {
			job, err = bw.Set(kv.coll.Doc(key), document{Value: m.Value()})
		}
		if err != nil {
			bw.End()
			return fmt.Errorf("firestore: queuing write of %q: %w", key, err)
		}
		jobs = append(jobs, job)
	}
	bw.End()
	var errs []error
	for i, job := range jobs {
		if _, err := job.Results(); err != nil {
			errs = append(errs, fmt.Errorf("firestore: writing %q: %w", order[i], err))
		}
	}
	return errors.Join(errs...)
}

func (kv *keyValue) Find(start, end string) sorted.Iterator {
	q := kv.coll.OrderBy(firestore.DocumentID, firestore.Asc)
	if start != "" {
		q = q.Where(firestore.DocumentID, ">=", kv.coll.Doc(start))
	}
	if end != "" {
		q = q.Where(firestore.DocumentID, "<", kv.coll.Doc(end))
	}
	it := &iter{coll: kv.coll, q: q}
	it.docs = q.Documents(context.Background())
	return it
}

func (kv *keyValue) Close() error {
	return kv.client.Close()
}

// Wipe deletes every document of the collection.
func (kv *keyValue) Wipe() error {
	ctx := context.Background()
	for {
		// Fetch only the IDs, in pages, and delete each page in bulk.
		// Select with no field returns documents without any data.
		docs := kv.coll.Select().Limit(2000).Documents(ctx)
		snaps, err := docs.GetAll()
		if err != nil {
			return fmt.Errorf("firestore: listing documents to wipe: %w", err)
		}
		if len(snaps) == 0 {
			return nil
		}
		bw := kv.client.BulkWriter(ctx)
		jobs := make([]*firestore.BulkWriterJob, 0, len(snaps))
		for _, snap := range snaps {
			job, err := bw.Delete(snap.Ref)
			if err != nil {
				bw.End()
				return fmt.Errorf("firestore: queuing delete of %q: %w", snap.Ref.ID, err)
			}
			jobs = append(jobs, job)
		}
		bw.End()
		for i, job := range jobs {
			if _, err := job.Results(); err != nil {
				return fmt.Errorf("firestore: deleting %q: %w", snaps[i].Ref.ID, err)
			}
		}
	}
}

// iter streams the documents of a query. A query stream can be cut by a
// transient error, so iter reopens the query after the last key it
// returned, a bounded number of times in a row.
type iter struct {
	coll *firestore.CollectionRef
	q    firestore.Query
	docs *firestore.DocumentIterator

	key, value string
	started    bool // whether a key was returned, so key can be a cursor
	resumes    int
	err        error
	closed     bool
}

func (it *iter) Next() bool {
	if it.closed || it.err != nil {
		return false
	}
	for {
		snap, err := it.docs.Next()
		if err == nil {
			var doc document
			if err := snap.DataTo(&doc); err != nil {
				it.err = fmt.Errorf("firestore: decoding %q: %w", snap.Ref.ID, err)
				return false
			}
			it.key, it.value = snap.Ref.ID, doc.Value
			it.started = true
			it.resumes = 0
			return true
		}
		if errors.Is(err, iterator.Done) {
			return false
		}
		if !it.started || it.resumes >= maxIteratorResumes || !retryable(err) {
			it.err = fmt.Errorf("firestore: iterating: %w", err)
			return false
		}
		it.resumes++
		log.Printf("firestore: query stream failed after %q (resume %d/%d): %v", it.key, it.resumes, maxIteratorResumes, err)
		time.Sleep(time.Duration(it.resumes) * 500 * time.Millisecond)
		it.docs.Stop()
		it.docs = it.q.StartAfter(it.coll.Doc(it.key)).Documents(context.Background())
	}
}

// retryable reports whether err is a transient gRPC failure after which
// a query stream is worth reopening.
func retryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Internal, codes.ResourceExhausted, codes.Aborted:
		return true
	}
	return false
}

func (it *iter) Key() string        { return it.key }
func (it *iter) KeyBytes() []byte   { return []byte(it.key) }
func (it *iter) Value() string      { return it.value }
func (it *iter) ValueBytes() []byte { return []byte(it.value) }

func (it *iter) Close() error {
	if !it.closed {
		it.closed = true
		it.docs.Stop()
	}
	return it.err
}
