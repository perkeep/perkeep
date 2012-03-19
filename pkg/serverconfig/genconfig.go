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

package serverconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/osutil"
)

// various parameters derived from the high-level user config
// and needed to set up the low-level config.
type configPrefixesParams struct {
	secretRing  string
	keyId       string
	indexerPath string
	blobPath    string
}

func addUiConfig(prefixes *jsonconfig.Obj, uiPrefix string, published ...interface{}) {
	ob := map[string]interface{}{}
	ob["handler"] = "ui"
	handlerArgs := map[string]interface{}{
		"blobRoot":     "/bs-and-maybe-also-index/",
		"searchRoot":   "/my-search/",
		"jsonSignRoot": "/sighelper/",
		"cache":        "/cache/",
		"scaledImage":  "lrucache",
	}
	if len(published) > 0 {
		handlerArgs["publishRoots"] = published
	}
	ob["handlerArgs"] = handlerArgs
	(*prefixes)[uiPrefix] = ob
}

// TODO(mpl): add auth info
func addMongoConfig(prefixes *jsonconfig.Obj, dbname string, servers string) {
	ob := map[string]interface{}{}
	ob["enabled"] = true
	ob["handler"] = "storage-mongodbindexer"
	ob["handlerArgs"] = map[string]interface{}{
		"servers":    servers,
		"database":   dbname,
		"blobSource": "/bs/",
	}
	(*prefixes)["/index-mongo/"] = ob
}

func addMysqlConfig(prefixes *jsonconfig.Obj, dbname string, dbinfo string) {
	fields := strings.Split(dbinfo, "@")
	if len(fields) != 2 {
		exitFailure("Malformed mysql config string. Want: \"user@host:password\"")
	}
	user := fields[0]
	fields = strings.Split(fields[1], ":")
	if len(fields) != 2 {
		exitFailure("Malformed mysql config string. Want: \"user@host:password\"")
	}
	ob := map[string]interface{}{}
	ob["enabled"] = true
	ob["handler"] = "storage-mysqlindexer"
	ob["handlerArgs"] = map[string]interface{}{
		"host":       fields[0],
		"user":       user,
		"password":   fields[1],
		"database":   dbname,
		"blobSource": "/bs/",
	}
	(*prefixes)["/index-mysql/"] = ob
}

func addMemindexConfig(prefixes *jsonconfig.Obj) {
	ob := map[string]interface{}{}
	ob["handler"] = "storage-memory-only-dev-indexer"
	ob["handlerArgs"] = map[string]interface{}{
		"blobSource": "/bs/",
	}
	(*prefixes)["/index-mem/"] = ob
}

func genLowLevelPrefixes(params *configPrefixesParams) jsonconfig.Obj {
	prefixes := map[string]interface{}{}

	ob := map[string]interface{}{}
	ob["handler"] = "root"
	ob["handlerArgs"] = map[string]interface{}{"stealth": false}
	prefixes["/"] = ob

	ob = map[string]interface{}{}
	ob["handler"] = "sync"
	ob["handlerArgs"] = map[string]interface{}{
		"from": "/bs/",
		"to":   params.indexerPath,
	}
	prefixes["/sync/"] = ob

	ob = map[string]interface{}{}
	ob["handler"] = "jsonsign"
	ob["handlerArgs"] = map[string]interface{}{
		"secretRing":    params.secretRing,
		"keyId":         params.keyId,
		"publicKeyDest": "/bs/",
	}
	prefixes["/sighelper/"] = ob

	ob = map[string]interface{}{}
	ob["handler"] = "storage-replica"
	ob["handlerArgs"] = map[string]interface{}{
		"backends": []interface{}{"/bs/", params.indexerPath},
	}
	prefixes["/bs-and-index/"] = ob

	ob = map[string]interface{}{}
	ob["handler"] = "storage-cond"
	ob["handlerArgs"] = map[string]interface{}{
		"write": map[string]interface{}{
			"if":   "isSchema",
			"then": "/bs-and-index/",
			"else": "/bs/",
		},
		"read": "/bs/",
	}
	prefixes["/bs-and-maybe-also-index/"] = ob

	ob = map[string]interface{}{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = map[string]interface{}{
		"path": params.blobPath,
	}
	prefixes["/bs/"] = ob

	ob = map[string]interface{}{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = map[string]interface{}{
		"path": filepath.Join(params.blobPath, "/cache"),
	}
	prefixes["/cache/"] = ob

	ob = map[string]interface{}{}
	ob["handler"] = "search"
	ob["handlerArgs"] = map[string]interface{}{
		"index": params.indexerPath,
		"owner": "sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
	}
	prefixes["/my-search/"] = ob

	return prefixes
}

func GenLowLevelConfig(conf *Config) (lowLevelConf *Config, err error) {
	var (
		baseUrl    = conf.RequiredString("listen")
		tlsOn      = conf.OptionalBool("TLS", false)
		auth       = conf.RequiredString("auth")
		dbname     = conf.OptionalString("dbname", "")
		secretRing = conf.OptionalString("secring", "")
		blobPath   = conf.RequiredString("blobPath")
		keyId      = conf.OptionalString("keyid", "")
		mysql      = conf.OptionalString("mysql", "")
		mongo      = conf.OptionalString("mongo", "")
		_          = conf.OptionalList("replicateTo")
		_          = conf.OptionalString("s3", "")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	obj := jsonconfig.Obj{}
	scheme := "http"
	if tlsOn {
		scheme = "https"
		obj["TLSCertFile"] = "config/selfgen_cert.pem"
		obj["TLSKeyFile"] = "config/selfgen_key.pem"
	}
	obj["baseURL"] = scheme + "://" + baseUrl
	obj["https"] = tlsOn
	obj["auth"] = auth

	if dbname == "" {
		username := os.Getenv("USER")
		if username == "" {
			return nil, fmt.Errorf("USER env var not set; needed to define dbname")
		}
		dbname = "camli" + username
	}

	if secretRing == "" {
		secretRing = filepath.Join(osutil.CamliConfigDir(), "secring.gpg")
		_, err = os.Stat(secretRing)
		if err != nil {
			return nil, fmt.Errorf("\"secring\" not set in config, and no default secret ring at %s", secretRing)
		}
	}

	if keyId == "" {
		// TODO(mpl): where do we get a default keyId from? Brad?
		keyId = "26F5ABDA"
	}

	indexerPath := "/index-mem/"

	prefixesParams := &configPrefixesParams{
		secretRing:  secretRing,
		keyId:       keyId,
		indexerPath: indexerPath,
		blobPath:    blobPath,
	}

	prefixes := genLowLevelPrefixes(prefixesParams)
	cacheDir := filepath.Join(blobPath, "/cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("Could not create blobs dir %s: %v", cacheDir, err)
	}

	addUiConfig(&prefixes, "/ui/")

	if mongo != "" && mysql != "" {
		return nil, fmt.Errorf("Cannot have both mysql and mongo in config, pick one")
	}
	if mysql != "" {
		addMysqlConfig(&prefixes, dbname, mysql)
	} else {
		if mongo != "" {
			addMongoConfig(&prefixes, dbname, mongo)
		} else {
			addMemindexConfig(&prefixes)
		}
	}

	obj["prefixes"] = (map[string]interface{})(prefixes)

	lowLevelConf = &Config{
		jsonconfig.Obj: obj,
		configPath:     conf.configPath,
	}
	return lowLevelConf, nil
}
