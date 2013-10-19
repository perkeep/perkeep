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
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
)

func init() {
	importer.Register("dummy", newFromConfig)
}

func newFromConfig(cfg jsonconfig.Obj) (importer.Importer, error) {
	im := &imp{
		url:       cfg.RequiredString("url"),
		username:  cfg.RequiredString("username"),
		authToken: cfg.RequiredString("authToken"),
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
}

func (im *imp) CanHandleURL(url string) bool { return false }
func (im *imp) ImportURL(url string) error   { panic("unused") }

func (im *imp) Run(h *importer.Host, intr importer.Interrupt) error {
	// TODO
	return nil
}
