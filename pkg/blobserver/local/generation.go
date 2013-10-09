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

/*
Package local implements functionality common to both the "localdisk" and
"diskpacked" storage mechanisms.
*/
package local

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

type Generationer struct {
	root string
}

// NewGenerationer returns a struct for implementing blobserver.Generationer
func NewGenerationer(rootDir string) *Generationer {
	return &Generationer{rootDir}
}

func (g Generationer) generationFile() string {
	return filepath.Join(g.root, "GENERATION.dat")
}

// StorageGeneration returns the generation's initialization time,
// and the random string. If the GENERATION.dat file does not exist under
// root yet, then creates it now (by calling ResetGeneration).
func (g Generationer) StorageGeneration() (initTime time.Time, random string, err error) {
	f, err := os.Open(g.generationFile())
	if os.IsNotExist(err) {
		if err = g.ResetStorageGeneration(); err != nil {
			return
		}
		f, err = os.Open(g.generationFile())
	}
	if err != nil {
		return
	}
	defer f.Close()
	bs, err := ioutil.ReadAll(f)
	if err != nil {
		return
	}
	if i := bytes.IndexByte(bs, '\n'); i != -1 {
		bs = bs[:i]
	}
	if fi, err := f.Stat(); err == nil {
		initTime = fi.ModTime()
	}
	random = string(bs)
	return
}

// ResetStorageGeneration reinitializes the generation by recreating the
// GENERATION.dat file with a new random string
func (g Generationer) ResetStorageGeneration() error {
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, rand.Reader, 20); err != nil {
		return err
	}
	hex := fmt.Sprintf("%x", buf.Bytes())
	buf.Reset()
	buf.WriteString(hex)
	buf.WriteString(`

This file's random string on the first line is an optimization and
paranoia facility for clients.

If the client sees the same random string in multiple upload sessions,
it assumes that the blobserver still has all the same blobs, and also
it's the same server.  This mechanism is not fundamental to
Camlistore's operation: the client could also check each blob before
uploading, or enumerate all blobs from the server too.  This is purely
an optimization so clients can mix this value into their "is this file
uploaded?" local cache keys.

If you deleted any blobs (or suspect any were corrupted), it's best to
delete this file so clients can safely re-upload them.

`)

	return ioutil.WriteFile(g.generationFile(), buf.Bytes(), 0644)
}
