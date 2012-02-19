/*
Copyright 2011 Google Inc.

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

package blobserver

import (
	"fmt"
	"net/http"
	"sync"

	"camlistore.org/pkg/jsonconfig"
)

type Loader interface {
	GetStorage(prefix string) (Storage, error)
	GetHandlerType(prefix string) string // or ""

	// Returns either a Storage or an http.Handler
	GetHandler(prefix string) (interface{}, error)

	FindHandlerByTypeIfLoaded(htype string) (prefix string, handler interface{}, err error)

	// If we're loading configuration in response to a web request
	// (as we do with App Engine), then this returns a request and
	// true.
	GetRequestContext() (ctx *http.Request, ok bool)
}

type StorageConstructor func(Loader, jsonconfig.Obj) (Storage, error)
type HandlerConstructor func(Loader, jsonconfig.Obj) (http.Handler, error)

var mapLock sync.Mutex
var storageConstructors = make(map[string]StorageConstructor)
var handlerConstructors = make(map[string]HandlerConstructor)

func RegisterStorageConstructor(typ string, ctor StorageConstructor) {
	mapLock.Lock()
	defer mapLock.Unlock()
	if _, ok := storageConstructors[typ]; ok {
		panic("blobserver: StorageConstructor already registered for type: " + typ)
	}
	storageConstructors[typ] = ctor
}

func CreateStorage(typ string, loader Loader, config jsonconfig.Obj) (Storage, error) {
	mapLock.Lock()
	ctor, ok := storageConstructors[typ]
	mapLock.Unlock()
	if !ok {
		return nil, fmt.Errorf("Storage type %q not known or loaded", typ)
	}
	return ctor(loader, config)
}

func RegisterHandlerConstructor(typ string, ctor HandlerConstructor) {
	mapLock.Lock()
	defer mapLock.Unlock()
	if _, ok := handlerConstructors[typ]; ok {
		panic("blobserver: HandlerConstrutor already registered for type: " + typ)
	}
	handlerConstructors[typ] = ctor
}

func CreateHandler(typ string, loader Loader, config jsonconfig.Obj) (http.Handler, error) {
	mapLock.Lock()
	ctor, ok := handlerConstructors[typ]
	mapLock.Unlock()
	if !ok {
		return nil, fmt.Errorf("blobserver: Handler type %q not known or loaded", typ)
	}
	return ctor(loader, config)
}
