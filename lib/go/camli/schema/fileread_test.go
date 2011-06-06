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

package schema

import (
	"camli/test"
	"io/ioutil"
	"log"
	"testing"
)

var _ = log.Printf

var testFetcher = &test.Fetcher{}

var blobA = &test.Blob{"AAAAAaaaaa"}
var blobB = &test.Blob{"BBBBBbbbbb"}
var blobC = &test.Blob{"CCCCCccccc"}

func init() {
	testFetcher.AddBlob(blobA)
	testFetcher.AddBlob(blobB)
	testFetcher.AddBlob(blobC)
}

type readTest struct {
	parts    []*ContentPart
	skip     uint64
	expected string
}

func part(blob *test.Blob, offset, size uint64) *ContentPart {
	return &ContentPart{BlobRef: blob.BlobRef(), Size: size, Offset: offset}
}

func all(blob *test.Blob) *ContentPart {
	return part(blob, 0, uint64(blob.Size()))
}

func parts(parts ...*ContentPart) []*ContentPart {
	return parts
}

func sizeSum(parts []*ContentPart) (s uint64) {
	for _, p := range parts {
		s += uint64(p.Size)
	}
	return
}

var readTests = []readTest{
	{parts(all(blobA)), 0, "AAAAAaaaaa"},
	{parts(all(blobA)), 2, "AAAaaaaa"},
	{parts(part(blobA, 0, 5)), 0, "AAAAA"},
	{parts(part(blobA, 2, 8)), 0, "AAAaaaaa"},
	{parts(part(blobA, 2, 8)), 1, "AAaaaaa"},
	{parts(part(blobA, 4, 6)), 0, "Aaaaaa"},
	{parts(all(blobA), all(blobB)), 0, "AAAAAaaaaaBBBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 1, "AAAAaaaaaBBBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 10, "BBBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 11, "BBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 100, ""},
	{parts(all(blobA), all(blobB), all(blobC)), 0, "AAAAAaaaaaBBBBBbbbbbCCCCCccccc"},
	{parts(all(blobA), all(blobB), all(blobC)), 20, "CCCCCccccc"},
	{parts(all(blobA), all(blobB), all(blobC)), 22, "CCCccccc"},
	{parts(part(blobA, 5, 5), part(blobB, 0, 5), part(blobC, 4, 2)), 1, "aaaaBBBBBCc"},
}

func TestReader(t *testing.T) {
	for idx, rt := range readTests {
		ss := new(Superset)
		ss.Type = "file"
		ss.Version = 1
		ss.Size = sizeSum(rt.parts)
		ss.ContentParts = rt.parts
		fr := ss.NewFileReader(testFetcher)
		fr.Skip(rt.skip)
		all, err := ioutil.ReadAll(fr)
		if err != nil {
			t.Errorf("read error on test %d: %v", idx, err)
			continue
		}
		if g, e := string(all), rt.expected; e != g {
			t.Errorf("test %d: expected %q; got %q", idx, e, g)
		}
	}
}
