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
	"bufio"
	"fmt"
	"json"
	"os"
	"strings"

	"camli/blobserver/googlestorage"
	"camli/third_party/code.google.com/goauth2/oauth"
)

func main() {
	var (
		err          os.Error
		clientId     string
		clientSecret string
	)

	if clientId, clientSecret, err = getClientInfo(); err != nil {
		panic(err)
	}
	transport := googlestorage.MakeOauthTransport(clientId, clientSecret, "")

	var accessCode string
	if accessCode, err = getAccessCode(transport.Config); err != nil {
		panic(err)
	}
	if _, err = transport.Exchange(accessCode); err != nil {
		panic(err)
	}

	fmt.Printf("\nYour Google Storage auth object:\n\n")
	enc := json.NewEncoder(os.Stdout)
	authObj := map[string]string{
		"client_id":     transport.ClientId,
		"client_secret": transport.ClientSecret,
		"refresh_token": transport.RefreshToken,
	}
	enc.Encode(authObj)
	fmt.Print("\n")
}

// Prompt the user for an input line.  Return the given input.
func prompt(promptText string) (string, os.Error) {
	fmt.Print(promptText)
	input := bufio.NewReader(os.Stdin)
	line, _, err := input.ReadLine()
	if err != nil {
		return "", fmt.Errorf("Failed to read line: %v", err)
	}
	return strings.TrimSpace(string(line)), nil
}

// Provide the authorization link, then prompt for the resulting access code
func getAccessCode(config *oauth.Config) (string, os.Error) {
	fmt.Printf("In order to obtain a storage access code, you will need to naviage to the following URL:\n\n")
	fmt.Printf("https://accounts.google.com/o/oauth2/auth?client_id=%s&redirect_uri=urn:ietf:wg:oauth:2.0:oob&scope=%s&response_type=code\n\n",
		config.ClientId, config.Scope)
	return prompt("Please enter the access code provided by that page:")
}

// Prompt for client id / secret
func getClientInfo() (string, string, os.Error) {
	fmt.Printf("Please provide the client id and client secret for your google storage account\n")
	fmt.Printf("(You can find these at http://code.google.com/apis/console > your project > API Access)\n")
	var (
		err          os.Error
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
