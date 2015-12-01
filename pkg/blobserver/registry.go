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
	"errors"
	"fmt"
	"net/http"
	"sync"

	"go4.org/jsonconfig"
)

var ErrHandlerTypeNotFound = errors.New("requested handler type not loaded")

type FindHandlerByTyper interface {
	// FindHandlerByType finds a handler by its handlerType and
	// returns its prefix and handler if it's loaded.  If it's not
	// loaded, the error will be ErrHandlerTypeNotFound.
	//
	// This is used by handlers to find siblings (such as the "ui" type handler)
	// which might have more knowledge about the configuration for discovery, etc.
	//
	// Note that if this is called during handler construction
	// time, only the prefix may be returned with a nil handler
	// and nil err.  Unlike GetHandler and GetStorage, this does
	// not cause the prefix to load immediately. At runtime (after
	// construction of all handlers), then prefix and handler will
	// both be non-nil when err is nil.
	FindHandlerByType(handlerType string) (prefix string, handler interface{}, err error)

	// AllHandlers returns a map from prefix to handler type, and
	// a map from prefix to handler.
	AllHandlers() (map[string]string, map[string]interface{})
}

type Loader interface {
	FindHandlerByTyper

	// MyPrefix returns the prefix of the handler currently being constructed,
	// with both leading and trailing slashes (e.g. "/ui/").
	MyPrefix() string

	// BaseURL returns the server's base URL, without trailing slash, and not including
	// the prefix (as returned by MyPrefix).
	BaseURL() string

	// GetHandlerType returns the handler's configured type, but does
	// not force it to start being loaded yet.
	GetHandlerType(prefix string) string // returns "" if unknown

	// GetHandler returns either a Storage or an http.Handler.
	// It forces the handler to be loaded and returns an error if
	// a cycle is created.
	GetHandler(prefix string) (interface{}, error)

	// GetStorage is like GetHandler but requires that the Handler be
	// a storage Handler.
	GetStorage(prefix string) (Storage, error)
}

// HandlerIniter is an optional interface which can be implemented
// by Storage or http.Handlers (from StorageConstructor or HandlerConstructor)
// to be called once all the handlers have been created.
type HandlerIniter interface {
	InitHandler(FindHandlerByTyper) error
}

// A StorageConstructor returns a Storage implementation from a Loader
// environment and a configuration.
type StorageConstructor func(Loader, jsonconfig.Obj) (Storage, error)

// A HandlerConstructor returns an http.Handler from a Loader
// environment and a configuration.
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

// RegisterHandlerConstructor registers an http Handler constructor function
// for a given handler type.
//
// It is an error to register the same handler type twice.
func RegisterHandlerConstructor(typ string, ctor HandlerConstructor) {
	mapLock.Lock()
	defer mapLock.Unlock()
	if _, ok := handlerConstructors[typ]; ok {
		panic("blobserver: HandlerConstrutor already registered for type: " + typ)
	}
	handlerConstructors[typ] = ctor
}

// CreateHandler instantiates an http Handler of type 'typ' from the
// provided JSON configuration, and finding peer handlers and
// configuration from the environment in 'loader'.
//
// The handler 'typ' must have been previously registered with
// RegisterHandlerConstructor.
func CreateHandler(typ string, loader Loader, config jsonconfig.Obj) (http.Handler, error) {
	mapLock.Lock()
	ctor, ok := handlerConstructors[typ]
	mapLock.Unlock()
	if !ok {
		return nil, fmt.Errorf("blobserver: Handler type %q not known or loaded", typ)
	}
	return ctor(loader, config)
}
