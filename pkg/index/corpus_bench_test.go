/*
Copyright 2013 The Camlistore AUTHORS

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

package index_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"camlistore.org/pkg/index"
	"camlistore.org/pkg/index/indextest"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
)

var (
	buildKvOnce    sync.Once
	kvForBenchmark sorted.KeyValue
)

func BenchmarkCorpusFromStorage(b *testing.B) {
	defer test.TLog(b)()
	buildKvOnce.Do(func() {
		kvForBenchmark = sorted.NewMemoryKeyValue()
		idx, err := index.New(kvForBenchmark)
		if err != nil {
			b.Fatal(err)
		}
		id := indextest.NewIndexDeps(idx)
		id.Fataler = b
		for i := 0; i < 10; i++ {
			fileRef, _ := id.UploadFile("file.txt", fmt.Sprintf("some file %d", i), time.Unix(1382073153, 0))
			pn := id.NewPlannedPermanode(fmt.Sprint(i))
			id.SetAttribute(pn, "camliContent", fileRef.String())
		}
	})
	defer index.SetVerboseCorpusLogging(true)
	index.SetVerboseCorpusLogging(false)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := index.NewCorpusFromStorage(kvForBenchmark)
		if err != nil {
			b.Fatal(err)
		}
	}
}
