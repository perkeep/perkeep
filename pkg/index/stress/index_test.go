/*
Copyright 2016 The Camlistore Authors.

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

package stress

import (
	"bytes"
	"crypto/sha1"
	"flag"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/server"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvfile"
	"camlistore.org/pkg/sorted/leveldb"
	"camlistore.org/pkg/sorted/sqlite"
	"camlistore.org/pkg/types/camtypes"
)

var (
	flagTempDir  = flag.String("tempDir", os.TempDir(), "dir where we'll write all the benchmarks dirs. In case the default is on too small a partition since we may use lots of data.")
	flagBenchDir = flag.String("benchDir", "", "the directory with a prepopulated blob server, needed by any benchmark that does not start with populating a blob server & index. Run a populating bench with -nowipe to obtain such a directory.")
	flagNoWipe   = flag.Bool("nowipe", false, "do not wipe the test dir at the end of the run.")
)

func benchmarkPopulate(b *testing.B, dbname string, sortedProvider func(dbfile string) (sorted.KeyValue, error)) {
	tempDir, err := ioutil.TempDir(*flagTempDir, "camli-index-stress")
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if *flagNoWipe {
			return
		}
		os.RemoveAll(tempDir)
	}()
	dbfile := filepath.Join(tempDir, dbname)
	idx := populate(b, dbfile, sortedProvider)
	if _, err := idx.KeepInMemory(); err != nil {
		b.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		b.Fatal(err)
	}
	b.Logf("size of %v: %v", dbfile, size(b, dbfile))
}

func BenchmarkPopulateLevelDB(b *testing.B) {
	benchmarkPopulate(b, "leveldb.db", func(dbfile string) (sorted.KeyValue, error) {
		return leveldb.NewStorage(dbfile)
	})
}

func BenchmarkPopulateCznic(b *testing.B) {
	benchmarkPopulate(b, "kvfile.db", func(dbfile string) (sorted.KeyValue, error) {
		return kvfile.NewStorage(dbfile)
	})
}

func BenchmarkPopulateSQLite(b *testing.B) {
	benchmarkPopulate(b, "sqlite.db", func(dbfile string) (sorted.KeyValue, error) {
		return sqlite.NewStorage(dbfile)
	})
}

func benchmarkReindex(b *testing.B, dbname string, sortedProvider func(dbfile string) (sorted.KeyValue, error)) {
	if *flagBenchDir == "" {
		b.Skip("Reindexing benchmark needs -benchDir")
	}
	dbfile := filepath.Join(*flagBenchDir, dbname)

	idx := reindex(b, dbfile, sortedProvider)
	if _, err := idx.KeepInMemory(); err != nil {
		b.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkReindexLevelDB(b *testing.B) {
	benchmarkReindex(b, "leveldb.db", func(dbfile string) (sorted.KeyValue, error) {
		return leveldb.NewStorage(dbfile)
	})
}

func BenchmarkReindexCznic(b *testing.B) {
	benchmarkReindex(b, "kvfile.db", func(dbfile string) (sorted.KeyValue, error) {
		return kvfile.NewStorage(dbfile)
	})
}

func BenchmarkReindexSQLite(b *testing.B) {
	benchmarkReindex(b, "sqlite.db", func(dbfile string) (sorted.KeyValue, error) {
		return sqlite.NewStorage(dbfile)
	})
}

// Testing EnumerateBlobMeta because that's one of the few non-corpus index reading ops we still actually use.
func BenchmarkEnumerateBlobMetaLevelDB(b *testing.B) {
	if *flagBenchDir == "" {
		b.Skip("Enumerating benchmark needs -benchDir")
	}
	dbfile := filepath.Join(*flagBenchDir, "leveldb.db")
	enumerateMeta(b, dbfile, func(dbfile string) (sorted.KeyValue, error) {
		return leveldb.NewStorage(dbfile)
	})
}

// Testing EnumerateBlobMeta because that's one of the few non-corpus index reading ops we still actually use.
func BenchmarkEnumerateBlobMetaCznic(b *testing.B) {
	if *flagBenchDir == "" {
		b.Skip("Enumerating benchmark needs -benchDir")
	}
	dbfile := filepath.Join(*flagBenchDir, "kvfile.db")
	enumerateMeta(b, dbfile, func(dbfile string) (sorted.KeyValue, error) {
		return kvfile.NewStorage(dbfile)
	})
}

// Testing EnumerateBlobMeta because that's one of the few non-corpus index reading ops we still actually use.
func BenchmarkEnumerateBlobMetaSQLite(b *testing.B) {
	if *flagBenchDir == "" {
		b.Skip("Enumerating benchmark needs -benchDir")
	}
	dbfile := filepath.Join(*flagBenchDir, "sqlite.db")
	enumerateMeta(b, dbfile, func(dbfile string) (sorted.KeyValue, error) {
		return sqlite.NewStorage(dbfile)
	})
}

// TODO(mpl): allow for setting killTime with a flag ?

func BenchmarkInterruptLevelDB(b *testing.B) {
	if *flagBenchDir == "" {
		b.Skip("Interrupt benchmark needs -benchDir")
	}
	dbfile := filepath.Join(*flagBenchDir, "leveldb.db")

	benchmarkKillReindex(b, 1, dbfile, func(dbfile string) (sorted.KeyValue, error) {
		return leveldb.NewStorage(dbfile)
	})
}

func BenchmarkInterruptCznic(b *testing.B) {
	if *flagBenchDir == "" {
		b.Skip("Interrupt benchmark needs -benchDir")
	}
	dbfile := filepath.Join(*flagBenchDir, "kvfile.db")

	// since cznic is much slower than levelDB at reindexing, we interrupt
	// it way less often. otherwise we might even blow up the max test run time
	// (10m) anyway.
	benchmarkKillReindex(b, 10, dbfile, func(dbfile string) (sorted.KeyValue, error) {
		return kvfile.NewStorage(dbfile)
	})
}

func BenchmarkInterruptSQLite(b *testing.B) {
	if *flagBenchDir == "" {
		b.Skip("Interrupt benchmark needs -benchDir")
	}
	dbfile := filepath.Join(*flagBenchDir, "sqlite.db")

	benchmarkKillReindex(b, 15, dbfile, func(dbfile string) (sorted.KeyValue, error) {
		return sqlite.NewStorage(dbfile)
	})
}

func benchmarkAll(b *testing.B, dbname string, sortedProvider func(dbfile string) (sorted.KeyValue, error)) {
	tempDir, err := ioutil.TempDir(*flagTempDir, "camli-index-stress")
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if *flagNoWipe {
			return
		}
		os.RemoveAll(tempDir)
	}()
	dbfile := filepath.Join(tempDir, dbname)
	stress(b, dbfile, sortedProvider)
}

func BenchmarkAllLevelDB(b *testing.B) {
	benchmarkAll(b, "leveldb.db", func(dbfile string) (sorted.KeyValue, error) {
		return leveldb.NewStorage(dbfile)
	})
}

func BenchmarkAllCznic(b *testing.B) {
	benchmarkAll(b, "kvfile.db", func(dbfile string) (sorted.KeyValue, error) {
		return kvfile.NewStorage(dbfile)
	})
}

func BenchmarkAllSQLite(b *testing.B) {
	benchmarkAll(b, "sqlite.db", func(dbfile string) (sorted.KeyValue, error) {
		return sqlite.NewStorage(dbfile)
	})
}

func size(b *testing.B, dbfile string) int64 {
	fi, err := os.Stat(dbfile)
	if err != nil {
		b.Fatal(err)
	}
	if !fi.IsDir() {
		return fi.Size()
	}
	dbdir, err := os.Open(dbfile)
	if err != nil {
		b.Fatal(err)
	}
	names, err := dbdir.Readdirnames(-1)
	if err != nil {
		b.Fatal(err)
	}
	defer dbdir.Close()
	var totalSize int64
	for _, name := range names {
		// TODO(mpl): works for leveldb, but what about others ?
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		fi, err := os.Stat(filepath.Join(dbfile, name))
		if err != nil {
			b.Fatal(err)
		}
		totalSize += fi.Size()
	}
	return totalSize
}

// Populates the bs, and the index at the same time through the sync handler
func populate(b *testing.B, dbfile string,
	sortedProvider func(dbfile string) (sorted.KeyValue, error)) *index.Index {
	b.Logf("populating %v", dbfile)
	kv, err := sortedProvider(dbfile)
	if err != nil {
		b.Fatal(err)
	}
	bsRoot := filepath.Join(filepath.Dir(dbfile), "bs")
	if err := os.MkdirAll(bsRoot, 0700); err != nil {
		b.Fatal(err)
	}
	dataDir, err := os.Open("testdata")
	if err != nil {
		b.Fatal(err)
	}
	fis, err := dataDir.Readdir(-1)
	if err != nil {
		b.Fatal(err)
	}
	if len(fis) == 0 {
		b.Fatalf("no files in %s dir", "testdata")
	}

	ks := doKeyStuff(b)

	bs, err := localdisk.New(bsRoot)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := blobserver.Receive(bs, ks.pubKeyRef, strings.NewReader(ks.pubKey)); err != nil {
		b.Fatal(err)
	}
	idx, err := index.New(kv)
	if err != nil {
		b.Fatal(err)
	}
	idx.InitBlobSource(bs)
	sh := server.NewSyncHandler("/bs/", "/index/", bs, idx, sorted.NewMemoryKeyValue())

	b.ResetTimer()
	for _, v := range fis {
		f, err := os.Open(filepath.Join(dataDir.Name(), v.Name()))
		if err != nil {
			b.Fatal(err)
		}
		td := &trackDigestReader{r: f}
		fm := schema.NewFileMap(v.Name())
		fm.SetModTime(v.ModTime())
		fileRef, err := schema.WriteFileMap(bs, fm, td)
		if err != nil {
			b.Fatal(err)
		}
		f.Close()

		unsigned := schema.NewPlannedPermanode(td.Sum())
		unsigned.SetSigner(ks.pubKeyRef)
		sr := &jsonsign.SignRequest{
			UnsignedJSON: unsigned.Blob().JSON(),
			// TODO(mpl): if we make a bs that discards, replace this with a memory bs that has only the pubkey
			Fetcher:       bs,
			EntityFetcher: ks.entityFetcher,
			SignatureTime: time.Unix(0, 0),
		}
		signed, err := sr.Sign()
		if err != nil {
			b.Fatal("problem signing: " + err.Error())
		}
		pn := blob.SHA1FromString(signed)
		// N.B: use blobserver.Receive so that the blob hub gets notified, and the blob gets enqueued into the index
		if _, err := blobserver.Receive(bs, pn, strings.NewReader(signed)); err != nil {
			b.Fatal(err)
		}

		contentAttr := schema.NewSetAttributeClaim(pn, "camliContent", fileRef.String())
		claimTime, ok := fm.ModTime()
		if !ok {
			b.Fatal(err)
		}
		contentAttr.SetClaimDate(claimTime)
		contentAttr.SetSigner(ks.pubKeyRef)
		sr = &jsonsign.SignRequest{
			UnsignedJSON: contentAttr.Blob().JSON(),
			// TODO(mpl): if we make a bs that discards, replace this with a memory bs that has only the pubkey
			Fetcher:       bs,
			EntityFetcher: ks.entityFetcher,
			SignatureTime: claimTime,
		}
		signed, err = sr.Sign()
		if err != nil {
			b.Fatal("problem signing: " + err.Error())
		}
		cl := blob.SHA1FromString(signed)
		if _, err := blobserver.Receive(bs, cl, strings.NewReader(signed)); err != nil {
			b.Fatal(err)
		}
	}
	sh.IdleWait()

	return idx
}

type keyStuff struct {
	secretRingFile string
	pubKey         string
	pubKeyRef      blob.Ref
	entityFetcher  jsonsign.EntityFetcher
}

func doKeyStuff(b *testing.B) keyStuff {
	camliRootPath, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		b.Fatal("Package camlistore.org no found in $GOPATH or $GOPATH not defined")
	}
	secretRingFile := filepath.Join(camliRootPath, "pkg", "jsonsign", "testdata", "test-secring.gpg")
	pubKey := `-----BEGIN PGP PUBLIC KEY BLOCK-----

xsBNBEzgoVsBCAC/56aEJ9BNIGV9FVP+WzenTAkg12k86YqlwJVAB/VwdMlyXxvi
bCT1RVRfnYxscs14LLfcMWF3zMucw16mLlJCBSLvbZ0jn4h+/8vK5WuAdjw2YzLs
WtBcjWn3lV6tb4RJz5gtD/o1w8VWxwAnAVIWZntKAWmkcChCRgdUeWso76+plxE5
aRYBJqdT1mctGqNEISd/WYPMgwnWXQsVi3x4z1dYu2tD9uO1dkAff12z1kyZQIBQ
rexKYRRRh9IKAayD4kgS0wdlULjBU98aeEaMz1ckuB46DX3lAYqmmTEL/Rl9cOI0
Enpn/oOOfYFa5h0AFndZd1blMvruXfdAobjVABEBAAE=
=28/7
-----END PGP PUBLIC KEY BLOCK-----`
	return keyStuff{
		secretRingFile: secretRingFile,
		pubKey:         pubKey,
		pubKeyRef:      blob.SHA1FromString(pubKey),
		entityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: secretRingFile},
		},
	}
}

func reindex(b *testing.B, dbfile string,
	sortedProvider func(dbfile string) (sorted.KeyValue, error)) *index.Index {
	b.Logf("reindexing")
	if err := os.RemoveAll(dbfile); err != nil {
		b.Fatal(err)
	}
	kv, err := sortedProvider(dbfile)
	if err != nil {
		b.Fatal(err)
	}
	bs, err := localdisk.New(filepath.Join(filepath.Dir(dbfile), "bs"))
	if err != nil {
		b.Fatal(err)
	}
	idx, err := index.New(kv)
	if err != nil {
		b.Fatal(err)
	}
	idx.InitBlobSource(bs)

	b.ResetTimer()
	if err := idx.Reindex(); err != nil {
		b.Fatal(err)
	}
	return idx
}

func enumerateMeta(b *testing.B, dbfile string,
	sortedProvider func(dbfile string) (sorted.KeyValue, error)) int {
	b.Logf("enumerating meta blobs")
	kv, err := sortedProvider(dbfile)
	if err != nil {
		b.Fatal(err)
	}
	bs, err := localdisk.New(filepath.Join(filepath.Dir(dbfile), "bs"))
	if err != nil {
		b.Fatal(err)
	}
	idx, err := index.New(kv)
	if err != nil {
		b.Fatal(err)
	}
	idx.InitBlobSource(bs)
	defer idx.Close()

	ch := make(chan camtypes.BlobMeta, 100)
	go func() {
		if err := idx.EnumerateBlobMeta(nil, ch); err != nil {
			b.Fatal(err)
		}
	}()
	n := 0
	for range ch {
		n++
	}
	b.Logf("Enumerated %d meta blobs", n)
	return n
}

func benchmarkKillReindex(b *testing.B, killTimeFactor int, dbfile string,
	sortedProvider func(dbfile string) (sorted.KeyValue, error)) {
	cmd := exec.Command("go", "test", "-c")
	if strings.HasSuffix(dbfile, "sqlite.db") {
		cmd = exec.Command("go", "test", "--tags", "with_sqlite", "-c")
	}
	if err := cmd.Run(); err != nil {
		b.Fatal(err)
	}
	i := 2
	for {
		// We start and kill the reindexing, with an increasing killTime. Until we get a full index.
		if killReindex(b, dbfile, time.Duration(killTimeFactor)*time.Duration(i)*time.Second, sortedProvider) {
			break
		}
		i++
	}
}

// TODO(mpl): sync from each partial index to another one (the same dest at
// every loop). See at the end if dest indexer is "complete". Or anything else that
// is proof that the (incomplete) index is not corrupted.

// killReindex starts a reindexing in a new process, and kills that process
// after killTime. It then (naively for now ?) verifies that the kv store file is
// not corrupted by reinitializing an (possibly incomplete) index (with a corpus)
// with it. If the indexing was completed before we could kill the process, it
// returns true, false otherwise.
func killReindex(b *testing.B, dbfile string, killTime time.Duration,
	sortedProvider func(dbfile string) (sorted.KeyValue, error)) bool {
	cmd := exec.Command(os.Args[0], "-test.run=TestChildIndexer")
	cmd.Env = append(cmd.Env, "TEST_BE_CHILD=1", "TEST_BE_CHILD_DBFILE="+dbfile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		b.Fatal(err)
	}

	waitc := make(chan error)
	go func() {
		waitc <- cmd.Wait()
	}()
	fullIndex := false
	select {
	case err := <-waitc:
		if err == nil {
			// indexer finished before we killed it
			fullIndex = true
			b.Logf("Finished indexing before being killed at %v", killTime)
			break
		}
		// TODO(mpl): do better
		if err.Error() != "signal: killed" {
			b.Fatalf("unexpected (not killed) error from indexer process: %v %v %v", err, stdout.String(), stderr.String())
		}
	case <-time.After(killTime):
		if err := cmd.Process.Kill(); err != nil {
			b.Fatal(err)
		}
		err := <-waitc
		// TODO(mpl): do better
		if err != nil && err.Error() != "signal: killed" {
			b.Fatalf("unexpected (not killed) error from indexer process: %v %v %v", err, stdout.String(), stderr.String())
		}
	}

	kv, err := sortedProvider(dbfile)
	if err != nil {
		b.Fatal(err)
	}
	idx, err := index.New(kv)
	if err != nil {
		b.Fatal(err)
	}
	bs, err := localdisk.New(filepath.Join(filepath.Dir(dbfile), "bs"))
	if err != nil {
		b.Fatal(err)
	}
	idx.InitBlobSource(bs)
	if _, err := idx.KeepInMemory(); err != nil {
		b.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		b.Fatal(err)
	}
	return fullIndex
}

// Does all the tests (currently, populating, then reindexing)
func stress(b *testing.B, dbfile string, sortedProvider func(dbfile string) (sorted.KeyValue, error)) {
	idx := populate(b, dbfile, sortedProvider)
	if _, err := idx.KeepInMemory(); err != nil {
		b.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		b.Fatal(err)
	}
	b.Logf("size of %v: %v", dbfile, size(b, dbfile))

	idx = reindex(b, dbfile, sortedProvider)
	if _, err := idx.KeepInMemory(); err != nil {
		b.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		b.Fatal(err)
	}
	enumerateMeta(b, dbfile, sortedProvider)
}

// trackDigestReader is an io.Reader wrapper which records the digest of what it reads.
type trackDigestReader struct {
	r io.Reader
	h hash.Hash
}

func (t *trackDigestReader) Read(p []byte) (n int, err error) {
	if t.h == nil {
		t.h = sha1.New()
	}
	n, err = t.r.Read(p)
	t.h.Write(p[:n])
	return
}

func (t *trackDigestReader) Sum() string {
	return fmt.Sprintf("sha1-%x", t.h.Sum(nil))
}

func TestChildIndexer(t *testing.T) {
	if os.Getenv("TEST_BE_CHILD") != "1" {
		t.Skip("not a real test; used as a child process by the benchmarks")
	}
	dbfile := os.Getenv("TEST_BE_CHILD_DBFILE")
	if dbfile == "" {
		log.Fatal("empty TEST_BE_CHILD_DBFILE")
	}
	if err := os.RemoveAll(dbfile); err != nil {
		log.Fatal(err)
	}
	var kv sorted.KeyValue
	var err error
	switch {
	case strings.HasSuffix(dbfile, "leveldb.db"):
		kv, err = leveldb.NewStorage(dbfile)
	case strings.HasSuffix(dbfile, "kvfile.db"):
		kv, err = kvfile.NewStorage(dbfile)
	case strings.HasSuffix(dbfile, "sqlite.db"):
		kv, err = sqlite.NewStorage(dbfile)
	default:
		log.Fatalf("unknown sorted provider for %v", dbfile)
	}
	if err != nil {
		log.Fatal(err)
	}
	bs, err := localdisk.New(filepath.Join(filepath.Dir(dbfile), "bs"))
	if err != nil {
		log.Fatal(err)
	}
	idx, err := index.New(kv)
	if err != nil {
		log.Fatal(err)
	}
	idx.InitBlobSource(bs)
	defer func() {
		if err := idx.Close(); err != nil {
			log.Fatal(err)
		}
	}()
	if err := idx.Reindex(); err != nil {
		log.Fatal(err)
	}
}
