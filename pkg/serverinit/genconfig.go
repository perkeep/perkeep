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
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/types/serverconfig"
)

// various parameters derived from the high-level user config
// and needed to set up the low-level config.
type configPrefixesParams struct {
	secretRing       string
	keyId            string
	indexerPath      string
	blobPath         string
	packBlobs        bool
	searchOwner      blob.Ref
	shareHandlerPath string
	flickr           string
	memoryIndex      bool
}

var (
	tempDir = os.TempDir
	noMkdir bool // for tests to not call os.Mkdir
)

func addPublishedConfig(prefixes jsonconfig.Obj,
	published map[string]*serverconfig.Publish,
	sourceRoot string) ([]interface{}, error) {
	pubPrefixes := []interface{}{}
	for k, v := range published {
		name := strings.Replace(k, "/", "", -1)
		rootName := name + "Root"
		if !v.Root.Valid() {
			return nil, fmt.Errorf("Invalid or missing \"rootPermanode\" key in configuration for %s.", k)
		}
		if v.GoTemplate == "" {
			return nil, fmt.Errorf("Missing \"goTemplate\" key in configuration for %s.", k)
		}
		ob := map[string]interface{}{}
		ob["handler"] = "publish"
		handlerArgs := map[string]interface{}{
			"rootName":      rootName,
			"blobRoot":      "/bs-and-maybe-also-index/",
			"searchRoot":    "/my-search/",
			"cache":         "/cache/",
			"rootPermanode": []interface{}{"/sighelper/", v.Root.String()},
		}
		if sourceRoot != "" {
			handlerArgs["sourceRoot"] = sourceRoot
		}
		handlerArgs["goTemplate"] = v.GoTemplate
		if v.Style != "" {
			handlerArgs["css"] = []interface{}{v.Style}
		}
		if v.Javascript != "" {
			handlerArgs["js"] = []interface{}{v.Javascript}
		}
		// TODO(mpl): we'll probably want to use osutil.CacheDir() if thumbnails.kv
		// contains private info? same for some of the other "camli-cache" ones?
		thumbsCacheDir := filepath.Join(tempDir(), "camli-cache")
		handlerArgs["scaledImage"] = map[string]interface{}{
			"type": "kv",
			"file": filepath.Join(thumbsCacheDir, name+"-thumbnails.kv"),
		}
		if err := os.MkdirAll(thumbsCacheDir, 0700); err != nil {
			return nil, fmt.Errorf("Could not create cache dir %s: %v", thumbsCacheDir, err)
		}
		ob["handlerArgs"] = handlerArgs
		prefixes[k] = ob
		pubPrefixes = append(pubPrefixes, k)
	}
	return pubPrefixes, nil
}

func addUIConfig(params *configPrefixesParams,
	prefixes jsonconfig.Obj,
	uiPrefix string,
	published []interface{},
	sourceRoot string) {

	args := map[string]interface{}{
		"jsonSignRoot": "/sighelper/",
		"cache":        "/cache/",
	}
	if len(published) > 0 {
		args["publishRoots"] = published
	}
	if sourceRoot != "" {
		args["sourceRoot"] = sourceRoot
	}
	if params.blobPath != "" {
		args["scaledImage"] = map[string]interface{}{
			"type": "kv",
			"file": filepath.Join(params.blobPath, "thumbmeta.kv"),
		}
	}
	prefixes[uiPrefix] = map[string]interface{}{
		"handler":     "ui",
		"handlerArgs": args,
	}
}

func addMongoConfig(prefixes jsonconfig.Obj, dbname string, dbinfo string) {
	fields := strings.Split(dbinfo, "@")
	if len(fields) != 2 {
		exitFailure("Malformed mongo config string. Got \"%v\", want: \"user:password@host\"", dbinfo)
	}
	host := fields[1]
	fields = strings.Split(fields[0], ":")
	if len(fields) != 2 {
		exitFailure("Malformed mongo config string. Got \"%v\", want: \"user:password\"", fields[0])
	}
	ob := map[string]interface{}{}
	ob["enabled"] = true
	ob["handler"] = "storage-mongodbindexer"
	ob["handlerArgs"] = map[string]interface{}{
		"host":       host,
		"user":       fields[0],
		"password":   fields[1],
		"database":   dbname,
		"blobSource": "/bs/",
	}
	prefixes["/index-mongo/"] = ob
}

func addSQLConfig(rdbms string, prefixes jsonconfig.Obj, dbname string, dbinfo string) {
	fields := strings.Split(dbinfo, "@")
	if len(fields) != 2 {
		exitFailure("Malformed " + rdbms + " config string. Want: \"user@host:password\"")
	}
	user := fields[0]
	fields = strings.Split(fields[1], ":")
	if len(fields) != 2 {
		exitFailure("Malformed " + rdbms + " config string. Want: \"user@host:password\"")
	}
	ob := map[string]interface{}{}
	ob["enabled"] = true
	ob["handler"] = "storage-" + rdbms + "indexer"
	ob["handlerArgs"] = map[string]interface{}{
		"host":       fields[0],
		"user":       user,
		"password":   fields[1],
		"database":   dbname,
		"blobSource": "/bs/",
	}
	prefixes["/index-"+rdbms+"/"] = ob
}

func addPostgresConfig(prefixes jsonconfig.Obj, dbname string, dbinfo string) {
	addSQLConfig("postgres", prefixes, dbname, dbinfo)
}

func addMySQLConfig(prefixes jsonconfig.Obj, dbname string, dbinfo string) {
	addSQLConfig("mysql", prefixes, dbname, dbinfo)
}

func addSQLiteConfig(prefixes jsonconfig.Obj, file string) {
	ob := map[string]interface{}{}
	ob["handler"] = "storage-sqliteindexer"
	ob["handlerArgs"] = map[string]interface{}{
		"blobSource": "/bs/",
		"file":       file,
	}
	prefixes["/index-sqlite/"] = ob
}

func addKVConfig(prefixes jsonconfig.Obj, file string) {
	prefixes["/index-kv/"] = map[string]interface{}{
		"handler": "storage-kvfileindexer",
		"handlerArgs": map[string]interface{}{
			"blobSource": "/bs/",
			"file":       file,
		},
	}
}

func addS3Config(params *configPrefixesParams, prefixes jsonconfig.Obj, s3 string) error {
	f := strings.SplitN(s3, ":", 4)
	if len(f) < 3 {
		return errors.New(`genconfig: expected "s3" field to be of form "access_key_id:secret_access_key:bucket"`)
	}
	accessKey, secret, bucket := f[0], f[1], f[2]
	var hostname string
	if len(f) == 4 {
		hostname = f[3]
	}
	isPrimary := false
	if _, ok := prefixes["/bs/"]; !ok {
		isPrimary = true
	}
	s3Prefix := ""
	if isPrimary {
		s3Prefix = "/bs/"
	} else {
		s3Prefix = "/sto-s3/"
	}
	args := map[string]interface{}{
		"aws_access_key":        accessKey,
		"aws_secret_access_key": secret,
		"bucket":                bucket,
	}
	if hostname != "" {
		args["hostname"] = hostname
	}
	prefixes[s3Prefix] = map[string]interface{}{
		"handler":     "storage-s3",
		"handlerArgs": args,
	}
	if isPrimary {
		// TODO(mpl): s3CacheBucket
		// See http://code.google.com/p/camlistore/issues/detail?id=85
		prefixes["/cache/"] = map[string]interface{}{
			"handler": "storage-filesystem",
			"handlerArgs": map[string]interface{}{
				"path": filepath.Join(tempDir(), "camli-cache"),
			},
		}
	} else {
		if params.blobPath == "" {
			panic("unexpected empty blobpath with sync-to-s3")
		}
		prefixes["/sync-to-s3/"] = map[string]interface{}{
			"handler": "sync",
			"handlerArgs": map[string]interface{}{
				"from": "/bs/",
				"to":   s3Prefix,
				"queue": map[string]interface{}{
					"type": "kv",
					"file": filepath.Join(params.blobPath, "sync-to-s3-queue.kv"),
				},
			},
		}
	}
	return nil
}

func addGoogleDriveConfig(params *configPrefixesParams, prefixes jsonconfig.Obj, highCfg string) error {
	f := strings.SplitN(highCfg, ":", 4)
	if len(f) != 4 {
		return errors.New(`genconfig: expected "googledrive" field to be of form "client_id:client_secret:refresh_token:parent_id"`)
	}
	clientId, secret, refreshToken, parentId := f[0], f[1], f[2], f[3]

	isPrimary := false
	if _, ok := prefixes["/bs/"]; !ok {
		isPrimary = true
	}

	prefix := ""
	if isPrimary {
		prefix = "/bs/"
	} else {
		prefix = "/sto-googledrive/"
	}
	prefixes[prefix] = map[string]interface{}{
		"handler": "storage-googledrive",
		"handlerArgs": map[string]interface{}{
			"parent_id": parentId,
			"auth": map[string]interface{}{
				"client_id":     clientId,
				"client_secret": secret,
				"refresh_token": refreshToken,
			},
		},
	}

	if isPrimary {
		prefixes["/cache/"] = map[string]interface{}{
			"handler": "storage-filesystem",
			"handlerArgs": map[string]interface{}{
				"path": filepath.Join(tempDir(), "camli-cache"),
			},
		}
	} else {
		prefixes["/sync-to-googledrive/"] = map[string]interface{}{
			"handler": "sync",
			"handlerArgs": map[string]interface{}{
				"from": "/bs/",
				"to":   prefix,
				"queue": map[string]interface{}{
					"type": "kv",
					"file": filepath.Join(params.blobPath,
						"sync-to-googledrive-queue.kv"),
				},
			},
		}
	}

	return nil
}

func addGoogleCloudStorageConfig(params *configPrefixesParams, prefixes jsonconfig.Obj, highCfg string) error {
	f := strings.SplitN(highCfg, ":", 4)
	if len(f) != 4 {
		return errors.New(`genconfig: expected "googlecloudstorage" field to be of form "client_id:client_secret:refresh_token:bucket"`)
	}
	clientId, secret, refreshToken, bucket := f[0], f[1], f[2], f[3]

	isPrimary := false
	if _, ok := prefixes["/bs/"]; !ok {
		isPrimary = true
	}

	gsPrefix := ""
	if isPrimary {
		gsPrefix = "/bs/"
	} else {
		gsPrefix = "/sto-googlecloudstorage/"
	}

	prefixes[gsPrefix] = map[string]interface{}{
		"handler": "storage-googlecloudstorage",
		"handlerArgs": map[string]interface{}{
			"bucket": bucket,
			"auth": map[string]interface{}{
				"client_id":     clientId,
				"client_secret": secret,
				"refresh_token": refreshToken,
				// If high-level config is for the common user then fullSyncOnStart = true
				// Then the default just works.
				//"fullSyncOnStart": true,
				//"blockingFullSyncOnStart": false
			},
		},
	}

	if isPrimary {
		// TODO: cacheBucket like s3CacheBucket?
		prefixes["/cache/"] = map[string]interface{}{
			"handler": "storage-filesystem",
			"handlerArgs": map[string]interface{}{
				"path": filepath.Join(tempDir(), "camli-cache"),
			},
		}
	} else {
		prefixes["/sync-to-googlecloudstorage/"] = map[string]interface{}{
			"handler": "sync",
			"handlerArgs": map[string]interface{}{
				"from": "/bs/",
				"to":   gsPrefix,
				"queue": map[string]interface{}{
					"type": "kv",
					"file": filepath.Join(params.blobPath,
						"sync-to-googlecloud-queue.kv"),
				},
			},
		}
	}
	return nil
}

func genLowLevelPrefixes(params *configPrefixesParams, ownerName string) (m jsonconfig.Obj) {
	m = make(jsonconfig.Obj)

	haveIndex := params.indexerPath != ""
	root := "/bs/"
	pubKeyDest := root
	if haveIndex {
		root = "/bs-and-maybe-also-index/"
		pubKeyDest = "/bs-and-index/"
	}

	rootArgs := map[string]interface{}{
		"stealth":    false,
		"blobRoot":   root,
		"statusRoot": "/status/",
	}
	if ownerName != "" {
		rootArgs["ownerName"] = ownerName
	}
	m["/"] = map[string]interface{}{
		"handler":     "root",
		"handlerArgs": rootArgs,
	}
	if haveIndex {
		setMap(m, "/", "handlerArgs", "searchRoot", "/my-search/")
	}

	m["/setup/"] = map[string]interface{}{
		"handler": "setup",
	}

	m["/status/"] = map[string]interface{}{
		"handler": "status",
	}

	if params.shareHandlerPath != "" {
		m[params.shareHandlerPath] = map[string]interface{}{
			"handler": "share",
			"handlerArgs": map[string]interface{}{
				"blobRoot": "/bs/",
			},
		}
	}

	m["/sighelper/"] = map[string]interface{}{
		"handler": "jsonsign",
		"handlerArgs": map[string]interface{}{
			"secretRing":    params.secretRing,
			"keyId":         params.keyId,
			"publicKeyDest": pubKeyDest,
		},
	}

	storageType := "filesystem"
	if params.packBlobs {
		storageType = "diskpacked"
	}
	if params.blobPath != "" {
		m["/bs/"] = map[string]interface{}{
			"handler": "storage-" + storageType,
			"handlerArgs": map[string]interface{}{
				"path": params.blobPath,
			},
		}

		m["/cache/"] = map[string]interface{}{
			"handler": "storage-" + storageType,
			"handlerArgs": map[string]interface{}{
				"path": filepath.Join(params.blobPath, "/cache"),
			},
		}
	}

	if params.flickr != "" {
		m["/importer-flickr/"] = map[string]interface{}{
			"handler": "importer-flickr",
			"handlerArgs": map[string]interface{}{
				"apiKey": params.flickr,
			},
		}
	}

	if haveIndex {
		syncArgs := map[string]interface{}{
			"from": "/bs/",
			"to":   params.indexerPath,
		}
		// TODO(mpl): Brad says the cond should be dest == /index-*.
		// But what about when dest is index-mem and we have a local disk;
		// don't we want to have an active synchandler to do the fullSyncOnStart?
		// Anyway, that condition works for now.
		if params.blobPath == "" {
			// When our primary blob store is remote (s3 or google cloud),
			// i.e not an efficient replication source, we do not want the
			// synchandler to mirror to the indexer. But we still want a
			// synchandler to provide the discovery for e.g tools like
			// camtool sync. See http://camlistore.org/issue/201
			syncArgs["idle"] = true
		} else {
			syncArgs["queue"] = map[string]interface{}{
				"type": "kv",
				"file": filepath.Join(params.blobPath, "sync-to-index-queue.kv"),
			}
		}
		m["/sync/"] = map[string]interface{}{
			"handler":     "sync",
			"handlerArgs": syncArgs,
		}

		m["/bs-and-index/"] = map[string]interface{}{
			"handler": "storage-replica",
			"handlerArgs": map[string]interface{}{
				"backends": []interface{}{"/bs/", params.indexerPath},
			},
		}

		m["/bs-and-maybe-also-index/"] = map[string]interface{}{
			"handler": "storage-cond",
			"handlerArgs": map[string]interface{}{
				"write": map[string]interface{}{
					"if":   "isSchema",
					"then": "/bs-and-index/",
					"else": "/bs/",
				},
				"read": "/bs/",
			},
		}

		searchArgs := map[string]interface{}{
			"index": params.indexerPath,
			"owner": params.searchOwner.String(),
		}
		if params.memoryIndex {
			searchArgs["slurpToMemory"] = true
		}
		m["/my-search/"] = map[string]interface{}{
			"handler":     "search",
			"handlerArgs": searchArgs,
		}
	}

	return
}

// genLowLevelConfig returns a low-level config from a high-level config.
func genLowLevelConfig(conf *serverconfig.Config) (lowLevelConf *Config, err error) {
	obj := jsonconfig.Obj{}
	if conf.HTTPS {
		if (conf.HTTPSCert != "") != (conf.HTTPSKey != "") {
			return nil, errors.New("Must set both httpsCert and httpsKey (or neither to generate a self-signed cert)")
		}
		if conf.HTTPSCert != "" {
			obj["httpsCert"] = conf.HTTPSCert
			obj["httpsKey"] = conf.HTTPSKey
		} else {
			obj["httpsCert"] = osutil.DefaultTLSCert()
			obj["httpsKey"] = osutil.DefaultTLSKey()
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
		obj["baseURL"] = u.String()
	}
	if conf.Listen != "" {
		obj["listen"] = conf.Listen
	}
	obj["https"] = conf.HTTPS
	obj["auth"] = conf.Auth

	username := ""
	if conf.DBName == "" {
		username = osutil.Username()
		if username == "" {
			return nil, fmt.Errorf("USER (USERNAME on windows) env var not set; needed to define dbname")
		}
		conf.DBName = "camli" + username
	}

	var indexerPath string
	numIndexers := numSet(conf.Mongo, conf.MySQL, conf.PostgreSQL, conf.SQLite, conf.KVFile)
	runIndex := conf.RunIndex.Get()
	switch {
	case runIndex && numIndexers == 0:
		return nil, fmt.Errorf("Unless runIndex is set to false, you must specify an index option (kvIndexFile, mongo, mysql, postgres, sqlite).")
	case runIndex && numIndexers != 1:
		return nil, fmt.Errorf("With runIndex set true, you can only pick exactly one indexer (mongo, mysql, postgres, sqlite).")
	case !runIndex && numIndexers != 0:
		return nil, fmt.Errorf("With runIndex disabled, you can't specify any of mongo, mysql, postgres, sqlite.")
	case conf.MySQL != "":
		indexerPath = "/index-mysql/"
	case conf.PostgreSQL != "":
		indexerPath = "/index-postgres/"
	case conf.Mongo != "":
		indexerPath = "/index-mongo/"
	case conf.SQLite != "":
		indexerPath = "/index-sqlite/"
	case conf.KVFile != "":
		indexerPath = "/index-kv/"
	}

	entity, err := jsonsign.EntityFromSecring(conf.Identity, conf.IdentitySecretRing)
	if err != nil {
		return nil, err
	}
	armoredPublicKey, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		return nil, err
	}

	nolocaldisk := conf.BlobPath == ""
	if nolocaldisk {
		if conf.S3 == "" && conf.GoogleCloudStorage == "" {
			return nil, errors.New("You need at least one of blobPath (for localdisk) or s3 or googlecloudstorage configured for a blobserver.")
		}
		if conf.S3 != "" && conf.GoogleCloudStorage != "" {
			return nil, errors.New("Using S3 as a primary storage and Google Cloud Storage as a mirror is not supported for now.")
		}
	}

	if conf.ShareHandler && conf.ShareHandlerPath == "" {
		conf.ShareHandlerPath = "/share/"
	}

	prefixesParams := &configPrefixesParams{
		secretRing:       conf.IdentitySecretRing,
		keyId:            conf.Identity,
		indexerPath:      indexerPath,
		blobPath:         conf.BlobPath,
		packBlobs:        conf.PackBlobs,
		searchOwner:      blob.SHA1FromString(armoredPublicKey),
		shareHandlerPath: conf.ShareHandlerPath,
		flickr:           conf.Flickr,
		memoryIndex:      conf.MemoryIndex.Get(),
	}

	prefixes := genLowLevelPrefixes(prefixesParams, conf.OwnerName)
	var cacheDir string
	if nolocaldisk {
		// Whether camlistored is run from EC2 or not, we use
		// a temp dir as the cache when primary storage is S3.
		// TODO(mpl): s3CacheBucket
		// See http://code.google.com/p/camlistore/issues/detail?id=85
		cacheDir = filepath.Join(tempDir(), "camli-cache")
	} else {
		cacheDir = filepath.Join(conf.BlobPath, "cache")
	}
	if !noMkdir {
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			return nil, fmt.Errorf("Could not create blobs cache dir %s: %v", cacheDir, err)
		}
	}

	published := []interface{}{}
	if len(conf.Publish) > 0 {
		if !runIndex {
			return nil, fmt.Errorf("publishing requires an index")
		}
		published, err = addPublishedConfig(prefixes, conf.Publish, conf.SourceRoot)
		if err != nil {
			return nil, fmt.Errorf("Could not generate config for published: %v", err)
		}
	}

	if runIndex {
		addUIConfig(prefixesParams, prefixes, "/ui/", published, conf.SourceRoot)
	}

	if conf.MySQL != "" {
		addMySQLConfig(prefixes, conf.DBName, conf.MySQL)
	}
	if conf.PostgreSQL != "" {
		addPostgresConfig(prefixes, conf.DBName, conf.PostgreSQL)
	}
	if conf.Mongo != "" {
		addMongoConfig(prefixes, conf.DBName, conf.Mongo)
	}
	if conf.SQLite != "" {
		addSQLiteConfig(prefixes, conf.SQLite)
	}
	if conf.KVFile != "" {
		addKVConfig(prefixes, conf.KVFile)
	}
	if conf.S3 != "" {
		if err := addS3Config(prefixesParams, prefixes, conf.S3); err != nil {
			return nil, err
		}
	}
	if conf.GoogleDrive != "" {
		if err := addGoogleDriveConfig(prefixesParams, prefixes, conf.GoogleDrive); err != nil {
			return nil, err
		}
	}
	if conf.GoogleCloudStorage != "" {
		if err := addGoogleCloudStorageConfig(prefixesParams, prefixes, conf.GoogleCloudStorage); err != nil {
			return nil, err
		}
	}

	obj["prefixes"] = (map[string]interface{})(prefixes)

	lowLevelConf = &Config{
		Obj: obj,
	}
	return lowLevelConf, nil
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

func setMap(m map[string]interface{}, v ...interface{}) {
	if len(v) < 2 {
		panic("too few args")
	}
	if len(v) == 2 {
		m[v[0].(string)] = v[1]
		return
	}
	setMap(m[v[0].(string)].(map[string]interface{}), v[1:]...)
}
