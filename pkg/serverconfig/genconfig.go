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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign"
)

// various parameters derived from the high-level user config
// and needed to set up the low-level config.
type configPrefixesParams struct {
	secretRing  string
	keyId       string
	indexerPath string
	blobPath    string
	searchOwner *blobref.BlobRef
}

func addPublishedConfig(prefixes *jsonconfig.Obj, published jsonconfig.Obj) ([]interface{}, error) {
	pubPrefixes := []interface{}{}
	for k, v := range published {
		p, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("Wrong type for %s; was expecting map[string]interface{}, got %T", k, v)
		}
		rootName := strings.Replace(k, "/", "", -1) + "Root"
		rootPermanode, template, style := "", "", ""
		for pk, pv := range p {
			val, ok := pv.(string)
			if !ok {
				return nil, fmt.Errorf("Was expecting type string for %s, got %T", pk, pv)
			}
			switch pk {
			case "rootPermanode":
				rootPermanode = val
			case "template":
				template = val
			case "style":
				style = val
			default:
				return nil, fmt.Errorf("Unexpected key %q in config for %s", pk, k)
			}
		}
		if rootPermanode == "" || template == "" {
			return nil, fmt.Errorf("Missing key in configuration for %s, need \"rootPermanode\" and \"template\"", k)
		}
		ob := map[string]interface{}{}
		ob["handler"] = "publish"
		handlerArgs := map[string]interface{}{
			"rootName":      rootName,
			"blobRoot":      "/bs-and-maybe-also-index/",
			"searchRoot":    "/my-search/",
			"cache":         "/cache/",
			"rootPermanode": []interface{}{"/sighelper/", rootPermanode},
		}
		switch template {
		case "gallery":
			if style == "" {
				style = "pics.css"
			}
			handlerArgs["css"] = []interface{}{style}
			handlerArgs["js"] = []interface{}{"camli.js", "pics.js"}
			handlerArgs["scaledImage"] = "lrucache"
		case "blog":
			if style != "" {
				handlerArgs["css"] = []interface{}{style}
			}
		}
		ob["handlerArgs"] = handlerArgs
		(*prefixes)[k] = ob
		pubPrefixes = append(pubPrefixes, k)
	}
	return pubPrefixes, nil
}

func addUIConfig(prefixes *jsonconfig.Obj, uiPrefix string, published []interface{}) {
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
	ob["handler"] = "setup"
	prefixes["/setup/"] = ob

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
		"publicKeyDest": "/bs-and-index/",
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
		"owner": params.searchOwner.String(),
	}
	prefixes["/my-search/"] = ob

	return prefixes
}

func GenLowLevelConfig(conf *Config) (lowLevelConf *Config, err error) {
	var (
		baseURL    = conf.OptionalString("baseURL", "")
		listen     = conf.OptionalString("listen", "")
		auth       = conf.RequiredString("auth")
		keyId      = conf.RequiredString("identity")
		secretRing = conf.RequiredString("identitySecretRing")
		blobPath   = conf.RequiredString("blobPath")
		tlsOn      = conf.OptionalBool("TLS", false)
		tlsCert    = conf.OptionalString("TLSCertFile", "")
		tlsKey     = conf.OptionalString("TLSKeyFile", "")
		dbname     = conf.OptionalString("dbname", "")
		mysql      = conf.OptionalString("mysql", "")
		mongo      = conf.OptionalString("mongo", "")
		_          = conf.OptionalList("replicateTo")
		_          = conf.OptionalString("s3", "")
		publish    = conf.OptionalObject("publish")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	obj := jsonconfig.Obj{}
	if tlsOn {
		if (tlsCert != "") != (tlsKey != "") {
			return nil, errors.New("Must set both TLSCertFile and TLSKeyFile (or neither to generate a self-signed cert)")
		}
		if tlsCert != "" {
			obj["TLSCertFile"] = tlsCert
			obj["TLSKeyFile"] = tlsKey
		} else {
			obj["TLSCertFile"] = "config/selfgen_cert.pem"
			obj["TLSKeyFile"] = "config/selfgen_key.pem"
		}
	}

	if baseURL != "" {
		if strings.HasSuffix(baseURL, "/") {
			baseURL = baseURL[:len(baseURL)-1]
		}
		obj["baseURL"] = baseURL
	}
	if listen != "" {
		obj["listen"] = listen
	}
	obj["https"] = tlsOn
	obj["auth"] = auth

	if dbname == "" {
		username := os.Getenv("USER")
		if username == "" {
			return nil, fmt.Errorf("USER env var not set; needed to define dbname")
		}
		dbname = "camli" + username
	}

	var indexerPath string
	switch {
	case mongo != "" && mysql != "":
		return nil, fmt.Errorf("Cannot have both mysql and mongo in config, pick one")
	case mysql != "":
		indexerPath = "/index-mysql/"
	case mongo != "":
		indexerPath = "/index-mongo/"
	default:
		indexerPath = "/index-mem/"
	}

	entity, err := jsonsign.EntityFromSecring(keyId, secretRing)
	if err != nil {
		return nil, err
	}
	armoredPublicKey, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		return nil, err
	}

	prefixesParams := &configPrefixesParams{
		secretRing:  secretRing,
		keyId:       keyId,
		indexerPath: indexerPath,
		blobPath:    blobPath,
		searchOwner: blobref.SHA1FromString(armoredPublicKey),
	}

	prefixes := genLowLevelPrefixes(prefixesParams)
	cacheDir := filepath.Join(blobPath, "/cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("Could not create blobs dir %s: %v", cacheDir, err)
	}

	published := []interface{}{}
	if publish != nil {
		published, err = addPublishedConfig(&prefixes, publish)
		if err != nil {
			return nil, fmt.Errorf("Could not generate config for published: %v", err)
		}
	}

	addUIConfig(&prefixes, "/ui/", published)

	if mysql != "" {
		addMysqlConfig(&prefixes, dbname, mysql)
	}
	if mongo != "" {
		addMongoConfig(&prefixes, dbname, mongo)
	}
	if indexerPath == "/index-mem/" {
		addMemindexConfig(&prefixes)
	}

	obj["prefixes"] = (map[string]interface{})(prefixes)

	lowLevelConf = &Config{
		jsonconfig.Obj: obj,
		configPath:     conf.configPath,
	}
	return lowLevelConf, nil
}
