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
	"log"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"camli/auth"
	"camli/blobref"
	"camli/jsonconfig"
	"camli/jsonsign"
	"camli/osutil"
)

// These, if set, override the JSON config file ~/.camli/config
// "server" and "password" keys.
//
// A main binary must call AddFlags to expose these.
var flagServer *string

func AddFlags() {
	flagServer = flag.String("blobserver", "", "camlistore blob server")
}

func ConfigFilePath() string {
	return filepath.Join(osutil.CamliConfigDir(), "config")
}

var configOnce sync.Once
var config = make(map[string]interface{})

func parseConfig() {
	configPath := ConfigFilePath()

	var err os.Error
	if config, err = jsonconfig.ReadFile(configPath); err != nil {
		log.Fatal(err.String())
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

func blobServerOrDie() string {
	if flagServer != nil && *flagServer != "" {
		return cleanServer(*flagServer)
	}
	configOnce.Do(parseConfig)
	value, ok := config["blobServer"]
	var server string
	if ok {
		server = value.(string)
	}
	server = cleanServer(server)
	if !ok || server == "" {
		log.Fatalf("Missing or invalid \"blobServer\" in %q", ConfigFilePath())
	}
	return server
}

func (c *Client) SetupAuth() os.Error {
	configOnce.Do(parseConfig)
	return c.SetupAuthFromConfig(config)
}

func (c *Client) SetupAuthFromConfig(conf jsonconfig.Obj) (err os.Error) {
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
func (c *Client) SignerPublicKeyBlobref() *blobref.BlobRef {
	return SignerPublicKeyBlobref()
}

func (c *Client) SecretRingFile() string {
	configOnce.Do(parseConfig)
	keyRing, ok := config["secretRing"].(string)
	if ok && keyRing != "" {
		return keyRing
	}
	return jsonsign.DefaultSecRingPath()
}

// TODO: move to config package?
func SignerPublicKeyBlobref() *blobref.BlobRef {
	configOnce.Do(parseConfig)
	key := "keyId"
	keyId, ok := config[key].(string)
	if !ok {
		log.Printf("No key %q in JSON configuration file %q; have you run \"camput init\"?", key, ConfigFilePath())
		return nil
	}
	keyRing, _ := config["secretRing"].(string)

	entity, err := jsonsign.EntityFromSecring(keyId, keyRing)
	if err != nil {
		log.Printf("Couldn't find keyId %q in secret ring: %v", keyId, err)
		return nil
	}
	armored, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		log.Printf("Error serializing public key: %v", err)
		return nil
	}

	selfPubKeyDir, ok := config["selfPubKeyDir"].(string)
	if !ok {
		log.Printf("No 'selfPubKeyDir' defined in %q", ConfigFilePath())
		return nil
	}
	fi, err := os.Stat(selfPubKeyDir)
	if err != nil || !fi.IsDirectory() {
		log.Printf("selfPubKeyDir of %q doesn't exist or not a directory", selfPubKeyDir)
		return nil
	}

	br := blobref.SHA1FromString(armored)

	pubFile := filepath.Join(selfPubKeyDir, br.String()+".camli")
	log.Printf("key file: %q", pubFile)
	fi, err = os.Stat(pubFile)
	if err != nil {
		err = ioutil.WriteFile(pubFile, []byte(armored), 0644)
		if err != nil {
			log.Printf("Error writing public key to %q: %v", pubFile, err)
			return nil
		}
	}

	return br
}

func (c *Client) GetBlobFetcher() blobref.SeekFetcher {
	// Use blobref.NewSeriesFetcher(...all configured fetch paths...)
	return blobref.NewConfigDirFetcher()
}
