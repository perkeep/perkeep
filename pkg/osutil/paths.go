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

// Package osutil provides operating system-specific path information,
// and other utility functions.
package osutil

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// HomeDir returns the path to the user's home directory.
// It returns the empty string if the value isn't known.
func HomeDir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEPATH")
	}
	return os.Getenv("HOME")
}

// Username returns the current user's username, as
// reported by the relevant environment variable.
func Username() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("USERNAME")
	}
	return os.Getenv("USER")
}

var cacheDirOnce sync.Once

func CacheDir() string {
	cacheDirOnce.Do(makeCacheDir)
	return cacheDir()
}

func cacheDir() string {
	if d := os.Getenv("CAMLI_CACHE_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(HomeDir(), "Library", "Caches", "Camlistore")
	case "windows":
		// Per http://technet.microsoft.com/en-us/library/cc749104(v=ws.10).aspx
		// these should both exist. But that page overwhelms me. Just try them
		// both. This seems to work.
		for _, ev := range []string{"TEMP", "TMP"} {
			if v := os.Getenv(ev); v != "" {
				return filepath.Join(v, "Camlistore")
			}
		}
		panic("No Windows TEMP or TMP environment variables found; please file a bug report.")
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "camlistore")
	}
	return filepath.Join(HomeDir(), ".cache", "camlistore")
}

func makeCacheDir() {
	err := os.MkdirAll(cacheDir(), 0700)
	if err != nil {
		log.Fatalf("Could not create cacheDir %v: %v", cacheDir(), err)
	}
}

func CamliVarDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Camlistore")
	case "darwin":
		return filepath.Join(HomeDir(), "Library", "Camlistore")
	}
	return filepath.Join(HomeDir(), "var", "camlistore")
}

func CamliBlobRoot() string {
	return filepath.Join(CamliVarDir(), "blobs")
}

func CamliConfigDir() string {
	if p := os.Getenv("CAMLI_CONFIG_DIR"); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "Camlistore")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "camlistore")
	}
	return filepath.Join(HomeDir(), ".config", "camlistore")
}

func UserServerConfigPath() string {
	return filepath.Join(CamliConfigDir(), "server-config.json")
}

func UserClientConfigPath() string {
	return filepath.Join(CamliConfigDir(), "client-config.json")
}

// IdentitySecretRing returns the path to the default GPG
// secret keyring. It is overriden by the CAMLI_SECRET_RING
// environment variable.
func IdentitySecretRing() string {
	if e := os.Getenv("CAMLI_SECRET_RING"); e != "" {
		return e
	}
	return filepath.Join(CamliConfigDir(), "identity-secring.gpg")
}

// KeyBlobsDir returns the path to the directory containing
// the blob(s) for the public gpg key(s). It is overriden by
// the CAMLI_DEV_KEYBLOBS environment variable.
func KeyBlobsDir() string {
	if e := os.Getenv("CAMLI_DEV_KEYBLOBS"); e != "" {
		return e
	}
	return filepath.Join(CamliConfigDir(), "keyblobs")
}

// DefaultTLSCert returns the path to the default TLS certificate
// file that is used (creating if necessary) when TLS is specified
// without the cert file.
func DefaultTLSCert() string {
	return filepath.Join(CamliConfigDir(), "selfgen_pem.crt")
}

// DefaultTLSKey returns the path to the default TLS key
// file that is used (creating if necessary) when TLS is specified
// without the key file.
func DefaultTLSKey() string {
	return filepath.Join(CamliConfigDir(), "selfgen_pem.key")
}

// Find the correct absolute path corresponding to a relative path,
// searching the following sequence of directories:
// 1. Working Directory
// 2. CAMLI_CONFIG_DIR (deprecated, will complain if this is on env)
// 3. (windows only) APPDATA/camli
// 4. All directories in CAMLI_INCLUDE_PATH (standard PATH form for OS)
func FindCamliInclude(configFile string) (absPath string, err error) {
	// Try to open as absolute / relative to CWD
	_, err = os.Stat(configFile)
	if err == nil {
		return configFile, nil
	}
	if filepath.IsAbs(configFile) {
		// End of the line for absolute path
		return "", err
	}

	// Try the config dir
	configDir := CamliConfigDir()
	if _, err = os.Stat(filepath.Join(configDir, configFile)); err == nil {
		return filepath.Join(configDir, configFile), nil
	}

	// Finally, search CAMLI_INCLUDE_PATH
	p := os.Getenv("CAMLI_INCLUDE_PATH")
	for _, d := range strings.Split(p, string(filepath.ListSeparator)) {
		if _, err = os.Stat(filepath.Join(d, configFile)); err == nil {
			return filepath.Join(d, configFile), nil
		}
	}

	return "", os.ErrNotExist
}

// GoPackagePath returns the path to the provided Go package's
// source directory.
// pkg may be a path prefix without any *.go files.
// The error is os.ErrNotExist if GOPATH is unset or the directory
// doesn't exist in any GOPATH component.
func GoPackagePath(pkg string) (path string, err error) {
	gp := os.Getenv("GOPATH")
	if gp == "" {
		return path, os.ErrNotExist
	}
	for _, p := range filepath.SplitList(gp) {
		dir := filepath.Join(p, "src", filepath.FromSlash(pkg))
		fi, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !fi.IsDir() {
			continue
		}
		return dir, nil
	}
	return path, os.ErrNotExist
}
