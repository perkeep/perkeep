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

package importer

import (
	"context"
	"errors"
	"fmt"

	"perkeep.org/pkg/secretstore"
)

// CredentialSnapshot pins credential values and revisions before an operation
// such as a remote OAuth refresh. Its contents are private so callers cannot
// accidentally replace the pinned revisions with newer ones before saving.
// A snapshot contains plaintext credentials and must not be logged.
type CredentialSnapshot struct {
	object  *Object
	entries map[string]secretstore.Entry
}

// Value returns a captured credential, or an empty string if it was absent.
func (s *CredentialSnapshot) Value(key string) string {
	return s.entries[key].Value
}

// CredentialSnapshot reads only server-local credential attributes. There is no
// fallback to historical permanode claims, even when the store is unavailable.
// All captured keys must be supplied to SetSecretAttrsIfRevision: they form one
// optimistic transaction, including dependencies whose values do not change.
func (o *Object) CredentialSnapshot(ctx context.Context, keys ...string) (*CredentialSnapshot, error) {
	if len(keys) == 0 {
		return nil, errors.New("importer: empty credential snapshot")
	}
	for _, key := range keys {
		if !isSecretAttribute(key) {
			return nil, errors.New("importer: snapshot attribute is not a credential")
		}
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.h == nil || o.h.secrets == nil {
		return nil, o.recordCredentialError(errors.New("importer: credential store unavailable"))
	}
	s := &CredentialSnapshot{object: o, entries: make(map[string]secretstore.Entry)}
	for _, key := range keys {
		if _, ok := s.entries[key]; ok {
			continue
		}
		entry, _, err := o.h.secrets.GetState(ctx, o.secretName(key))
		if err != nil {
			return nil, o.recordCredentialError(fmt.Errorf("importer: credential snapshot read: %w", err))
		}
		// Never-present credentials have revision zero; deleted credentials
		// retain their tombstone's revision. Both have an empty value.
		s.entries[key] = entry
	}
	return s, nil
}

// SetSecretAttrsIfRevision atomically replaces all credentials in snapshot only
// if their revisions still match. Capture the snapshot BEFORE remote work, not
// when saving the response. A concurrent administrator replacement or deletion
// causes an error wrapping secretstore.ErrConflict; discard the remote response
// rather than retrying it against freshly read revisions.
//
// Every captured key must occur exactly once in keyval, and no other keys are
// allowed. An empty value deletes the credential. Migration warnings remain
// sticky. No credential claims are signed, and no public metadata is changed.
func (o *Object) SetSecretAttrsIfRevision(ctx context.Context, snapshot *CredentialSnapshot, keyval ...string) error {
	if snapshot == nil || snapshot.object != o || len(snapshot.entries) == 0 ||
		len(keyval) != 2*len(snapshot.entries) {
		return errors.New("importer: invalid credential snapshot update")
	}
	seen := make(map[string]bool)
	mutations := make([]secretstore.Mutation, 0, len(snapshot.entries))
	for i := 0; i < len(keyval); i += 2 {
		key, value := keyval[i], keyval[i+1]
		entry, ok := snapshot.entries[key]
		if !ok || seen[key] || !isSecretAttribute(key) {
			return errors.New("importer: invalid credential snapshot update")
		}
		seen[key] = true
		mutations = append(mutations, secretstore.Mutation{
			Name: o.secretName(key), Value: value, Delete: value == "",
			ExpectedRevision: entry.Revision, CompareRecord: true,
		})
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.h == nil || o.h.secrets == nil {
		return o.recordCredentialError(errors.New("importer: credential store unavailable"))
	}
	if err := o.prepareWriteLocked(ctx); err != nil {
		return err
	}
	if _, err := o.h.secrets.Apply(ctx, mutations); err != nil {
		return fmt.Errorf("importer: credential snapshot write: %w", err)
	}
	for key := range snapshot.entries {
		delete(o.attr, key)
	}
	return nil
}
