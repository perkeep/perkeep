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

package camtypes

import (
	"perkeep.org/pkg/blob"
	"time"
)

// ArchiveIdentity identifies the owner described inside a provider's export.
// FilenameUserID is only an advisory tie-breaker, never sufficient identity.
type ArchiveIdentity struct {
	UserName       string `json:"userName,omitempty"`
	Email          string `json:"email,omitempty"`
	Name           string `json:"name,omitempty"`
	FilenameUserID string `json:"filenameUserID,omitempty"`
}

// ArchiveImportRequest inspects or starts an import of an already uploaded file
// schema. Action must be "inspect" or "start". Inspection never creates accounts.
type ArchiveImportRequest struct {
	Action  string   `json:"action"`
	FileRef blob.Ref `json:"fileRef"`
}

// ArchiveImportProgress describes provider work, not an authoritative data
// checkpoint. Providers must keep their reconciliation state on permanodes.
type ArchiveImportProgress struct {
	ItemsTotal     int      `json:"itemsTotal"`
	ItemsDone      int      `json:"itemsDone"`
	ItemsUnchanged int      `json:"itemsUnchanged"`
	Warnings       []string `json:"warnings,omitempty"`
	WarningCount   int      `json:"warningCount"`
}

// ArchiveImportStatus is durable per-account, per-input run history. State is
// running, complete, error, or interrupted (a previous process stopped running).
type ArchiveImportStatus struct {
	AccountRef blob.Ref              `json:"accountRef"`
	FileRef    blob.Ref              `json:"fileRef"`
	State      string                `json:"state"`
	Error      string                `json:"error,omitempty"`
	Started    time.Time             `json:"started"`
	Finished   time.Time             `json:"finished,omitempty"`
	Progress   ArchiveImportProgress `json:"progress"`
}

// ArchiveImportResult is returned by archive inspect and start operations.
type ArchiveImportResult struct {
	Identity ArchiveIdentity `json:"identity"`
	// omitempty does not omit a zero blob.Ref struct, which marshals as null.
	AccountRef blob.Ref             `json:"accountRef,omitzero"`
	Created    bool                 `json:"created,omitempty"`
	Status     *ArchiveImportStatus `json:"status,omitempty"`
}
