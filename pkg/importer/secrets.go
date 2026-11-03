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
	"slices"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/secretstore"
)

// Credential names are reserved on importer Objects, including objects whose
// node type has not yet been set. This prevents mixed setup writes from ever
// signing a credential. Keep client IDs with their corresponding secrets.
// instanceURL binds Mastodon credentials to their authorized destination and
// must not be signer-editable. Feed URLs remain ordinary account metadata;
// any future feed authentication should use separate credentials.
var secretAttributes = []string{
	"authClientID", "authClientSecret",
	"oauthTempToken", "oauthTempSecret", "oauthAccessToken", "oauthAccessTokenSecret",
	"authToken", "plaidAccountToken", "my_token", "oauthClientID", "oauthClientSecret",
	"stravaRefreshToken", "stravaOAuthState", "stravaTokenClientID", "oauthToken", "instanceURL",
	attrFileSourceOAuthClient, attrFileSourceRefreshToken, attrFileSourceOAuthPending,
}

func isSecretAttribute(key string) bool { return slices.Contains(secretAttributes, key) }

func (o *Object) secretName(key string) string { return "importer/" + o.pn.String() + "/" + key }

// SecretAttr reads a credential from the server-local store. Unlike Attr, it
// can report an unavailable store. There is deliberately no permanode fallback.
// Credentials have one value; SetAttrValues rejects ambiguous multi-values.
func (o *Object) SecretAttr(ctx context.Context, key string) (string, error) {
	if !isSecretAttribute(key) {
		return "", errors.New("importer: attribute is not a credential")
	}
	e, _, err := o.secretEntry(ctx, key)
	return e.Value, err
}

func (o *Object) secretEntry(ctx context.Context, key string) (secretstore.Entry, bool, error) {
	if o.h == nil || o.h.secrets == nil {
		return secretstore.Entry{}, false, o.recordCredentialError(errors.New("importer: credential store unavailable"))
	}
	e, err := o.h.secrets.Get(ctx, o.secretName(key))
	if errors.Is(err, secretstore.ErrNotFound) {
		return secretstore.Entry{}, false, nil
	}
	if err != nil {
		return secretstore.Entry{}, false, o.recordCredentialError(fmt.Errorf("importer: credential store read: %w", err))
	}
	return e, true, nil
}

func (o *Object) recordCredentialError(err error) error {
	o.secretMu.Lock()
	if o.secretErr == nil {
		o.secretErr = err
	}
	o.secretMu.Unlock()
	return err
}

// CredentialError reports a credential read failure encountered by legacy
// Attr/Attrs/ForeachAttr callers, which cannot themselves return an error.
func (o *Object) CredentialError() error {
	o.secretMu.Lock()
	defer o.secretMu.Unlock()
	return o.secretErr
}

// checkCredentials checks current store availability, never a cached snapshot.
// Call it before starting work and after loading an existing object.
func (o *Object) checkCredentials() error {
	for _, key := range secretAttributes {
		if _, _, err := o.secretEntry(context.TODO(), key); err != nil {
			return err
		}
	}
	o.secretMu.Lock()
	o.secretErr = nil
	o.secretMu.Unlock()
	return nil
}

// MigratedFromPermanode reports whether any current credential was previously
// stored in signed permanode claims. Updating a value does not clear this flag;
// only an explicit rotation acknowledgement in the secret-store admin tools can.
func (o *Object) MigratedFromPermanode() bool {
	for _, key := range secretAttributes {
		if e, ok, _ := o.secretEntry(context.TODO(), key); ok && e.MigratedFromPermanode {
			return true
		}
	}
	return false
}

func (im *importer) MigratedFromPermanode() bool {
	if im.host.search == nil {
		return false
	}
	n, err := im.existingNode()
	return err == nil && n != nil && n.MigratedFromPermanode()
}

func (ia *importerAcct) MigratedFromPermanode() bool {
	return ia.acct.MigratedFromPermanode() || ia.im.MigratedFromPermanode()
}

// writeSecretsLocked saves one atomic credential batch. The caller holds o.mu.
// Nil values are explicit deletes; the store retains tombstones so a later
// startup cannot reimport a deleted historical credential.
func (o *Object) writeSecretsLocked(updates map[string][]string, revisions map[string]int64) error {
	if len(updates) == 0 {
		return nil
	}
	if err := o.prepareWriteLocked(context.TODO()); err != nil {
		return err
	}
	var mutations []secretstore.Mutation
	for _, key := range secretAttributes {
		values, ok := updates[key]
		if !ok {
			continue
		}
		m := secretstore.Mutation{Name: o.secretName(key), ExpectedRevision: revisions[key], Delete: len(values) == 0}
		if len(values) != 0 {
			m.Value = values[0]
		}
		mutations = append(mutations, m)
	}
	_, err := o.h.secrets.Apply(context.TODO(), mutations)
	if err != nil {
		return fmt.Errorf("importer: credential store write: %w", err)
	}
	// Even test-created Objects must not retain a credential in the claim map.
	for key := range updates {
		delete(o.attr, key)
	}
	return nil
}

func (o *Object) deleteSecret(key, value string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok, err := o.secretEntry(context.TODO(), key)
	if err != nil {
		return err
	}
	if value != "" && (!ok || value != e.Value) {
		return nil
	}
	var revision int64
	if ok {
		revision = e.Revision
	}
	return o.writeSecretsLocked(map[string][]string{key: nil}, map[string]int64{key: revision})
}

func (o *Object) setSecretValues(key string, values []string) error {
	unique := make(map[string]bool)
	for _, v := range values {
		unique[v] = true
	}
	if len(unique) > 1 {
		return errors.New("importer: credential attributes require a single value")
	}
	if len(values) == 0 {
		return o.DelAttr(key, "")
	}
	return o.SetAttr(key, values[0])
}

// migrateCredentials runs before InitHandler releases any importer work. Query
// all importer node types explicitly, without expression parsing's SkipHidden
// default or registered-importer filtering. Deleted accounts remain permanodes
// under a renamed type and must be scrubbed too.
func (h *Host) migrateCredentials(ctx context.Context) error {
	var constraint *search.Constraint
	for _, typ := range []string{nodeTypeImporter, nodeTypeImporterAccount, nodeTypeImporterAccount + "-deleted"} {
		c := &search.Constraint{Permanode: &search.PermanodeConstraint{Attr: attrNodeType, Value: typ, SkipHidden: false}}
		if constraint == nil {
			constraint = c
		} else {
			constraint = &search.Constraint{Logical: &search.LogicalConstraint{Op: "or", A: constraint, B: c}}
		}
	}
	res, err := h.search.Query(ctx, &search.SearchQuery{Constraint: constraint, Sort: search.Unsorted, Limit: -1})
	if err != nil {
		return fmt.Errorf("importer: discovering legacy credentials: %w", err)
	}
	for _, match := range res.Blobs {
		if err := h.migrateObjectCredentials(ctx, match.Blob); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) migrateObjectCredentials(ctx context.Context, pn blob.Ref) error {
	res, err := h.search.Describe(ctx, &search.DescribeRequest{BlobRef: pn, Depth: 1})
	if err != nil {
		return fmt.Errorf("importer: describing legacy credential node: %w", err)
	}
	db := res.Meta[pn.String()]
	if db == nil || db.Permanode == nil {
		return errors.New("importer: missing legacy credential permanode description")
	}
	var entries []secretstore.LegacyEntry
	var claims []*schema.Builder
	for _, key := range secretAttributes {
		values := db.Permanode.Attr[key]
		if len(values) == 0 {
			continue
		}
		for _, value := range values[1:] {
			if value != values[0] {
				return errors.New("importer: legacy credential has multiple distinct values; resolve before startup")
			}
		}
		entries = append(entries, secretstore.LegacyEntry{Name: "importer/" + pn.String() + "/" + key, Value: values[0]})
		// Empty-value delete-all claims never repeat a historical secret.
		claims = append(claims, schema.NewDelAttributeClaim(blob.Ref{}, key, ""))
	}
	if len(entries) == 0 {
		return nil
	}
	if err := h.secrets.Migrate(ctx, entries); err != nil {
		return fmt.Errorf("importer: saving legacy credentials: %w", err)
	}
	o := &Object{h: h, pn: pn}
	date := time.Now().UTC()
	if !date.After(db.Permanode.ModTime) {
		date = db.Permanode.ModTime.Add(time.Nanosecond)
	}
	if err := o.uploadAttributeClaimsAt(ctx, claims, date); err != nil {
		return fmt.Errorf("importer: removing migrated credential attributes: %w", err)
	}
	return nil
}
