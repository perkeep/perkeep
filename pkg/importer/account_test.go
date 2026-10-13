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
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/search"
)

type concurrentAccountDescriber struct {
	search.QueryDescriber // Query is not used by account loading.
	account, root         blob.Ref
	entered               chan struct{}
	release               chan struct{}
}

func (s *concurrentAccountDescriber) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	var attrs url.Values
	switch req.BlobRef {
	case s.account:
		// Keep both callers outside the cache until each has started loading
		// its own account Object. No scheduling delays are needed.
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		attrs = url.Values{
			attrNodeType:     {nodeTypeImporterAccount},
			attrImporterType: {"dummy1"},
			attrImportRoot:   {s.root.String()},
			"oauthTestState": {"pending-nonce"},
		}
	case s.root:
		attrs = url.Values{"title": {"Existing import root"}}
	default:
		return nil, fmt.Errorf("unexpected Describe blobref %v", req.BlobRef)
	}
	// Each response owns a fresh attribute map and value slices, as real
	// Describe responses do; sharing fixture maps would hide the regression.
	return &search.DescribeResponse{Meta: search.MetaMap{
		req.BlobRef.String(): {
			BlobRef: req.BlobRef, CamliType: "permanode",
			Permanode: &search.DescribedPermanode{Attr: attrs},
		},
	}}, nil
}

func TestConcurrentAccountColdLoad(t *testing.T) {
	describer := &concurrentAccountDescriber{
		account: blob.RefFromString("existing importer account"),
		root:    blob.RefFromString("existing import root"),
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(describer.release) })
	defer release()
	host, err := NewHost(HostConfig{Search: describer})
	if err != nil {
		t.Fatal(err)
	}
	im := host.imp["dummy1"]
	if im == nil {
		t.Fatal("dummy1 importer is not registered")
	}
	type result struct {
		account *importerAcct
		err     error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			ia, err := im.account(describer.account)
			results <- result{ia, err}
		}()
	}
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for range 2 {
		select {
		case <-describer.entered:
		case <-timeout.C:
			t.Fatal("both cold account loads did not reach Describe")
		}
	}
	release()
	var accounts [2]*importerAcct
	for i := range accounts {
		select {
		case res := <-results:
			if res.err != nil {
				t.Fatal(res.err)
			}
			accounts[i] = res.account
		case <-timeout.C:
			t.Fatal("concurrent account loads did not finish")
		}
	}
	first, second := accounts[0], accounts[1]
	if first != second {
		t.Errorf("concurrent loads returned different importerAcct instances: %p and %p", first, second)
	}
	if first.acct != second.acct || first.root != second.root {
		t.Errorf("concurrent loads did not share account and root Objects")
	}
	im.acctmu.Lock()
	cached := im.acct[describer.account]
	im.acctmu.Unlock()
	if cached != first || cached != second {
		t.Errorf("cached account %p differs from returned accounts %p and %p", cached, first, second)
	}
	if got := second.acct.Attr("oauthTestState"); got != "pending-nonce" {
		t.Fatalf("pending OAuth state = %q; want pending-nonce", got)
	}
	// Simulate nonce consumption without a signer or upload target. Both
	// callbacks must observe the same in-memory account state after loading.
	first.acct.mu.Lock()
	delete(first.acct.attr, "oauthTestState")
	first.acct.mu.Unlock()
	if got := second.acct.Attr("oauthTestState"); got != "" {
		t.Errorf("second account Object retained consumed OAuth state %q", got)
	}
}
