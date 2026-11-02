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

package main

// Permanodes of uploaded files.
//
// The server's web UI shows permanodes, not bare files, so an uploaded
// file needs one, with a camliContent claim naming it. The core makes it
// as pk-put -filenodes, the takeout importer, and the Immich handler do,
// so they make the same permanode for the same contents: a planned
// permanode keyed by the contents' whole-file ref, signed at the Unix
// epoch, whose claim is dated by the file's time. The server signs them,
// with the access key's Make claims permission.
//
// An item counts as on the server only once its contents have a
// permanode, of any kind, as the server's contentpermanodes search
// reports. The core makes one only for contents without.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
)

// plannedPermanodeTime is when planned permanodes are signed.
var plannedPermanodeTime = time.Unix(0, 0)

// contentPermanodesBatch is how many whole-file refs are asked about per
// contentpermanodes request, keeping its URL to a few kilobytes.
const contentPermanodesBatch = 100

// contentPermanodes returns which of the whole-file refs have permanodes
// on the server.
func (ac *accountConn) contentPermanodes(ctx context.Context, refs []string) (map[string]bool, error) {
	sr, err := ac.c.SearchRoot()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for i := 0; i < len(refs); i += contentPermanodesBatch {
		q := url.Values{"wholedigest": refs[i:min(i+contentPermanodesBatch, len(refs))]}
		var res struct {
			Permanodes map[string][]string `json:"permanodes"`
			Error      string              `json:"error"`
		}
		if err := ac.c.GetJSON(ctx, sr+"camli/search/contentpermanodes?"+q.Encode(), &res); err != nil {
			return nil, err
		}
		if res.Error != "" {
			return nil, errors.New(res.Error)
		}
		if res.Permanodes == nil {
			return nil, errors.New("the server doesn't support finding files' permanodes; update perkeepd")
		}
		for ref, pns := range res.Permanodes {
			if len(pns) > 0 {
				have[ref] = true
			}
		}
	}
	return have, nil
}

// ensurePermanode makes a permanode for the uploaded file fileRef, with
// the whole-file refs refs, unless its contents already have one. Its
// camliContent claim is dated claimTime, or now if that's zero.
func (ac *accountConn) ensurePermanode(ctx context.Context, refs []blob.Ref, fileRef blob.Ref, claimTime time.Time) error {
	var refStrs []string
	var key blob.Ref
	defaultHash := blob.RefFromHash(blob.NewHash()).HashName()
	for _, br := range refs {
		refStrs = append(refStrs, br.String())
		if br.HashName() == defaultHash {
			key = br
		}
	}
	if !key.Valid() {
		return fmt.Errorf("no %s whole-file ref to key a permanode with", defaultHash)
	}
	// Files with the same contents may be uploaded at once; make their
	// permanode once.
	mu, _ := ac.permanodeMu.LoadOrStore(key.String(), new(sync.Mutex))
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if slices.ContainsFunc(refStrs, func(r string) bool { have, _ := ac.have.get(r); return have }) {
		return nil
	}
	have, err := ac.contentPermanodes(ctx, refStrs)
	if err != nil {
		return err
	}
	if len(have) > 0 {
		ac.noteHave(refStrs)
		return nil
	}
	if claimTime.IsZero() {
		claimTime = time.Now()
	}
	pn, err := ac.sign(ctx, schema.NewPlannedPermanode(key.String()), plannedPermanodeTime)
	if err != nil {
		return fmt.Errorf("signing permanode: %w", err)
	}
	claimBB := schema.NewSetAttributeClaim(blob.RefFromString(pn), "camliContent", fileRef.String())
	claim, err := ac.sign(ctx, claimBB, claimTime)
	if err != nil {
		return fmt.Errorf("signing camliContent claim: %w", err)
	}
	// The claim first, so that once the permanode exists, so does its
	// claim. The index doesn't need the permanode first.
	for _, s := range []string{claim, pn} {
		if _, err := ac.c.Upload(ctx, client.NewUploadHandleFromString(s)); err != nil {
			return err
		}
	}
	ac.noteHave(refStrs)
	return nil
}

// noteHave notes that the server has a permanode of the contents with
// the whole-file refs.
func (ac *accountConn) noteHave(refs []string) {
	for _, r := range refs {
		ac.have.put(r, true)
	}
}

// sign has the server sign bb as dated, and as if at, time t, as
// schema.Builder.SignAt does, so the same blob signed the same way here
// and by a client with the key is the same.
func (ac *accountConn) sign(ctx context.Context, bb *schema.Builder, t time.Time) (string, error) {
	pub, err := ac.c.ServerPublicKeyBlobRef()
	if err != nil {
		return "", err
	}
	bb.SetSigner(pub)
	bb.SetClaimDate(t)
	unsigned, err := bb.JSON()
	if err != nil {
		return "", err
	}
	form := url.Values{
		"json":          {unsigned},
		"signatureTime": {t.UTC().Format(time.RFC3339Nano)},
	}
	signed, err := ac.c.Sign(ctx, ac.base, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	return string(signed), nil
}
