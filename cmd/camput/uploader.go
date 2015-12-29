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
	"net/http"
	"strings"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/httputil"

	"go4.org/syncutil"
)

// TODO(mpl): move Uploader to pkg/client, or maybe its own pkg, and clean up files.go

type Uploader struct {
	*client.Client

	// fdGate guards gates the creation of file descriptors.
	fdGate *syncutil.Gate

	fileOpts *fileOptions // per-file options; may be nil

	// for debugging; normally nil, but overrides Client if set
	// TODO(bradfitz): clean this up? embed a StatReceiver instead
	// of a Client?
	altStatReceiver blobserver.StatReceiver

	stats     *httputil.StatsTransport // if non-nil, HTTP statistics
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
	// perform for the client the actions needing gpg signing when uploading a file.
	vivify       bool
	exifTime     bool // use the time in exif metadata as the modtime if possible.
	capCtime     bool // use mtime as ctime if ctime > mtime
	contentsOnly bool // do not store any of the file's attributes, only its contents.
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

func (o *fileOptions) wantVivify() bool {
	return o != nil && o.vivify
}

func (o *fileOptions) wantCapCtime() bool {
	return o != nil && o.capCtime
}

func (up *Uploader) uploadString(s string) (*client.PutResult, error) {
	return up.Upload(client.NewUploadHandleFromString(s))
}

func (up *Uploader) Close() error {
	var grp syncutil.Group
	if up.haveCache != nil {
		grp.Go(up.haveCache.Close)
	}
	grp.Go(up.Client.Close)
	return grp.Err()
}
