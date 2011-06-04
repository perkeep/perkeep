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
	"json"
	"log"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"camli/blobref"
	"camli/errorutil"
	"camli/jsonconfig"
	"camli/jsonsign"
	"camli/osutil"
)

// These override the JSON config file ~/.camli/config "server" and
// "password" keys
var flagServer *string = flag.String("blobserver", "", "camlistore blob server")
var flagPassword *string = flag.String("password", "", "password for blob server")

func ConfigFilePath() string {
	return filepath.Join(osutil.CamliConfigDir(), "config")
}

var configOnce sync.Once
var config = make(map[string]interface{})

func parseConfig() {
	configPath := ConfigFilePath()
	f, err := os.Open(configPath)
	switch {
	case err != nil && err.(*os.PathError).Error.(os.Errno) == syscall.ENOENT:
		// TODO: write empty file?
		return
	case err != nil:
		log.Printf("Error opening config file %q: %v", ConfigFilePath(), err)
		return
	}
	defer f.Close()
	dj := json.NewDecoder(f)
	if err := dj.Decode(&config); err != nil {
		extra := ""
		if serr, ok := err.(*json.SyntaxError); ok {
			if _, serr := f.Seek(0, os.SEEK_SET); serr != nil {
				log.Fatalf("seek error: %v", serr)
			}
			line, col, highlight := errorutil.HighlightBytePosition(f, serr.Offset)
			extra = fmt.Sprintf(":\nError at line %d, column %d (file offset %d):\n%s",
				line, col, serr.Offset, highlight)
		}
		log.Fatalf("error parsing JSON object in config file %s%s\n%v",
			ConfigFilePath(), extra, err)
	}
	if err := jsonconfig.EvaluateExpressions(config); err != nil {
		log.Fatalf("error expanding JSON config expressions in %s: %v", configPath, err)
	}

}

func cleanServer(server string) string {
	// Remove trailing slash if provided.
	if strings.HasSuffix(server, "/") {
		server = server[0 : len(server)-1]
	}
	// Add "http://" prefix if not present:
	if !strings.HasPrefix(server, "http") {
		server = "http://" + server
	}
	return server
}

func blobServerOrDie() string {
	if *flagServer != "" {
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

func passwordOrDie() string {
	if *flagPassword != "" {
		return *flagPassword
	}
	configOnce.Do(parseConfig)
	value, ok := config["blobServerPassword"]
	var password string
	if ok {
		password, ok = value.(string)
	}
	if !ok {
		log.Fatalf("No --password parameter specified, and no \"blobServerPassword\" defined in %q", ConfigFilePath())
	}
	if password == "" {
		// TODO: provide way to override warning?
		// Or make a way to do deferred errors?  A blank password might
		// be valid, but it might also signal the root cause of an error
		// in the future.
		log.Printf("Warning: blank \"blobServerPassword\" defined in %q", ConfigFilePath())
	}
	return password
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
		log.Printf("No key %q in JSON configuration file %q; have you run \"camput --init\"?", key, ConfigFilePath())
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

	br := blobref.Sha1FromString(armored)

	pubFile := filepath.Join(selfPubKeyDir, br.String() + ".camli")
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
