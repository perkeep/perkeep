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

package integration

import (
	"bufio"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"camlistore.org/pkg/test"
)

var (
	testFileRel  = filepath.Join("pkg", "test", "integration", "100M.dat")
	testFileSize = 100 * 1024 * 1024
)

func BenchmarkLocal(b *testing.B) {
	benchmarkWrite(b, "bench-localdisk-server-config.json")
}

func BenchmarkDiskpacked(b *testing.B) {
	benchmarkWrite(b, "bench-diskpacked-server-config.json")
}

func benchmarkWrite(b *testing.B, cfg string) {
	w, err := test.WorldFromConfig(cfg)
	if err != nil {
		b.Fatalf("could not create server for config: %v\nError: %v", cfg, err)
	}
	testFile := filepath.Join(w.CamliSourceRoot(), testFileRel)
	createTestFile(b, testFile, testFileSize)
	defer os.Remove(testFile)
	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		err = w.Start()
		if err != nil {
			b.Fatalf("could not start server for config: %v\nError: %v", cfg, err)
		}
		b.StartTimer()
		test.MustRunCmd(b, w.Cmd("camput", "file", testFile))
		b.StopTimer()
		w.Stop()
	}

	b.SetBytes(int64(testFileSize))
}

func createTestFile(tb testing.TB, file string, n int) {
	f, err := os.Create(file)
	if err != nil {
		tb.Fatal(err)
	}
	w := bufio.NewWriter(f)
	tot := 0
	var b [8]byte
	for tot < n {
		c := rand.Int63()
		b = [8]byte{
			byte(c),
			byte(c >> 8),
			byte(c >> 16),
			byte(c >> 24),
			byte(c >> 32),
			byte(c >> 40),
			byte(c >> 48),
			byte(c >> 56),
		}
		wn, err := w.Write(b[:])
		if err != nil {
			tb.Fatal(err)
		}
		if wn < len(b) {
			tb.Fatalf("short write, got %d expected %d", wn, len(b))
		}
		tot += wn
	}
	if err := w.Flush(); err != nil {
		tb.Fatal(err)
	}
	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
}
