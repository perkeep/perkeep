/*
Copyright 2011 The Perkeep Authors

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

package osutil

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/buildinfo"
)

// HomeDir returns the path to the user's home directory.
// It returns the empty string if the value isn't known.
func HomeDir() string {
	failInTests()
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
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
	if d := os.Getenv("PERKEEP_CACHE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("CAMLI_CACHE_DIR"); d != "" {
		return d
	}
	failInTests()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(HomeDir(), "Library", "Caches", "Camlistore")
	case "windows":
		// Per http://technet.microsoft.com/en-us/library/cc749104(v=ws.10).aspx
		// these should both exist. But that page overwhelms me. Just try them
		// both. This seems to work.
		for _, ev := range []string{"TEMP", "TMP"} {
			if v := os.Getenv(ev); v != "" {
				return filepath.Join(v, "Perkeep")
			}
		}
		panic("No Windows TEMP or TMP environment variables found; please file a bug report.")
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "perkeep")
	}
	return filepath.Join(HomeDir(), ".cache", "perkeep")
}

func makeCacheDir() {
	err := os.MkdirAll(cacheDir(), 0700)
	if err != nil {
		log.Fatalf("Could not create cacheDir %v: %v", cacheDir(), err)
	}
}

func upperFirst(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}

func CamliVarDir() (string, error) {
	oldName := camliVarDirOf("camlistore")
	newName := camliVarDirOf("perkeep")

	if fi, err := os.Lstat(oldName); err == nil && fi.IsDir() && oldName != newName {
		n := numRegularFilesUnder(oldName)
		if n == 0 {
			log.Printf("removing old, empty var directory %s", oldName)
			os.RemoveAll(oldName)
		} else {
			return "", fmt.Errorf("Now that Perkeep has been renamed from Camlistore, you need to rename your data directory from %s to %s", oldName, newName)
		}
	}
	return newName, nil
}

func numRegularFilesUnder(dir string) (n int) {
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if fi != nil && fi.Mode().IsRegular() {
			n++
		}
		return nil
	})
	return
}

func camliVarDirOf(name string) string {
	if d := os.Getenv("CAMLI_VAR_DIR"); d != "" {
		return d
	}
	failInTests()
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), upperFirst(name))
	case "darwin":
		return filepath.Join(HomeDir(), "Library", upperFirst(name))
	}
	return filepath.Join(HomeDir(), "var", name)
}

func CamliBlobRoot() (string, error) {
	varDir, err := CamliVarDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(varDir, "blobs"), nil
}

// RegisterConfigDirFunc registers a func f to return the Perkeep configuration directory.
// It may skip by returning the empty string.
func RegisterConfigDirFunc(f func() string) {
	configDirFuncs = append(configDirFuncs, f)
}

var configDirFuncs []func() string

func PerkeepConfigDir() (string, error) {
	if p := os.Getenv("CAMLI_CONFIG_DIR"); p != "" {
		return p, nil
	}
	for _, f := range configDirFuncs {
		if v := f(); v != "" {
			return v, nil
		}
	}

	failInTests()
	return perkeepConfigDir()
}

func perkeepConfigDir() (string, error) {
	oldName := configDirNamed("camlistore")
	newName := configDirNamed("perkeep")
	if fi, err := os.Lstat(oldName); err == nil && fi.IsDir() && oldName != newName {
		n := numRegularFilesUnder(oldName)
		if n == 0 {
			log.Printf("removing old, empty config dir %s", oldName)
			os.RemoveAll(oldName)
		} else {
			return "", fmt.Errorf("Error: old configuration directory detected. Not running until it's moved.\nRename %s to %s\n", oldName, newName)
		}
	}
	return newName, nil
}

var configDirNamedTestHook func(string) string

func configDirNamed(name string) string {
	if h := configDirNamedTestHook; h != nil {
		return h(name)
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), upperFirst(name))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, name)
	}
	return filepath.Join(HomeDir(), ".config", name)
}

func UserServerConfigPath() string {
	dir, err := PerkeepConfigDir()
	if err != nil {
		log.Fatalf("Could not compute UserServerConfigPath: %v", err)
	}
	return filepath.Join(dir, "server-config.json")
}

func UserClientConfigPath() string {
	dir, err := PerkeepConfigDir()
	if err != nil {
		log.Fatalf("Could not compute UserClientConfigPath: %v", err)
	}
	return filepath.Join(dir, "client-config.json")
}

// If set, flagSecretRing overrides the JSON config file
// ~/.config/perkeep/client-config.json
// (i.e. UserClientConfigPath()) "identitySecretRing" key.
var (
	flagSecretRing      string
	secretRingFlagAdded bool
)

// AddSecretRingFlag registers the "secret-keyring" flag, accessible via
// ExplicitSecretRingFile.
func AddSecretRingFlag() {
	flag.StringVar(&flagSecretRing, "secret-keyring", "", "GnuPG secret keyring file to use.")
	secretRingFlagAdded = true
}

// HasSecretRingFlag reports whether the "secret-keywring" command-line flag was
// registered. If so, it is safe to use ExplicitSecretRingFile.
func HasSecretRingFlag() bool { return secretRingFlagAdded }

// ExplicitSecretRingFile returns the path to the user's GPG secret ring
// file and true if it was ever set through the --secret-keyring flag or
// the CAMLI_SECRET_RING var. It returns "", false otherwise.
// Use of this function requires the program to call AddSecretRingFlag,
// and before flag.Parse is called.
func ExplicitSecretRingFile() (string, bool) {
	if !secretRingFlagAdded {
		panic("proper use of ExplicitSecretRingFile requires exposing flagSecretRing with AddSecretRingFlag")
	}
	if flagSecretRing != "" {
		return flagSecretRing, true
	}
	if e := os.Getenv("CAMLI_SECRET_RING"); e != "" {
		return e, true
	}
	return "", false
}

// DefaultSecretRingFile returns the path to the default GPG secret
// keyring. It is not influenced by any flag or CAMLI* env var.
func DefaultSecretRingFile() string {
	dir, err := perkeepConfigDir()
	if err != nil {
		log.Fatalf("couldn't compute DefaultSecretRingFile: %v", err)
	}
	return filepath.Join(dir, "identity-secring.gpg")
}

// identitySecretRing returns the path to the default GPG
// secret keyring. It is still affected by CAMLI_CONFIG_DIR.
func identitySecretRing() string {
	dir, err := PerkeepConfigDir()
	if err != nil {
		log.Fatalf("Could not compute identitySecretRing: %v", err)
	}
	return filepath.Join(dir, "identity-secring.gpg")
}

// SecretRingFile returns the path to the user's GPG secret ring file.
// The value comes from either the --secret-keyring flag (if previously
// registered with AddSecretRingFlag), or the CAMLI_SECRET_RING environment
// variable, or the operating system default location.
func SecretRingFile() string {
	if flagSecretRing != "" {
		return flagSecretRing
	}
	if e := os.Getenv("CAMLI_SECRET_RING"); e != "" {
		return e
	}
	return identitySecretRing()
}

// DefaultTLSCert returns the path to the default TLS certificate
// file that is used (creating if necessary) when TLS is specified
// without the cert file.
func DefaultTLSCert() string {
	dir, err := PerkeepConfigDir()
	if err != nil {
		log.Fatalf("Could not compute DefaultTLSCert: %v", err)
	}
	return filepath.Join(dir, "tls.crt")
}

// DefaultTLSKey returns the path to the default TLS key
// file that is used (creating if necessary) when TLS is specified
// without the key file.
func DefaultTLSKey() string {
	dir, err := PerkeepConfigDir()
	if err != nil {
		log.Fatalf("Could not compute DefaultTLSKey: %v", err)
	}
	return filepath.Join(dir, "tls.key")
}

// RegisterLetsEncryptCacheFunc registers a func f to return the path to the
// default Let's Encrypt cache.
// It may skip by returning the empty string.
func RegisterLetsEncryptCacheFunc(f func() string) {
	letsEncryptCacheFuncs = append(letsEncryptCacheFuncs, f)
}

var letsEncryptCacheFuncs []func() string

// DefaultLetsEncryptCache returns the path to the default Let's Encrypt cache
// directory (or file, depending on the ACME implementation).
func DefaultLetsEncryptCache() string {
	for _, f := range letsEncryptCacheFuncs {
		if v := f(); v != "" {
			return v
		}
	}
	dir, err := PerkeepConfigDir()
	if err != nil {
		log.Fatalf("Could not compute DefaultLetsEncryptCache: %v", err)
	}
	return filepath.Join(dir, "letsencrypt.cache")
}

// NewJSONConfigParser returns a jsonconfig.ConfigParser with its IncludeDirs
// set with PerkeepConfigDir and the contents of CAMLI_INCLUDE_PATH.
func NewJSONConfigParser() *jsonconfig.ConfigParser {
	var cp jsonconfig.ConfigParser
	dir, err := PerkeepConfigDir()
	if err != nil {
		log.Fatalf("NewJSONConfigParser error: %v", err)
	}
	cp.IncludeDirs = append([]string{dir}, filepath.SplitList(os.Getenv("CAMLI_INCLUDE_PATH"))...)
	return &cp
}

// PkSourceRoot returns the root of the source tree, or an error.
func PkSourceRoot() (string, error) {
	root, err := GoModPackagePath()
	if err == nil {
		return root, nil
	}
	err = fmt.Errorf("could not found go.mod, trying GOPATH: %w", err)
	root, errp := GoPackagePath("perkeep.org")
	if errors.Is(errp, os.ErrNotExist) {
		return "", fmt.Errorf("directory \"perkeep.org\" not found under GOPATH/src; "+
			"can't run Perkeep integration tests: %v", errors.Join(err, errp))
	}
	return root, nil
}

// GoPackagePath returns the path to the provided Go package's
// source directory.
// pkg may be a path prefix without any *.go files.
// The error is os.ErrNotExist if GOPATH is unset.
func GoPackagePath(pkg string) (path string, err error) {
	gp := os.Getenv("GOPATH")
	if gp == "" {
		cmd := exec.Command("go", "env", "GOPATH")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("could not run 'go env GOPATH': %v, %s", err, out)
		}
		gp = strings.TrimSpace(string(out))
		if gp == "" {
			return "", os.ErrNotExist
		}
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

// GoModPackagePath return the absolute path for the go.mod file without "go.mod" suffix.
func GoModPackagePath() (string, error) {
	gmp := os.Getenv("GOMOD")
	if gmp == "" {
		cmd := exec.Command("go", "env", "GOMOD")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("could not run 'go env GOMOD': %v, %s", err, out)
		}
		gmp = strings.TrimSuffix(strings.TrimSpace(string(out)), "go.mod")
		if gmp == "" {
			return "", os.ErrNotExist
		}
	}
	fi, err := os.Stat(gmp)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory: %w", gmp, os.ErrNotExist)
	}
	return gmp, nil
}

func failInTests() {
	if buildinfo.TestingLinked() {
		panic("Unexpected non-hermetic use of host configuration during testing. (alternatively: the 'testing' package got accidentally linked in)")
	}
}

func goPathBinDir() (string, error) {
	cmd := exec.Command("go", "env", "GOPATH")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("could not get GOPATH: %v, %s", err, out)
	}
	paths := filepath.SplitList(strings.TrimSpace(string(out)))
	if len(paths) < 1 {
		return "", errors.New("no GOPATH")
	}
	return filepath.Join(paths[0], "bin"), nil
}

// LookPathGopath uses exec.LookPath to find binName, and then falls back to
// looking in $GOPATH/bin.
func LookPathGopath(binName string) (string, error) {
	binPath, err := exec.LookPath(binName)
	if err == nil {
		return binPath, nil
	}
	binDir, err := goPathBinDir()
	if err != nil {
		return "", fmt.Errorf("command %q not found in $PATH, and could not look in $GOPATH/bin because %v", binName, err)
	}
	binPath = filepath.Join(binDir, binName)
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	if _, err := os.Stat(binPath); err != nil {
		return "", err
	}
	return binPath, nil
}
