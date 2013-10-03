/*
Copyright 2013 Google Inc.

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

package diskpacked

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
    "io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index/kvfile"
	"camlistore.org/pkg/sorted"
	"camlistore.org/third_party/github.com/camlistore/lock"
)

// Reindex rewrites the index files of the diskpacked .pack files
func Reindex(root string, overwrite bool) (err error) {
	// there is newStorage, but that may open a file for writing
	var s = &storage{root: root}
	index, err := kvfile.NewStorage(filepath.Join(root, "index.kv"))
	if err != nil {
		return err
	}
	defer func() {
		closeErr := index.Close()
		// just returning the first error - if the index or disk is corrupt
		// and can't close, it's very likely these two errors are related and
		// have the same root cause.
		if err == nil {
			err = closeErr
		}
	}()

	verbose := false // TODO: use env var?
	for i := int64(0); i >= 0; i++ {
		fh, err := os.Open(s.filename(i))
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return err
		}
		err = reindexOne(index, overwrite, verbose, fh, fh.Name(), i)
		fh.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func reindexOne(index sorted.KeyValue, overwrite, verbose bool, r io.ReadSeeker, name string, packId int64) error {
	l, err := lock.Lock(name + ".lock")
	defer l.Close()

	var pos, size int64

	errAt := func(prefix, suffix string) error {
		if prefix != "" {
			prefix = prefix + " "
		}
		if suffix != "" {
			suffix = " " + suffix
		}
		return fmt.Errorf(prefix+"at %d (0x%x) in %q:"+suffix, pos, pos, name)
	}

	var batch sorted.BatchMutation
	if overwrite {
		batch = index.BeginBatch()
	}

	allOk := true
	br := bufio.NewReaderSize(r, 512)
	for {
		if b, err := br.ReadByte(); err != nil {
			if err == io.EOF {
				break
			}
			return errAt("error while reading", err.Error())
		} else if b != '[' {
			return errAt(fmt.Sprintf("found byte 0x%x", b), "but '[' should be here!")
		}
		chunk, err := br.ReadSlice(']')
		if err != nil {
			if err == io.EOF {
				break
			}
			return errAt("error reading blob header", err.Error())
		}
		m := len(chunk)
		chunk = chunk[:m-1]
		i := bytes.IndexByte(chunk, byte(' '))
		if i <= 0 {
			return errAt("", fmt.Sprintf("bad header format (no space in %q)", chunk))
		}
		if size, err = strconv.ParseInt(string(chunk[i+1:]), 10, 64); err != nil {
			return errAt(fmt.Sprintf("cannot parse size %q as int", chunk[i+1:]), err.Error())
		}
		ref, ok := blob.Parse(string(chunk[:i]))
		if !ok {
			return errAt("", fmt.Sprintf("cannot parse %q as blobref", chunk[:i]))
		}
		if verbose {
			log.Printf("found %s at %d", ref, pos)
		}

		meta := blobMeta{packId, pos + 1 + int64(m), size}.String()
		if overwrite && batch != nil {
			batch.Set(ref.String(), meta)
		} else {
			if old, err := index.Get(ref.String()); err != nil {
				allOk = false
				if err == sorted.ErrNotFound {
					log.Println(ref.String() + ": cannot find in index!")
				} else {
					log.Println(ref.String()+": error getting from index: ", err.Error())
				}
			} else if old != meta {
				allOk = false
				log.Printf("%s: index mismatch - index=%s data=%s", ref.String(), old, meta)
			}
		}

		pos += 1 + int64(m)
		// TODO(tgulacsi78): not just seek, but check the hashes of the files
		// maybe with a different command-line flag, only.
		if pos, err = r.Seek(pos+size, 0); err != nil {
			return errAt("", "cannot seek +"+strconv.FormatInt(size, 10)+" bytes")
		}
		// drain the buffer after the underlying reader Seeks
		io.CopyN(ioutil.Discard, br, int64(br.Buffered()))
	}

	if overwrite && batch != nil {
		log.Printf("overwriting %s from %s", index, name)
		if err = index.CommitBatch(batch); err != nil {
			return err
		}
	} else if !allOk {
		return fmt.Errorf("index does not match data in %q", name)
	}
	return nil
}
