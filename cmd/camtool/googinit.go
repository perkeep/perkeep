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
	"camlistore.org/pkg/constants/google"

	"go4.org/oauthutil"
	"golang.org/x/oauth2"
	"google.golang.org/cloud/storage"
)

type googinitCmd struct {
	storageType string
}

func init() {
	cmdmain.RegisterCommand("googinit", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(googinitCmd)
		flags.StringVar(&cmd.storageType, "type", "", "Storage type: drive or cloud")
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
		oauthConfig  *oauth2.Config
	)

	if c.storageType != "drive" && c.storageType != "cloud" {
		return cmdmain.UsageError("Invalid storage type: must be drive for Google Drive or cloud for Google Cloud Storage.")
	}

	clientId, clientSecret = getClientInfo()

	switch c.storageType {
	case "drive":
		oauthConfig = &oauth2.Config{
			Scopes:       []string{drive.Scope},
			Endpoint:     google.Endpoint,
			ClientID:     clientId,
			ClientSecret: clientSecret,
			RedirectURL:  oauthutil.TitleBarRedirectURL,
		}
	case "cloud":
		oauthConfig = &oauth2.Config{
			Scopes:       []string{storage.ScopeReadWrite},
			Endpoint:     google.Endpoint,
			ClientID:     clientId,
			ClientSecret: clientSecret,
			RedirectURL:  oauthutil.TitleBarRedirectURL,
		}
	}

	token, err := oauth2.ReuseTokenSource(nil, &oauthutil.TokenSource{
		Config: oauthConfig,
		AuthCode: func() string {
			fmt.Fprintf(cmdmain.Stdout, "Get auth code from:\n\n")
			fmt.Fprintf(cmdmain.Stdout, "%v\n\n", oauthConfig.AuthCodeURL("", oauth2.AccessTypeOffline, oauth2.ApprovalForce))
			return prompt("Enter auth code:")
		},
	}).Token()
	if err != nil {
		return fmt.Errorf("could not acquire token: %v", err)
	}

	fmt.Fprintf(cmdmain.Stdout, "\nYour Google auth object:\n\n")
	enc := json.NewEncoder(cmdmain.Stdout)
	authObj := map[string]string{
		"client_id":     clientId,
		"client_secret": clientSecret,
		"refresh_token": token.RefreshToken,
	}
	enc.Encode(authObj)
	fmt.Fprint(cmdmain.Stdout, "\n")
	return nil
}

// Prompt the user for an input line.  Return the given input.
func prompt(promptText string) string {
	fmt.Fprint(cmdmain.Stdout, promptText)
	sc := bufio.NewScanner(cmdmain.Stdin)
	sc.Scan()
	return strings.TrimSpace(sc.Text())
}

// Prompt for client id / secret
func getClientInfo() (string, string) {
	fmt.Fprintf(cmdmain.Stdout, "Please provide the client id and client secret \n")
	fmt.Fprintf(cmdmain.Stdout, "(You can find these at http://code.google.com/apis/console > your project > API Access)\n")
	var (
		clientId     string
		clientSecret string
	)
	clientId = prompt("Client ID:")
	clientSecret = prompt("Client Secret:")
	return clientId, clientSecret
}
