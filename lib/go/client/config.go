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
	"camli/blobref"
	"flag"
	"json"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
)

// These override the JSON config file ~/.camli/config "server" and
// "password" keys
var flagServer *string = flag.String("blobserver", "", "camlistore blob server")
var flagPassword *string = flag.String("password", "", "password for blob server")

func ConfigDir() string {
	return path.Join(os.Getenv("HOME"), ".camli")
}

func ConfigFilePath() string {
	return path.Join(os.Getenv("HOME"), ".camli", "config")
}

var configOnce sync.Once
var config = make(map[string]interface{})
func parseConfig() {
	f, err := os.Open(ConfigFilePath(), os.O_RDONLY, 0)
	switch {
	case err != nil && err.(*os.PathError).Error.(os.Errno) == syscall.ENOENT:
		// TODO: write empty file?
		return
	case err != nil:
		log.Printf("Error opening config file %q: %v", ConfigFilePath(), err)
		return
	default:
		defer f.Close()
		dj := json.NewDecoder(f)
		if err := dj.Decode(&config); err != nil {
			log.Printf("Error parsing JSON in config file %q: %v", ConfigFilePath(), err)
		}
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
		log.Exitf("Missing or invalid \"blobServer\" in %q", ConfigFilePath())
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
		log.Exitf("No --password parameter specified, and no \"blobServerPassword\" defined in %q", ConfigFilePath())
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
	configOnce.Do(parseConfig)
	key := "publicKeyBlobref"
	v, ok := config[key]
	if !ok {
		log.Printf("No key %q in JSON configuration file %q; have you run \"camput --init\"?", key, ConfigFilePath())
		return nil
	}
	s, ok := v.(string)
	if !ok {
		log.Printf("Expected a string value for key %q in JSON file %q",
			key, ConfigFilePath())
	}
	ref := blobref.Parse(s)
	if ref == nil {
		log.Printf("Bogus value %#v for key %q in file %q; not a valid blobref",
			s, key, ConfigFilePath())
	}
	return ref
}

func (c *Client) GetBlobFetcher() blobref.Fetcher {
	// TODO: make a NewSeriesAttemptFetcher(...all configured fetch paths...)
	return blobref.NewSimpleDirectoryFetcher(path.Join(os.Getenv("HOME"), ".camli", "keyblobs"))
}
