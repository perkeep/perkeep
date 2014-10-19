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

package test_test

import (
	"log"
	"reflect"
	"testing"

	"camlistore.org/pkg/index"
	. "camlistore.org/pkg/test"
)

var _ index.Interface = (*FakeIndex)(nil)

type tbLogger struct {
	testing.TB
	log []string
}

func (l *tbLogger) Log(args ...interface{}) {
	l.log = append(l.log, args[0].(string))
}

func TestTLog(t *testing.T) {
	tb := new(tbLogger)
	defer TLog(tb)()
	defer log.SetFlags(log.Flags())
	log.SetFlags(0)

	log.Printf("hello")
	log.Printf("hello\n")
	log.Printf("some text\nand more text\n")
	want := []string{
		"hello",
		"hello",
		"some text\nand more text",
	}
	if !reflect.DeepEqual(tb.log, want) {
		t.Errorf("Got %q; want %q", tb.log, want)
	}
}
