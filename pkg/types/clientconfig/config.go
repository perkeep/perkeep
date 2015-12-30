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

// Package clientconfig provides types related to the client configuration
// file.
package clientconfig

import (
	"errors"
	"fmt"
	"strings"

	"camlistore.org/pkg/httputil"
	"go4.org/jsonconfig"

	"go4.org/wkfs"
)

// Config holds the values from the JSON client config file.
type Config struct {
	Servers            map[string]*Server `json:"servers"`                      // maps server alias to server config.
	Identity           string             `json:"identity"`                     // GPG identity.
	IdentitySecretRing string             `json:"identitySecretRing,omitempty"` // location of the secret ring file.
	IgnoredFiles       []string           `json:"ignoredFiles,omitempty"`       // list of files that camput should ignore.
}

// Server holds the values specific to each server found in the JSON client
// config file.
type Server struct {
	Server       string   `json:"server"`                 // server URL (scheme + hostname).
	Auth         string   `json:"auth"`                   // auth scheme and values (ex: userpass:foo:bar).
	IsDefault    bool     `json:"default,omitempty"`      // whether this server is the default one.
	TrustedCerts []string `json:"trustedCerts,omitempty"` // list of trusted certificates fingerprints.
}

// Alias returns the alias of the server from conf that matches server, or the
// empty string if no match. A match means the server from the config is a
// prefix of the input server. The longest match prevails.
func (conf *Config) Alias(server string) string {
	longestMatch := ""
	serverAlias := ""
	for alias, serverConf := range conf.Servers {
		if strings.HasPrefix(server, serverConf.Server) {
			if len(serverConf.Server) > len(longestMatch) {
				longestMatch = serverConf.Server
				serverAlias = alias
			}
		}
	}
	return serverAlias
}

// GenerateClientConfig retuns a client configuration which can be used to
// access a server defined by the provided low-level server configuration.
func GenerateClientConfig(serverConfig jsonconfig.Obj) (*Config, error) {
	missingConfig := func(param string) (*Config, error) {
		return nil, fmt.Errorf("required value for %q not found", param)
	}

	if serverConfig == nil {
		return nil, errors.New("server config is a required parameter")
	}
	param := "auth"
	auth := serverConfig.OptionalString(param, "")
	if auth == "" {
		return missingConfig(param)
	}

	listen := serverConfig.OptionalString("listen", "")
	baseURL := serverConfig.OptionalString("baseURL", "")
	if listen == "" {
		listen = baseURL
	}
	if listen == "" {
		return nil, errors.New("required value for 'listen' or 'baseURL' not found")
	}

	https := serverConfig.OptionalBool("https", false)
	if !strings.HasPrefix(listen, "http://") && !strings.HasPrefix(listen, "https://") {
		if !https {
			listen = "http://" + listen
		} else {
			listen = "https://" + listen
		}
	}

	param = "httpsCert"
	httpsCert := serverConfig.OptionalString(param, "")
	if https && httpsCert == "" {
		return missingConfig(param)
	}

	// TODO(mpl): See if we can detect that the cert is not self-signed,and in
	// that case not add it to the trustedCerts
	var trustedList []string
	if https && httpsCert != "" {
		certPEMBlock, err := wkfs.ReadFile(httpsCert)
		if err != nil {
			return nil, fmt.Errorf("could not read certificate: %v", err)
		}
		sig, err := httputil.CertFingerprint(certPEMBlock)
		if err != nil {
			return nil, fmt.Errorf("could not get fingerprints of certificate: %v", err)
		}
		trustedList = []string{sig}
	}

	param = "prefixes"
	prefixes := serverConfig.OptionalObject(param)
	if len(prefixes) == 0 {
		return missingConfig(param)
	}

	param = "/sighelper/"
	sighelper := prefixes.OptionalObject(param)
	if len(sighelper) == 0 {
		return missingConfig(param)
	}

	param = "handlerArgs"
	handlerArgs := sighelper.OptionalObject(param)
	if len(handlerArgs) == 0 {
		return missingConfig(param)
	}

	param = "keyId"
	keyId := handlerArgs.OptionalString(param, "")
	if keyId == "" {
		return missingConfig(param)
	}

	param = "secretRing"
	secretRing := handlerArgs.OptionalString(param, "")
	if secretRing == "" {
		return missingConfig(param)
	}

	return &Config{
		Servers: map[string]*Server{
			"default": {
				Server:       listen,
				Auth:         auth,
				IsDefault:    true,
				TrustedCerts: trustedList,
			},
		},
		Identity:           keyId,
		IdentitySecretRing: secretRing,
		IgnoredFiles:       []string{".DS_Store", "*~"},
	}, nil
}
