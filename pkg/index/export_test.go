/*
Copyright 2012 Google Inc.

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

package index

import (
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types/camtypes"
)

func ExpReverseTimeString(s string) string {
	return reverseTimeString(s)
}

func ExpUnreverseTimeString(s string) string {
	return unreverseTimeString(s)
}

func ExpSetLogCorpusStats(v bool) { logCorpusStats = v }

func ExpNewCorpus() *Corpus {
	return newCorpus()
}

func (c *Corpus) Exp_mergeFileInfoRow(k, v string) error {
	return c.mergeFileInfoRow(k, v)
}

func (c *Corpus) Exp_files(br blob.Ref) camtypes.FileInfo {
	return c.files[br]
}
