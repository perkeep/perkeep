/*
Copyright 2013 The Camlistore Authors

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

// Package dummy is an example importer for development purposes.
package dummy

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"camlistore.org/pkg/context"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
)

func init() {
	importer.Register("dummy", importer.TODOImporter)
	importer.Register("flickr", importer.TODOImporter)
	importer.Register("picasa", importer.TODOImporter)
	importer.Register("twitter", importer.TODOImporter)
}

func newFromConfig(cfg jsonconfig.Obj, host *importer.Host) (*imp, error) {
	im := &imp{
		url:       cfg.RequiredString("url"),
		username:  cfg.RequiredString("username"),
		authToken: cfg.RequiredString("authToken"),
		host:      host,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return im, nil
}

type imp struct {
	url       string
	username  string
	authToken string
	host      *importer.Host
}

func (im *imp) Prefix() string {
	return fmt.Sprintf("dummy:%s", im.username)
}

func (im *imp) Run(ctx *context.Context) (err error) {
	log.Printf("Running dummy importer.")
	defer func() {
		log.Printf("Dummy importer returned: %v", err)
	}()
	root, err := im.host.RootObject()
	if err != nil {
		return err
	}
	fileRef, err := schema.WriteFileFromReader(im.host.Target(), "foo.txt", strings.NewReader("Some file.\n"))
	if err != nil {
		return err
	}
	obj, err := root.ChildPathObject("foo.txt")
	if err != nil {
		return err
	}
	return obj.SetAttr("camliContent", fileRef.String())
}

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
}
