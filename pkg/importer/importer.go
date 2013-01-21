/*
Copyright 2013 Google Inc.

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

// Package importer imports content from third-party websites.
//
// TODO(bradfitz): Finish this. Barely started.
package importer

import (
	"net/http"

	"camlistore.org/pkg/blobserver"
)

// An Importer imports content from third-party websites into a Camlistore repo.
type Importer struct {
	// Target is the blobserver to populate.
	Target blobserver.StatReceiver

	// TODO: SearchClient?

	// ProgressChan optionally specifies a channel to receive
	// progress messages of various types.  The types sent may be:
	//   - *ProgressMessage
	//   - *NewPermanodeMessage
	ProgressChan chan<- interface{}

	// Client optionally specifies how to fetch external network
	// resources.  If nil, http.DefaultClient is used.
	Client *http.Client
}

func (im *Importer) client() *http.Client {
	if im.Client == nil {
		return http.DefaultClient
	}
	return im.Client
}

type ProgressMessage struct {
	ItemsDone, ItemsTotal int
	BytesDone, BytesTotal int64
}

func (im *Importer) Fetch(url string) error {
	res, err := im.client().Get(url)
	if err != nil {
		return err
	}
	return im.ImportResponse(res)
}

func (im *Importer) ImportResponse(res *http.Response) error {
	defer res.Body.Close()
	panic("TODO(bradfitz): implement")
}

var parsers = make(map[string]Parser)

func RegisterParser(name string, p Parser) {
	if _, dup := parsers[name]; dup {
		panic("Dup registration of parser " + name)
	}
	parsers[name] = p
}

// YesNoMaybe is a tri-state of "yes", "no", and "maybe".
type YesNoMaybe int

const (
	No YesNoMaybe = iota
	Yes
	Maybe
)

// A Parser
type Parser interface {
	CanHandleURL(url string) YesNoMaybe
	CanHandleResponse(res *http.Response) bool
	Import(im *Importer) error
}
