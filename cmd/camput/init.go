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
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/client/android"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/types/clientconfig"
)

type initCmd struct {
	newKey      bool   // whether to create a new GPG ring and key.
	noconfig    bool   // whether to generate a client config file.
	keyId       string // GPG key ID to use.
	secretRing  string // GPG secret ring file to use.
	userPass    string // username and password to use when asking a server for the config.
	insecureTLS bool   // TLS certificate verification disabled
}

func init() {
	cmdmain.RegisterCommand("init", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(initCmd)
		flags.BoolVar(&cmd.newKey, "newkey", false,
			"Automatically generate a new identity in a new secret ring at the default location (~/.config/camlistore/identity-secring.gpg on linux).")
		flags.StringVar(&cmd.keyId, "gpgkey", "", "GPG key ID to use for signing (overrides $GPGKEY environment)")
		flags.BoolVar(&cmd.noconfig, "noconfig", false, "Stop after creating the public key blob, and do not try and create a config file.")
		flags.StringVar(&cmd.userPass, "userpass", "", "username:password to use when asking a server for a client configuration. Requires --server global option.")
		flags.BoolVar(&cmd.insecureTLS, "insecure", false, "If set, when getting configuration from a server (with --server and --userpass) over TLS, the server's certificate verification is disabled. Needed when the server is using a self-signed certificate.")
		return cmd
	})
}

func (c *initCmd) Describe() string {
	return "Initialize the camput configuration file. With no option, it tries to use the GPG key found in the default identity secret ring."
}

func (c *initCmd) Usage() {
	usage := "Usage: camput [--server host] init [opts]\n\nExamples:\n"
	for _, v := range c.usageExamples() {
		usage += v + "\n"
	}
	fmt.Fprintf(cmdmain.Stderr, usage)
}

func (c *initCmd) usageExamples() []string {
	var examples []string
	for _, v := range c.Examples() {
		examples = append(examples, "camput init "+v)
	}
	return append(examples,
		"camput --server=https://localhost:3179 init --userpass=foo:bar --insecure=true")
}

func (c *initCmd) Examples() []string {
	// TODO(mpl): I can't add the correct -userpass example to that list, because
	// it requires the global --server flag, which has to be passed before the
	// "init" subcommand. We should have a way to override that.
	// Or I could just add a -server flag to the init subcommand, but it sounds
	// like a lame hack.
	return []string{
		"",
		"--gpgkey=XXXXX",
		"--newkey #Creates a new identity",
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

func (c *initCmd) clientConfigFromServer() (*clientconfig.Config, error) {
	if c.noconfig {
		log.Print("--userpass and --noconfig are mutually exclusive")
		return nil, cmdmain.ErrUsage
	}
	server := client.ExplicitServer()
	if server == "" {
		log.Print("--userpass requires --server")
		return nil, cmdmain.ErrUsage
	}
	fields := strings.Split(c.userPass, ":")
	if len(fields) != 2 {
		log.Printf("wrong userpass; wanted username:password, got %q", c.userPass)
		return nil, cmdmain.ErrUsage
	}

	cl := client.NewFromParams(server,
		auth.NewBasicAuth(fields[0], fields[1]),
		client.OptionInsecure(c.insecureTLS))

	helpRoot, err := cl.HelpRoot()
	if err != nil {
		return nil, err
	}

	var cc clientconfig.Config
	if err := cl.GetJSON(helpRoot+"?clientConfig=true", &cc); err != nil {
		return nil, err
	}
	return &cc, nil
}

func (c *initCmd) writeConfig(cc *clientconfig.Config) error {
	configFilePath := osutil.UserClientConfigPath()
	if _, err := os.Stat(configFilePath); err == nil {
		return fmt.Errorf("Config file %q already exists; quitting without touching it.", configFilePath)
	}
	if err := os.MkdirAll(filepath.Dir(configFilePath), 0700); err != nil {
		return err
	}

	jsonBytes, err := json.MarshalIndent(cc, "", "  ")
	if err != nil {
		log.Fatalf("JSON serialization error: %v", err)
	}
	if err := ioutil.WriteFile(configFilePath, jsonBytes, 0600); err != nil {
		return fmt.Errorf("could not write client config file %v: %v", configFilePath, err)
	}
	log.Printf("Wrote %q; modify as necessary.", configFilePath)
	return nil

}

func (c *initCmd) RunCommand(args []string) error {
	if len(args) > 0 {
		return cmdmain.ErrUsage
	}

	if c.newKey && c.keyId != "" {
		log.Fatal("--newkey and --gpgkey are mutually exclusive")
	}

	if c.userPass != "" {
		cc, err := c.clientConfigFromServer()
		if err != nil {
			return err
		}
		return c.writeConfig(cc)
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

	return c.writeConfig(&clientconfig.Config{
		Servers: map[string]*clientconfig.Server{
			"localhost": {
				Server:    "http://localhost:3179",
				IsDefault: true,
				Auth:      "localhost",
			},
		},
		Identity:     c.keyId,
		IgnoredFiles: []string{".DS_Store"},
	})
}
