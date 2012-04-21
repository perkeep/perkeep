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

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/schema"
)

type Uploader struct {
	*client.Client

	rollSplits bool // rolling checksum file splitting

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

func (up *Uploader) SignMap(m map[string]interface{}) (string, error) {
	camliSigBlobref := up.Client.SignerPublicKeyBlobref()
	if camliSigBlobref == nil {
		// TODO: more helpful error message
		return "", errors.New("No public key configured.")
	}

	m["camliSigner"] = camliSigBlobref.String()
	unsigned, err := schema.MapToCamliJSON(m)
	if err != nil {
		return "", err
	}
	sr := &jsonsign.SignRequest{
		UnsignedJson:  unsigned,
		Fetcher:       up.Client.GetBlobFetcher(),
		EntityFetcher: up.entityFetcher,
	}
	return sr.Sign()
}

func (up *Uploader) UploadMap(m map[string]interface{}) (*client.PutResult, error) {
	json, err := schema.MapToCamliJSON(m)
	if err != nil {
		return nil, err
	}
	return up.uploadString(json)
}

func (up *Uploader) UploadAndSignMap(m map[string]interface{}) (*client.PutResult, error) {
	signed, err := up.SignMap(m)
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
