package main

import (
	"camli/blobref"
	"camli/client"
	"camli/jsonsign"
	"crypto/sha1"
	"exec"
	"flag"
	"os"
	"io/ioutil"
	"path"
	"json"
	"log"
)

var flagGpgKey = flag.String("gpgkey", "", "(init option only) GPG key to use for signing.")

func doInit() {
	blobDir := path.Join(client.ConfigDir(), "keyblobs")
	os.Mkdir(client.ConfigDir(), 0700)
	os.Mkdir(blobDir, 0700)

	keyId := *flagGpgKey
	if keyId == "" {
		keyId = os.Getenv("GPGKEY")
	}
	if keyId == "" {
		// TODO: run and parse gpg --list-secret-keys and see if there's just one and suggest that?  Or show
		// a list of them?
		log.Exitf("Initialization requires your public GPG key.  Set --gpgkey=<pubid> or set $GPGKEY in your environment.  Run gpg --list-secret-keys to find their key IDs.")
	}

	if os.Getenv("GPG_AGENT_INFO") == "" {
		log.Printf("No GPG_AGENT_INFO found in environment; you should setup gnupg-agent.  camput will be annoying otherwise.")
	}

	// TODO: use same command-line flag as the jsonsign package.
	// unify them into a shared package just for gpg-related stuff?
	gpgBinary, err := exec.LookPath("gpg")
	if err != nil {
		log.Exitf("Failed to find gpg binary in your path.")
	}
	cmd, err := exec.Run(gpgBinary,
		[]string{"gpg", "--export", "--armor", keyId},
		os.Environ(),
		"/",
		exec.DevNull,
		exec.Pipe,
		exec.DevNull)
	if err != nil {
		log.Exitf("Error running gpg to export public key: %v", err)
	}
	keyBytes, err := ioutil.ReadAll(cmd.Stdout)
	if err != nil {
                log.Exitf("Error read from gpg to export public key: %v", err)
        }
	
	hash := sha1.New()
	hash.Write(keyBytes)
	bref := blobref.FromHash("sha1", hash)
	
	keyBlobPath := path.Join(blobDir, bref.String() + ".camli")
	if err = ioutil.WriteFile(keyBlobPath, keyBytes, 0644); err != nil {
		log.Exitf("Error writing public key blob to %q: %v", keyBlobPath, err)
	}
	
	if ok, err := jsonsign.VerifyPublicKeyFile(keyBlobPath, keyId); !ok {
		log.Exitf("Error verifying public key at %q: %v", keyBlobPath, err)
	}
	
	log.Printf("Your Camlistore identity (your GPG public key's blobref) is: %s", bref.String())

	_, err = os.Stat(client.ConfigFilePath())
	if err == nil {
		log.Exitf("Config file %q already exists; quitting without touching it.", client.ConfigFilePath())
	}

	if f, err := os.Open(client.ConfigFilePath(), os.O_CREAT|os.O_WRONLY, 0600); err == nil {
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
			log.Exitf("JSON serialization error: %v", err)
		}
		_, err = f.Write(jsonBytes)
		if err != nil {
			log.Exitf("Error writing to %q: %v", client.ConfigFilePath(), err)
		}
		log.Printf("Wrote %q; modify as necessary.", client.ConfigFilePath())
	}
}