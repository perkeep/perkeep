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

package livejournal

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"perkeep.org/internal/attrclaims"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

// Attributes of the permanodes of the server's importer accounts (see
// pkg/importer), which LinkImporterAccount makes and updates, as clients
// can't use that package.
const (
	attrImporterType        = "importerType"
	attrImportRoot          = "importRoot"
	nodeTypeImporterAccount = "importerAccount"
	importerType            = "livejournal"

	// AcctAttrUserName is the LiveJournal user of an account, as
	// importer.AcctAttrUserName.
	AcctAttrUserName = "userName"
	// AcctAttrPasswordFile is the path, on the server, of a file with
	// the user's password.
	AcctAttrPasswordFile = "livejournalPasswordFile"
	// AcctAttrTimeZone is the time zone of the journal's entries' times.
	AcctAttrTimeZone = "livejournalTimeZone"
	// AcctAttrServer is the LiveJournal server's URL, if not
	// DefaultBaseURL.
	AcctAttrServer = "livejournalServer"

	// RootPathJournal is the path, in an account's import root, of the
	// journal's set.
	RootPathJournal = "journal"
)

// ImporterAccountStore is a Store that can also search, to find
// importer accounts.
type ImporterAccountStore interface {
	Store
	Query(ctx context.Context, req *search.SearchQuery) (*search.SearchResult, error)
}

// LinkImporterAccount makes sure that st has a LiveJournal importer
// account of user (making one if none), with time zone tz, whose import
// root has journal, the journal's set (see Sync), at RootPathJournal, as
// the importer's runs leave it. So journals that a client keeps are
// listed with the server's importer accounts. An account it makes has
// no password file, which the importer's runs need; it's set up on the
// server. It returns the account's permanode and whether it changed
// anything.
func LinkImporterAccount(ctx context.Context, st ImporterAccountStore, user, tz string, journal blob.Ref) (acct blob.Ref, changed bool, err error) {
	res, err := st.Query(ctx, &search.SearchQuery{
		Expression: fmt.Sprintf("attr:%s:%s attr:%s:%s", nodeattr.Type, nodeTypeImporterAccount, attrImporterType, importerType),
		Limit:      -1,
		Describe:   &search.DescribeRequest{},
	})
	if err != nil {
		return blob.Ref{}, false, err
	}
	var accts []blob.Ref
	attrs := map[blob.Ref]url.Values{}
	for _, b := range res.Blobs {
		db := res.Describe.Meta.Get(b.Blob)
		if db == nil || db.Permanode == nil || db.Permanode.Attr.Get(AcctAttrUserName) != user {
			continue
		}
		accts = append(accts, b.Blob)
		attrs[b.Blob] = db.Permanode.Attr
	}
	now := time.Now()
	upload := func(pn blob.Ref, changes []attrclaims.Change) error {
		if len(changes) == 0 {
			return nil
		}
		changed = true
		claims, err := attrclaims.Sign(ctx, st.Signer(), pn, changes, now)
		if err != nil {
			return err
		}
		for _, c := range claims {
			if err := st.Upload(ctx, c); err != nil {
				return err
			}
		}
		return nil
	}
	newPermanode := func() (blob.Ref, error) {
		signed, err := schema.NewUnsignedPermanode().Sign(ctx, st.Signer())
		if err != nil {
			return blob.Ref{}, err
		}
		return blob.RefFromString(signed), st.Upload(ctx, signed)
	}

	var root blob.Ref
	if len(accts) > 0 {
		// The oldest, if more than one, by ref for a stable choice.
		slices.SortFunc(accts, func(a, b blob.Ref) int { return strings.Compare(a.String(), b.String()) })
		acct = accts[0]
		cur := attrs[acct]
		var changes []attrclaims.Change
		if cur.Get(AcctAttrTimeZone) == "" && tz != "" {
			changes = append(changes, attrclaims.Change{Op: "set", Attr: AcctAttrTimeZone, Value: tz})
		}
		root, _ = blob.Parse(cur.Get(attrImportRoot))
		if !root.Valid() {
			if root, err = newPermanode(); err != nil {
				return blob.Ref{}, false, err
			}
			changes = append(changes, attrclaims.Change{Op: "set", Attr: attrImportRoot, Value: root.String()})
		}
		if err := upload(acct, changes); err != nil {
			return blob.Ref{}, false, err
		}
	} else {
		if acct, err = newPermanode(); err != nil {
			return blob.Ref{}, false, err
		}
		if root, err = newPermanode(); err != nil {
			return blob.Ref{}, false, err
		}
		changes := []attrclaims.Change{
			{Op: "set", Attr: nodeattr.Title, Value: importerType + " account"},
			{Op: "set", Attr: nodeattr.Type, Value: nodeTypeImporterAccount},
			{Op: "set", Attr: attrImporterType, Value: importerType},
			{Op: "set", Attr: attrImportRoot, Value: root.String()},
			{Op: "set", Attr: AcctAttrUserName, Value: user},
		}
		if tz != "" {
			changes = append(changes, attrclaims.Change{Op: "set", Attr: AcctAttrTimeZone, Value: tz})
		}
		if err := upload(acct, changes); err != nil {
			return blob.Ref{}, false, err
		}
	}

	dr, err := st.Describe(ctx, &search.DescribeRequest{BlobRef: root})
	if err != nil {
		return blob.Ref{}, false, err
	}
	cur := url.Values{}
	if db := dr.Meta.Get(root); db != nil && db.Permanode != nil {
		cur = db.Permanode.Attr
	}
	var changes []attrclaims.Change
	for _, kv := range [][2]string{
		{nodeattr.Title, RootTitle(user)},
		{"camliPath:" + RootPathJournal, journal.String()},
	} {
		if cur.Get(kv[0]) != kv[1] {
			changes = append(changes, attrclaims.Change{Op: "set", Attr: kv[0], Value: kv[1]})
		}
	}
	if err := upload(root, changes); err != nil {
		return blob.Ref{}, false, err
	}
	return acct, changed, nil
}

// RootTitle returns the title of the import root of user's account.
func RootTitle(user string) string { return "LiveJournal data for " + user }
