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
	"encoding/json"
	"errors"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

// objectClaimReceiver records upload attempts, including failed uploads. The
// callback only examines the incoming JSON: calling Object.Attr here would
// deadlock when the writer correctly holds the object's lock during upload.
type objectClaimReceiver struct {
	memory.Storage
	mu     sync.Mutex
	bodies []string
	err    error
	before func(string)
}

func (r *objectClaimReceiver) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return blob.SizedRef{}, err
	}
	body := string(data)
	r.mu.Lock()
	r.bodies = append(r.bodies, body)
	uploadErr, before := r.err, r.before
	r.mu.Unlock()
	if before != nil {
		before(body)
	}
	if uploadErr != nil {
		return blob.SizedRef{}, uploadErr
	}
	return r.Storage.ReceiveBlob(ctx, br, strings.NewReader(body))
}

func (r *objectClaimReceiver) uploads() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bodies)
}

type objectClaimFixture struct {
	o      *Object
	target *objectClaimReceiver
	pubref blob.Ref
}

func newObjectClaimFixture(t *testing.T, attrs map[string][]string) *objectClaimFixture {
	t.Helper()
	// Use the checked-in key rather than generating one or consulting the
	// user's keyring. Each fixture gets its own entity, including failure tests.
	ent, err := jsonsign.EntityFromSecring("26F5ABDA", "../jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	pubref := blob.RefFromString(pub)
	signer, err := schema.NewSigner(pubref, strings.NewReader(pub), ent)
	if err != nil {
		t.Fatal(err)
	}
	target := new(objectClaimReceiver)
	if _, err := blobserver.ReceiveString(context.Background(), &target.Storage, pub); err != nil {
		t.Fatal(err)
	}
	h, err := NewHost(HostConfig{Target: target, Signer: signer})
	if err != nil {
		t.Fatal(err)
	}
	cache := maps.Clone(attrs)
	for key, values := range cache {
		cache[key] = slices.Clone(values)
	}
	return &objectClaimFixture{
		o:      &Object{h: h, pn: blob.RefFromString("importer object claims test permanode"), attr: cache},
		target: target,
		pubref: pubref,
	}
}

type objectClaimMutation struct {
	typ        schema.ClaimType
	key, value string
}

func (f *objectClaimFixture) wantClaims(t *testing.T, want ...objectClaimMutation) {
	t.Helper()
	bodies := f.target.uploads()
	if len(bodies) != 1 {
		t.Fatalf("uploaded %d blobs; want one claim", len(bodies))
	}
	body := bodies[0]
	vr := jsonsign.NewVerificationRequest(body, &f.target.Storage)
	if _, err := vr.Verify(context.Background()); err != nil {
		t.Fatalf("claim signature verification: %v", err)
	}
	b, err := schema.BlobFromReader(blob.RefFromString(body), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	claim, ok := b.AsClaim()
	if !ok {
		t.Fatal("uploaded blob is not a claim")
	}
	if claim.Signer() != f.pubref || claim.Signature() == "" {
		t.Errorf("missing or incorrect signer/signature: %v", claim)
	}
	if date, err := b.ClaimDate(); err != nil || date.IsZero() {
		t.Errorf("claim date = %v, %v; want a date", date, err)
	}
	ops, err := claim.AttributeClaims()
	if err != nil {
		t.Fatal(err)
	}
	var expected []schema.AttributeClaim
	for _, op := range want {
		expected = append(expected, schema.AttributeClaim{
			Type: op.typ, Permanode: f.o.pn, Attribute: op.key, Value: op.value,
		})
	}
	if !reflect.DeepEqual(ops, expected) {
		t.Errorf("attribute operations = %#v; want %#v", ops, expected)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["permaNode"] != f.o.pn.String() {
		t.Errorf("claim permanode = %v; want %v", envelope["permaNode"], f.o.pn)
	}
	if len(want) == 1 {
		if claim.ClaimType() != want[0].typ {
			t.Errorf("single operation claim type = %v; want %v", claim.ClaimType(), want[0].typ)
		}
		if _, ok := envelope["claims"]; ok {
			t.Error("single operation unnecessarily wrapped in multi claim")
		}
		return
	}
	if claim.ClaimType() != schema.MultiClaim {
		t.Fatalf("claim type = %v; want multi", claim.ClaimType())
	}
	children, ok := envelope["claims"].([]any)
	if !ok || len(children) != len(want) {
		t.Fatalf("multi claim children = %#v; want %d", envelope["claims"], len(want))
	}
	for i, child := range children {
		fields, ok := child.(map[string]any)
		if !ok {
			t.Fatalf("child %d is not an object: %#v", i, child)
		}
		for _, key := range []string{"permaNode", "claimDate", "camliVersion", "camliType", "camliSigner", "camliSig"} {
			if _, ok := fields[key]; ok {
				t.Errorf("child %d contains redundant envelope field %q", i, key)
			}
		}
	}
	// The child operations and inherited target are covered by the signature.
	for _, tampered := range []string{
		strings.Replace(body, f.o.pn.String(), blob.RefFromString("other object").String(), 1),
		strings.Replace(body, `"attribute":`, `"changedAttribute":`, 1),
	} {
		if tampered == body {
			t.Fatal("signature tampering did not change JSON")
		}
		vr := jsonsign.NewVerificationRequest(tampered, &f.target.Storage)
		if _, err := vr.Verify(context.Background()); err == nil {
			t.Error("tampered multi claim passed signature verification")
		}
	}
}

func TestObjectSetAttrsClaims(t *testing.T) {
	for _, method := range []string{"SetAttrs", "SetAttrs2"} {
		t.Run(method, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				args   []string
				want   []objectClaimMutation
				cached map[string][]string
			}{
				{
					name: "multiple changes skip unchanged",
					args: []string{"title", "old", "description", "new description", "title", "new title", "missing", ""},
					want: []objectClaimMutation{
						{schema.SetAttributeClaim, "description", "new description"},
						{schema.SetAttributeClaim, "title", "new title"},
					},
					cached: map[string][]string{"title": {"new title"}, "description": {"new description"}},
				},
				{
					name: "one change",
					args: []string{"title", "old", "description", "new description"},
					want: []objectClaimMutation{{schema.SetAttributeClaim, "description", "new description"}},
					cached: map[string][]string{
						"title": {"old"}, "description": {"new description"},
					},
				},
				{
					name: "repeated key follows preceding operations",
					args: []string{"title", "first", "title", "first", "title", "second", "title", "old"},
					want: []objectClaimMutation{
						{schema.SetAttributeClaim, "title", "first"},
						{schema.SetAttributeClaim, "title", "second"},
						{schema.SetAttributeClaim, "title", "old"},
					},
					cached: map[string][]string{"title": {"old"}},
				},
				{
					name:   "unchanged",
					args:   []string{"title", "old", "missing", ""},
					cached: map[string][]string{"title": {"old"}},
				},
				{
					name:   "empty input",
					cached: map[string][]string{"title": {"old"}},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := newObjectClaimFixture(t, map[string][]string{"title": {"old"}})
					var err error
					if method == "SetAttrs" {
						err = f.o.SetAttrs(tc.args...)
					} else {
						var changes bool
						changes, err = f.o.SetAttrs2(tc.args...)
						if changes != (len(tc.want) > 0) {
							t.Errorf("changes = %v; want %v", changes, len(tc.want) > 0)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(tc.want) > 0 {
						f.wantClaims(t, tc.want...)
					} else if got := len(f.target.uploads()); got != 0 {
						t.Errorf("no-op uploaded %d blobs", got)
					}
					if !reflect.DeepEqual(f.o.attr, tc.cached) {
						t.Errorf("cached attributes = %v; want %v", f.o.attr, tc.cached)
					}
				})
			}
		})
	}
}

func TestObjectSetAttrsOddArguments(t *testing.T) {
	f := newObjectClaimFixture(t, nil)
	defer func() {
		if recover() == nil {
			t.Error("SetAttrs2 with odd argument count did not panic")
		}
		if got := len(f.target.uploads()); got != 0 {
			t.Errorf("invalid argument count uploaded %d blobs", got)
		}
	}()
	f.o.SetAttrs2("title", "value", "unpaired")
}

func TestObjectSetAttrValuesClaims(t *testing.T) {
	for _, tc := range []struct {
		name      string
		old, next []string
		want      []objectClaimMutation
	}{
		{
			name: "additions follow requested order and deduplicate",
			old:  []string{"kept"},
			next: []string{"second", "kept", "first", "second"},
			want: []objectClaimMutation{
				{schema.AddAttributeClaim, "tag", "second"},
				{schema.AddAttributeClaim, "tag", "first"},
			},
		},
		{
			name: "additions precede ordered removals",
			old:  []string{"remove second", "kept", "remove first", "remove second"},
			next: []string{"add second", "kept", "add first"},
			want: []objectClaimMutation{
				{schema.AddAttributeClaim, "tag", "add second"},
				{schema.AddAttributeClaim, "tag", "add first"},
				{schema.DelAttributeClaim, "tag", "remove second"},
				{schema.DelAttributeClaim, "tag", "remove first"},
			},
		},
		{
			name: "clear",
			old:  []string{"second", "first"},
			want: []objectClaimMutation{
				{schema.DelAttributeClaim, "tag", "second"},
				{schema.DelAttributeClaim, "tag", "first"},
			},
		},
		{
			name: "single addition",
			next: []string{"new"},
			want: []objectClaimMutation{{schema.AddAttributeClaim, "tag", "new"}},
		},
		{
			name: "single removal",
			old:  []string{"kept", "removed"},
			next: []string{"kept"},
			want: []objectClaimMutation{{schema.DelAttributeClaim, "tag", "removed"}},
		},
		{
			name: "empty string addition",
			old:  []string{"kept"},
			next: []string{"kept", ""},
			want: []objectClaimMutation{{schema.AddAttributeClaim, "tag", ""}},
		},
		{
			name: "empty string kept during ordinary diff",
			old:  []string{"", "removed"},
			next: []string{"added", ""},
			want: []objectClaimMutation{
				{schema.AddAttributeClaim, "tag", "added"},
				{schema.DelAttributeClaim, "tag", "removed"},
			},
		},
		{
			name: "remove empty string clears before rebuilding",
			old:  []string{"", "kept", "removed"},
			next: []string{"new", "kept", "new"},
			want: []objectClaimMutation{
				{schema.DelAttributeClaim, "tag", ""},
				{schema.AddAttributeClaim, "tag", "new"},
				{schema.AddAttributeClaim, "tag", "kept"},
			},
		},
		{
			name: "clear including empty string",
			old:  []string{"", "other"},
			want: []objectClaimMutation{{schema.DelAttributeClaim, "tag", ""}},
		},
		{
			name: "same set in different order",
			old:  []string{"first", "second"},
			next: []string{"second", "first", "second"},
		},
		{
			name: "empty",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newObjectClaimFixture(t, map[string][]string{"tag": tc.old, "unrelated": {"unchanged"}})
			callerValues := slices.Clone(tc.next)
			if err := f.o.SetAttrValues("tag", callerValues); err != nil {
				t.Fatal(err)
			}
			if len(tc.want) > 0 {
				f.wantClaims(t, tc.want...)
			} else if got := len(f.target.uploads()); got != 0 {
				t.Errorf("unchanged set uploaded %d blobs", got)
			}
			if got := f.o.Attrs("tag"); !slices.Equal(got, tc.next) {
				t.Errorf("cached values = %v; want %v", got, tc.next)
			}
			if got := f.o.Attr("unrelated"); got != "unchanged" {
				t.Errorf("unrelated attribute = %q", got)
			}
			if len(callerValues) > 0 {
				callerValues[0] = "caller changed its slice"
				if got := f.o.Attrs("tag"); !slices.Equal(got, tc.next) {
					t.Errorf("cache aliases caller's values: %v; want %v", got, tc.next)
				}
			}
		})
	}
}

func TestObjectAttributeClaimFailure(t *testing.T) {
	uploadErr := errors.New("test upload failure")
	for _, tc := range []struct {
		name string
		call func(*Object) error
	}{
		{"SetAttrs", func(o *Object) error { return o.SetAttrs("title", "new", "description", "new") }},
		{"SetAttrs2", func(o *Object) error {
			changed, err := o.SetAttrs2("title", "new", "description", "new")
			if !changed {
				t.Error("SetAttrs2 returned changes=false after attempting a changed upload")
			}
			return err
		}},
		{"SetAttrs2 single", func(o *Object) error {
			changed, err := o.SetAttrs2("title", "new")
			if !changed {
				t.Error("SetAttrs2 returned changes=false after attempting a changed upload")
			}
			return err
		}},
		{"SetAttrValues", func(o *Object) error { return o.SetAttrValues("tag", []string{"new", "kept"}) }},
		{"SetAttrValues clear", func(o *Object) error { return o.SetAttrValues("tag", nil) }},
		{"SetAttrValues reset empty", func(o *Object) error { return o.SetAttrValues("empty", []string{"new", "kept"}) }},
		{"SetAttr", func(o *Object) error { return o.SetAttr("title", "new") }},
		{"DelAttr", func(o *Object) error { return o.DelAttr("tag", "removed") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, signingFails := range []bool{false, true} {
				name := "upload"
				if signingFails {
					name = "signing"
				}
				t.Run(name, func(t *testing.T) {
					original := map[string][]string{
						"title": {"old"}, "tag": {"removed", "kept"}, "empty": {"", "kept"},
					}
					f := newObjectClaimFixture(t, original)
					f.target.before = func(string) {
						if f.o.mu.TryRLock() {
							f.o.mu.RUnlock()
							t.Error("object not exclusively locked during upload")
						}
						if !reflect.DeepEqual(f.o.attr, original) {
							t.Errorf("cache changed before upload succeeded: %v", f.o.attr)
						}
					}
					if signingFails {
						// The signer still has a valid public key and entity but
						// cannot create a signature. No storage call should occur.
						f.o.h.signer.Entity().PrivateKey = nil
					} else {
						f.target.err = uploadErr
					}
					err := tc.call(f.o)
					if err == nil || (!signingFails && !errors.Is(err, uploadErr)) {
						t.Errorf("error = %v; want signing/upload failure", err)
					}
					wantUploads := 1
					if signingFails {
						wantUploads = 0
					}
					if got := len(f.target.uploads()); got != wantUploads {
						t.Errorf("upload attempts = %d; want %d", got, wantUploads)
					}
					if !reflect.DeepEqual(f.o.attr, original) {
						t.Errorf("failed mutation changed cache to %v; want %v", f.o.attr, original)
					}
				})
			}
		})
	}
}

func TestObjectSetAttrsConstructorFailure(t *testing.T) {
	original := map[string][]string{"title": {"old"}}
	f := newObjectClaimFixture(t, original)
	changed, err := f.o.SetAttrs2("title", "new", "", "invalid empty attribute")
	if !changed || err == nil {
		t.Errorf("SetAttrs2 = %v, %v; want changes and constructor error", changed, err)
	}
	if got := len(f.target.uploads()); got != 0 {
		t.Errorf("constructor failure uploaded %d blobs", got)
	}
	if !reflect.DeepEqual(f.o.attr, original) {
		t.Errorf("constructor failure changed cache to %v; want %v", f.o.attr, original)
	}
}

func TestObjectAttributeClaimsTooLarge(t *testing.T) {
	large := strings.Repeat("x", schema.MaxSchemaBlobSize)
	for _, tc := range []struct {
		name string
		call func(*Object) error
	}{
		{"SetAttrs2", func(o *Object) error {
			changed, err := o.SetAttrs2("title", large, "description", "new")
			if !changed {
				t.Error("oversized SetAttrs2 returned changes=false")
			}
			return err
		}},
		{"SetAttrValues", func(o *Object) error { return o.SetAttrValues("tag", []string{large, "new"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := map[string][]string{"title": {"old"}, "tag": {"old"}}
			f := newObjectClaimFixture(t, original)
			if err := tc.call(f.o); err == nil {
				t.Error("oversized mutation succeeded")
			}
			if got := len(f.target.uploads()); got != 0 {
				t.Errorf("oversized mutation attempted %d uploads", got)
			}
			if !reflect.DeepEqual(f.o.attr, original) {
				t.Errorf("oversized mutation changed cache to %v; want %v", f.o.attr, original)
			}
		})
	}
}

func TestObjectSingleAttributeClaims(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Object) error
		want objectClaimMutation
	}{
		{"set", func(o *Object) error { return o.SetAttr("tag", "new") }, objectClaimMutation{schema.SetAttributeClaim, "tag", "new"}},
		{"delete one", func(o *Object) error { return o.DelAttr("tag", "first") }, objectClaimMutation{schema.DelAttributeClaim, "tag", "first"}},
		{"delete all", func(o *Object) error { return o.DelAttr("tag", "") }, objectClaimMutation{schema.DelAttributeClaim, "tag", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newObjectClaimFixture(t, map[string][]string{"tag": {"first", "second"}})
			// Uploads of all mutators must occur while the object is locked.
			f.target.before = func(string) {
				if f.o.mu.TryRLock() {
					f.o.mu.RUnlock()
					t.Error("object not exclusively locked during upload")
				}
			}
			if err := tc.call(f.o); err != nil {
				t.Fatal(err)
			}
			f.wantClaims(t, tc.want)
		})
	}
}

func TestObjectBatchSerializesSetAttr(t *testing.T) {
	f := newObjectClaimFixture(t, map[string][]string{"title": {"old"}, "description": {"old"}})
	entered, release := make(chan struct{}), make(chan struct{})
	var block sync.Once
	f.target.before = func(string) {
		block.Do(func() {
			if f.o.mu.TryRLock() {
				f.o.mu.RUnlock()
				t.Error("batch did not hold exclusive object lock during upload")
			}
			close(entered)
			<-release
		})
	}
	batchDone := make(chan error, 1)
	go func() { batchDone <- f.o.SetAttrs("title", "new", "description", "new") }()
	<-entered
	singleDone := make(chan struct{})
	var singleErr error
	started := make(chan struct{}, 2)
	go func() {
		started <- struct{}{}
		// This initially matches the cache. Comparison must wait until
		// after the batch has committed, or this write is silently lost.
		singleErr = f.o.SetAttr("title", "old")
		close(singleDone)
	}()
	readDone := make(chan struct{})
	var attrs map[string][]string
	go func() {
		started <- struct{}{}
		attrs = make(map[string][]string)
		f.o.ForeachAttr(func(key, value string) { attrs[key] = append(attrs[key], value) })
		close(readDone)
	}()
	<-started
	<-started
	select {
	case <-singleDone:
		t.Errorf("SetAttr completed while batch upload was blocked: %v", singleErr)
	default:
	}
	select {
	case <-readDone:
		t.Errorf("reader observed cache during blocked batch: %v", attrs)
	default:
	}
	close(release)
	if err := <-batchDone; err != nil {
		t.Fatal(err)
	}
	<-singleDone
	if singleErr != nil {
		t.Fatal(singleErr)
	}
	<-readDone
	if attrs["description"][0] != "new" {
		t.Errorf("reader saw uncommitted batch attributes: %v", attrs)
	}
	if got := f.o.Attr("title"); got != "old" {
		t.Errorf("serialized SetAttr lost: title = %q; want old", got)
	}
	if got := len(f.target.uploads()); got != 2 {
		t.Errorf("uploaded %d blobs; want batch and ordinary SetAttr", got)
	}
}

func TestObjectClaimsIndexRoundTrip(t *testing.T) {
	f := newObjectClaimFixture(t, nil)
	ctx := context.Background()
	ix := index.NewMemoryIndex()
	t.Cleanup(func() {
		if err := ix.Close(); err != nil {
			t.Error(err)
		}
	})
	ix.InitBlobSource(&f.target.Storage)
	corpus, err := ix.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := search.NewHandler(ix, index.NewOwner(f.o.h.signer.KeyIDLong(), f.pubref))
	h.SetCorpus(corpus)
	f.o.h.search = h
	// Index the same signed bytes that the Host uploads, not a reconstructed
	// claim or a test-only interpretation of the operations.
	f.target.before = func(body string) {
		if _, err := blobserver.ReceiveString(ctx, ix, body); err != nil {
			t.Errorf("indexing uploaded blob: %v", err)
		}
	}
	o, err := f.o.h.NewObject()
	if err != nil {
		t.Fatal(err)
	}
	if err := o.SetAttrs("title", "first", "description", "description", "title", "final"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetAttrValues("tag", []string{"", "kept", "removed"}); err != nil {
		t.Fatal(err)
	}
	if err := o.SetAttrValues("tag", []string{"new", "kept"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := o.h.ObjectFromRef(o.pn)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"title": {"final"}, "description": {"description"}, "tag": {"new", "kept"},
	}
	if !reflect.DeepEqual(reloaded.attr, want) {
		t.Errorf("indexed attributes = %v; want %v", reloaded.attr, want)
	}
}
