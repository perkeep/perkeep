/*
Copyright 2014 The Perkeep Authors.

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
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"perkeep.org/pkg/blobserver/google/drive"

	"cloud.google.com/go/storage"
	"go4.org/oauthutil"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func runAdminGoogInit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("perkeepd admin googinit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	storageType := flags.String("type", "", "Storage type: drive or cloud")
	flags.Usage = func() {
		fmt.Fprint(stderr, `Usage: perkeepd admin googinit --type=drive|cloud

Init Google Drive or Google Cloud Storage credentials for the server.
Prompts for the OAuth client ID, client secret, and authorization code.
The output contains credentials; it is not redacted.

Options:
`)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "googinit does not accept positional arguments")
		flags.Usage()
		return 2
	}
	if *storageType != "drive" && *storageType != "cloud" {
		fmt.Fprintln(stderr, "Invalid storage type: must be drive for Google Drive or cloud for Google Cloud Storage.")
		flags.Usage()
		return 2
	}
	if err := googInit(*storageType, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "perkeepd admin googinit: %v\n", err)
		return 1
	}
	return 0
}

func googInit(storageType string, stdin io.Reader, stdout io.Writer) error {
	// Reuse the scanner so read-ahead from piped input survives each prompt.
	sc := bufio.NewScanner(stdin)
	prompt := func(text string) (string, error) {
		if _, err := fmt.Fprint(stdout, text); err != nil {
			return "", err
		}
		if !sc.Scan() || sc.Err() != nil {
			err := sc.Err()
			if err == nil {
				err = io.EOF
			}
			return "", fmt.Errorf("reading %s %w", text, err)
		}
		return strings.TrimSpace(sc.Text()), nil
	}
	if _, err := fmt.Fprint(stdout, "Please provide the client id and client secret \n"+
		"(You can find these at https://console.developers.google.com/apis/credentials)\n"); err != nil {
		return err
	}
	clientID, err := prompt("Client ID:")
	if err != nil {
		return err
	}
	clientSecret, err := prompt("Client Secret:")
	if err != nil {
		return err
	}

	var scope string
	switch storageType {
	case "drive":
		scope = drive.Scope
	case "cloud":
		scope = storage.ScopeReadWrite
	}
	oauthConfig := &oauth2.Config{
		Scopes:       []string{scope},
		Endpoint:     google.Endpoint,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  oauthutil.TitleBarRedirectURL,
	}
	if _, err := fmt.Fprintf(stdout, "Get auth code from:\n\n%v\n\n",
		oauthConfig.AuthCodeURL("", oauth2.AccessTypeOffline, oauth2.ApprovalForce)); err != nil {
		return err
	}
	authCode, err := prompt("Enter auth code:")
	if err != nil {
		return err
	}
	token, err := oauth2.ReuseTokenSource(nil, &oauthutil.TokenSource{
		Config:   oauthConfig,
		AuthCode: func() string { return authCode },
	}).Token()
	if err != nil {
		return fmt.Errorf("could not acquire token: %w", err)
	}

	if _, err := fmt.Fprint(stdout, "\nYour Google auth object:\n\n"); err != nil {
		return err
	}
	authObj := map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"refresh_token": token.RefreshToken,
	}
	if err := json.NewEncoder(stdout).Encode(authObj); err != nil {
		return err
	}
	if _, err := fmt.Fprint(stdout, "\n\nFor server-config.json, your 'googlecloudstorage' value (update with your bucket name and path):\n\n"); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s:%s:%s:bucketName[/optional/dir]\n", clientID, clientSecret, token.RefreshToken)
	return err
}
