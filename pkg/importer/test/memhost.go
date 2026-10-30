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

package test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

// indexingStorage is memory storage that also indexes what it receives.
type indexingStorage struct {
	*memory.Storage
	ix *index.Index
}

func (s indexingStorage) ReceiveBlob(ctx context.Context, br blob.Ref, r io.Reader) (blob.SizedRef, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return blob.SizedRef{}, err
	}
	sb, err := s.Storage.ReceiveBlob(ctx, br, strings.NewReader(string(b)))
	if err != nil {
		return sb, err
	}
	_, err = s.ix.ReceiveBlob(ctx, br, strings.NewReader(string(b)))
	return sb, err
}

// NewMemoryHost returns an importer host in the test process, unlike
// ImporterTest's: on memory storage with an in-memory index and corpus,
// whose blobs are signed with the test key. hc's Target, BlobSource,
// Signer, and Search are set; its other fields are used as they are,
// with a default BaseURL and Prefix.
func NewMemoryHost(t testing.TB, hc importer.HostConfig) *importer.Host {
	t.Helper()
	ctx := context.Background()
	root, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	entity, err := jsonsign.EntityFromSecring("26F5ABDA", filepath.Join(root, "pkg", "jsonsign", "testdata", "test-secring.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		t.Fatal(err)
	}
	pubRef := blob.RefFromString(pub)
	signer, err := schema.NewSigner(pubRef, strings.NewReader(pub), entity)
	if err != nil {
		t.Fatal(err)
	}
	mem := new(memory.Storage)
	ix := index.NewMemoryIndex()
	t.Cleanup(func() { ix.Close() })
	ix.InitBlobSource(mem)
	corpus, err := ix.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	st := indexingStorage{mem, ix}
	if _, err := st.ReceiveBlob(ctx, pubRef, strings.NewReader(pub)); err != nil {
		t.Fatal(err)
	}
	sh := search.NewHandler(ix, index.NewOwner(signer.KeyIDLong(), pubRef))
	sh.SetCorpus(corpus)

	hc.Target, hc.BlobSource, hc.Signer, hc.Search = st, mem, signer, sh
	if hc.BaseURL == "" {
		hc.BaseURL = "https://perkeep.example"
	}
	if hc.Prefix == "" {
		hc.Prefix = "/importer/"
	}
	h, err := importer.NewHost(hc)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
