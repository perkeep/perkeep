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

func configFilePath() string {
	return path.Join(os.Getenv("HOME"), ".camli", "config")
}

var configOnce sync.Once
var config = make(map[string]interface{})
func parseConfig() {
	f, err := os.Open(configFilePath(), os.O_RDONLY, 0)
	switch {
	case err != nil && err.(*os.PathError).Error.(os.Errno) == syscall.ENOENT:
		// TODO: write empty file?
		return
	case err != nil:
		log.Printf("Error opening config file %q: %v", configFilePath(), err)
		return
	default:
		defer f.Close()
		dj := json.NewDecoder(f)
		if err := dj.Decode(&config); err != nil {
			log.Printf("Error parsing JSON in config file %q: %v", configFilePath(), err)
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

	log.Exitf("No --blobserver parameter specified.")
	return ""
}

func passwordOrDie() string {
	if *flagPassword != "" {
		return *flagPassword
	}
	configOnce.Do(parseConfig)

	log.Exitf("No --password parameter specified.")
	return ""
}

// Returns blobref of signer's public key, or nil if unconfigured.
func (c *Client) SignerPublicKeyBlobref() *blobref.BlobRef {
	configOnce.Do(parseConfig)
	key := "publicKeyBlobref"
	v, ok := config[key]
	if !ok {
		log.Printf("No key %q in JSON configuration file %q", key, configFilePath())
		return nil
	}
	s, ok := v.(string)
	if !ok {
		log.Printf("Expected a string value for key %q in JSON file %q",
			key, configFilePath())
	}
	ref := blobref.Parse(s)
	if ref == nil {
		log.Printf("Bogus value %#v for key %q in file %q; not a valid blobref",
			s, key, configFilePath())
	}
	return ref
}

func (c *Client) GetBlobFetcher() blobref.Fetcher {
	// TODO: make a NewSeriesAttemptFetcher(...all configured fetch paths...)
	return blobref.NewSimpleDirectoryFetcher(path.Join(os.Getenv("HOME"), ".camli", "blobs"))
}
