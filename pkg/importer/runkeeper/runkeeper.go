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

// Package runkeeper imports local Runkeeper account archives into stable
// provider-independent fitness activity permanodes, without network requests.
package runkeeper // import "perkeep.org/pkg/importer/runkeeper"

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sync"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/types/camtypes"
)

type imp struct{}

var _ importer.Importer = imp{}
var _ importer.ArchiveImporter = imp{}

func init() { importer.Register("runkeeper", imp{}) }
func (imp) Properties() importer.Properties {
	return importer.Properties{Title: "Runkeeper", Description: "import your Runkeeper account ZIP archives", SupportsIncremental: true}
}
func (imp) IsAccountReady(acct *importer.Object) (bool, error) {
	return acct.Attr(importer.AcctAttrUserName) != "" || acct.Attr(importer.AcctAttrEmail) != "", nil
}
func (imp) SummarizeAccount(acct *importer.Object) string {
	name := acct.Attr(importer.AcctAttrUserName)
	if name == "" {
		name = acct.Attr(importer.AcctAttrEmail)
	}
	return "Runkeeper account " + name
}

var errArchiveOnly = errors.New("runkeeper: archive-only importer; use pk-import-runkeeper <archive.zip>")

func (imp) Run(*importer.RunContext) error { return errArchiveOnly }
func (imp) ServeSetup(http.ResponseWriter, *http.Request, *importer.SetupContext) error {
	return errArchiveOnly
}
func (imp) ServeCallback(w http.ResponseWriter, _ *http.Request, _ *importer.SetupContext) {
	http.Error(w, errArchiveOnly.Error(), http.StatusBadRequest)
}
func (imp) CallbackRequestAccount(*http.Request) (blob.Ref, error) { return blob.Ref{}, errArchiveOnly }
func (imp) CallbackURLParameters(blob.Ref) url.Values              { return nil }

var filenameID = regexp.MustCompile(`(?:^|-)runkeeper-data-export-([0-9]+)-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}\.zip$`)

func (imp) InspectArchive(ctx context.Context, a *importer.Archive) (camtypes.ArchiveIdentity, error) {
	var identity camtypes.ArchiveIdentity
	if err := ctx.Err(); err != nil {
		return identity, err
	}
	p, err := readProfile(a)
	if err != nil {
		return identity, err
	}
	// Recognize the activity table even for a profile-only or empty export;
	// inspection validates bounded records without creating any permanodes.
	count := 0
	seen := make(map[string]bool)
	if err := readCSV(ctx, a, "cardioActivities.csv", maxMetadata, func(row rawRow) error {
		count++
		if count > maxActivities {
			return fmt.Errorf("activity count exceeds %d", maxActivities)
		}
		activity, err := parseActivity(row)
		if err != nil {
			return err
		}
		if seen[activity.ID] {
			return fmt.Errorf("duplicate activity UUID %s", activity.ID)
		}
		seen[activity.ID] = true
		return nil
	}); err != nil {
		return identity, err
	}
	identity = camtypes.ArchiveIdentity{UserName: p.UserName, Email: p.Email, Name: p.Name}
	if m := filenameID.FindStringSubmatch(filepath.Base(a.FileName)); m != nil {
		identity.FilenameUserID = m[1]
	}
	return identity, nil
}

// The host serializes managed runs. Stripes also protect direct RunArchive
// callers from ChildPathObject's non-atomic find/create sequence.
var accountLocks [64]sync.Mutex

func (imp) RunArchive(rc *importer.RunContext, a *importer.Archive) error {
	mu := &accountLocks[rc.AccountNode().PermanodeRef().Sum32()%uint32(len(accountLocks))]
	mu.Lock()
	defer mu.Unlock()
	names := make(map[string]bool, len(a.Zip.File))
	for _, f := range a.Zip.File {
		names[f.Name] = !f.FileInfo().IsDir()
	}
	return importArchive(rc, a, names, func(p importProgress) error {
		return rc.SetArchiveProgress(camtypes.ArchiveImportProgress{ItemsTotal: p.Total, ItemsDone: p.Done, ItemsUnchanged: p.Unchanged, Warnings: p.Warnings, WarningCount: p.WarningCount})
	})
}
