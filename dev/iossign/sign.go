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

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// entitlementsPlist returns the entitlements to sign the app with.
// They're derived only from iossign's own configuration, never from the
// artifact being signed.
func entitlementsPlist(teamID, bundleID string, development bool) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	str := func(k, v string) {
		b.WriteString("\t<key>" + k + "</key>\n\t<string>")
		xml.EscapeText(&b, []byte(v))
		b.WriteString("</string>\n")
	}
	boolean := func(k string, v bool) {
		fmt.Fprintf(&b, "\t<key>%s</key>\n\t<%v/>\n", k, v)
	}
	str("application-identifier", teamID+"."+bundleID)
	str("com.apple.developer.team-identifier", teamID)
	boolean("get-task-allow", development)
	if !development {
		boolean("beta-reports-active", true)
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// checkProfile verifies that the provisioning profile at path grants the
// entitlements iossign signs with, so a mismatch fails here with a clear
// error rather than as a vague launch failure on the device. tmpDir is
// for scratch files.
func checkProfile(path, tmpDir, teamID, bundleID string, development bool) error {
	decoded := filepath.Join(tmpDir, "profile.plist")
	if _, err := output("security", "cms", "-D", "-i", path, "-o", decoded); err != nil {
		return err
	}
	// The whole profile can't be converted to JSON (it has dates and
	// data), but these parts can.
	var teams []string
	var ents map[string]any
	for key, dst := range map[string]any{"TeamIdentifier": &teams, "Entitlements": &ents} {
		j, err := output("plutil", "-extract", key, "json", "-o", "-", decoded)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(j, dst); err != nil {
			return fmt.Errorf("profile %s: %w", key, err)
		}
	}
	return checkProfileEntitlements(teams, ents, teamID, bundleID, development)
}

func checkProfileEntitlements(teams []string, ents map[string]any, teamID, bundleID string, development bool) error {
	if !slices.Contains(teams, teamID) {
		return fmt.Errorf("provisioning profile is for team %v, but iossign.json has teamID %q", teams, teamID)
	}
	appID, _ := ents["application-identifier"].(string)
	want := teamID + "." + bundleID
	if appID != want && !(strings.HasSuffix(appID, ".*") && strings.HasPrefix(want, strings.TrimSuffix(appID, "*"))) {
		return fmt.Errorf("provisioning profile is for application-identifier %q, not %q", appID, want)
	}
	if got, _ := ents["get-task-allow"].(bool); got != development {
		return fmt.Errorf("provisioning profile has get-task-allow=%v; want %v", got, development)
	}
	return nil
}

// checkApp verifies that dir looks like an app bundle iossign knows how
// to sign, and returns its file list.
func checkApp(dir string) ([]string, error) {
	if !strings.HasSuffix(dir, ".app") {
		return nil, fmt.Errorf("%s: not a .app bundle", dir)
	}
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s: symlinks not allowed", rel)
		case d.IsDir() && (rel == "Frameworks" || rel == "PlugIns" || strings.HasSuffix(rel, ".appex") || strings.HasSuffix(rel, ".framework")):
			return fmt.Errorf("%s: nested code isn't supported yet", rel)
		case rel == "_CodeSignature" || rel == "embedded.mobileprovision":
			return fmt.Errorf("%s: artifact is already signed; want an unsigned build", rel)
		case d.Type().IsRegular():
			files = append(files, rel)
		case !d.IsDir():
			return fmt.Errorf("%s: unexpected file type %v", rel, d.Type())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// copyApp copies the app bundle src to the new directory dst.
func copyApp(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func output(name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, stderr.Bytes())
	}
	return out, nil
}

func plistGet(plist, key string) (string, error) {
	out, err := output("plutil", "-extract", key, "raw", "-o", "-", plist)
	return strings.TrimSpace(string(out)), err
}

func plistSet(plist, key, value string) error {
	return run("plutil", "-replace", key, "-string", value, plist)
}

var identityRx = regexp.MustCompile(`^\s*\d+\) ([0-9A-F]{40}) "(.*)"$`)

// codesigningIdentities returns the valid code signing identities in the
// keychain, keyed by uppercase hex SHA-1 of the certificate.
func codesigningIdentities() (map[string]string, error) {
	out, err := output("security", "find-identity", "-v", "-p", "codesigning")
	if err != nil {
		return nil, err
	}
	return parseIdentities(out), nil
}

func parseIdentities(out []byte) map[string]string {
	ids := map[string]string{}
	for line := range strings.Lines(string(out)) {
		if m := identityRx.FindStringSubmatch(strings.TrimRight(line, "\n")); m != nil {
			ids[m[1]] = m[2]
		}
	}
	return ids
}

// signingIdentity is a certificate that's both registered with Apple and
// usable for signing in the local keychain.
type signingIdentity struct {
	certID string // App Store Connect certificate ID
	sha1   string // uppercase hex; what codesign -s takes
	name   string
}

// ensureIdentity finds a certificate of type certType (DEVELOPMENT or
// DISTRIBUTION) with its private key in the local keychain, or creates
// one with a new key and imports it.
func ensureIdentity(ctx context.Context, c *ascClient, certType string) (signingIdentity, error) {
	local, err := codesigningIdentities()
	if err != nil {
		return signingIdentity{}, err
	}
	certs, err := c.certificates(ctx, certType)
	if err != nil {
		return signingIdentity{}, err
	}
	for _, cert := range certs {
		sum := sha1.Sum(cert.DER)
		h := strings.ToUpper(hex.EncodeToString(sum[:]))
		if name, ok := local[h]; ok {
			return signingIdentity{certID: cert.ID, sha1: h, name: name}, nil
		}
	}

	fmt.Printf("No %s certificate with a private key in this keychain; creating one.\n", certType)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return signingIdentity{}, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "Perkeep iossign"},
	}, key)
	if err != nil {
		return signingIdentity{}, err
	}
	cert, err := c.createCertificate(ctx, certType, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))
	if err != nil {
		return signingIdentity{}, err
	}
	if err := importIdentity(key, cert.DER); err != nil {
		return signingIdentity{}, fmt.Errorf("created certificate %s but failed to import it: %w", cert.ID, err)
	}
	sum := sha1.Sum(cert.DER)
	return signingIdentity{certID: cert.ID, sha1: strings.ToUpper(hex.EncodeToString(sum[:])), name: cert.Name}, nil
}

// importIdentity imports a private key and its certificate into the
// login keychain, allowing codesign to use the key.
func importIdentity(key *rsa.PrivateKey, certDER []byte) error {
	dir, err := os.MkdirTemp("", "iossign-id")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyFile := filepath.Join(dir, "key.pem")
	certFile := filepath.Join(dir, "cert.pem")
	p12File := filepath.Join(dir, "id.p12")
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		return err
	}
	pass := rand.Text()
	// macOS's own LibreSSL openssl writes PKCS#12 files that the
	// keychain can import; a Homebrew OpenSSL 3 might not.
	if err := run("/usr/bin/openssl", "pkcs12", "-export",
		"-inkey", keyFile, "-in", certFile, "-out", p12File,
		"-passout", "pass:"+pass); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return run("security", "import", p12File,
		"-k", filepath.Join(home, "Library/Keychains/login.keychain-db"),
		"-P", pass, "-T", "/usr/bin/codesign")
}

const wwdrG3URL = "https://www.apple.com/certificateauthority/AppleWWDRCAG3.cer"

// ensureWWDR makes sure Apple's intermediate certificate, which codesign
// needs to build a chain for Apple-issued signing certificates, is in
// the keychain. Xcode normally installs it.
func ensureWWDR() error {
	if err := exec.Command("security", "find-certificate", "-c", "Apple Worldwide Developer Relations Certification Authority").Run(); err == nil {
		return nil
	}
	fmt.Println("Installing Apple WWDR G3 intermediate certificate")
	dir, err := os.MkdirTemp("", "iossign-wwdr")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	f := filepath.Join(dir, "AppleWWDRCAG3.cer")
	if err := run("curl", "-fsSL", "-o", f, wwdrG3URL); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return run("security", "import", f, "-k", filepath.Join(home, "Library/Keychains/login.keychain-db"))
}

// device is an iOS device paired with this Mac.
type device struct {
	UDID string
	Name string
}

func pairedDevices() ([]device, error) {
	f, err := os.CreateTemp("", "iossign-devices-*.json")
	if err != nil {
		return nil, err
	}
	f.Close()
	defer os.Remove(f.Name())
	if _, err := output("xcrun", "devicectl", "list", "devices", "--quiet", "--json-output", f.Name()); err != nil {
		return nil, err
	}
	j, err := os.ReadFile(f.Name())
	if err != nil {
		return nil, err
	}
	return parseDevices(j)
}

func parseDevices(j []byte) ([]device, error) {
	var v struct {
		Result struct {
			Devices []struct {
				ConnectionProperties struct {
					PairingState string `json:"pairingState"`
				} `json:"connectionProperties"`
				DeviceProperties struct {
					Name string `json:"name"`
				} `json:"deviceProperties"`
				HardwareProperties struct {
					Platform string `json:"platform"`
					UDID     string `json:"udid"`
				} `json:"hardwareProperties"`
			} `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal(j, &v); err != nil {
		return nil, err
	}
	var devs []device
	for _, d := range v.Result.Devices {
		if d.HardwareProperties.Platform == "iOS" && d.ConnectionProperties.PairingState == "paired" {
			devs = append(devs, device{UDID: d.HardwareProperties.UDID, Name: d.DeviceProperties.Name})
		}
	}
	return devs, nil
}

func pickDevice(want string) (device, error) {
	devs, err := pairedDevices()
	if err != nil {
		return device{}, err
	}
	for _, d := range devs {
		if want != "" && (d.UDID == want || d.Name == want) {
			return d, nil
		}
	}
	if want == "" && len(devs) == 1 {
		return devs[0], nil
	}
	var names []string
	for _, d := range devs {
		names = append(names, fmt.Sprintf("%q (%s)", d.Name, d.UDID))
	}
	if want != "" {
		return device{}, fmt.Errorf("device %q not paired; paired devices: %s", want, strings.Join(names, ", "))
	}
	if len(devs) == 0 {
		return device{}, errors.New("no paired iOS devices; connect your iPhone by USB and tap Trust")
	}
	return device{}, fmt.Errorf("multiple paired devices; pick one with -device: %s", strings.Join(names, ", "))
}
