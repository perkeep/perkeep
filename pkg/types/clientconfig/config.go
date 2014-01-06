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

// Package clientconfig provides types related to the client configuration file.
package clientconfig

// Config holds the values from the JSON client config file.
type Config struct {
	Servers            map[string]*Server `json:"servers"`                      // maps server alias to server config.
	Identity           string             `json:"identity"`                     // GPG identity.
	IdentitySecretRing string             `json:"identitySecretRing,omitempty"` // location of the secret ring file.
	IgnoredFiles       []string           `json:"ignoredFiles,omitempty"`       // list of files that camput should ignore.
}

// Server holds the values specific to each server found in the JSON client config file.
type Server struct {
	Server       string   `json:"server"`                 // server URL (scheme + hostname).
	Auth         string   `json:"auth"`                   // auth scheme and values (ex: userpass:foo:bar).
	IsDefault    bool     `json:"default,omitempty"`      // whether this server is the default one.
	TrustedCerts []string `json:"trustedCerts,omitempty"` // list of trusted certificates fingerprints.
}
