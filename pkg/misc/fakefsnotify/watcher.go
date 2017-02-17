/*
Copyright 2017 The Camlistore Authors.

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

// Package fakefsnotify provides just enough of a mock of
// github.com/fsnotify/fsnotify in order to build github.com/gopherjs/gopherjs.
// Obviously the 'gopherjs serve' command (which normally relies on fsnotify) is
// not expected to work when gopherjs is built with fakefsnotify.
package fakefsnotify

const (
	Create = iota
	Write  = iota
	Remove = iota
	Rename = iota
)

type Watcher struct {
	Events chan FakeEvent
	Errors chan error
}

type FakeEvent struct {
	Op   int
	Name string
}

func NewWatcher() (*Watcher, error) {
	return &Watcher{
		Events: make(chan FakeEvent),
		Errors: make(chan error),
	}, nil
}

func (fw *Watcher) Add(packagePath string) {
	// NOOP
}

func (fw *Watcher) Close() error {
	return nil
}
