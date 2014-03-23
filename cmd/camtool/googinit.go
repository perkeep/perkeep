/*
Copyright 2014 The Camlistore Authors.

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
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"camlistore.org/pkg/blobserver/google/drive"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/googlestorage"
	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

type googinitCmd struct {
	storageType string
}

func init() {
	cmdmain.RegisterCommand("googinit", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(googinitCmd)
		flags.StringVar(&cmd.storageType, "type", "drive", "Storage type: drive or cloud")
		return cmd
	})
}

func (c *googinitCmd) Describe() string {
	return "Init Google Drive or Google Cloud Storage."
}

func (c *googinitCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: camtool [globalopts] googinit [commandopts] \n")
}

func (c *googinitCmd) RunCommand(args []string) error {
	var (
		err          error
		clientId     string
		clientSecret string
		transport    *oauth.Transport
	)

	if c.storageType != "drive" && c.storageType != "cloud" {
		return cmdmain.UsageError("Invalid storage type.")
	}

	if clientId, clientSecret, err = getClientInfo(); err != nil {
		return err
	}

	switch c.storageType {
	case "drive":
		transport = drive.MakeOauthTransport(clientId, clientSecret, "")
	case "cloud":
		transport = googlestorage.MakeOauthTransport(clientId, clientSecret, "")
	}

	var accessCode string
	if accessCode, err = getAccessCode(transport.Config); err != nil {
		return err
	}
	if _, err = transport.Exchange(accessCode); err != nil {
		return err
	}

	fmt.Fprintf(cmdmain.Stdout, "\nYour Google auth object:\n\n")
	enc := json.NewEncoder(cmdmain.Stdout)
	authObj := map[string]string{
		"client_id":     transport.ClientId,
		"client_secret": transport.ClientSecret,
		"refresh_token": transport.RefreshToken,
	}
	enc.Encode(authObj)
	fmt.Fprint(cmdmain.Stdout, "\n")
	return nil
}

// Prompt the user for an input line.  Return the given input.
func prompt(promptText string) (string, error) {
	fmt.Fprint(cmdmain.Stdout, promptText)
	input := bufio.NewReader(cmdmain.Stdin)
	line, _, err := input.ReadLine()
	if err != nil {
		return "", fmt.Errorf("Failed to read line: %v", err)
	}
	return strings.TrimSpace(string(line)), nil
}

// Provide the authorization link, then prompt for the resulting access code
func getAccessCode(config *oauth.Config) (string, error) {
	fmt.Fprintf(cmdmain.Stdout, "In order to obtain an access code, you will need to navigate to the following URL:\n\n")
	fmt.Fprintf(cmdmain.Stdout, "https://accounts.google.com/o/oauth2/auth?client_id=%s&redirect_uri=urn:ietf:wg:oauth:2.0:oob&scope=%s&response_type=code\n\n",
		config.ClientId, config.Scope)
	return prompt("Please enter the access code provided by that page:")
}

// Prompt for client id / secret
func getClientInfo() (string, string, error) {
	fmt.Fprintf(cmdmain.Stdout, "Please provide the client id and client secret \n")
	fmt.Fprintf(cmdmain.Stdout, "(You can find these at http://code.google.com/apis/console > your project > API Access)\n")
	var (
		err          error
		clientId     string
		clientSecret string
	)
	if clientId, err = prompt("Client ID:"); err != nil {
		return "", "", err
	}
	if clientSecret, err = prompt("Client Secret:"); err != nil {
		return "", "", err
	}
	return clientId, clientSecret, nil
}
