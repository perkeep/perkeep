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

package main

import (
	"crypto/sha1"
	"exec"
	"flag"
	"os"
	"io/ioutil"
	"path"
	"json"
	"log"

	"camli/blobref"
	"camli/client"
	"camli/jsonsign"
	"camli/osutil"
)

var flagGpgKey = flag.String("gpgkey", "", "(init option only) GPG key to use for signing.")

func doInit() {
	blobDir := path.Join(osutil.CamliConfigDir(), "keyblobs")
	os.Mkdir(osutil.CamliConfigDir(), 0700)
	os.Mkdir(blobDir, 0700)

	keyId := *flagGpgKey
	if keyId == "" {
		keyId = os.Getenv("GPGKEY")
	}
	if keyId == "" {
		// TODO: run and parse gpg --list-secret-keys and see if there's just one and suggest that?  Or show
		// a list of them?
		log.Fatalf("Initialization requires your public GPG key.  Set --gpgkey=<pubid> or set $GPGKEY in your environment.  Run gpg --list-secret-keys to find their key IDs.")
	}

	if os.Getenv("GPG_AGENT_INFO") == "" {
		log.Printf("No GPG_AGENT_INFO found in environment; you should setup gnupg-agent.  camput will be annoying otherwise.")
	}

	// TODO: use same command-line flag as the jsonsign package.
	// unify them into a shared package just for gpg-related stuff?
	keyBytes, err := exec.Command("gpg", "--export", "--armor", keyId).Output()
	if err != nil {
                log.Fatalf("Error running gpg to export public key: %v", err)
        }
	
	hash := sha1.New()
	hash.Write(keyBytes)
	bref := blobref.FromHash("sha1", hash)
	
	keyBlobPath := path.Join(blobDir, bref.String() + ".camli")
	if err = ioutil.WriteFile(keyBlobPath, keyBytes, 0644); err != nil {
		log.Fatalf("Error writing public key blob to %q: %v", keyBlobPath, err)
	}
	
	if ok, err := jsonsign.VerifyPublicKeyFile(keyBlobPath, keyId); !ok {
		log.Fatalf("Error verifying public key at %q: %v", keyBlobPath, err)
	}
	
	log.Printf("Your Camlistore identity (your GPG public key's blobref) is: %s", bref.String())

	_, err = os.Stat(client.ConfigFilePath())
	if err == nil {
		log.Fatalf("Config file %q already exists; quitting without touching it.", client.ConfigFilePath())
	}

	if f, err := os.OpenFile(client.ConfigFilePath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		defer f.Close()
		m := make(map[string]interface{})
		m["publicKeyBlobref"] = bref.String()

		blobPut := make([]map[string]string, 1)
		blobPut[0] = map[string]string{
			"alias": "local",
			"host": "http://localhost:3179/",
			"password": "test",
		}
		m["blobPut"] = blobPut

		blobGet := make([]map[string]string, 2)
		blobGet[0] = map[string]string{
			"alias": "keyblobs",
			"path": "$HOME/.camli/keyblobs",
		}
		blobGet[1] = map[string]string{
			"alias": "local",
			"host": "http://localhost:3179/",
			"password": "test",
		}
		m["blobGet"] = blobGet


		jsonBytes, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			log.Fatalf("JSON serialization error: %v", err)
		}
		_, err = f.Write(jsonBytes)
		if err != nil {
			log.Fatalf("Error writing to %q: %v", client.ConfigFilePath(), err)
		}
		log.Printf("Wrote %q; modify as necessary.", client.ConfigFilePath())
	}
}
