/*
Copyright 2026 The Perkeep Authors

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

// The iossign command signs an unsigned Perkeep iOS app build (from
// clients/mobile/ios/build.sh) and installs it on a device or uploads it
// to TestFlight.
//
// It's meant to run on a Mac that holds Apple credentials, separate from
// the machine that builds the app, which needs none. The bundle ID,
// entitlements, and build number come from iossign's configuration and
// flags, not from the artifact, and iossign shows what it's about to sign
// and asks before doing so.
//
// Usage:
//
//	iossign install [-device name-or-udid] [-y] Perkeep.app
//	iossign upload [-build N] [-ipa file] [-no-upload] [-y] Perkeep.app
//
// Configuration is read from iossign.json in the perkeep directory under
// os.UserConfigDir (~/Library/Application Support/perkeep on macOS):
//
//	{
//	  "teamID":   "ABCDE12345",
//	  "keyID":    "XXXXXXXXXX",
//	  "issuerID": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
//	  "bundleID": "org.perkeep.mobile"
//	}
//
// The App Store Connect API key is read from
// ~/.appstoreconnect/private_keys/AuthKey_<keyID>.p8 unless "keyPath" is
// set. iossign creates Apple signing certificates (with private keys kept
// in the login keychain), registers the bundle ID and devices, and
// manages provisioning profiles as needed.
//
// It needs Xcode installed for devicectl and altool, but never opens it.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type config struct {
	TeamID   string `json:"teamID"`
	KeyID    string `json:"keyID"`
	IssuerID string `json:"issuerID"`
	KeyPath  string `json:"keyPath"`
	BundleID string `json:"bundleID"`
}

func loadConfig() (*config, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "perkeep", "iossign.json")
	j, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(j, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.TeamID == "" || c.KeyID == "" || c.IssuerID == "" || c.BundleID == "" {
		return nil, fmt.Errorf("%s: teamID, keyID, issuerID, and bundleID are required", path)
	}
	if c.KeyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		c.KeyPath = filepath.Join(home, ".appstoreconnect", "private_keys", "AuthKey_"+c.KeyID+".p8")
	}
	return &c, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n\tiossign install [-device name-or-udid] [-y] Perkeep.app\n\tiossign upload [-build N] [-ipa file] [-no-upload] [-y] Perkeep.app\n")
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "install":
		err = cmdInstall(os.Args[2:])
	case "upload":
		err = cmdUpload(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		log.Fatalf("iossign: %v", err)
	}
}

// job is one signing operation.
type job struct {
	cfg         *config
	src         string // unsigned .app from the builder
	development bool
	buildNumber string // if non-empty, replaces CFBundleVersion
	target      string // human description of where it's going
	yes         bool   // don't prompt

	asc   *ascClient
	ident signingIdentity
	work  string // temp dir
	app   string // signed copy of src under work
}

func newJob(cfg *config, src string, development bool) (*job, error) {
	src = filepath.Clean(src)
	if _, err := checkApp(src); err != nil {
		return nil, err
	}
	c, err := newASCClient(cfg.KeyID, cfg.IssuerID, cfg.KeyPath)
	if err != nil {
		return nil, err
	}
	return &job{cfg: cfg, src: src, development: development, asc: c}, nil
}

// prepare copies the app to a work directory and sets its bundle ID and
// build number.
func (j *job) prepare() error {
	var err error
	j.work, err = os.MkdirTemp("", "iossign")
	if err != nil {
		return err
	}
	j.app = filepath.Join(j.work, "Payload", filepath.Base(j.src))
	if err := copyApp(j.src, j.app); err != nil {
		return err
	}
	// Check the copy too, in case src changed while being copied.
	if _, err := checkApp(j.app); err != nil {
		return err
	}
	plist := filepath.Join(j.app, "Info.plist")
	if err := plistSet(plist, "CFBundleIdentifier", j.cfg.BundleID); err != nil {
		return err
	}
	if j.buildNumber != "" {
		if err := plistSet(plist, "CFBundleVersion", j.buildNumber); err != nil {
			return err
		}
	}
	return nil
}

func (j *job) cleanup() {
	if j.work != "" {
		os.RemoveAll(j.work)
	}
}

// confirm describes the signing operation and asks the user to approve
// it. It's called before iossign changes anything at Apple.
func (j *job) confirm() error {
	plist := filepath.Join(j.app, "Info.plist")
	exe, err := plistGet(plist, "CFBundleExecutable")
	if err != nil {
		return err
	}
	if exe == "" || strings.ContainsAny(exe, `/\`) {
		return fmt.Errorf("bad CFBundleExecutable %q", exe)
	}
	version, _ := plistGet(plist, "CFBundleShortVersionString")
	build, _ := plistGet(plist, "CFBundleVersion")
	files, err := checkApp(j.app)
	if err != nil {
		return err
	}
	sum, err := fileSHA256(filepath.Join(j.app, exe))
	if err != nil {
		return err
	}
	fmt.Printf("\nAbout to sign:\n")
	fmt.Printf("  source:     %s\n", j.src)
	fmt.Printf("  bundle ID:  %s\n", j.cfg.BundleID)
	fmt.Printf("  version:    %s (%s)\n", version, build)
	fmt.Printf("  executable: %s sha256:%s\n", exe, sum)
	fmt.Printf("  files:      %s\n", strings.Join(files, ", "))
	fmt.Printf("  for:        %s\n", j.target)
	if j.yes {
		return nil
	}
	fmt.Printf("Proceed? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if s := strings.TrimSpace(strings.ToLower(line)); s != "y" && s != "yes" {
		return errors.New("aborted")
	}
	return nil
}

// sign embeds profile and signs the prepared app.
func (j *job) sign(profile []byte) error {
	if err := os.WriteFile(filepath.Join(j.app, "embedded.mobileprovision"), profile, 0o644); err != nil {
		return err
	}
	ent := filepath.Join(j.work, "entitlements.plist")
	if err := os.WriteFile(ent, entitlementsPlist(j.cfg.TeamID, j.cfg.BundleID, j.development), 0o644); err != nil {
		return err
	}
	if err := run("codesign", "--force", "--sign", j.ident.sha1,
		"--entitlements", ent, "--generate-entitlement-der", "--timestamp=none", j.app); err != nil {
		return err
	}
	return run("codesign", "--verify", "--strict", "--verbose=1", j.app)
}

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	devFlag := fs.String("device", "", "device name or UDID; required if more than one is paired")
	yes := fs.Bool("y", false, "don't ask for confirmation")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	j, err := newJob(cfg, fs.Arg(0), true)
	if err != nil {
		return err
	}
	defer j.cleanup()
	j.yes = *yes

	dev, err := pickDevice(*devFlag)
	if err != nil {
		return err
	}
	j.target = fmt.Sprintf("install on %q (%s)", dev.Name, dev.UDID)

	if err := j.prepare(); err != nil {
		return err
	}
	if err := j.confirm(); err != nil {
		return err
	}

	ctx := context.Background()
	if err := ensureWWDR(); err != nil {
		return err
	}
	bundleID, err := j.asc.ensureBundleID(ctx, cfg.BundleID)
	if err != nil {
		return err
	}
	deviceID, err := j.asc.ensureDevice(ctx, dev.UDID, dev.Name)
	if err != nil {
		return err
	}
	if j.ident, err = ensureIdentity(ctx, j.asc, "DEVELOPMENT"); err != nil {
		return err
	}
	profile, err := j.asc.ensureProfile(ctx,
		"iossign dev "+cfg.BundleID+" "+dev.UDID, "IOS_APP_DEVELOPMENT",
		bundleID, j.ident.certID, []string{deviceID})
	if err != nil {
		return err
	}

	fmt.Printf("Signing as %s\n", j.ident.name)
	if err := j.sign(profile); err != nil {
		return err
	}
	if err := run("xcrun", "devicectl", "device", "install", "app", "--device", dev.UDID, j.app); err != nil {
		return err
	}
	return run("xcrun", "devicectl", "device", "process", "launch", "--device", dev.UDID, cfg.BundleID)
}

func cmdUpload(args []string) error {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	build := fs.String("build", "", "build number (CFBundleVersion); must increase with each upload. Default is the current UTC time as YYYYMMDDHHMM")
	ipa := fs.String("ipa", "", "also write the signed IPA to this file")
	noUpload := fs.Bool("no-upload", false, "sign and write the IPA (requires -ipa) but don't upload it")
	yes := fs.Bool("y", false, "don't ask for confirmation")
	fs.Parse(args)
	if fs.NArg() != 1 || (*noUpload && *ipa == "") {
		usage()
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	j, err := newJob(cfg, fs.Arg(0), false)
	if err != nil {
		return err
	}
	defer j.cleanup()
	j.yes = *yes
	j.buildNumber = *build
	if j.buildNumber == "" {
		j.buildNumber = time.Now().UTC().Format("200601021504")
	}
	j.target = "App Store Connect (TestFlight)"
	if *noUpload {
		j.target = "an App Store IPA at " + *ipa + " (not uploaded)"
	}

	if err := j.prepare(); err != nil {
		return err
	}
	if err := j.confirm(); err != nil {
		return err
	}

	ctx := context.Background()
	if err := ensureWWDR(); err != nil {
		return err
	}
	bundleID, err := j.asc.ensureBundleID(ctx, cfg.BundleID)
	if err != nil {
		return err
	}
	if j.ident, err = ensureIdentity(ctx, j.asc, "DISTRIBUTION"); err != nil {
		return err
	}
	profile, err := j.asc.ensureProfile(ctx,
		"iossign store "+cfg.BundleID, "IOS_APP_STORE",
		bundleID, j.ident.certID, nil)
	if err != nil {
		return err
	}

	fmt.Printf("Signing as %s\n", j.ident.name)
	if err := j.sign(profile); err != nil {
		return err
	}
	out := filepath.Join(j.work, "Perkeep.ipa")
	if err := run("ditto", "-c", "-k", "--sequesterRsrc", "--keepParent",
		filepath.Join(j.work, "Payload"), out); err != nil {
		return err
	}
	if *ipa != "" {
		if err := run("cp", out, *ipa); err != nil {
			return err
		}
		fmt.Printf("Wrote %s\n", *ipa)
	}
	if *noUpload {
		return nil
	}
	// altool finds the key by ID in API_PRIVATE_KEYS_DIR, and requires
	// the AuthKey_<keyID>.p8 file name.
	if filepath.Base(cfg.KeyPath) != "AuthKey_"+cfg.KeyID+".p8" {
		return fmt.Errorf("altool requires the API key file to be named AuthKey_%s.p8", cfg.KeyID)
	}
	os.Setenv("API_PRIVATE_KEYS_DIR", filepath.Dir(cfg.KeyPath))
	return run("xcrun", "altool", "--upload-app", "-f", out, "-t", "ios",
		"--apiKey", cfg.KeyID, "--apiIssuer", cfg.IssuerID)
}
