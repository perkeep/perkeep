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

// TODO(mpl): Shouldn't we move this subcommand to camtool? e.g as 'camtool configinit' ?
// It does not feel like a good fit in camput since this does not send anything
// to a server.

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
)

type initCmd struct {
	newKey bool
	gpgkey string
}

func init() {
	cmdmain.RegisterCommand("init", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(initCmd)
		flags.BoolVar(&cmd.newKey, "newkey", false, "Automatically generate a new identity in a new secret ring.")
		flags.StringVar(&cmd.gpgkey, "gpgkey", "", "GPG key to use for signing (overrides $GPGKEY environment)")
		return cmd
	})
}

func (c *initCmd) Describe() string {
	return "Initialize the camput configuration file. With no option, it tries to use the GPG key found in the default identity secret ring."
}

func (c *initCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: camput init [opts]")
}

func (c *initCmd) Examples() []string {
	return []string{
		"",
		"--gpgkey=XXXXX",
		"--newkey Creates a new identity",
	}
}

// keyId returns the current keyId. It checks, in this order,
// the --gpgkey flag, the GPGKEY env var, and the default
// identity secret ring.
func (c *initCmd) keyId(secRing string) (string, error) {
	if k := c.gpgkey; k != "" {
		return k, nil
	}
	if k := os.Getenv("GPGKEY"); k != "" {
		return k, nil
	}

	k, err := jsonsign.KeyIdFromRing(secRing)
	if err != nil {
		log.Printf("No suitable gpg key was found in %v: %v", secRing, err)
	} else {
		if k != "" {
			log.Printf("Re-using identity with keyId %q found in file %s", k, secRing)
			return k, nil
		}
	}

	// TODO: run and parse gpg --list-secret-keys and see if there's just one and suggest that?  Or show
	// a list of them?
	return "", errors.New("Initialization requires your public GPG key.\nYou can set --gpgkey=<pubid> or set $GPGKEY in your environment. Run gpg --list-secret-keys to find their key IDs.\nOr you can create a new secret ring and key with 'camput init --newkey'.")
}

func (c *initCmd) getPublicKeyArmoredFromFile(secretRingFileName, keyId string) (b []byte, err error) {
	entity, err := jsonsign.EntityFromSecring(keyId, secretRingFileName)
	if err == nil {
		pubArmor, err := jsonsign.ArmoredPublicKey(entity)
		if err == nil {
			return []byte(pubArmor), nil
		}
	}
	b, err = exec.Command("gpg", "--export", "--armor", keyId).Output()
	if err != nil {
		return nil, fmt.Errorf("Error running gpg to export public key %q: %v", keyId, err)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("gpg export of public key %q was empty.", keyId)
	}
	return b, nil
}

func (c *initCmd) getPublicKeyArmored(keyId string) (b []byte, err error) {
	files := []string{osutil.IdentitySecretRing(), jsonsign.DefaultSecRingPath()}
	for _, file := range files {
		b, err = c.getPublicKeyArmoredFromFile(file, keyId)
		if err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("failed to export armored public key ID %q from locations: %q", keyId, files)
}

func (c *initCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return cmdmain.ErrUsage
	}

	if c.newKey && c.gpgkey != "" {
		log.Fatal("--newkey and --gpgkey are mutually exclusive")
	}

	blobDir := path.Join(osutil.CamliConfigDir(), "keyblobs")
	os.Mkdir(osutil.CamliConfigDir(), 0700)
	os.Mkdir(blobDir, 0700)

	var keyId string
	var err error
	secRing := osutil.IdentitySecretRing()
	if c.newKey {
		keyId, err = jsonsign.GenerateNewSecRing(secRing)
		if err != nil {
			return err
		}
	} else {
		keyId, err = c.keyId(secRing)
		if err != nil {
			return err
		}
	}

	if os.Getenv("GPG_AGENT_INFO") == "" {
		log.Printf("No GPG_AGENT_INFO found in environment; you should setup gnupg-agent.  camput might be annoying otherwise, if your private key is encrypted.")
	}

	pubArmor, err := c.getPublicKeyArmored(keyId)
	if err != nil {
		return err
	}

	bref := blobref.SHA1FromString(string(pubArmor))

	keyBlobPath := path.Join(blobDir, bref.String()+".camli")
	if err = ioutil.WriteFile(keyBlobPath, pubArmor, 0644); err != nil {
		log.Fatalf("Error writing public key blob to %q: %v", keyBlobPath, err)
	}

	if ok, err := jsonsign.VerifyPublicKeyFile(keyBlobPath, keyId); !ok {
		log.Fatalf("Error verifying public key at %q: %v", keyBlobPath, err)
	}

	log.Printf("Your Camlistore identity (your GPG public key's blobref) is: %s", bref.String())

	configFilePath := osutil.UserClientConfigPath()
	_, err = os.Stat(configFilePath)
	if err == nil {
		log.Fatalf("Config file %q already exists; quitting without touching it.", configFilePath)
	}

	if f, err := os.OpenFile(configFilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		defer f.Close()
		m := make(map[string]interface{})
		m["keyId"] = keyId                    // TODO(bradfitz): make this 'identity' to match server config?
		m["publicKeyBlobref"] = bref.String() // TODO(bradfitz): not used anymore?
		m["server"] = "http://localhost:3179/"
		m["selfPubKeyDir"] = blobDir
		m["auth"] = "localhost"

		jsonBytes, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			log.Fatalf("JSON serialization error: %v", err)
		}
		_, err = f.Write(jsonBytes)
		if err != nil {
			log.Fatalf("Error writing to %q: %v", configFilePath, err)
		}
		log.Printf("Wrote %q; modify as necessary.", configFilePath)
	}
	return nil
}
