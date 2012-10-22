/*
Copyright 2012 Google Inc.

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
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/schema"
)

// A HaveCache tracks whether a remove blobserver has a blob or not.
// TODO(bradfitz): add a notion of a per-blobserver unique ID (reset on wipe/generation/config change).
type HaveCache interface {
	BlobExists(br *blobref.BlobRef) bool
	NoteBlobExists(br *blobref.BlobRef)
}

type Uploader struct {
	*client.Client

	rollSplits bool         // rolling checksum file splitting
	fileOpts   *fileOptions // per-file options; may be nil

	// for debugging; normally nil, but overrides Client if set
	// TODO(bradfitz): clean this up? embed a StatReceiver instead
	// of a Client?
	altStatReceiver blobserver.StatReceiver

	entityFetcher jsonsign.EntityFetcher

	transport *tinkerTransport // for HTTP statistics
	pwd       string
	statCache UploadCache
	haveCache HaveCache

	fs http.FileSystem // virtual filesystem to read from; nil means OS filesystem.
}

// possible options when uploading a file
type fileOptions struct {
	permanode bool // create a content-based permanode for each uploaded file
	// tag is an optional tag or comma-delimited tags to apply to
	// the above permanode.
	tag string
}

func (o *fileOptions) tags() []string {
	if o == nil || o.tag == "" {
		return nil
	}
	return strings.Split(o.tag, ",")
}

func (o *fileOptions) wantFilePermanode() bool {
	return o != nil && o.permanode
}

// sigTime optionally specifies the signature time.
// If zero, the current time is used.
func (up *Uploader) SignMap(m schema.Map, sigTime time.Time) (string, error) {
	camliSigBlobref := up.Client.SignerPublicKeyBlobref()
	if camliSigBlobref == nil {
		// TODO: more helpful error message
		return "", errors.New("No public key configured.")
	}

	m["camliSigner"] = camliSigBlobref.String()
	unsigned, err := m.JSON()
	if err != nil {
		return "", err
	}
	sr := &jsonsign.SignRequest{
		UnsignedJSON:  unsigned,
		Fetcher:       up.Client.GetBlobFetcher(),
		EntityFetcher: up.entityFetcher,
		SignatureTime: sigTime,
	}
	return sr.Sign()
}

func (up *Uploader) UploadMap(m schema.Map) (*client.PutResult, error) {
	json, err := m.JSON()
	if err != nil {
		return nil, err
	}
	return up.uploadString(json)
}

func (up *Uploader) UploadAndSignMap(m schema.Map) (*client.PutResult, error) {
	signed, err := up.SignMap(m, time.Time{})
	if err != nil {
		return nil, err
	}
	return up.uploadString(signed)
}

func (up *Uploader) uploadString(s string) (*client.PutResult, error) {
	uh := client.NewUploadHandleFromString(s)
	if c := up.haveCache; c != nil && c.BlobExists(uh.BlobRef) {
		cachelog.Printf("HaveCache HIT for %s / %d", uh.BlobRef, uh.Size)
		return &client.PutResult{BlobRef: uh.BlobRef, Size: uh.Size, Skipped: true}, nil
	}
	pr, err := up.Upload(uh)
	if err == nil && up.haveCache != nil {
		up.haveCache.NoteBlobExists(uh.BlobRef)
	}
	if pr == nil && err == nil {
		log.Fatalf("Got nil/nil in uploadString while uploading %s", s)
	}
	return pr, err
}

func (up *Uploader) UploadNewPermanode() (*client.PutResult, error) {
	unsigned := schema.NewUnsignedPermanode()
	return up.UploadAndSignMap(unsigned)
}

func (up *Uploader) UploadPlannedPermanode(key string, sigTime time.Time) (*client.PutResult, error) {
	unsigned := schema.NewPlannedPermanode(key)
	signed, err := up.SignMap(unsigned, sigTime)
	if err != nil {
		return nil, err
	}
	return up.uploadString(signed)
}
