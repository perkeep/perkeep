/*
Copyright 2011 The Camlistore Authors.

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
	"camlistore.org/pkg/client/android"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
)

// These, if set, override the JSON config file
// ~/.config/camlistore/client-config.json
// (i.e. osutil.UserClientConfigPath()) "server" and "password" keys.
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
var config *clientConfig

// clientConfig holds the values found in the JSON client config file
// once it's been parsed and validated by parseConfig.
// Unless otherwise specified by the comments, no default values were
// used when parsing.
type clientConfig struct {
	auth               string
	server             string
	identity           string
	identitySecretRing string // defaults to osutil.IdentitySecretRing()
	trustedCerts       []string
	ignoredFiles       []string
}

func parseConfig() {
	if android.OnAndroid() {
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

	conf, err := jsonconfig.ReadFile(configPath)
	if err != nil {
		log.Fatal(err.Error())
	}
	cfg := jsonconfig.Obj(conf)
	config = &clientConfig{
		auth:               cfg.OptionalString("auth", ""),
		server:             cfg.OptionalString("server", ""),
		identity:           cfg.OptionalString("identity", ""),
		identitySecretRing: cfg.OptionalString("identitySecretRing", osutil.IdentitySecretRing()),
		trustedCerts:       cfg.OptionalList("trustedCerts"),
		ignoredFiles:       cfg.OptionalList("ignoredFiles"),
	}
	if err := cfg.Validate(); err != nil {
		printConfigChangeHelp(cfg)
		log.Fatalf("Error in config file: %v", err)
	}
}

// printConfigChangeHelp checks if conf contains obsolete keys,
// and prints additional help in this case.
func printConfigChangeHelp(conf jsonconfig.Obj) {
	// rename maps from old key names to the new ones.
	// If there is no new one, the value is the empty string.
	rename := map[string]string{
		"keyId":            "identity",
		"publicKeyBlobref": "",
		"selfPubKeyDir":    "",
		"secretRing":       "identitySecretRing",
	}
	oldConfig := false
	configChangedMsg := fmt.Sprintf("The client configuration file (%s) keys have changed.\n", osutil.UserClientConfigPath())
	for _, unknown := range conf.UnknownKeys() {
		for k, v := range rename {
			if unknown == k {
				if v != "" {
					configChangedMsg += fmt.Sprintf("%q should be renamed %q.\n", k, v)
				} else {
					configChangedMsg += fmt.Sprintf("%q should be removed.\n", k)
				}
				oldConfig = true
				break
			}
		}
	}
	if oldConfig {
		configChangedMsg += "Please see http://camlistore.org/docs/client-config, or use camput init to recreate a default one."
		log.Print(configChangedMsg)
	}
}

// serverKeyId returns the public gpg key id ("identity" field)
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
	server := cleanServer(config.server)
	if server == "" {
		log.Fatalf("Missing or invalid \"server\" in %q", osutil.UserClientConfigPath())
	}
	return server
}

func (c *Client) useTLS() bool {
	return strings.HasPrefix(c.server, "https://")
}

// SetupAuth sets the client's authMode from the client
// configuration file or from the environment.
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
	var err error
	if config.auth == "" {
		c.authMode, err = auth.FromEnv()
	} else {
		c.authMode, err = auth.FromConfig(config.auth)
	}
	return err
}

// SetupAuthFromConfig sets the Client's authMode using the "auth" key in conf
// if found, or the environment otherwise.
func (c *Client) SetupAuthFromConfig(conf jsonconfig.Obj) error {
	// TODO(mpl): leaving this one alone for now because it's used by remote as well.
	// See about converting/removing it later.
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

// SecretRingFile returns the filename to the user's GPG secret ring.
// The value comes from either a command-line flag, the
// CAMLI_SECRET_RING environment variable, the client config file's
// "identitySecretRing" value, or the operating system default location.
func (c *Client) SecretRingFile() string {
	if flagSecretRing != "" {
		return flagSecretRing
	}
	if e := os.Getenv("CAMLI_SECRET_RING"); e != "" {
		return e
	}
	configOnce.Do(parseConfig)
	return config.identitySecretRing
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// SignerPublicKeyBlobref returns the blobref of signer's public key.
// The blobref may not be valid (zero blob.Ref) if e.g the configuration
// is invalid or incomplete.
func (c *Client) SignerPublicKeyBlobref() blob.Ref {
	c.initSignerPublicKeyBlobrefOnce.Do(c.initSignerPublicKeyBlobref)
	return c.signerPublicKeyRef
}

func (c *Client) initSignerPublicKeyBlobref() {
	configOnce.Do(parseConfig)
	keyId := config.identity
	if keyId == "" {
		log.Fatalf("No 'identity' key in JSON configuration file %q; have you run \"camput init\"?", osutil.UserClientConfigPath())
	}
	keyRing := c.SecretRingFile()
	if !fileExists(keyRing) {
		log.Fatalf("Could not find keyId %q, because secret ring file %q does not exist.", keyId, keyRing)
	}
	entity, err := jsonsign.EntityFromSecring(keyId, keyRing)
	if err != nil {
		log.Fatalf("Couldn't find keyId %q in secret ring %v: %v", keyId, keyRing, err)
	}
	armored, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		log.Fatalf("Error serializing public key: %v", err)
	}

	// TODO(mpl): completely get rid of it if possible
	// http://camlistore.org/issue/377
	selfPubKeyDir := osutil.KeyBlobsDir()
	fi, err := os.Stat(selfPubKeyDir)
	if err != nil || !fi.IsDir() {
		log.Fatalf("selfPubKeyDir as %q doesn't exist or not a directory", selfPubKeyDir)
	}

	br := blob.SHA1FromString(armored)
	pubFile := filepath.Join(selfPubKeyDir, br.String()+".camli")
	fi, err = os.Stat(pubFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat %q: %v", pubFile, err)
		}
		err = ioutil.WriteFile(pubFile, []byte(armored), 0644)
		if err != nil {
			log.Fatalf("Error writing public key to %q: %v", pubFile, err)
		}
	}
	c.signerPublicKeyRef = br
	c.publicKeyArmored = armored
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
	if config.trustedCerts == nil {
		return
	}
	for _, trustedCert := range config.trustedCerts {
		c.trustedCerts = append(c.trustedCerts, strings.ToLower(trustedCert))
	}
}

func (c *Client) getTrustedCerts() []string {
	c.initTrustedCertsOnce.Do(c.initTrustedCerts)
	return c.trustedCerts
}

// config[ignoredFiles] is the list of files that camput should ignore
// and not try to upload.
// See Client.ignoredFiles in client.go
const ignoredFiles = "ignoredFiles"

func (c *Client) initIgnoredFiles() {
	if e := os.Getenv("CAMLI_IGNORED_FILES"); e != "" {
		c.ignoredFiles = strings.Split(e, ",")
		return
	}
	c.ignoredFiles = []string{}
	configOnce.Do(parseConfig)
	if config.ignoredFiles == nil {
		return
	}
	c.ignoredFiles = config.ignoredFiles
}

func (c *Client) getIgnoredFiles() []string {
	c.initIgnoredFilesOnce.Do(c.initIgnoredFiles)
	return c.ignoredFiles
}
