/*
Copyright 2011 Google Inc.

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

package client

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
)

// These, if set, override the JSON config file ~/.camlistore/config
// "server" and "password" keys.
//
// A main binary must call AddFlags to expose these.
var (
	flagServer     string
	flagSecretRing string
)

func AddFlags() {
	defaultPath := osutil.UserClientConfigPath()
	flag.StringVar(&flagServer, "server", "", "Camlistore server prefix. If blank, the default from the \"server\" field of "+defaultPath+" is used. Acceptable forms: https://you.example.com, example.com:1345 (https assumed), or http://you.example.com/alt-root")
	flag.StringVar(&flagSecretRing, "secret-keyring", "", "GnuPG secret keyring file to use.")
}

// ExplicitServer returns the blobserver given in the flags, if any.
func ExplicitServer() string {
	return flagServer
}

var configOnce sync.Once
var config = make(map[string]interface{})

// serverGPGKey returns the public gpg key id ("identity" field)
// from the user's server config , if any.
// It returns the empty string otherwise.
func serverKeyId() string {
	serverConfigFile := osutil.UserServerConfigPath()
	if _, err := os.Stat(serverConfigFile); err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		log.Fatalf("Could not stat %v: %v", serverConfigFile, err)
	}
	obj, err := jsonconfig.ReadFile(serverConfigFile)
	if err != nil {
		return ""
	}
	keyId, ok := obj["identity"].(string)
	if !ok {
		return ""
	}
	return keyId
}

func parseConfig() {
	if onAndroid() {
		return
	}
	configPath := osutil.UserClientConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		errMsg := fmt.Sprintf("Client configuration file %v does not exist. See 'camput init' to generate it.", configPath)
		if keyId := serverKeyId(); keyId != "" {
			hint := fmt.Sprintf("\nThe key id %v was found in the server config %v, so you might want:\n'camput init -gpgkey %v'", keyId, osutil.UserServerConfigPath(), keyId)
			errMsg += hint
		}
		log.Fatal(errMsg)
	}

	var err error
	if config, err = jsonconfig.ReadFile(configPath); err != nil {
		log.Fatal(err.Error())
		return
	}
}

func cleanServer(server string) string {
	// Remove trailing slash if provided.
	if strings.HasSuffix(server, "/") {
		server = server[0 : len(server)-1]
	}
	// Default to "https://" when not specified
	if !strings.HasPrefix(server, "http") && !strings.HasPrefix(server, "https") {
		server = "https://" + server
	}
	return server
}

func serverOrDie() string {
	if flagServer != "" {
		return cleanServer(flagServer)
	}
	configOnce.Do(parseConfig)
	value, ok := config["server"]
	var server string
	if ok {
		server = value.(string)
	}
	server = cleanServer(server)
	if !ok || server == "" {
		log.Fatalf("Missing or invalid \"server\" in %q", osutil.UserClientConfigPath())
	}
	return server
}

func (c *Client) useTLS() bool {
	return strings.HasPrefix(c.server, "https://")
}

func (c *Client) SetupAuth() error {
	if flagServer != "" {
		// If using an explicit blobserver, don't use auth
		// configured from the config file, so we don't send
		// our password to a friend's blobserver.
		var err error
		c.authMode, err = auth.FromEnv()
		if err == auth.ErrNoAuth {
			log.Printf("Using explicit --server parameter; not using config file auth, and no auth mode set in environment")
		}
		return err
	}
	configOnce.Do(parseConfig)
	return c.SetupAuthFromConfig(config)
}

func (c *Client) SetupAuthFromConfig(conf jsonconfig.Obj) error {
	var err error
	value, ok := conf["auth"]
	authString := ""
	if ok {
		authString, ok = value.(string)
		c.authMode, err = auth.FromConfig(authString)
	} else {
		c.authMode, err = auth.FromEnv()
	}
	return err
}

// Returns blobref of signer's public key, or nil if unconfigured.
func (c *Client) SignerPublicKeyBlobref() blob.Ref {
	return SignerPublicKeyBlobref()
}

// SecretRingFile returns the filename to the user's GPG secret ring.
// The value comes from either a command-line flag,
// the client config file's "secretRing" value, or the operating
// system default location.
func (c *Client) SecretRingFile() string {
	if flagSecretRing != "" {
		return flagSecretRing
	}
	configOnce.Do(parseConfig)
	keyRing, ok := config["secretRing"].(string)
	if ok && keyRing != "" {
		return keyRing
	}
	if keyRing = osutil.IdentitySecretRing(); fileExists(keyRing) {
		return keyRing
	}
	return jsonsign.DefaultSecRingPath()
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

var (
	signerPublicKeyRefOnce sync.Once
	signerPublicKeyRef     blob.Ref // of publicKeyArmored
	publicKeyArmored       string
)

// TODO: move to config package?
func SignerPublicKeyBlobref() blob.Ref {
	signerPublicKeyRefOnce.Do(initSignerPublicKeyBlobref)
	return signerPublicKeyRef
}

func initSignerPublicKeyBlobref() {
	signerPublicKeyRef, publicKeyArmored, _ = getSignerPublicKeyBlobref()
}

func getSignerPublicKeyBlobref() (signerRef blob.Ref, armored string, ok bool) {
	configOnce.Do(parseConfig)
	key := "keyId"
	keyId, ok := config[key].(string)
	if !ok {
		log.Printf("No key %q in JSON configuration file %q; have you run \"camput init\"?", key, osutil.UserClientConfigPath())
		return
	}
	keyRing, hasKeyRing := config["secretRing"].(string)
	if !hasKeyRing {
		if fn := osutil.IdentitySecretRing(); fileExists(fn) {
			keyRing = fn
		} else if fn := jsonsign.DefaultSecRingPath(); fileExists(fn) {
			keyRing = fn
		} else {
			log.Printf("Couldn't find keyId %q; no 'secretRing' specified in config file, and no standard secret ring files exist.")
			return
		}
	}
	entity, err := jsonsign.EntityFromSecring(keyId, keyRing)
	if err != nil {
		log.Printf("Couldn't find keyId %q in secret ring: %v", keyId, err)
		return
	}
	armored, err = jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		log.Printf("Error serializing public key: %v", err)
		return
	}

	// TODO(mpl): integrate with getSelfPubKeyDir if possible.
	selfPubKeyDir, ok := config["selfPubKeyDir"].(string)
	if !ok {
		selfPubKeyDir = osutil.KeyBlobsDir()
		log.Printf("No 'selfPubKeyDir' defined in %q, defaulting to %v", osutil.UserClientConfigPath(), selfPubKeyDir)
	}
	fi, err := os.Stat(selfPubKeyDir)
	if err != nil || !fi.IsDir() {
		log.Printf("selfPubKeyDir of %q doesn't exist or not a directory", selfPubKeyDir)
		return
	}

	br := blob.SHA1FromString(armored)

	pubFile := filepath.Join(selfPubKeyDir, br.String()+".camli")
	fi, err = os.Stat(pubFile)
	if err != nil {
		err = ioutil.WriteFile(pubFile, []byte(armored), 0644)
		if err != nil {
			log.Printf("Error writing public key to %q: %v", pubFile, err)
			return
		}
	}

	return br, armored, true
}

func (c *Client) GetBlobFetcher() blob.SeekFetcher {
	// Use blobref.NewSeriesFetcher(...all configured fetch paths...)
	return blob.NewSimpleDirectoryFetcher(c.getSelfPubKeyDir())
}

// config[selfPubKeyDir] is the dir containing the public key(s) blob(s)
const selfPubKeyDir = "selfPubKeyDir"

func (c *Client) initSelfPubKeyDir() {
	if e := os.Getenv("CAMLI_DEV_KEYBLOBS"); e != "" {
		c.selfPubKeyDir = e
		return
	}
	configOnce.Do(parseConfig)
	v, ok := config[selfPubKeyDir].(string)
	if !ok {
		c.selfPubKeyDir = osutil.KeyBlobsDir()
		log.Printf("selfPubKeyDir: was expecting a string, got %T. Defaulting to %v", v, c.selfPubKeyDir)
		return
	}
	c.selfPubKeyDir = v
}

// TODO(mpl): integrate with getSignerPublicKeyBlobref above.
func (c *Client) getSelfPubKeyDir() string {
	c.initSelfPubKeyDirOnce.Do(c.initSelfPubKeyDir)
	return c.selfPubKeyDir
}

// config[trustedCerts] is the list of trusted certificates fingerprints.
// Case insensitive.
// See Client.trustedCerts in client.go
const trustedCerts = "trustedCerts"

func (c *Client) initTrustedCerts() {
	if e := os.Getenv("CAMLI_TRUSTED_CERT"); e != "" {
		c.trustedCerts = strings.Split(e, ",")
		return
	}
	c.trustedCerts = []string{}
	configOnce.Do(parseConfig)
	val, ok := config[trustedCerts].([]interface{})
	if !ok {
		return
	}
	for _, v := range val {
		trustedCert, ok := v.(string)
		if !ok {
			log.Printf("trustedCert: was expecting a string, got %T", v)
			return
		}
		c.trustedCerts = append(c.trustedCerts, strings.ToLower(trustedCert))
	}
}

func (c *Client) GetTrustedCerts() []string {
	c.initTrustedCertsOnce.Do(c.initTrustedCerts)
	return c.trustedCerts
}

// config[ignoredFiles] is the list of files that camput should ignore
// and not try to upload when using -filenodes.
// See Client.ignoredFiles in client.go
const ignoredFiles = "ignoredFiles"

func (c *Client) initIgnoredFiles() {
	if e := os.Getenv("CAMLI_IGNORED_FILES"); e != "" {
		c.ignoredFiles = strings.Split(e, ",")
		return
	}
	c.ignoredFiles = []string{}
	configOnce.Do(parseConfig)
	val, ok := config[ignoredFiles].([]interface{})
	if !ok {
		return
	}
	for _, v := range val {
		ignoredFile, ok := v.(string)
		if !ok {
			log.Printf("ignoredFile: was expecting a string, got %T", v)
			return
		}
		c.ignoredFiles = append(c.ignoredFiles, ignoredFile)
	}
}

func (c *Client) getIgnoredFiles() []string {
	c.initIgnoredFilesOnce.Do(func() { c.initIgnoredFiles() })
	return c.ignoredFiles
}
