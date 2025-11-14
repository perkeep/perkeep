/*
Copyright 2012 The Perkeep Authors

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
	"strconv"
	"strings"

	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/serverconfig"

	"go4.org/wkfs"
)

var (
	tempDir = os.TempDir
	noMkdir bool // for tests to not call os.Mkdir
)

type tlsOpts struct {
	autoCert  bool // use Perkeep's Let's Encrypt cache. but httpsCert takes precedence, if set.
	httpsCert string
	httpsKey  string
}

// genLowLevelConfig returns a low-level config from a high-level config.
func genLowLevelConfig(conf *serverconfig.Config) (lowLevelConf *Config, err error) {
	b := &lowBuilder{
		high: conf,
		low: jsonconfig.Obj{
			"prefixes": make(map[string]any),
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
type args map[string]any

func (b *lowBuilder) addPrefix(at, handler string, a args) {
	v := map[string]any{
		"handler": handler,
	}
	if a != nil {
		v["handlerArgs"] = (map[string]any)(a)
	}
	b.low["prefixes"].(map[string]any)[at] = v
}

func (b *lowBuilder) hasPrefix(p string) bool {
	_, ok := b.low["prefixes"].(map[string]any)[p]
	return ok
}

func (b *lowBuilder) runIndex() bool          { return b.high.RunIndex.Get() }
func (b *lowBuilder) copyIndexToMemory() bool { return b.high.CopyIndexToMemory.Get() }

type dbname string

// possible arguments to dbName
const (
	dbIndex           dbname = "index"
	dbBlobpackedIndex dbname = "blobpacked-index"
	dbDiskpackedIndex dbname = "diskpacked-index"
	dbUIThumbcache    dbname = "ui-thumbcache"
	dbSyncQueue       dbname = "queue-sync-to-" // only a prefix. the last part is the sync destination, e.g. "index".
)

// dbUnique returns the uniqueness string that is used in databases names to
// differentiate them from databases used by other Perkeep instances on the same
// DBMS.
func (b *lowBuilder) dbUnique() string {
	if b.high.DBUnique != "" {
		return b.high.DBUnique
	}
	if b.high.Identity != "" {
		return strings.ToLower(b.high.Identity)
	}
	return osutil.Username() // may be empty, if $USER unset
}

// dbName returns which database to use for the provided user ("of"), which can
// only be one of the const defined above. Returned values all follow the same name
// scheme for consistency:
// -prefixed with "pk_", so as to distinguish them from databases for other programs
// -followed by a username-based uniqueness string
// -last part says which component/part of perkeep it is about
func (b *lowBuilder) dbName(of dbname) string {
	unique := b.dbUnique()
	if unique == "" {
		log.Printf("Could not define uniqueness for database of %q. Do not use the same index DBMS with other Perkeep instances.", of)
	}
	if unique == useDBNamesConfig {
		// this is the hint that we should revert to the old style DBNames, so this
		// instance can reuse its existing databases
		return b.oldDBNames(of)
	}
	prefix := "pk_"
	if unique != "" {
		prefix += unique + "_"
	}
	switch of {
	case dbIndex:
		if b.high.DBName != "" {
			return b.high.DBName
		}
		return prefix + "index"
	case dbBlobpackedIndex:
		return prefix + "blobpacked"
	case dbDiskpackedIndex:
		return prefix + "diskpacked"
	case dbUIThumbcache:
		return prefix + "uithumbmeta"
	}
	asString := string(of)
	if after, ok := strings.CutPrefix(asString, string(dbSyncQueue)); ok {
		return prefix + "syncto_" + after
	}
	return ""
}

// As of rev 7eda9fd5027fda88166d6c03b6490cffbf2de5fb, we changed how the
// databases names were defined. But we wanted the existing GCE instances to keep
// on working with the old names, so that nothing would break for existing users,
// without any intervention needed. Through the help of the perkeep-config-version
// variable, set by the GCE launcher, we can know whether an instance is such an
// "old" one, and in that case we keep on using the old database names. oldDBNames
// returns these names.
func (b *lowBuilder) oldDBNames(of dbname) string {
	switch of {
	case dbIndex:
		return "camlistore_index"
	case dbBlobpackedIndex:
		return "blobpacked_index"
	case "queue-sync-to-index":
		return "sync_index_queue"
	case dbUIThumbcache:
		return "ui_thumbmeta_cache"
	}
	return ""
}

var errNoOwner = errors.New("no owner")

// Error is errNoOwner if no identity configured
func (b *lowBuilder) searchOwner() (owner *serverconfig.Owner, err error) {
	if b.high.Identity == "" {
		return nil, errNoOwner
	}
	if b.high.IdentitySecretRing == "" {
		return nil, errNoOwner
	}
	return &serverconfig.Owner{
		Identity:    b.high.Identity,
		SecringFile: b.high.IdentitySecretRing,
	}, nil
}

// longIdentity returns the long form (16 chars) of the GPG key ID, in case the
// user provided the short form (8 chars) or the fingerprint (40 chars) in the config.
func (b *lowBuilder) longIdentity() (string, error) {
	if b.high.Identity == "" {
		return "", errNoOwner
	}
	if strings.ToUpper(b.high.Identity) != b.high.Identity {
		return "", fmt.Errorf("identity %q is not all upper-case", b.high.Identity)
	}
	if len(b.high.Identity) == 16 {
		return b.high.Identity, nil
	}
	if len(b.high.Identity) == 40 {
		return b.high.Identity[24:], nil
	}
	if b.high.IdentitySecretRing == "" {
		return "", errNoOwner
	}
	keyID, err := jsonsign.KeyIdFromRing(b.high.IdentitySecretRing)
	if err != nil {
		return "", fmt.Errorf("could not find any keyID in file %q: %v", b.high.IdentitySecretRing, err)
	}
	if !strings.HasSuffix(keyID, b.high.Identity) {
		return "", fmt.Errorf("%q identity not found in secret ring %v", b.high.Identity, b.high.IdentitySecretRing)
	}
	return keyID, nil
}

func addAppConfig(config map[string]any, appConfig *serverconfig.App, low jsonconfig.Obj) {
	if appConfig.Listen != "" {
		config["listen"] = appConfig.Listen
	}
	if appConfig.APIHost != "" {
		config["apiHost"] = appConfig.APIHost
	}
	if appConfig.BackendURL != "" {
		config["backendURL"] = appConfig.BackendURL
	}
	if low["listen"] != nil && low["listen"].(string) != "" {
		config["serverListen"] = low["listen"].(string)
	}
	if low["baseURL"] != nil && low["baseURL"].(string) != "" {
		config["serverBaseURL"] = low["baseURL"].(string)
	}
}

func (b *lowBuilder) addPublishedConfig(tlsO *tlsOpts) error {
	published := b.high.Publish
	for k, v := range published {
		// trick in case all of the fields of v.App were omitted, which would leave v.App nil.
		if v.App == nil {
			v.App = &serverconfig.App{}
		}
		if v.CamliRoot == "" {
			return fmt.Errorf("missing \"camliRoot\" key in configuration for %s", k)
		}
		if v.GoTemplate == "" {
			return fmt.Errorf("missing \"goTemplate\" key in configuration for %s", k)
		}
		appConfig := map[string]any{
			"camliRoot":  v.CamliRoot,
			"cacheRoot":  v.CacheRoot,
			"goTemplate": v.GoTemplate,
		}
		if v.SourceRoot != "" {
			appConfig["sourceRoot"] = v.SourceRoot
		}
		if v.HTTPSCert != "" && v.HTTPSKey != "" {
			// user can specify these directly in the publish section
			appConfig["httpsCert"] = v.HTTPSCert
			appConfig["httpsKey"] = v.HTTPSKey
		} else {
			// default to Perkeep parameters, if any
			if tlsO != nil {
				if tlsO.autoCert {
					appConfig["certManager"] = tlsO.autoCert
				}
				if tlsO.httpsCert != "" {
					appConfig["httpsCert"] = tlsO.httpsCert
				}
				if tlsO.httpsKey != "" {
					appConfig["httpsKey"] = tlsO.httpsKey
				}
			}
		}
		program := "publisher"
		if v.Program != "" {
			program = v.Program
		}
		a := args{
			"prefix":    k,
			"program":   program,
			"appConfig": appConfig,
		}
		addAppConfig(a, v.App, b.low)
		b.addPrefix(k, "app", a)
	}
	return nil
}

func (b *lowBuilder) addScanCabConfig(tlsO *tlsOpts) error {
	if b.high.ScanCab == nil {
		return nil
	}
	scancab := b.high.ScanCab
	if scancab.App == nil {
		scancab.App = &serverconfig.App{}
	}
	if scancab.Prefix == "" {
		return errors.New("missing \"prefix\" key in configuration for scanning cabinet")
	}

	program := "scanningcabinet"
	if scancab.Program != "" {
		program = scancab.Program
	}

	auth := scancab.Auth
	if auth == "" {
		auth = b.high.Auth
	}
	appConfig := map[string]any{
		"auth": auth,
	}
	if scancab.HTTPSCert != "" && scancab.HTTPSKey != "" {
		appConfig["httpsCert"] = scancab.HTTPSCert
		appConfig["httpsKey"] = scancab.HTTPSKey
	} else {
		// default to Perkeep parameters, if any
		if tlsO != nil {
			appConfig["httpsCert"] = tlsO.httpsCert
			appConfig["httpsKey"] = tlsO.httpsKey
		}
	}
	a := args{
		"prefix":    scancab.Prefix,
		"program":   program,
		"appConfig": appConfig,
	}
	addAppConfig(a, scancab.App, b.low)
	b.addPrefix(scancab.Prefix, "app", a)
	return nil
}

func (b *lowBuilder) sortedName() string {
	switch {
	case b.high.MySQL != "":
		return "MySQL"
	case b.high.PostgreSQL != "":
		return "PostgreSQL"
	case b.high.Mongo != "":
		return "MongoDB"
	case b.high.MemoryIndex:
		return "in memory LevelDB"
	case b.high.SQLite != "":
		return "SQLite"
	case b.high.KVFile != "":
		return "KVFile"
	case b.high.LevelDB != "":
		return "LevelDB"
	}
	panic("internal error: sortedName didn't find a sorted implementation")
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
	args := map[string]any{
		"cache": "/cache/",
	}
	if b.high.SourceRoot != "" {
		args["sourceRoot"] = b.high.SourceRoot
	}
	var thumbCache map[string]any
	if b.high.BlobPath != "" {
		thumbCache = map[string]any{
			"type": b.kvFileType(),
			"file": filepath.Join(b.high.BlobPath, "thumbmeta."+b.kvFileType()),
		}
	}
	if thumbCache == nil {
		sorted, err := b.sortedStorage(dbUIThumbcache)
		if err == nil {
			thumbCache = sorted
		}
	}
	if thumbCache != nil {
		args["scaledImage"] = thumbCache
	}
	b.addPrefix("/ui/", "ui", args)
}

func (b *lowBuilder) mongoIndexStorage(confStr string, sortedType dbname) (map[string]any, error) {
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
			return map[string]any{
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

func (b *lowBuilder) dbIndexStorage(rdbms, confStr string, sortedType dbname) (map[string]any, error) {
	dbName := b.dbName(sortedType)
	if dbName == "" {
		return nil, fmt.Errorf("no database name configured for sorted store %q", sortedType)
	}
	user, host, password, ok := parseUserHostPass(confStr)
	if !ok {
		return nil, fmt.Errorf("Malformed %s config string. Want: \"user@host:password\"", rdbms)
	}
	return map[string]any{
		"type":     rdbms,
		"host":     host,
		"user":     user,
		"password": password,
		"database": dbName,
	}, nil
}

func (b *lowBuilder) sortedStorage(sortedType dbname) (map[string]any, error) {
	return b.sortedStorageAt(sortedType, "")
}

// sortedDBMS returns the configuration for a name database on one of the
// DBMS, if any was found in the configuration. It returns nil otherwise.
func (b *lowBuilder) sortedDBMS(named dbname) (map[string]any, error) {
	if b.high.MySQL != "" {
		return b.dbIndexStorage("mysql", b.high.MySQL, named)
	}
	if b.high.PostgreSQL != "" {
		return b.dbIndexStorage("postgres", b.high.PostgreSQL, named)
	}
	if b.high.Mongo != "" {
		return b.mongoIndexStorage(b.high.Mongo, named)
	}
	return nil, nil
}

// filePrefix gives a file path of where to put the database. It can be omitted by
// some sorted implementations, but is required by others.
// The filePrefix should be to a file, not a directory, and should not end in a ".ext" extension.
// An extension like ".kv" or ".sqlite" will be added.
func (b *lowBuilder) sortedStorageAt(sortedType dbname, filePrefix string) (map[string]any, error) {
	dbms, err := b.sortedDBMS(sortedType)
	if err != nil {
		return nil, err
	}
	if dbms != nil {
		return dbms, nil
	}
	if b.high.MemoryIndex {
		return map[string]any{
			"type": "memory",
		}, nil
	}
	if sortedType != "index" && filePrefix == "" {
		return nil, fmt.Errorf("internal error: use of sortedStorageAt with a non-index type (%v) and no file location for non-database sorted implementation", sortedType)
	}
	// dbFile returns path directly if sortedType == "index", else it returns filePrefix+"."+ext.
	dbFile := func(path, ext string) string {
		if sortedType == "index" {
			return path
		}
		return filePrefix + "." + ext
	}
	if b.high.SQLite != "" {
		return map[string]any{
			"type": "sqlite",
			"file": dbFile(b.high.SQLite, "sqlite"),
		}, nil
	}
	if b.high.KVFile != "" {
		return map[string]any{
			"type": "kv",
			"file": dbFile(b.high.KVFile, "kv"),
		}, nil
	}
	if b.high.LevelDB != "" {
		return map[string]any{
			"type": "leveldb",
			"file": dbFile(b.high.LevelDB, "leveldb"),
		}, nil
	}
	panic("internal error: sortedStorageAt didn't find a sorted implementation")
}

func (b *lowBuilder) thatQueueUnlessMemory(thatQueue map[string]any) (queue map[string]any) {
	// TODO(mpl): what about if b.high.MemoryIndex ?
	if b.high.MemoryStorage {
		return map[string]any{
			"type": "memory",
		}
	}
	return thatQueue
}

func (b *lowBuilder) addS3Config(s3 string, vendor string) error {
	f := strings.SplitN(s3, ":", 4)
	if len(f) < 3 {
		m := fmt.Sprintf(`genconfig: expected "%s" field to be of form "access_key_id:secret_access_key:bucket[/optional/dir][:hostname]"`, vendor)
		return errors.New(m)
	}
	accessKey, secret, bucket := f[0], f[1], f[2]
	var hostname string
	if len(f) == 4 {
		hostname = f[3]
	}
	isReplica := b.hasPrefix("/bs/")
	s3Prefix := "/bs/"
	if isReplica {
		s3Prefix = fmt.Sprintf("/sto-%s/", vendor)
	}

	s3Args := func(bucket string) args {
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

	if !b.high.PackRelated {
		b.addPrefix(s3Prefix, "storage-s3", s3Args(bucket))
	} else {
		bsLoose := "/bs-loose/"
		bsPacked := "/bs-packed/"
		if isReplica {
			bsLoose = fmt.Sprintf("/sto-%s-bs-loose/", vendor)
			bsPacked = fmt.Sprintf("/sto-%s-bs-packed/", vendor)
		}

		b.addPrefix(bsLoose, "storage-s3", s3Args(path.Join(bucket, "loose")))
		b.addPrefix(bsPacked, "storage-s3", s3Args(path.Join(bucket, "packed")))

		// If index is DBMS, then blobPackedIndex is in DBMS too.
		// Otherwise blobPackedIndex is same file-based DB as the index,
		// in same dir, but named packindex.dbtype.
		blobPackedIndex, err := b.sortedStorageAt(dbBlobpackedIndex, filepath.Join(b.indexFileDir(), "packindex"))
		if err != nil {
			return err
		}
		b.addPrefix(s3Prefix, "storage-blobpacked", args{
			"smallBlobs": bsLoose,
			"largeBlobs": bsPacked,
			"metaIndex":  blobPackedIndex,
		})
	}

	if isReplica {
		if b.high.BlobPath == "" && !b.high.MemoryStorage {
			panic("unexpected empty blobpath with sync-to-s3")
		}
		p := fmt.Sprintf("/sync-to-%s/", vendor)
		queue := fmt.Sprintf("sync-to-%s-queue.", vendor)
		b.addPrefix(p, "sync", args{
			"from": "/bs/",
			"to":   s3Prefix,
			"queue": b.thatQueueUnlessMemory(
				map[string]any{
					"type": b.kvFileType(),
					"file": filepath.Join(b.high.BlobPath, queue+b.kvFileType()),
				}),
		})
		return nil
	}

	// TODO(mpl): s3CacheBucket
	// See https://perkeep.org/issue/85
	b.addPrefix("/cache/", "storage-filesystem", args{
		"path": filepath.Join(tempDir(), "camli-cache"),
	})

	return nil
}

func (b *lowBuilder) addB2Config(b2 string) error {
	return b.addS3Config(b2, "b2")
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
		"auth": map[string]any{
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
				map[string]any{
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
	gsPrefix := "/bs/"
	if isReplica {
		gsPrefix = "/sto-googlecloudstorage/"
	}

	gsArgs := func(bucket string) args {
		a := args{
			"bucket": bucket,
			"auth": map[string]any{
				"client_id":     clientID,
				"client_secret": secret,
				"refresh_token": refreshToken,
			},
		}
		return a
	}

	if !b.high.PackRelated {
		b.addPrefix(gsPrefix, "storage-googlecloudstorage", gsArgs(bucket))
	} else {
		bsLoose := "/bs-loose/"
		bsPacked := "/bs-packed/"
		if isReplica {
			bsLoose = "/sto-googlecloudstorage-bs-loose/"
			bsPacked = "/sto-googlecloudstorage-bs-packed/"
		}

		b.addPrefix(bsLoose, "storage-googlecloudstorage", gsArgs(path.Join(bucket, "loose")))
		b.addPrefix(bsPacked, "storage-googlecloudstorage", gsArgs(path.Join(bucket, "packed")))

		// If index is DBMS, then blobPackedIndex is in DBMS too.
		// Otherwise blobPackedIndex is same file-based DB as the index,
		// in same dir, but named packindex.dbtype.
		blobPackedIndex, err := b.sortedStorageAt(dbBlobpackedIndex, filepath.Join(b.indexFileDir(), "packindex"))
		if err != nil {
			return err
		}
		b.addPrefix(gsPrefix, "storage-blobpacked", args{
			"smallBlobs": bsLoose,
			"largeBlobs": bsPacked,
			"metaIndex":  blobPackedIndex,
		})
	}

	if isReplica {
		if b.high.BlobPath == "" && !b.high.MemoryStorage {
			panic("unexpected empty blobpath with sync-to-googlecloudstorage")
		}
		b.addPrefix("/sync-to-googlecloudstorage/", "sync", args{
			"from": "/bs/",
			"to":   gsPrefix,
			"queue": b.thatQueueUnlessMemory(
				map[string]any{
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

func (b *lowBuilder) syncToIndexArgs() (map[string]any, error) {
	a := map[string]any{
		"from": "/bs/",
		"to":   "/index/",
	}

	// TODO(mpl): see if we want to have the same logic with all the other queues. probably.
	const sortedType = "queue-sync-to-index"
	if dbName := b.dbName(sortedType); dbName != "" {
		qj, err := b.sortedDBMS(sortedType)
		if err != nil {
			return nil, err
		}
		if qj == nil && b.high.MemoryIndex {
			qj = map[string]any{
				"type": "memory",
			}
		}
		if qj != nil {
			// i.e. the index is configured on a DBMS, so we put the queue there too
			a["queue"] = qj
			return a, nil
		}
	}

	// TODO: currently when using s3, the index must be
	// sqlite or kvfile, since only through one of those
	// can we get a directory.
	if !b.high.MemoryStorage && b.high.BlobPath == "" && b.indexFileDir() == "" {
		// We don't actually have a working sync handler, but we keep a stub registered
		// so it can be referred to from other places.
		// See http://perkeep.org/issue/201
		a["idle"] = true
		return a, nil
	}

	dir := b.high.BlobPath
	if dir == "" {
		dir = b.indexFileDir()
	}
	a["queue"] = b.thatQueueUnlessMemory(
		map[string]any{
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

	rootArgs := map[string]any{
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
	if path := b.high.ShareHandlerPath; path != "" {
		rootArgs["shareRoot"] = path
		b.addPrefix(path, "share", args{
			"blobRoot": "/bs/",
			"index":    "/index/",
		})
	}
	b.addPrefix("/", "root", rootArgs)
	b.addPrefix("/status/", "status", nil)
	b.addPrefix("/help/", "help", nil)

	importerArgs := args{}
	if b.high.Flickr != "" {
		importerArgs["flickr"] = map[string]any{
			"clientSecret": b.high.Flickr,
		}
	}
	if b.high.Picasa != "" {
		importerArgs["picasa"] = map[string]any{
			"clientSecret": b.high.Picasa,
		}
	}
	if b.high.Instapaper != "" {
		importerArgs["instapaper"] = map[string]any{
			"clientSecret": b.high.Instapaper,
		}
	}
	if b.runIndex() {
		b.addPrefix("/importer/", "importer", importerArgs)
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
			blobPackedIndex, err := b.sortedStorageAt(dbBlobpackedIndex, filepath.Join(b.high.BlobPath, "packed", "packindex"))
			if err != nil {
				return err
			}
			b.addPrefix("/bs/", "storage-blobpacked", args{
				"smallBlobs": "/bs-loose/",
				"largeBlobs": "/bs-packed/",
				"metaIndex":  blobPackedIndex,
			})
		} else if b.high.PackBlobs {
			diskpackedIndex, err := b.sortedStorageAt(dbDiskpackedIndex, filepath.Join(b.high.BlobPath, "diskpacked-index"))
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
				"metaIndex": map[string]any{
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
			"backends": []any{"/bs/", "/index/"},
		})

		b.addPrefix("/bs-and-maybe-also-index/", "storage-cond", args{
			"write": map[string]any{
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
			"owner": map[string]any{
				"identity":    owner.Identity,
				"secringFile": owner.SecringFile,
			},
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
		}
	}

	if conf.BaseURL != "" {
		u, err := url.Parse(conf.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("Error parsing baseURL %q as a URL: %v", conf.BaseURL, err)
		}
		if u.Path != "" && u.Path != "/" {
			return nil, fmt.Errorf("baseURL can't have a path, only a scheme, host, and optional port")
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
		log.Printf("Indexer disabled, but %v will be used for other indexes, queues, caches, etc.", b.sortedName())
	}

	longID, err := b.longIdentity()
	if err != nil {
		return nil, err
	}
	b.high.Identity = longID

	noLocalDisk := conf.BlobPath == ""
	if noLocalDisk {
		if !conf.MemoryStorage && conf.S3 == "" && conf.B2 == "" && conf.GoogleCloudStorage == "" {
			return nil, errors.New("Unless memoryStorage is set, you must specify at least one storage option for your blobserver (blobPath (for localdisk), s3, b2, googlecloudstorage).")
		}
		if !conf.MemoryStorage && conf.S3 != "" && conf.GoogleCloudStorage != "" {
			return nil, errors.New("Using S3 as a primary storage and Google Cloud Storage as a mirror is not supported for now.")
		}
		if !conf.MemoryStorage && conf.B2 != "" && conf.GoogleCloudStorage != "" {
			return nil, errors.New("Using B2 as a primary storage and Google Cloud Storage as a mirror is not supported for now.")
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
		// Whether perkeepd is run from EC2 or not, we use
		// a temp dir as the cache when primary storage is S3.
		// TODO(mpl): s3CacheBucket
		// See https://perkeep.org/issue/85
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
		} else if conf.HTTPS {
			tlsO = &tlsOpts{
				autoCert: true,
			}
		}
		if err := b.addPublishedConfig(tlsO); err != nil {
			return nil, fmt.Errorf("Could not generate config for published: %v", err)
		}
	}

	if conf.ScanCab != nil {
		if !b.runIndex() {
			return nil, fmt.Errorf("scanning cabinet requires an index")
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
		if err := b.addScanCabConfig(tlsO); err != nil {
			return nil, fmt.Errorf("Could not generate config for scanning cabinet: %v", err)
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
		if err := b.addS3Config(conf.S3, "s3"); err != nil {
			return nil, err
		}
	}
	if conf.B2 != "" {
		if err := b.addB2Config(conf.B2); err != nil {
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

	return &Config{jconf: b.low}, nil
}

func numSet(vv ...any) (num int) {
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
// file at filePath. The default indexer will use SQLite.
// If filePath already exists, it is overwritten.
func WriteDefaultConfigFile(filePath string) error {
	conf := defaultBaseConfig
	blobDir, err := osutil.CamliBlobRoot()
	if err != nil {
		return err
	}
	varDir, err := osutil.CamliVarDir()
	if err != nil {
		return err
	}
	if err := wkfs.MkdirAll(blobDir, 0700); err != nil {
		return fmt.Errorf("Could not create default blobs directory: %v", err)
	}
	conf.BlobPath = blobDir
	conf.PackRelated = true

	conf.SQLite = filepath.Join(varDir, "index.sqlite")

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
