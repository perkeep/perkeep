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

package strava

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/secretstore"
)

func credentialName(f *coreFixture, attr string) string {
	return "importer/" + f.rc.AccountNode().PermanodeRef().String() + "/" + attr
}

func markFixtureCredentialsMigrated(t *testing.T, f *coreFixture) {
	t.Helper()
	var entries []secretstore.LegacyEntry
	for _, key := range []string{attrRefreshToken, importer.AcctAttrAccessToken, attrTokenClientID} {
		value := f.rc.AccountNode().Attr(key)
		if value == "" {
			continue
		}
		entries = append(entries, secretstore.LegacyEntry{
			Name: credentialName(f, key), Value: value,
		})
	}
	if err := f.hostConfig.Secrets.Migrate(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
}

func assertFixtureCredentialsNotSigned(t *testing.T, f *coreFixture) {
	t.Helper()
	err := blobserver.EnumerateAll(context.Background(), &f.storage.Storage, func(sb blob.SizedRef) error {
		body, _ := f.storage.BlobContents(sb.Ref)
		for _, private := range []string{
			attrRefreshToken, importer.AcctAttrAccessToken, attrTokenClientID,
			"rotated-refresh", "rotated-access", "administrator-replacement", f.clientID,
		} {
			if strings.Contains(body, private) {
				t.Error("credential name or value appeared in immutable blob")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRefreshDiscardsResponseAfterAdministratorCredentialChange(t *testing.T) {
	for _, key := range []string{attrRefreshToken, importer.AcctAttrAccessToken, attrTokenClientID} {
		for _, operation := range []string{"replace", "delete", "same-value-replacement", "delete-absent"} {
			if operation == "delete-absent" && key != importer.AcctAttrAccessToken {
				continue
			}
			t.Run(key+"/"+operation, func(t *testing.T) {
				ctx := context.Background()
				started, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				defer unblock()
				body := coreJSON(t, tokenResponse{
					AccessToken: "rotated-access", RefreshToken: "rotated-refresh",
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				})
				f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path != "/oauth/token" {
						t.Errorf("unexpected request: %s", req.URL.Path)
					}
					if err := req.ParseForm(); err != nil {
						return nil, err
					}
					if req.PostForm.Get("refresh_token") != "test-refresh" {
						t.Error("refresh did not use the captured original credential")
					}
					close(started)
					<-release
					return coreResponse(req, http.StatusOK, body), nil
				}))
				if operation == "delete-absent" {
					// Access has never been present: deleting it while a
					// refresh runs must still invalidate the old snapshot.
					if err := f.rc.AccountNode().SetAttrs(importer.AcctAttrUserID, "7",
						attrRefreshToken, "test-refresh", attrTokenClientID, f.clientID); err != nil {
						t.Fatal(err)
					}
				} else {
					f.configure(t)
				}
				markFixtureCredentialsMigrated(t, f)
				if err := f.rc.AccountNode().SetAttr(attrTokenExpiry, "1"); err != nil {
					t.Fatal(err)
				}
				type result struct {
					client *apiClient
					err    error
				}
				done := make(chan result, 1)
				go func() {
					client, err := (&run{f.rc}).client()
					done <- result{client, err}
				}()
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("refresh request did not start")
				}
				store := f.hostConfig.Secrets
				name := credentialName(f, key)
				entry, err := store.Get(ctx, name)
				if err != nil && !(operation == "delete-absent" && errors.Is(err, secretstore.ErrNotFound)) {
					t.Fatal(err)
				}
				want := "administrator-replacement"
				if operation == "delete" || operation == "delete-absent" {
					// This is the same revision-checked Delete used by the
					// shared admin API and CLI; no importer lock is involved.
					err = store.Delete(ctx, name, entry.Revision)
					want = ""
				} else {
					if operation == "same-value-replacement" {
						want = entry.Value
					}
					_, err = store.Put(ctx, name, want, secretstore.PutOptions{ExpectedRevision: entry.Revision})
				}
				if err != nil {
					t.Fatal(err)
				}
				unblock()
				select {
				case got := <-done:
					if !errors.Is(got.err, secretstore.ErrConflict) || got.client != nil {
						t.Fatalf("stale refresh returned client=%t, err=%v; want conflict without client", got.client != nil, got.err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("refresh did not finish")
				}
				coreAttrs(t, f.rc.AccountNode(), map[string]string{key: want, attrTokenExpiry: "1"})
				for _, other := range []string{attrRefreshToken, importer.AcctAttrAccessToken, attrTokenClientID} {
					if other == key && (operation == "delete" || operation == "delete-absent") {
						if _, err := store.Get(ctx, credentialName(f, other)); !errors.Is(err, secretstore.ErrNotFound) {
							t.Fatal("stale refresh resurrected deleted credential")
						}
						continue
					}
					entry, err := store.Get(ctx, credentialName(f, other))
					if err != nil || !entry.MigratedFromPermanode {
						t.Fatal("credential lost sticky migration warning")
					}
					if other != key && (entry.Value == "rotated-refresh" || entry.Value == "rotated-access") {
						t.Fatal("conflicting refresh partially saved credentials")
					}
				}
				assertFixtureCredentialsNotSigned(t, f)
			})
		}
	}
}

func TestRefreshCredentialsPersistBeforeExpiryClaim(t *testing.T) {
	for _, failExpiry := range []bool{false, true} {
		t.Run(strconv.FormatBool(failExpiry), func(t *testing.T) {
			expiry := time.Now().Add(time.Hour).Unix()
			body := coreJSON(t, tokenResponse{
				AccessToken: "rotated-access", RefreshToken: "rotated-refresh", ExpiresAt: expiry,
			})
			f := newCoreFixture(t, coreTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/oauth/token" {
					t.Errorf("unexpected request: %s", req.URL.Path)
				}
				return coreResponse(req, http.StatusOK, body), nil
			}))
			f.configure(t)
			markFixtureCredentialsMigrated(t, f)
			if err := f.rc.AccountNode().SetAttr(attrTokenExpiry, "1"); err != nil {
				t.Fatal(err)
			}
			if failExpiry {
				f.storage.failNextClaim(attrTokenExpiry)
			}
			client, err := (&run{f.rc}).client()
			wantExpiry := strconv.FormatInt(expiry, 10)
			if failExpiry {
				if !errors.Is(err, errCoreClaimFailure) || client != nil {
					t.Fatalf("expiry claim failure returned client=%t, err=%v", client != nil, err)
				}
				wantExpiry = "1"
			} else if err != nil || client == nil || client.accessToken != "rotated-access" {
				t.Fatalf("normal refresh returned client=%t, err=%v", client != nil, err)
			}
			reloaded, err := f.rc.Host.ObjectFromRef(f.rc.AccountNode().PermanodeRef())
			if err != nil {
				t.Fatal(err)
			}
			coreAttrs(t, reloaded, map[string]string{
				attrRefreshToken: "rotated-refresh", importer.AcctAttrAccessToken: "rotated-access",
				attrTokenClientID: f.clientID, attrTokenExpiry: wantExpiry,
			})
			for _, key := range []string{attrRefreshToken, importer.AcctAttrAccessToken, attrTokenClientID} {
				entry, err := f.hostConfig.Secrets.Get(context.Background(), credentialName(f, key))
				if err != nil || !entry.MigratedFromPermanode {
					t.Fatal("normal refresh cleared migration warning")
				}
			}
			assertFixtureCredentialsNotSigned(t, f)
		})
	}
}
