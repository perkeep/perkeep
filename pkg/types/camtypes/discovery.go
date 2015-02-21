/*
Copyright 2015 The Camlistore Authors

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
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types"
)

// Discovery is the JSON response for discovery requests.
type Discovery struct {
	BlobRoot     string `json:"blobRoot"`
	JSONSignRoot string `json:"jsonSignRoot"`
	HelpRoot     string `json:"helpRoot"`
	ImporterRoot string `json:"importerRoot"`
	SearchRoot   string `json:"searchRoot"`
	StatusRoot   string `json:"statusRoot"`

	OwnerName string `json:"ownerName"` // Name of the owner.
	UserName  string `json:"userName"`  // Name of the user.

	// StorageGeneration is the UUID for the storage generation.
	StorageGeneration string `json:"storageGeneration,omitempty"`
	// StorageGenerationError is the error that occurred on generating the storage, if any.
	StorageGenerationError string `json:"storageGenerationError,omitempty"`
	// StorageInitTime is the initialization time of the storage.
	StorageInitTime types.Time3339 `json:"storageInitTime,omitempty"`

	ThumbVersion string `json:"thumbVersion"` // Thumbnailing version.
	WSAuthToken  string `json:"wsAuthToken"`  // Authentication token for the WebSocket.

	// SyncHandlers lists discovery information about the available sync handlers.
	SyncHandlers []SyncHandlerDiscovery `json:"syncHanlders,omitempty"`
	// Signing contains discovery information for signing.
	Signing *SignDiscovery `json:"signing,omitempty"`
	// UIDiscovery contains discovery information for the UI.
	*UIDiscovery
}

// SignDiscovery contains discovery information for jsonsign.
// It is part of the server's JSON response for discovery requests.
type SignDiscovery struct {
	// PublicKey is the path to the public signing key.
	PublicKey string `json:"publicKey,omitempty"`
	// PublicKeyBlobRef is the blob.Ref for the public key.
	PublicKeyBlobRef blob.Ref `json:"publicKeyBlobRef,omitempty"`
	// PublicKeyID is the ID of the public key.
	PublicKeyID string `json:"publicKeyId"`
	// SignHandler is the URL path prefix to the signing handler.
	SignHandler string `json:"signHandler"`
	// VerifyHandler it the URL path prefix to the signature verification handler.
	VerifyHandler string `json:"verifyHandler"`
}

// SyncHandlerDiscovery contains discovery information about a sync handler.
// It is part of the JSON response to discovery requests.
type SyncHandlerDiscovery struct {
	// From is the source of the sync handler.
	From string `json:"from"`
	// To is the destination of the sync handler.
	To string `json:"to"`
	// ToIndex is true if the sync is from a blob storage to an index.
	ToIndex bool `json:"toIndex"`
}

// UIDiscovery contains discovery information for the user interface.
// It is part of the JSON response to discovery requests.
type UIDiscovery struct {
	// UIRoot is the URL prefix path to the UI handler.
	UIRoot string `json:"uiRoot"`
	// UploadHelper is the path to the upload helper.
	UploadHelper string `json:"uploadHelper"`
	// DirectoryHelper is the path to the directory helper.
	DirectoryHelper string `json:"directoryHelper"`
	// DownloaderHelper is the path to the downloader helper.
	DownloadHelper string `json:"downloadHelper"`
	// PublishRoots lists discovery information for all publishing roots,
	// mapped by the respective root name.
	PublishRoots map[string]*PublishRootDiscovery `json:"publishRoots"`
}

// PublishRootDiscovery contains discovery information for the publish roots.
type PublishRootDiscovery struct {
	Name string `json:"name"`
	// Prefix lists prefixes belonging to the publishing root.
	Prefix []string `json:"prefix"`
	// CurrentPermanode is the permanode associated with the publishing root.
	CurrentPermanode blob.Ref `json:"currentPermanode"`
}
