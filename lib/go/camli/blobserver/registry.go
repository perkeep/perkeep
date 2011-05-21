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
	"http"
	"os"
	"sync"

	"camli/jsonconfig"
)

type Loader interface {
	GetStorage(prefix string) (Storage, os.Error)
}

type StorageConstructor func(Loader, jsonconfig.Obj) (Storage, os.Error)

// TODO: HandlerConstructor never ended up being used, but that was from
// before the Loader interface was introduced. Perhaps camlistored server
// could have all its non-Storage handlers simplified and no longer
// special-cased by using HandlerConstructor with a Loader parameter.
type HandlerConstructor func(config jsonconfig.Obj) (http.Handler, os.Error)

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

func CreateStorage(typ string, loader Loader, config jsonconfig.Obj) (Storage, os.Error) {
	mapLock.Lock()
	ctor, ok := storageConstructors[typ]
	mapLock.Unlock()
	if !ok {
		return nil, fmt.Errorf("Storage type %q not known or loaded", typ)
	}
	return ctor(loader, config)
}

func RegisterHandlerConstrutor(typ string, ctor HandlerConstructor) {
	mapLock.Lock()
        defer mapLock.Unlock()
	if _, ok := handlerConstructors[typ]; ok {
                panic("blobserver: HandlerConstrutor already registered for type: " + typ)
        }
	handlerConstructors[typ] = ctor
}

