/*
Copyright 2015 The Camlistore Authors.

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

package sqlkv

import (
	"strings"
	"testing"
)

var queries = []string{
	"REPLACE INTO /*TPRE*/rows (k, v) VALUES (?, ?)",
	"DELETE FROM /*TPRE*/rows WHERE k=?",
	"SELECT v FROM /*TPRE*/rows WHERE k=?",
	"REPLACE INTO /*TPRE*/rows (k, v) VALUES (?, ?)",
	"DELETE FROM /*TPRE*/rows WHERE k=?",
	"DELETE FROM /*TPRE*/rows",
	"SELECT k, v FROM /*TPRE*/rows WHERE k >= ? ORDER BY k ",
	"SELECT k, v FROM /*TPRE*/rows WHERE k >= ? AND k < ? ORDER BY k ",
}

var (
	qmarkRepl = strings.NewReplacer("?", ":placeholder")

	kv = &KeyValue{
		TablePrefix:     "T_",
		PlaceHolderFunc: func(q string) string { return qmarkRepl.Replace(q) },
	}
)

func TestSql(t *testing.T) {
	repl := strings.NewReplacer("/*TPRE*/", "T_", "?", ":placeholder")
	for i, q := range queries {
		want := repl.Replace(q)
		got := kv.sql(q)
		if want != got {
			t.Errorf("%d. got %q, wanted %q.", i, got, want)
		}
	}
}

func BenchmarkSql(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range queries {
			kv.sql(s)
		}
	}
}
