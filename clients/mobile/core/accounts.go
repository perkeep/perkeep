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

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// An Account is a Perkeep server the app backs up to, and which photos to
// back up there. Its password, if any, is kept by the native app in the
// platform's secure storage, not here.
type Account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Server   string `json:"server"` // base URL
	Username string `json:"username"`

	// Include and Exclude select which photo albums to back up: photos
	// in any included album, except those in any excluded album.
	Include []Album `json:"include"`
	Exclude []Album `json:"exclude"`
}

// An Album is a photo album on the device, as reported by the native app,
// or a reference to one in an Account's Include or Exclude list.
//
// A reference matches the device's Camera Roll if CameraRoll is set,
// otherwise the album with the same ID if ID is set, otherwise any album
// with the same title. Title-only references let defaults like "exclude
// WhatsApp" apply to albums that don't exist yet.
type Album struct {
	ID         string `json:"id"` // platform-specific; empty in title-only references
	Title      string `json:"title"`
	CameraRoll bool   `json:"cameraRoll"`
}

func (ref Album) matches(a Album) bool {
	switch {
	case ref.CameraRoll:
		return a.CameraRoll
	case ref.ID != "":
		return ref.ID == a.ID
	default:
		return ref.Title != "" && strings.EqualFold(ref.Title, a.Title)
	}
}

// AlbumState is whether an album's photos are backed up.
type AlbumState string

const (
	AlbumInclude AlbumState = "include"
	AlbumExclude AlbumState = "exclude"
	AlbumNone    AlbumState = "none"
)

// newAccountAlbums returns the album selection for new accounts.
func newAccountAlbums() (include, exclude []Album) {
	return []Album{{Title: "Camera Roll", CameraRoll: true}},
		[]Album{{Title: "WhatsApp"}}
}

// albumState returns whether a's photos are backed up for acct. If both
// an include and an exclude reference match, exclude wins.
func (acct *Account) albumState(a Album) AlbumState {
	match := func(ref Album) bool { return ref.matches(a) }
	switch {
	case slices.ContainsFunc(acct.Exclude, match):
		return AlbumExclude
	case slices.ContainsFunc(acct.Include, match):
		return AlbumInclude
	}
	return AlbumNone
}

// setAlbumState replaces any references matching a with one setting its
// state.
func (acct *Account) setAlbumState(a Album, state AlbumState) error {
	ref := Album{ID: a.ID, Title: a.Title, CameraRoll: a.CameraRoll}
	if ref.CameraRoll {
		ref.ID = "" // device-specific and not needed
	}
	match := func(r Album) bool { return r.matches(a) }
	acct.Include = slices.DeleteFunc(acct.Include, match)
	acct.Exclude = slices.DeleteFunc(acct.Exclude, match)
	switch state {
	case AlbumInclude:
		acct.Include = append(acct.Include, ref)
	case AlbumExclude:
		acct.Exclude = append(acct.Exclude, ref)
	case AlbumNone:
	default:
		return fmt.Errorf("unknown album state %q", state)
	}
	return nil
}

// unmatched returns the references in refs that match none of albums.
func unmatched(refs, albums []Album) []Album {
	out := []Album{}
	for _, ref := range refs {
		if !slices.ContainsFunc(albums, ref.matches) {
			out = append(out, ref)
		}
	}
	return out
}

// normalizeServer cleans up a user-entered server URL, defaulting to
// HTTPS if no scheme is given.
func normalizeServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("server URL is required")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("server URL must be http or https, not %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("server URL has no host")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("server URL can't have a query, fragment, or user info")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), nil
}

// accountList is the persistent set of accounts.
type accountList struct {
	Current  string     `json:"current"` // ID of the account in use, or empty
	Accounts []*Account `json:"accounts"`
}

func (l *accountList) find(id string) *Account {
	for _, a := range l.Accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

const accountsFile = "accounts.json"

func loadAccounts(dir string) (*accountList, error) {
	l := &accountList{Accounts: []*Account{}}
	j, err := os.ReadFile(filepath.Join(dir, accountsFile))
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(j, l); err != nil {
		return nil, fmt.Errorf("%s: %w", accountsFile, err)
	}
	return l, nil
}

func (l *accountList) save(dir string) error {
	j, err := json.MarshalIndent(l, "", "\t")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, accountsFile+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(j); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, accountsFile))
}

func newAccountID() string {
	return strings.ToLower(rand.Text()[:16])
}
