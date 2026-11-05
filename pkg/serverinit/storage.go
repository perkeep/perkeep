/*
Copyright 2026 The Perkeep Authors

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

package serverinit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"perkeep.org/pkg/types/serverconfig"
)

// storageNameRx matches the names of the high-level config's storage,
// which appear in handlers' prefixes and file names.
var storageNameRx = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// reservedStorageNames are the names of the storages and syncs that the
// high-level config makes itself, as with /sync-to-s3/, which a storage
// of its Storage mustn't have even if they aren't made, so that a config
// that adds one later doesn't conflict with it.
var reservedStorageNames = []string{"s3", "b2", "googlecloudstorage", "googledrive", "index"}

// addStorage adds the high-level config's Storage, each at /sto-<name>/,
// and the syncs to those in ReplicateTo, at /sync-to-<name>/. It's called
// after all of the other handlers are added, to check that its handlers'
// prefixes aren't theirs.
func (b *lowBuilder) addStorage() error {
	conf := b.high
	if len(conf.Storage) == 0 && len(conf.ReplicateTo) == 0 && len(conf.UnwiredStorage) == 0 {
		return nil
	}

	// Each storage is used one way: replicated to, or unwired.
	uses := make(map[string]string) // by name
	for _, list := range []struct {
		field string
		names []string
	}{{"replicateTo", conf.ReplicateTo}, {"unwiredStorage", conf.UnwiredStorage}} {
		for _, name := range list.names {
			if _, ok := conf.Storage[name]; !ok {
				return fmt.Errorf("%s: %q is not in storage", list.field, name)
			}
			if prev, ok := uses[name]; ok {
				if prev == list.field {
					return fmt.Errorf("%s: %q is listed twice", list.field, name)
				}
				return fmt.Errorf("storage %q is in both %s and %s; it must be in one", name, prev, list.field)
			}
			uses[name] = list.field
		}
	}
	if len(conf.ReplicateTo) > 0 && conf.BlobPath == "" {
		return fmt.Errorf("replicateTo requires blobPath, for its sync queues")
	}

	names := make([]string, 0, len(conf.Storage))
	for name := range conf.Storage {
		names = append(names, name)
	}
	slices.Sort(names)
	dirs := make(map[string]string) // the storages' clean dirs, by name; "" for blobPath's
	if conf.BlobPath != "" {
		dirs[""] = filepath.Clean(conf.BlobPath)
	}
	for _, name := range names {
		s := conf.Storage[name]
		switch {
		case !storageNameRx.MatchString(name):
			return fmt.Errorf("storage %q: names must be lowercase letters, digits and single hyphens", name)
		case slices.Contains(reservedStorageNames, name):
			return fmt.Errorf("storage %q: the name is reserved for the storage that the high-level config makes itself", name)
		case s == nil:
			return fmt.Errorf("storage %q: no type", name)
		case uses[name] == "":
			return fmt.Errorf("storage %q isn't used: list it in replicateTo, or in unwiredStorage to define it without using it", name)
		}
		var err error
		switch {
		case s.Local != nil:
			err = b.addLocalStorage(name, s.Type, s.Local, dirs)
		case s.S3 != nil:
			err = b.addS3Storage(name, s.S3)
		default:
			err = fmt.Errorf("storage %q: type %q has no fields", name, s.Type)
		}
		if err != nil {
			return err
		}
		if uses[name] == "replicateTo" {
			p := "/sync-to-" + name + "/"
			if err := b.addUniquePrefix(name, p, "sync", args{
				"from": "/bs/",
				"to":   "/sto-" + name + "/",
				"queue": b.thatQueueUnlessMemory(map[string]any{
					"type": b.kvFileType(),
					"file": filepath.Join(conf.BlobPath, "sync-to-"+name+"-queue."+b.kvFileType()),
				}),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// addLocalStorage adds the handlers of the storage name, of the local type
// typ, in the layouts that blobPath can have, checking that its dir
// doesn't overlap those in dirs, the dirs of blobPath (by "") and the
// storages before it, and adding its own.
func (b *lowBuilder) addLocalStorage(name, typ string, s *serverconfig.LocalStorage, dirs map[string]string) error {
	if !rooted(s.Dir) {
		return fmt.Errorf("storage %q: dir %q must be an absolute path", name, s.Dir)
	}
	dir := filepath.Clean(s.Dir)
	for other, otherDir := range dirs {
		if within(dir, otherDir) || within(otherDir, dir) {
			what := "blobPath"
			if other != "" {
				what = fmt.Sprintf("storage %q's dir", other)
			}
			return fmt.Errorf("storage %q: dir %s overlaps %s, %s", name, dir, what, otherDir)
		}
	}
	dirs[name] = dir

	prefix := "/sto-" + name + "/"
	kv := b.kvFileType()
	switch typ {
	case "blobpacked":
		loose, packed := "/sto-"+name+"-loose/", "/sto-"+name+"-packed/"
		if err := b.addUniquePrefix(name, loose, "storage-filesystem", args{"path": dir}); err != nil {
			return err
		}
		if err := b.addUniquePrefix(name, packed, "storage-filesystem", args{"path": filepath.Join(dir, "packed")}); err != nil {
			return err
		}
		return b.addUniquePrefix(name, prefix, "storage-blobpacked", args{
			"smallBlobs": loose,
			"largeBlobs": packed,
			"metaIndex": map[string]any{
				"type": kv,
				"file": filepath.Join(dir, "packed", "packindex."+kv),
			},
		})
	case "filesystem":
		return b.addUniquePrefix(name, prefix, "storage-filesystem", args{"path": dir})
	case "diskpacked":
		return b.addUniquePrefix(name, prefix, "storage-diskpacked", args{
			"path": dir,
			"metaIndex": map[string]any{
				"type": kv,
				"file": filepath.Join(dir, "diskpacked-index."+kv),
			},
		})
	}
	return fmt.Errorf("storage %q: unknown local type %q", name, typ)
}

// addS3Storage adds the handlers of the storage name, of type "s3": as the
// high-level config's s3 storage makes, blobpacked or not, but with its
// packed blobs' index in a file of its own, packindex-<name>.<type>,
// beside the server's index, or else in blobPath.
func (b *lowBuilder) addS3Storage(name string, s *serverconfig.S3Storage) error {
	accessKey, secret := s.AccessKey, s.SecretKey
	if err := resolveCredentials(fmt.Sprintf("storage %q", name), &accessKey, &secret); err != nil {
		return err
	}
	switch {
	case s.Bucket == "":
		return fmt.Errorf("storage %q: no bucket", name)
	case accessKey == "" || secret == "":
		return fmt.Errorf("storage %q: accessKey and secretKey are required", name)
	}
	s3Args := func(bucket string) args {
		a := args{
			"bucket":                bucket,
			"aws_access_key":        accessKey,
			"aws_secret_access_key": secret,
		}
		if s.Hostname != "" {
			a["hostname"] = s.Hostname
		}
		return a
	}

	prefix := "/sto-" + name + "/"
	packed := b.packRelated("s3")
	if s.Packed != nil {
		packed = *s.Packed
	}
	if !packed {
		return b.addUniquePrefix(name, prefix, "storage-s3", s3Args(s.Bucket))
	}
	indexDir := b.indexFileDir()
	if indexDir == "" {
		indexDir = b.high.BlobPath
	}
	if indexDir == "" {
		return fmt.Errorf("storage %q: a packed s3 storage needs a file-based index or blobPath, for its packed blobs' index", name)
	}
	loose, packedPrefix := "/sto-"+name+"-loose/", "/sto-"+name+"-packed/"
	if err := b.addUniquePrefix(name, loose, "storage-s3", s3Args(path.Join(s.Bucket, "loose"))); err != nil {
		return err
	}
	if err := b.addUniquePrefix(name, packedPrefix, "storage-s3", s3Args(path.Join(s.Bucket, "packed"))); err != nil {
		return err
	}
	kv := b.kvFileType()
	return b.addUniquePrefix(name, prefix, "storage-blobpacked", args{
		"smallBlobs": loose,
		"largeBlobs": packedPrefix,
		"metaIndex": map[string]any{
			"type": kv,
			"file": filepath.Join(indexDir, "packindex-"+name+"."+kv),
		},
	})
}

// addUniquePrefix adds the handler at prefix p for the storage name,
// unless the config has a handler at p already.
func (b *lowBuilder) addUniquePrefix(name, p, handler string, a args) error {
	if b.hasPrefix(p) {
		return fmt.Errorf("storage %q: its handler at %s conflicts with another of that prefix", name, p)
	}
	b.addPrefix(p, handler, a)
	return nil
}

// rooted reports whether dir is absolute, or, on Windows, at least rooted,
// as \dir is, on the current drive: not relative to the current directory.
func rooted(dir string) bool {
	return filepath.IsAbs(dir) || dir != "" && os.IsPathSeparator(dir[0])
}

// within reports whether dir is parent, or within it. Both are clean.
func within(dir, parent string) bool {
	rel, err := filepath.Rel(parent, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
