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

package serverinit

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/types/serverconfig"
	"go4.org/jsonconfig"

	"go4.org/wkfs"
)

var (
	tempDir = os.TempDir
	noMkdir bool // for tests to not call os.Mkdir
)

type tlsOpts struct {
	httpsCert string
	httpsKey  string
}

// genLowLevelConfig returns a low-level config from a high-level config.
func genLowLevelConfig(conf *serverconfig.Config) (lowLevelConf *Config, err error) {
	b := &lowBuilder{
		high: conf,
		low: jsonconfig.Obj{
			"prefixes": make(map[string]interface{}),
		},
	}
	return b.build()
}

// A lowBuilder builds a low-level config from a high-level config.
type lowBuilder struct {
	high *serverconfig.Config // high-level config (input)
	low  jsonconfig.Obj       // low-level handler config (output)
}

// args is an alias for map[string]interface{} just to cut down on
// noise below.  But we take care to convert it back to
// map[string]interface{} in the one place where we accept it.
type args map[string]interface{}

func (b *lowBuilder) addPrefix(at, handler string, a args) {
	v := map[string]interface{}{
		"handler": handler,
	}
	if a != nil {
		v["handlerArgs"] = (map[string]interface{})(a)
	}
	b.low["prefixes"].(map[string]interface{})[at] = v
}

func (b *lowBuilder) hasPrefix(p string) bool {
	_, ok := b.low["prefixes"].(map[string]interface{})[p]
	return ok
}

func (b *lowBuilder) runIndex() bool          { return b.high.RunIndex.Get() }
func (b *lowBuilder) copyIndexToMemory() bool { return b.high.CopyIndexToMemory.Get() }

// dbName returns which database to use for the provided user ("of").
// The user should be a key as described in pkg/types/serverconfig/config.go's
// description of DBNames: "index", "queue-sync-to-index", etc.
func (b *lowBuilder) dbName(of string) string {
	if v, ok := b.high.DBNames[of]; ok && v != "" {
		return v
	}
	if of == "index" {
		if b.high.DBName != "" {
			return b.high.DBName
		}
		username := osutil.Username()
		if username == "" {
			envVar := "USER"
			if runtime.GOOS == "windows" {
				envVar += "NAME"
			}
			return "camlistore_index"
		}
		return "camli" + username
	}
	return ""
}

var errNoOwner = errors.New("no owner")

// Error is errNoOwner if no identity configured
func (b *lowBuilder) searchOwner() (br blob.Ref, err error) {
	if b.high.Identity == "" {
		return br, errNoOwner
	}
	entity, err := jsonsign.EntityFromSecring(b.high.Identity, b.high.IdentitySecretRing)
	if err != nil {
		return br, err
	}
	armoredPublicKey, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		return br, err
	}
	return blob.SHA1FromString(armoredPublicKey), nil
}

func (b *lowBuilder) addPublishedConfig(tlsO *tlsOpts) error {
	published := b.high.Publish
	for k, v := range published {
		if v.CamliRoot == "" {
			return fmt.Errorf("Missing \"camliRoot\" key in configuration for %s.", k)
		}
		if v.GoTemplate == "" {
			return fmt.Errorf("Missing \"goTemplate\" key in configuration for %s.", k)
		}

		appConfig := map[string]interface{}{
			"camliRoot":  v.CamliRoot,
			"cacheRoot":  v.CacheRoot,
			"goTemplate": v.GoTemplate,
		}
		if v.HTTPSCert != "" && v.HTTPSKey != "" {
			// user can specify these directly in the publish section
			appConfig["httpsCert"] = v.HTTPSCert
			appConfig["httpsKey"] = v.HTTPSKey
		} else {
			// default to Camlistore parameters, if any
			if tlsO != nil {
				appConfig["httpsCert"] = tlsO.httpsCert
				appConfig["httpsKey"] = tlsO.httpsKey
			}
		}
		a := args{
			"program":   v.Program,
			"appConfig": appConfig,
		}
		if v.BaseURL != "" {
			a["baseURL"] = v.BaseURL
		}
		program := "publisher"
		if v.Program != "" {
			program = v.Program
		}
		a["program"] = program
		b.addPrefix(k, "app", a)
	}
	return nil
}

// kvFileType returns the file based sorted type defined for index storage, if
// any. It defaults to "leveldb" otherwise.
func (b *lowBuilder) kvFileType() string {
	switch {
	case b.high.SQLite != "":
		return "sqlite"
	case b.high.KVFile != "":
		return "kv"
	case b.high.LevelDB != "":
		return "leveldb"
	default:
		return sorted.DefaultKVFileType
	}
}

func (b *lowBuilder) addUIConfig() {
	args := map[string]interface{}{
		"cache": "/cache/",
	}
	if b.high.SourceRoot != "" {
		args["sourceRoot"] = b.high.SourceRoot
	}
	var thumbCache map[string]interface{}
	if b.high.BlobPath != "" {
		thumbCache = map[string]interface{}{
			"type": b.kvFileType(),
			"file": filepath.Join(b.high.BlobPath, "thumbmeta."+b.kvFileType()),
		}
	}
	if thumbCache == nil {
		sorted, err := b.sortedStorage("ui_thumbcache")
		if err == nil {
			thumbCache = sorted
		}
	}
	if thumbCache != nil {
		args["scaledImage"] = thumbCache
	}
	b.addPrefix("/ui/", "ui", args)
}

func (b *lowBuilder) mongoIndexStorage(confStr, sortedType string) (map[string]interface{}, error) {
	dbName := b.dbName(sortedType)
	if dbName == "" {
		return nil, fmt.Errorf("no database name configured for sorted store %q", sortedType)
	}
	fields := strings.Split(confStr, "@")
	if len(fields) == 2 {
		host := fields[1]
		fields = strings.Split(fields[0], ":")
		if len(fields) == 2 {
			user, pass := fields[0], fields[1]
			return map[string]interface{}{
				"type":     "mongo",
				"host":     host,
				"user":     user,
				"password": pass,
				"database": dbName,
			}, nil
		}
	}
	return nil, errors.New("Malformed mongo config string; want form: \"user:password@host\"")
}

// parses "user@host:password", which you think would be easy, but we
// documented this format without thinking about port numbers, so this
// uses heuristics to guess what extra colons mean.
func parseUserHostPass(v string) (user, host, password string, ok bool) {
	f := strings.SplitN(v, "@", 2)
	if len(f) != 2 {
		return
	}
	user = f[0]
	f = strings.Split(f[1], ":")
	if len(f) < 2 {
		return "", "", "", false
	}
	host = f[0]
	f = f[1:]
	if len(f) >= 2 {
		if _, err := strconv.ParseUint(f[0], 10, 16); err == nil {
			host = host + ":" + f[0]
			f = f[1:]
		}
	}
	password = strings.Join(f, ":")
	ok = true
	return
}

func (b *lowBuilder) dbIndexStorage(rdbms string, confStr string, sortedType string) (map[string]interface{}, error) {
	dbName := b.dbName(sortedType)
	if dbName == "" {
		return nil, fmt.Errorf("no database name configured for sorted store %q", sortedType)
	}
	user, host, password, ok := parseUserHostPass(confStr)
	if !ok {
		return nil, fmt.Errorf("Malformed %s config string. Want: \"user@host:password\"", rdbms)
	}
	return map[string]interface{}{
		"type":     rdbms,
		"host":     host,
		"user":     user,
		"password": password,
		"database": b.dbName(sortedType),
	}, nil
}

func (b *lowBuilder) sortedStorage(sortedType string) (map[string]interface{}, error) {
	return b.sortedStorageAt(sortedType, "")
}

// filePrefix gives a file path of where to put the database. It can be omitted by
// some sorted implementations, but is required by others.
// The filePrefix should be to a file, not a directory, and should not end in a ".ext" extension.
// An extension like ".kv" or ".sqlite" will be added.
func (b *lowBuilder) sortedStorageAt(sortedType, filePrefix string) (map[string]interface{}, error) {
	if b.high.MySQL != "" {
		return b.dbIndexStorage("mysql", b.high.MySQL, sortedType)
	}
	if b.high.PostgreSQL != "" {
		return b.dbIndexStorage("postgres", b.high.PostgreSQL, sortedType)
	}
	if b.high.Mongo != "" {
		return b.mongoIndexStorage(b.high.Mongo, sortedType)
	}
	if b.high.MemoryIndex {
		return map[string]interface{}{
			"type": "memory",
		}, nil
	}
	if sortedType != "index" && filePrefix == "" {
		return nil, fmt.Errorf("internal error: use of sortedStorageAt with a non-index type and no file location for non-database sorted implementation")
	}
	// dbFile returns path directly if sortedType == "index", else it returns filePrefix+"."+ext.
	dbFile := func(path, ext string) string {
		if sortedType == "index" {
			return path
		}
		return filePrefix + "." + ext
	}
	if b.high.SQLite != "" {
		return map[string]interface{}{
			"type": "sqlite",
			"file": dbFile(b.high.SQLite, "sqlite"),
		}, nil
	}
	if b.high.KVFile != "" {
		return map[string]interface{}{
			"type": "kv",
			"file": dbFile(b.high.KVFile, "kv"),
		}, nil
	}
	if b.high.LevelDB != "" {
		return map[string]interface{}{
			"type": "leveldb",
			"file": dbFile(b.high.LevelDB, "leveldb"),
		}, nil
	}
	panic("internal error: sortedStorageAt didn't find a sorted implementation")
}

func (b *lowBuilder) thatQueueUnlessMemory(thatQueue map[string]interface{}) (queue map[string]interface{}) {
	if b.high.MemoryStorage {
		return map[string]interface{}{
			"type": "memory",
		}
	}
	return thatQueue
}

func (b *lowBuilder) addS3Config(s3 string) error {
	f := strings.SplitN(s3, ":", 4)
	if len(f) < 3 {
		return errors.New(`genconfig: expected "s3" field to be of form "access_key_id:secret_access_key:bucket[/optional/dir][:hostname]"`)
	}
	accessKey, secret, bucket := f[0], f[1], f[2]
	var hostname string
	if len(f) == 4 {
		hostname = f[3]
	}
	isReplica := b.hasPrefix("/bs/")
	s3Prefix := ""
	s3Args := args{
		"aws_access_key":        accessKey,
		"aws_secret_access_key": secret,
		"bucket":                bucket,
	}
	if hostname != "" {
		s3Args["hostname"] = hostname
	}
	if isReplica {
		s3Prefix = "/sto-s3/"
		b.addPrefix(s3Prefix, "storage-s3", s3Args)
		if b.high.BlobPath == "" && !b.high.MemoryStorage {
			panic("unexpected empty blobpath with sync-to-s3")
		}
		b.addPrefix("/sync-to-s3/", "sync", args{
			"from": "/bs/",
			"to":   s3Prefix,
			"queue": b.thatQueueUnlessMemory(
				map[string]interface{}{
					"type": b.kvFileType(),
					"file": filepath.Join(b.high.BlobPath, "sync-to-s3-queue."+b.kvFileType()),
				}),
		})
		return nil
	}

	// TODO(mpl): s3CacheBucket
	// See https://camlistore.org/issue/85
	b.addPrefix("/cache/", "storage-filesystem", args{
		"path": filepath.Join(tempDir(), "camli-cache"),
	})

	s3Prefix = "/bs/"
	if !b.high.PackRelated {
		b.addPrefix(s3Prefix, "storage-s3", s3Args)
		return nil
	}
	packedS3Args := func(bucket string) args {
		a := args{
			"bucket":                bucket,
			"aws_access_key":        accessKey,
			"aws_secret_access_key": secret,
		}
		if hostname != "" {
			a["hostname"] = hostname
		}
		return a
	}

	b.addPrefix("/bs-loose/", "storage-s3", packedS3Args(path.Join(bucket, "loose")))
	b.addPrefix("/bs-packed/", "storage-s3", packedS3Args(path.Join(bucket, "packed")))

	// TODO(mpl): I think that should be the job of sortedStorageAt, shouldn't
	// it? It could use its sortedType argument to create a file path if the
	// filePrefix argument is empty.
	var packIndexDir string
	if b.high.SQLite != "" {
		packIndexDir = b.high.SQLite
	} else if b.high.KVFile != "" {
		packIndexDir = b.high.KVFile
	} else if b.high.LevelDB != "" {
		packIndexDir = b.high.LevelDB
	}
	blobPackedIndex, err := b.sortedStorageAt("blobpacked_index", filepath.Join(filepath.Dir(packIndexDir), "packindex"))
	if err != nil {
		return err
	}
	b.addPrefix(s3Prefix, "storage-blobpacked", args{
		"smallBlobs": "/bs-loose/",
		"largeBlobs": "/bs-packed/",
		"metaIndex":  blobPackedIndex,
	})

	return nil
}

func (b *lowBuilder) addGoogleDriveConfig(v string) error {
	f := strings.SplitN(v, ":", 4)
	if len(f) != 4 {
		return errors.New(`genconfig: expected "googledrive" field to be of form "client_id:client_secret:refresh_token:parent_id"`)
	}
	clientId, secret, refreshToken, parentId := f[0], f[1], f[2], f[3]

	isPrimary := !b.hasPrefix("/bs/")
	prefix := ""
	if isPrimary {
		prefix = "/bs/"
		if b.high.PackRelated {
			return errors.New("TODO: finish packRelated support for Google Drive")
		}
	} else {
		prefix = "/sto-googledrive/"
	}
	b.addPrefix(prefix, "storage-googledrive", args{
		"parent_id": parentId,
		"auth": map[string]interface{}{
			"client_id":     clientId,
			"client_secret": secret,
			"refresh_token": refreshToken,
		},
	})

	if isPrimary {
		b.addPrefix("/cache/", "storage-filesystem", args{
			"path": filepath.Join(tempDir(), "camli-cache"),
		})
	} else {
		b.addPrefix("/sync-to-googledrive/", "sync", args{
			"from": "/bs/",
			"to":   prefix,
			"queue": b.thatQueueUnlessMemory(
				map[string]interface{}{
					"type": b.kvFileType(),
					"file": filepath.Join(b.high.BlobPath, "sync-to-googledrive-queue."+b.kvFileType()),
				}),
		})
	}

	return nil
}

var errGCSUsage = errors.New(`genconfig: expected "googlecloudstorage" field to be of form "client_id:client_secret:refresh_token:bucket[/dir/]" or ":bucketname[/dir/]"`)

func (b *lowBuilder) addGoogleCloudStorageConfig(v string) error {
	var clientID, secret, refreshToken, bucket string
	f := strings.SplitN(v, ":", 4)
	switch len(f) {
	default:
		return errGCSUsage
	case 4:
		clientID, secret, refreshToken, bucket = f[0], f[1], f[2], f[3]
	case 2:
		if f[0] != "" {
			return errGCSUsage
		}
		bucket = f[1]
		clientID = "auto"
	}

	isReplica := b.hasPrefix("/bs/")
	if isReplica {
		gsPrefix := "/sto-googlecloudstorage/"
		b.addPrefix(gsPrefix, "storage-googlecloudstorage", args{
			"bucket": bucket,
			"auth": map[string]interface{}{
				"client_id":     clientID,
				"client_secret": secret,
				"refresh_token": refreshToken,
			},
		})

		b.addPrefix("/sync-to-googlecloudstorage/", "sync", args{
			"from": "/bs/",
			"to":   gsPrefix,
			"queue": b.thatQueueUnlessMemory(
				map[string]interface{}{
					"type": b.kvFileType(),
					"file": filepath.Join(b.high.BlobPath, "sync-to-googlecloud-queue."+b.kvFileType()),
				}),
		})
		return nil
	}

	// TODO: cacheBucket like s3CacheBucket?
	b.addPrefix("/cache/", "storage-filesystem", args{
		"path": filepath.Join(tempDir(), "camli-cache"),
	})
	if b.high.PackRelated {
		b.addPrefix("/bs-loose/", "storage-googlecloudstorage", args{
			"bucket": bucket + "/loose",
			"auth": map[string]interface{}{
				"client_id":     clientID,
				"client_secret": secret,
				"refresh_token": refreshToken,
			},
		})
		b.addPrefix("/bs-packed/", "storage-googlecloudstorage", args{
			"bucket": bucket + "/packed",
			"auth": map[string]interface{}{
				"client_id":     clientID,
				"client_secret": secret,
				"refresh_token": refreshToken,
			},
		})
		blobPackedIndex, err := b.sortedStorageAt("blobpacked_index", "")
		if err != nil {
			return err
		}
		b.addPrefix("/bs/", "storage-blobpacked", args{
			"smallBlobs": "/bs-loose/",
			"largeBlobs": "/bs-packed/",
			"metaIndex":  blobPackedIndex,
		})
		return nil
	}
	b.addPrefix("/bs/", "storage-googlecloudstorage", args{
		"bucket": bucket,
		"auth": map[string]interface{}{
			"client_id":     clientID,
			"client_secret": secret,
			"refresh_token": refreshToken,
		},
	})

	return nil
}

// indexFileDir returns the directory of the sqlite or kv file, or the
// empty string.
func (b *lowBuilder) indexFileDir() string {
	switch {
	case b.high.SQLite != "":
		return filepath.Dir(b.high.SQLite)
	case b.high.KVFile != "":
		return filepath.Dir(b.high.KVFile)
	case b.high.LevelDB != "":
		return filepath.Dir(b.high.LevelDB)
	}
	return ""
}

func (b *lowBuilder) syncToIndexArgs() (map[string]interface{}, error) {
	a := map[string]interface{}{
		"from": "/bs/",
		"to":   "/index/",
	}

	const sortedType = "queue-sync-to-index"
	if dbName := b.dbName(sortedType); dbName != "" {
		qj, err := b.sortedStorage(sortedType)
		if err != nil {
			return nil, err
		}
		a["queue"] = qj
		return a, nil
	}

	// TODO: currently when using s3, the index must be
	// sqlite or kvfile, since only through one of those
	// can we get a directory.
	if !b.high.MemoryStorage && b.high.BlobPath == "" && b.indexFileDir() == "" {
		// We don't actually have a working sync handler, but we keep a stub registered
		// so it can be referred to from other places.
		// See http://camlistore.org/issue/201
		a["idle"] = true
		return a, nil
	}

	dir := b.high.BlobPath
	if dir == "" {
		dir = b.indexFileDir()
	}
	a["queue"] = b.thatQueueUnlessMemory(
		map[string]interface{}{
			"type": b.kvFileType(),
			"file": filepath.Join(dir, "sync-to-index-queue."+b.kvFileType()),
		})

	return a, nil
}

func (b *lowBuilder) genLowLevelPrefixes() error {
	root := "/bs/"
	pubKeyDest := root
	if b.runIndex() {
		root = "/bs-and-maybe-also-index/"
		pubKeyDest = "/bs-and-index/"
	}

	rootArgs := map[string]interface{}{
		"stealth":      false,
		"blobRoot":     root,
		"helpRoot":     "/help/",
		"statusRoot":   "/status/",
		"jsonSignRoot": "/sighelper/",
	}
	if b.high.OwnerName != "" {
		rootArgs["ownerName"] = b.high.OwnerName
	}
	if b.runIndex() {
		rootArgs["searchRoot"] = "/my-search/"
	}
	b.addPrefix("/", "root", rootArgs)
	b.addPrefix("/setup/", "setup", nil)
	b.addPrefix("/status/", "status", nil)
	b.addPrefix("/help/", "help", nil)

	importerArgs := args{}
	if b.high.Flickr != "" {
		importerArgs["flickr"] = map[string]interface{}{
			"clientSecret": b.high.Flickr,
		}
	}
	if b.high.Picasa != "" {
		importerArgs["picasa"] = map[string]interface{}{
			"clientSecret": b.high.Picasa,
		}
	}
	if b.runIndex() {
		b.addPrefix("/importer/", "importer", importerArgs)
	}

	if path := b.high.ShareHandlerPath; path != "" {
		b.addPrefix(path, "share", args{
			"blobRoot": "/bs/",
		})
	}

	b.addPrefix("/sighelper/", "jsonsign", args{
		"secretRing":    b.high.IdentitySecretRing,
		"keyId":         b.high.Identity,
		"publicKeyDest": pubKeyDest,
	})

	storageType := "filesystem"
	if b.high.PackBlobs {
		storageType = "diskpacked"
	}
	if b.high.BlobPath != "" {
		if b.high.PackRelated {
			b.addPrefix("/bs-loose/", "storage-filesystem", args{
				"path": b.high.BlobPath,
			})
			b.addPrefix("/bs-packed/", "storage-filesystem", args{
				"path": filepath.Join(b.high.BlobPath, "packed"),
			})
			blobPackedIndex, err := b.sortedStorageAt("blobpacked_index", filepath.Join(b.high.BlobPath, "packed", "packindex"))
			if err != nil {
				return err
			}
			b.addPrefix("/bs/", "storage-blobpacked", args{
				"smallBlobs": "/bs-loose/",
				"largeBlobs": "/bs-packed/",
				"metaIndex":  blobPackedIndex,
			})
		} else if b.high.PackBlobs {
			diskpackedIndex, err := b.sortedStorageAt("diskpacked_index", filepath.Join(b.high.BlobPath, "diskpacked-index"))
			if err != nil {
				return err
			}
			b.addPrefix("/bs/", "storage-"+storageType, args{
				"path":      b.high.BlobPath,
				"metaIndex": diskpackedIndex,
			})
		} else {
			b.addPrefix("/bs/", "storage-"+storageType, args{
				"path": b.high.BlobPath,
			})
		}
		if b.high.PackBlobs {
			b.addPrefix("/cache/", "storage-"+storageType, args{
				"path": filepath.Join(b.high.BlobPath, "/cache"),
				"metaIndex": map[string]interface{}{
					"type": b.kvFileType(),
					"file": filepath.Join(b.high.BlobPath, "cache", "index."+b.kvFileType()),
				},
			})
		} else {
			b.addPrefix("/cache/", "storage-"+storageType, args{
				"path": filepath.Join(b.high.BlobPath, "/cache"),
			})
		}
	} else if b.high.MemoryStorage {
		b.addPrefix("/bs/", "storage-memory", nil)
		b.addPrefix("/cache/", "storage-memory", nil)
	}

	if b.runIndex() {
		syncArgs, err := b.syncToIndexArgs()
		if err != nil {
			return err
		}
		b.addPrefix("/sync/", "sync", syncArgs)

		b.addPrefix("/bs-and-index/", "storage-replica", args{
			"backends": []interface{}{"/bs/", "/index/"},
		})

		b.addPrefix("/bs-and-maybe-also-index/", "storage-cond", args{
			"write": map[string]interface{}{
				"if":   "isSchema",
				"then": "/bs-and-index/",
				"else": "/bs/",
			},
			"read": "/bs/",
		})

		owner, err := b.searchOwner()
		if err != nil {
			return err
		}
		searchArgs := args{
			"index": "/index/",
			"owner": owner.String(),
		}
		if b.copyIndexToMemory() {
			searchArgs["slurpToMemory"] = true
		}
		b.addPrefix("/my-search/", "search", searchArgs)
	}

	return nil
}

func (b *lowBuilder) build() (*Config, error) {
	conf, low := b.high, b.low
	if conf.HTTPS {
		if (conf.HTTPSCert != "") != (conf.HTTPSKey != "") {
			return nil, errors.New("Must set both httpsCert and httpsKey (or neither to generate a self-signed cert)")
		}
		if conf.HTTPSCert != "" {
			low["httpsCert"] = conf.HTTPSCert
			low["httpsKey"] = conf.HTTPSKey
		} else {
			low["httpsCert"] = osutil.DefaultTLSCert()
			low["httpsKey"] = osutil.DefaultTLSKey()
		}
	}

	if conf.BaseURL != "" {
		u, err := url.Parse(conf.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("Error parsing baseURL %q as a URL: %v", conf.BaseURL, err)
		}
		if u.Path != "" && u.Path != "/" {
			return nil, fmt.Errorf("baseURL can't have a path, only a scheme, host, and optional port.")
		}
		u.Path = ""
		low["baseURL"] = u.String()
	}
	if conf.Listen != "" {
		low["listen"] = conf.Listen
	}
	if conf.PackBlobs && conf.PackRelated {
		return nil, errors.New("can't use both packBlobs (for 'diskpacked') and packRelated (for 'blobpacked')")
	}
	low["https"] = conf.HTTPS
	low["auth"] = conf.Auth

	numIndexers := numSet(conf.LevelDB, conf.Mongo, conf.MySQL, conf.PostgreSQL, conf.SQLite, conf.KVFile, conf.MemoryIndex)

	switch {
	case b.runIndex() && numIndexers == 0:
		return nil, fmt.Errorf("Unless runIndex is set to false, you must specify an index option (kvIndexFile, leveldb, mongo, mysql, postgres, sqlite, memoryIndex).")
	case b.runIndex() && numIndexers != 1:
		return nil, fmt.Errorf("With runIndex set true, you can only pick exactly one indexer (mongo, mysql, postgres, sqlite, kvIndexFile, leveldb, memoryIndex).")
	case !b.runIndex() && numIndexers != 0:
		return nil, fmt.Errorf("With runIndex disabled, you can't specify any of mongo, mysql, postgres, sqlite.")
	}

	if conf.Identity == "" {
		return nil, errors.New("no 'identity' in server config")
	}

	noLocalDisk := conf.BlobPath == ""
	if noLocalDisk {
		if !conf.MemoryStorage && conf.S3 == "" && conf.GoogleCloudStorage == "" {
			return nil, errors.New("Unless memoryStorage is set, you must specify at least one storage option for your blobserver (blobPath (for localdisk), s3, googlecloudstorage).")
		}
		if !conf.MemoryStorage && conf.S3 != "" && conf.GoogleCloudStorage != "" {
			return nil, errors.New("Using S3 as a primary storage and Google Cloud Storage as a mirror is not supported for now.")
		}
	}
	if conf.ShareHandler && conf.ShareHandlerPath == "" {
		conf.ShareHandlerPath = "/share/"
	}
	if conf.MemoryStorage {
		noMkdir = true
		if conf.BlobPath != "" {
			return nil, errors.New("memoryStorage and blobPath are mutually exclusive.")
		}
		if conf.PackRelated {
			return nil, errors.New("memoryStorage doesn't support packRelated.")
		}
	}

	if err := b.genLowLevelPrefixes(); err != nil {
		return nil, err
	}

	var cacheDir string
	if noLocalDisk {
		// Whether camlistored is run from EC2 or not, we use
		// a temp dir as the cache when primary storage is S3.
		// TODO(mpl): s3CacheBucket
		// See https://camlistore.org/issue/85
		cacheDir = filepath.Join(tempDir(), "camli-cache")
	} else {
		cacheDir = filepath.Join(conf.BlobPath, "cache")
	}
	if !noMkdir {
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			return nil, fmt.Errorf("Could not create blobs cache dir %s: %v", cacheDir, err)
		}
	}

	if len(conf.Publish) > 0 {
		if !b.runIndex() {
			return nil, fmt.Errorf("publishing requires an index")
		}
		var tlsO *tlsOpts
		httpsCert, ok1 := low["httpsCert"].(string)
		httpsKey, ok2 := low["httpsKey"].(string)
		if ok1 && ok2 {
			tlsO = &tlsOpts{
				httpsCert: httpsCert,
				httpsKey:  httpsKey,
			}
		}
		if err := b.addPublishedConfig(tlsO); err != nil {
			return nil, fmt.Errorf("Could not generate config for published: %v", err)
		}
	}

	if b.runIndex() {
		b.addUIConfig()
		sto, err := b.sortedStorage("index")
		if err != nil {
			return nil, err
		}
		b.addPrefix("/index/", "storage-index", args{
			"blobSource": "/bs/",
			"storage":    sto,
		})
	}

	if conf.S3 != "" {
		if err := b.addS3Config(conf.S3); err != nil {
			return nil, err
		}
	}
	if conf.GoogleDrive != "" {
		if err := b.addGoogleDriveConfig(conf.GoogleDrive); err != nil {
			return nil, err
		}
	}
	if conf.GoogleCloudStorage != "" {
		if err := b.addGoogleCloudStorageConfig(conf.GoogleCloudStorage); err != nil {
			return nil, err
		}
	}

	return &Config{Obj: b.low}, nil
}

func numSet(vv ...interface{}) (num int) {
	for _, vi := range vv {
		switch v := vi.(type) {
		case string:
			if v != "" {
				num++
			}
		case bool:
			if v {
				num++
			}
		default:
			panic("unknown type")
		}
	}
	return
}

var defaultBaseConfig = serverconfig.Config{
	Listen: ":3179",
	HTTPS:  false,
	Auth:   "localhost",
}

// WriteDefaultConfigFile generates a new default high-level server configuration
// file at filePath. If useSQLite, the default indexer will use SQLite, otherwise
// leveldb. If filePath already exists, it is overwritten.
func WriteDefaultConfigFile(filePath string, useSQLite bool) error {
	conf := defaultBaseConfig
	blobDir := osutil.CamliBlobRoot()
	if err := wkfs.MkdirAll(blobDir, 0700); err != nil {
		return fmt.Errorf("Could not create default blobs directory: %v", err)
	}
	conf.BlobPath = blobDir
	conf.PackRelated = true
	if useSQLite {
		conf.SQLite = filepath.Join(osutil.CamliVarDir(), "index.sqlite")
	} else {
		conf.LevelDB = filepath.Join(osutil.CamliVarDir(), "index.leveldb")
	}

	keyID, secretRing, err := getOrMakeKeyring()
	if err != nil {
		return err
	}
	conf.Identity = keyID
	conf.IdentitySecretRing = secretRing

	confData, err := json.MarshalIndent(conf, "", "    ")
	if err != nil {
		return fmt.Errorf("Could not json encode config file : %v", err)
	}

	if err := wkfs.WriteFile(filePath, confData, 0600); err != nil {
		return fmt.Errorf("Could not create or write default server config: %v", err)
	}

	return nil
}

func getOrMakeKeyring() (keyID, secRing string, err error) {
	secRing = osutil.SecretRingFile()
	_, err = wkfs.Stat(secRing)
	switch {
	case err == nil:
		keyID, err = jsonsign.KeyIdFromRing(secRing)
		if err != nil {
			err = fmt.Errorf("Could not find any keyID in file %q: %v", secRing, err)
			return
		}
		log.Printf("Re-using identity with keyID %q found in file %s", keyID, secRing)
	case os.IsNotExist(err):
		keyID, err = jsonsign.GenerateNewSecRing(secRing)
		if err != nil {
			err = fmt.Errorf("Could not generate new secRing at file %q: %v", secRing, err)
			return
		}
		log.Printf("Generated new identity with keyID %q in file %s", keyID, secRing)
	default:
		err = fmt.Errorf("Could not stat secret ring %q: %v", secRing, err)
	}
	return
}
