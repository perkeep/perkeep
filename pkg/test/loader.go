/*
Copyright 2014 The Camlistore Authors

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

package test

import (
	"errors"
	"log"
	"strings"
	"sync"

	"camlistore.org/pkg/blobserver"
)

// NewLoader
func NewLoader() *Loader {
	return &Loader{}
}

type Loader struct {
	mu  sync.Mutex
	sto map[string]blobserver.Storage
}

var _ blobserver.Loader = (*Loader)(nil)

func (ld *Loader) FindHandlerByType(handlerType string) (prefix string, handler interface{}, err error) {
	panic("NOIMPL")
}

func (ld *Loader) AllHandlers() (map[string]string, map[string]interface{}) {
	panic("NOIMPL")
}

func (ld *Loader) MyPrefix() string {
	return "/lies/"
}

func (ld *Loader) BaseURL() string {
	return "http://localhost:1234"
}

func (ld *Loader) GetHandlerType(prefix string) string {
	log.Printf("test.Loader: GetHandlerType called but not implemented.")
	return ""
}

func (ld *Loader) GetHandler(prefix string) (interface{}, error) {
	log.Printf("test.Loader: GetHandler called but not implemented.")
	return nil, errors.New("doesn't exist")
}

func (ld *Loader) SetStorage(prefix string, s blobserver.Storage) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if ld.sto == nil {
		ld.sto = make(map[string]blobserver.Storage)
	}
	ld.sto[prefix] = s
}

func (ld *Loader) GetStorage(prefix string) (blobserver.Storage, error) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if bs, ok := ld.sto[prefix]; ok {
		return bs, nil
	}
	if ld.sto == nil {
		ld.sto = make(map[string]blobserver.Storage)
	}
	sto, err := ld.genStorage(prefix)
	if err != nil {
		return nil, err
	}
	ld.sto[prefix] = sto
	return sto, nil
}

func (ld *Loader) genStorage(prefix string) (blobserver.Storage, error) {
	if strings.HasPrefix(prefix, "/good") {
		return &Fetcher{}, nil
	}
	if strings.HasPrefix(prefix, "/fail") {
		return &Fetcher{ReceiveErr: errors.New("test.Loader intentional failure for /fail storage handler")}, nil
	}
	panic("test.Loader.GetStorage: unrecognized prefix type")
}
