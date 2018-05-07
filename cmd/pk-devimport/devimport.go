/*
Copyright 2017 The Perkeep Authors.

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

// The pk-devimport command runs an importer, using the importer code linked into the binary,
// against a Perkeep server. This enables easier interactive development of importers,
// without having to restart a server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/search"

	_ "perkeep.org/pkg/importer/allimporters"
)

const serverFlagHelp = "Format is is either a URL prefix (with optional path), a host[:port], a config file server alias, or blank to use the Perkeep client config's default server."

// newClient returns a Perkeep client for the server.
// The server may be:
//   * blank, to use the default in the config file
//   * an alias, to use that named alias in the config file
//   * host:port
//   * https?://host[:port][/path]
func newClient(server string, opts ...client.ClientOption) *client.Client {
	if server == "" {
		return client.NewOrFail(opts...)
	}
	cl := client.NewOrFail(append([]client.ClientOption{client.OptionServer(server)}, opts...)...)
	if err := cl.SetupAuth(); err != nil {
		log.Fatalf("Could not setup auth for connecting to %v: %v", server, err)
	}
	return cl
}

var (
	flagServer = flag.String("server", "", "Server to search. "+serverFlagHelp)
)

func usage() {
	fmt.Fprintf(os.Stderr,
		`Usage: pk-devimport <importerType> <accountNodeRef>

-importerType is one of the supported importers: feed, flickr, foursquare, gphotos, pinboard, plaid, twitter.
-accountNodeRef is the permanode of camliNodeType importerAccount representing the account to import.
`)
}

func newImporterHost(server string, importerType string) (*importer.Host, error) {
	cl := newClient(server)
	signer, err := cl.Signer()
	if err != nil {
		return nil, err
	}
	// TODO(mpl): technically not true, but works for now.
	baseURL, err := cl.BlobRoot()
	if err != nil {
		return nil, err
	}

	var clientID, clientSecret string

	if importer.All()[importerType].Properties().NeedsAPIKey {
		clientID, clientSecret, err = getCredentials(cl, importerType)
		if err != nil {
			return nil, err
		}
	}

	hc := importer.HostConfig{
		BaseURL:    baseURL,
		Prefix:     "/importer/", // TODO(mpl): do not hardcode this prefix
		Target:     cl,
		BlobSource: cl,
		Signer:     signer,
		Search:     cl,
		ClientId: map[string]string{
			importerType: clientID,
		},
		ClientSecret: map[string]string{
			importerType: clientSecret,
		},
	}

	return importer.NewHost(hc)
}

// getCredentials returns the OAuth clientID and clientSecret found in the
// importer node of the given importerType.
func getCredentials(sh search.QueryDescriber, importerType string) (string, string, error) {
	var clientID, clientSecret string
	res, err := sh.Query(context.TODO(), &search.SearchQuery{
		Expression: "attr:camliNodeType:importer and attr:importerType:" + importerType,
		Describe: &search.DescribeRequest{
			Depth: 1,
		},
	})
	if err != nil {
		return clientID, clientSecret, err
	}
	if res.Describe == nil {
		return clientID, clientSecret, errors.New("no importer node found")
	}
	var attrs url.Values
	for _, resBlob := range res.Blobs {
		blob := resBlob.Blob
		desBlob, ok := res.Describe.Meta[blob.String()]
		if !ok || desBlob.Permanode == nil {
			continue
		}
		attrs = desBlob.Permanode.Attr
		if attrs.Get("camliNodeType") != "importer" {
			return clientID, clientSecret, errors.New("search result returned non importer node")
		}
		if t := attrs.Get("importerType"); t != importerType {
			return clientID, clientSecret, fmt.Errorf("search result returned importer node of the wrong type: %v", t)
		}
		break
	}
	attrClientID, attrClientSecret := "authClientID", "authClientSecret"
	attr := attrs[attrClientID]
	if len(attr) != 1 {
		return clientID, clientSecret, fmt.Errorf("no %v attribute", attrClientID)
	}
	clientID = attr[0]
	attr = attrs[attrClientSecret]
	if len(attr) != 1 {
		return clientID, clientSecret, fmt.Errorf("no %v attribute", attrClientSecret)
	}
	clientSecret = attr[0]
	return clientID, clientSecret, nil
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		usage()
		os.Exit(2)
	}

	if _, ok := importer.All()[args[0]]; !ok {
		log.Fatalf("%v is not a valid importer name", args[0])
	}
	ref, ok := blob.Parse(args[1])
	if !ok {
		log.Fatalf("Not a valid blob ref: %q", args[1])
	}

	h, err := newImporterHost(*flagServer, args[0])
	if err != nil {
		log.Fatal(err)
	}
	if err := h.RunImporterAccount(args[0], ref); err != nil {
		log.Fatal(err)
	}
}
