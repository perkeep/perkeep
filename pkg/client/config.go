/*
Copyright 2011 The Perkeep Authors.

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

package client

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/client/android"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/types/camtypes"
	"perkeep.org/pkg/types/clientconfig"

	"go4.org/wkfs"
)

// If set, flagServer overrides the JSON config file
// ~/.config/perkeep/client-config.json
// (i.e. osutil.UserClientConfigPath()) "server" key.
//
// A main binary must call AddFlags to expose it.
var flagServer string

// AddFlags registers the "server" and "secret-keyring" string flags.
func AddFlags() {
	defaultPath := "/x/y/z/we're/in-a-test"
	if !buildinfo.TestingLinked() {
		defaultPath = osutil.UserClientConfigPath()
	}
	flag.StringVar(&flagServer, "server", "", "Perkeep server prefix. If blank, the default from the \"server\" field of "+defaultPath+" is used. Acceptable forms: https://you.example.com, example.com:1345 (https assumed), or http://you.example.com/alt-root")
	osutil.AddSecretRingFlag()
}

// ExplicitServer returns the Perkeep server given in the "server"
// flag, if any.
//
// Use AddFlags to register the flag before any flag.Parse call.
func ExplicitServer() string {
	return flagServer
}

var (
	configOnce sync.Once
	config     *clientconfig.Config

	configDisabled, _ = strconv.ParseBool(os.Getenv("CAMLI_DISABLE_CLIENT_CONFIG_FILE"))
)

// config parsing in the global environment.
func parseConfig() {
	var nilClient *Client
	nilClient.parseConfig()
}

// lazy config parsing when there's a known client already.
// The client c may be nil.
func (c *Client) parseConfig() {
	if android.OnAndroid() {
		panic("parseConfig should never have been called on Android")
	}
	if configDisabled {
		panic("parseConfig should never have been called with CAMLI_DISABLE_CLIENT_CONFIG_FILE set")
	}
	configPath := osutil.UserClientConfigPath()
	if _, err := wkfs.Stat(configPath); os.IsNotExist(err) {
		if c != nil && c.isSharePrefix {
			return
		}
		errMsg := fmt.Sprintf("Client configuration file %v does not exist. See 'pk-put init' to generate it.", configPath)
		if keyID := serverKeyId(); keyID != "" {
			hint := fmt.Sprintf("\nThe key id %v was found in the server config %v, so you might want:\n'pk-put init -gpgkey %v'", keyID, osutil.UserServerConfigPath(), keyID)
			errMsg += hint
		}
		log.Fatal(errMsg)
	}
	// TODO: instead of using jsonconfig, we could read the file,
	// and unmarshal into the structs that we now have in
	// pkg/types/clientconfig. But we'll have to add the old
	// fields (before the name changes, and before the
	// multi-servers change) to the structs as well for our
	// graceful conversion/error messages to work.
	conf, err := osutil.NewJSONConfigParser().ReadFile(configPath)
	if err != nil {
		log.Fatal(err.Error())
	}
	cfg := jsonconfig.Obj(conf)

	if singleServerAuth := cfg.OptionalString("auth", ""); singleServerAuth != "" {
		newConf, err := convertToMultiServers(cfg)
		if err != nil {
			log.Print(err)
		} else {
			cfg = newConf
		}
	}

	config = &clientconfig.Config{
		Identity:           cfg.OptionalString("identity", ""),
		IdentitySecretRing: cfg.OptionalString("identitySecretRing", ""),
		IgnoredFiles:       cfg.OptionalList("ignoredFiles"),
	}
	serversList := make(map[string]*clientconfig.Server)
	servers := cfg.OptionalObject("servers")
	for alias, vei := range servers {
		// An alias should never be confused with a host name,
		// so we forbid anything looking like one.
		if isURLOrHostPort(alias) {
			log.Fatalf("Server alias %q looks like a hostname; \".\" or \";\" are not allowed.", alias)
		}
		serverMap, ok := vei.(map[string]any)
		if !ok {
			log.Fatalf("entry %q in servers section is a %T, want an object", alias, vei)
		}
		serverConf := jsonconfig.Obj(serverMap)
		serverStr, err := cleanServer(serverConf.OptionalString("server", ""))
		if err != nil {
			log.Fatalf("invalid server alias %q: %v", alias, err)
		}
		server := &clientconfig.Server{
			Server:       serverStr,
			Auth:         serverConf.OptionalString("auth", ""),
			IsDefault:    serverConf.OptionalBool("default", false),
			TrustedCerts: serverConf.OptionalList("trustedCerts"),
		}
		if err := serverConf.Validate(); err != nil {
			log.Fatalf("Error in servers section of config file for server %q: %v", alias, err)
		}
		serversList[alias] = server
	}
	config.Servers = serversList
	if err := cfg.Validate(); err != nil {
		printConfigChangeHelp(cfg)
		log.Fatalf("Error in config file: %v", err)
	}
}

// isURLOrHostPort returns true if s looks like a URL, or a hostname, i.e it starts with a scheme and/or it contains a period or a colon.
func isURLOrHostPort(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.Contains(s, ".") || strings.Contains(s, ":")
}

// convertToMultiServers takes an old style single-server client configuration and maps it to new a multi-servers configuration that is returned.
func convertToMultiServers(conf jsonconfig.Obj) (jsonconfig.Obj, error) {
	server := conf.OptionalString("server", "")
	if server == "" {
		return nil, errors.New("could not convert config to multi-servers style: no \"server\" key found")
	}
	newConf := jsonconfig.Obj{
		"servers": map[string]any{
			"server": map[string]any{
				"auth":    conf.OptionalString("auth", ""),
				"default": true,
				"server":  server,
			},
		},
		"identity":           conf.OptionalString("identity", ""),
		"identitySecretRing": conf.OptionalString("identitySecretRing", ""),
	}
	if ignoredFiles := conf.OptionalList("ignoredFiles"); ignoredFiles != nil {
		var list []any
		for _, v := range ignoredFiles {
			list = append(list, v)
		}
		newConf["ignoredFiles"] = list
	}
	return newConf, nil
}

// printConfigChangeHelp checks if conf contains obsolete keys,
// and prints additional help in this case.
func printConfigChangeHelp(conf jsonconfig.Obj) {
	// rename maps from old key names to the new ones.
	// If there is no new one, the value is the empty string.
	rename := map[string]string{
		"keyId":            "identity",
		"publicKeyBlobref": "",
		"selfPubKeyDir":    "",
		"secretRing":       "identitySecretRing",
	}
	oldConfig := false
	configChangedMsg := fmt.Sprintf("The client configuration file (%s) keys have changed.\n", osutil.UserClientConfigPath())
	for _, unknown := range conf.UnknownKeys() {
		v, ok := rename[unknown]
		if ok {
			if v != "" {
				configChangedMsg += fmt.Sprintf("%q should be renamed %q.\n", unknown, v)
			} else {
				configChangedMsg += fmt.Sprintf("%q should be removed.\n", unknown)
			}
			oldConfig = true
		}
	}
	if oldConfig {
		configChangedMsg += "Please see https://perkeep.org/doc/client-config, or use pk-put init to recreate a default one."
		log.Print(configChangedMsg)
	}
}

// serverKeyId returns the public gpg key id ("identity" field)
// from the user's server config , if any.
// It returns the empty string otherwise.
func serverKeyId() string {
	serverConfigFile := osutil.UserServerConfigPath()
	if _, err := wkfs.Stat(serverConfigFile); err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		log.Fatalf("Could not stat %v: %v", serverConfigFile, err)
	}
	obj, err := jsonconfig.ReadFile(serverConfigFile)
	if err != nil {
		return ""
	}
	keyID, ok := obj["identity"].(string)
	if !ok {
		return ""
	}
	return keyID
}

// cleanServer returns the canonical URL of the provided server, which must be a URL, IP, host (with dot), or host/ip:port.
// The returned canonical URL will have trailing slashes removed and be prepended with "https://" if no scheme is provided.
func cleanServer(server string) (string, error) {
	if !isURLOrHostPort(server) {
		return "", fmt.Errorf("server %q does not look like a server address and could be confused with a server alias. It should look like [http[s]://]foo[.com][:port] with at least one of the optional parts.", server)
	}
	// Remove trailing slash if provided.
	server = strings.TrimSuffix(server, "/")

	// Default to "https://" when not specified
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "https://" + server
	}
	return server, nil
}

// getServer returns the server's URL found either as a command-line flag,
// or as the default server in the config file.
func getServer() (string, error) {
	if s := os.Getenv("CAMLI_SERVER"); s != "" {
		return cleanServer(s)
	}
	if flagServer != "" {
		if !isURLOrHostPort(flagServer) {
			configOnce.Do(parseConfig)
			serverConf, ok := config.Servers[flagServer]
			if ok {
				return serverConf.Server, nil
			}
			log.Printf("%q looks like a server alias, but no such alias found in config.", flagServer)
		} else {
			return cleanServer(flagServer)
		}
	}
	server, err := defaultServer()
	if err != nil {
		return "", err
	}
	if server == "" {
		return "", camtypes.ErrClientNoServer
	}
	return cleanServer(server)
}

func defaultServer() (string, error) {
	configOnce.Do(parseConfig)
	wantAlias := os.Getenv("CAMLI_DEFAULT_SERVER")
	for alias, serverConf := range config.Servers {
		if (wantAlias != "" && wantAlias == alias) || (wantAlias == "" && serverConf.IsDefault) {
			return cleanServer(serverConf.Server)
		}
	}
	return "", nil
}

func (c *Client) useTLS() bool {
	return strings.HasPrefix(c.discoRoot(), "https://")
}

// SetupAuth sets the client's authMode. It tries from the environment first if we're on android or in dev mode, and then from the client configuration.
func (c *Client) SetupAuth() error {
	if c.noExtConfig {
		if c.authMode != nil {
			if _, ok := c.authMode.(*auth.None); !ok {
				return nil
			}
		}
		return errors.New("client: noExtConfig set; auth should not be configured from config or env vars")
	}
	// env var takes precedence, but only if we're in dev mode or on android.
	// Too risky otherwise.
	if android.OnAndroid() ||
		env.IsDev() ||
		configDisabled {
		authMode, err := auth.FromEnv()
		if err == nil {
			c.authMode = authMode
			return nil
		}
		if err != auth.ErrNoAuth {
			return fmt.Errorf("Could not set up auth from env var CAMLI_AUTH: %v", err)
		}
	}
	if c.server == "" {
		return fmt.Errorf("no server defined for this client: can not set up auth")
	}
	authConf := serverAuth(c.server)
	if authConf == "" {
		c.authErr = fmt.Errorf("could not find auth key for server %q in config, defaulting to no auth", c.server)
		c.authMode = auth.None{}
		return nil
	}
	var err error
	c.authMode, err = auth.FromConfig(authConf)
	return err
}

// serverAuth returns the auth scheme for server from the config, or the empty string if the server was not found in the config.
func serverAuth(server string) string {
	configOnce.Do(parseConfig)
	alias := config.Alias(server)
	if alias == "" {
		return ""
	}
	return config.Servers[alias].Auth
}

// SetupAuthFromString configures the clients authentication mode from
// an explicit auth string.
func (c *Client) SetupAuthFromString(a string) error {
	// TODO(mpl): review the one using that (pkg/blobserver/remote/remote.go)
	var err error
	c.authMode, err = auth.FromConfig(a)
	return err
}

// SecretRingFile returns the filename to the user's GPG secret ring.
// The value comes from either the --secret-keyring flag, the
// CAMLI_SECRET_RING environment variable, the client config file's
// "identitySecretRing" value, or the operating system default location.
func (c *Client) SecretRingFile() string {
	if osutil.HasSecretRingFlag() {
		if secretRing, ok := osutil.ExplicitSecretRingFile(); ok {
			return secretRing
		}
	}
	if android.OnAndroid() {
		panic("on android, so CAMLI_SECRET_RING should have been defined, or --secret-keyring used.")
	}
	if c.noExtConfig {
		log.Print("client: noExtConfig set; cannot get secret ring file from config or env vars.")
		return ""
	}
	if configDisabled {
		panic("Need a secret ring, and config file disabled")
	}
	configOnce.Do(parseConfig)
	if config.IdentitySecretRing == "" {
		return osutil.SecretRingFile()
	}
	return config.IdentitySecretRing
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// SignerPublicKeyBlobref returns the blobref of signer's public key.
// The blobref may not be valid (zero blob.Ref) if e.g the configuration
// is invalid or incomplete.
func (c *Client) SignerPublicKeyBlobref() blob.Ref {
	c.initSignerPublicKeyBlobrefOnce.Do(c.initSignerPublicKeyBlobref)
	return c.signerPublicKeyRef
}

func (c *Client) initSignerPublicKeyBlobref() {
	if c.noExtConfig {
		log.Print("client: noExtConfig set; cannot get public key from config or env vars.")
		return
	}
	keyID := os.Getenv("CAMLI_KEYID")
	if keyID == "" {
		configOnce.Do(parseConfig)
		keyID = config.Identity
		if keyID == "" {
			log.Fatalf("No 'identity' key in JSON configuration file %q; have you run \"pk-put init\"?", osutil.UserClientConfigPath())
		}
	}
	keyRing := c.SecretRingFile()
	if !fileExists(keyRing) {
		log.Fatalf("Could not find keyID %q, because secret ring file %q does not exist.", keyID, keyRing)
	}
	entity, err := jsonsign.EntityFromSecring(keyID, keyRing)
	if err != nil {
		log.Fatalf("Couldn't find keyID %q in secret ring %v: %v", keyID, keyRing, err)
	}
	armored, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		log.Fatalf("Error serializing public key: %v", err)
	}

	c.signerPublicKeyRef = blob.RefFromString(armored)
	c.publicKeyArmored = armored
}

func (c *Client) initTrustedCerts() {
	if c.noExtConfig {
		return
	}
	if e := os.Getenv("CAMLI_TRUSTED_CERT"); e != "" {
		c.trustedCerts = strings.Split(e, ",")
		return
	}
	c.trustedCerts = []string{}
	if android.OnAndroid() || configDisabled {
		return
	}
	if c.server == "" {
		log.Printf("No server defined: can not define trustedCerts for this client.")
		return
	}
	trustedCerts := c.serverTrustedCerts(c.server)
	if trustedCerts == nil {
		return
	}
	for _, trustedCert := range trustedCerts {
		c.trustedCerts = append(c.trustedCerts, strings.ToLower(trustedCert))
	}
}

// serverTrustedCerts returns the trusted certs for server from the config.
func (c *Client) serverTrustedCerts(server string) []string {
	configOnce.Do(c.parseConfig)
	if config == nil {
		return nil
	}
	alias := config.Alias(server)
	if alias == "" {
		return nil
	}
	return config.Servers[alias].TrustedCerts
}

func (c *Client) getTrustedCerts() []string {
	c.initTrustedCertsOnce.Do(c.initTrustedCerts)
	return c.trustedCerts
}

func (c *Client) initIgnoredFiles() {
	defer func() {
		c.ignoreChecker = newIgnoreChecker(c.ignoredFiles)
	}()
	if c.noExtConfig {
		return
	}
	if e := os.Getenv("CAMLI_IGNORED_FILES"); e != "" {
		c.ignoredFiles = strings.Split(e, ",")
		return
	}
	c.ignoredFiles = []string{}
	if android.OnAndroid() || configDisabled {
		return
	}
	configOnce.Do(parseConfig)
	c.ignoredFiles = config.IgnoredFiles
}

var osutilHomeDir = osutil.HomeDir // changed by tests

// newIgnoreChecker uses ignoredFiles to build and return a func that returns whether the file path argument should be ignored. See IsIgnoredFile for the ignore rules.
func newIgnoreChecker(ignoredFiles []string) func(path string) (shouldIgnore bool) {
	var fns []func(string) bool

	// copy of ignoredFiles for us to mutate
	ignFiles := append([]string(nil), ignoredFiles...)
	for k, v := range ignFiles {
		if strings.HasPrefix(v, filepath.FromSlash("~/")) {
			ignFiles[k] = filepath.Join(osutilHomeDir(), v[2:])
		}
	}
	// We cache the ignoredFiles patterns in 3 categories (not necessarily exclusive):
	// 1) shell patterns
	// 3) absolute paths
	// 4) paths components
	for _, pattern := range ignFiles {
		_, err := filepath.Match(pattern, "whatever")
		if err == nil {
			fns = append(fns, func(v string) bool { return isShellPatternMatch(pattern, v) })
		}
	}
	for _, pattern := range ignFiles {
		if filepath.IsAbs(pattern) {
			fns = append(fns, func(v string) bool { return hasDirPrefix(filepath.Clean(pattern), v) })
		} else {
			fns = append(fns, func(v string) bool { return hasComponent(filepath.Clean(pattern), v) })
		}
	}

	return func(path string) bool {
		for _, fn := range fns {
			if fn(path) {
				return true
			}
		}
		return false
	}
}

var filepathSeparatorString = string(filepath.Separator)

// isShellPatternMatch returns whether fullpath matches the shell pattern, as defined by http://golang.org/pkg/path/filepath/#Match. As an additional special case, when the pattern looks like a basename, the last path element of fullpath is also checked against it.
func isShellPatternMatch(shellPattern, fullpath string) bool {
	match, _ := filepath.Match(shellPattern, fullpath)
	if match {
		return true
	}
	if !strings.Contains(shellPattern, filepathSeparatorString) {
		match, _ := filepath.Match(shellPattern, filepath.Base(fullpath))
		if match {
			return true
		}
	}
	return false
}

// hasDirPrefix reports whether the path has the provided directory prefix.
// Both should be absolute paths.
func hasDirPrefix(dirPrefix, fullpath string) bool {
	if !strings.HasPrefix(fullpath, dirPrefix) {
		return false
	}
	if len(fullpath) == len(dirPrefix) {
		return true
	}
	if fullpath[len(dirPrefix)] == filepath.Separator {
		return true
	}
	return false
}

// hasComponent returns whether the pathComponent is a path component of
// fullpath. i.e it is a part of fullpath that fits exactly between two path
// separators.
func hasComponent(component, fullpath string) bool {
	// trim Windows volume name
	fullpath = strings.TrimPrefix(fullpath, filepath.VolumeName(fullpath))
	for {
		i := strings.Index(fullpath, component)
		if i == -1 {
			return false
		}
		if i != 0 && fullpath[i-1] == filepath.Separator {
			componentEnd := i + len(component)
			if componentEnd == len(fullpath) {
				return true
			}
			if fullpath[componentEnd] == filepath.Separator {
				return true
			}
		}
		fullpath = fullpath[i+1:]
	}
}
