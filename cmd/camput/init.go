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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client/android"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/types/clientconfig"
)

type initCmd struct {
	newKey     bool   // whether to create a new GPG ring and key.
	noconfig   bool   // whether to generate a client config file.
	keyId      string // GPG key ID to use.
	secretRing string // GPG secret ring file to use.
}

func init() {
	cmdmain.RegisterCommand("init", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(initCmd)
		flags.BoolVar(&cmd.newKey, "newkey", false,
			"Automatically generate a new identity in a new secret ring at the default location (~/.config/camlistore/identity-secring.gpg on linux).")
		flags.StringVar(&cmd.keyId, "gpgkey", "", "GPG key ID to use for signing (overrides $GPGKEY environment)")
		flags.BoolVar(&cmd.noconfig, "noconfig", false, "Stop after creating the public key blob, and do not try and create a config file.")
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

// initSecretRing sets c.secretRing. It tries, in this order, the --secret-keyring flag,
// the CAMLI_SECRET_RING env var, then defaults to the operating system dependent location
// otherwise.
// It returns an error if the file does not exist.
func (c *initCmd) initSecretRing() error {
	if secretRing, ok := osutil.ExplicitSecretRingFile(); ok {
		c.secretRing = secretRing
	} else {
		if android.OnAndroid() {
			panic("on android, so CAMLI_SECRET_RING should have been defined, or --secret-keyring used.")
		}
		c.secretRing = osutil.SecretRingFile()
	}
	if _, err := os.Stat(c.secretRing); err != nil {
		hint := "\nA GPG key is required, please use 'camput init --newkey'.\n\nOr if you know what you're doing, you can set the global camput flag --secret-keyring, or the CAMLI_SECRET_RING env var, to use your own GPG ring. And --gpgkey=<pubid> or GPGKEY to select which key ID to use."
		return fmt.Errorf("Could not use secret ring file %v: %v.\n%v", c.secretRing, err, hint)
	}
	return nil
}

// initKeyId sets c.keyId. It checks, in this order, the --gpgkey flag, the GPGKEY env var,
// and in the default identity secret ring.
func (c *initCmd) initKeyId() error {
	if k := c.keyId; k != "" {
		return nil
	}
	if k := os.Getenv("GPGKEY"); k != "" {
		c.keyId = k
		return nil
	}

	k, err := jsonsign.KeyIdFromRing(c.secretRing)
	if err != nil {
		hint := "You can set --gpgkey=<pubid> or the GPGKEY env var to select which key ID to use.\n"
		return fmt.Errorf("No suitable gpg key was found in %v: %v.\n%v", c.secretRing, err, hint)
	}
	c.keyId = k
	log.Printf("Re-using identity with keyId %q found in file %s", c.keyId, c.secretRing)
	return nil
}

func (c *initCmd) getPublicKeyArmored() ([]byte, error) {
	entity, err := jsonsign.EntityFromSecring(c.keyId, c.secretRing)
	if err != nil {
		return nil, fmt.Errorf("Could not find keyId %v in ring %v: %v", c.keyId, c.secretRing, err)
	}
	pubArmor, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		return nil, fmt.Errorf("failed to export armored public key ID %q from %v: %v", c.keyId, c.secretRing, err)
	}
	return []byte(pubArmor), nil
}

func (c *initCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return cmdmain.ErrUsage
	}

	if c.newKey && c.keyId != "" {
		log.Fatal("--newkey and --gpgkey are mutually exclusive")
	}

	var err error
	if c.newKey {
		c.secretRing = osutil.DefaultSecretRingFile()
		c.keyId, err = jsonsign.GenerateNewSecRing(c.secretRing)
		if err != nil {
			return err
		}
	} else {
		if err := c.initSecretRing(); err != nil {
			return err
		}
		if err := c.initKeyId(); err != nil {
			return err
		}
	}

	pubArmor, err := c.getPublicKeyArmored()
	if err != nil {
		return err
	}

	bref := blob.SHA1FromString(string(pubArmor))

	log.Printf("Your Camlistore identity (your GPG public key's blobref) is: %s", bref.String())

	if c.noconfig {
		return nil
	}

	configFilePath := osutil.UserClientConfigPath()
	_, err = os.Stat(configFilePath)
	if err == nil {
		log.Fatalf("Config file %q already exists; quitting without touching it.", configFilePath)
	}
	if err := os.MkdirAll(filepath.Dir(configFilePath), 0700); err != nil {
		return err
	}
	if f, err := os.OpenFile(configFilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		defer f.Close()
		m := &clientconfig.Config{
			Servers: map[string]*clientconfig.Server{
				"localhost": {
					Server:    "http://localhost:3179",
					IsDefault: true,
					Auth:      "localhost",
				},
			},
			Identity:     c.keyId,
			IgnoredFiles: []string{".DS_Store"},
		}

		jsonBytes, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			log.Fatalf("JSON serialization error: %v", err)
		}
		_, err = f.Write(jsonBytes)
		if err != nil {
			log.Fatalf("Error writing to %q: %v", configFilePath, err)
		}
		log.Printf("Wrote %q; modify as necessary.", configFilePath)
	} else {
		return fmt.Errorf("could not write client config file %v: %v", configFilePath, err)
	}
	return nil
}
