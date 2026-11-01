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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// entitlementsPlist returns the entitlements to sign a bundle (the app
// or an extension) with. They're derived only from iossign's own
// configuration, never from the artifact being signed. keychainGroup is
// the keychain access group the app and its extensions share, which is
// first, so where their keychain items go by default.
func entitlementsPlist(teamID, bundleID, keychainGroup string, development bool) []byte {
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
	b.WriteString("\t<key>keychain-access-groups</key>\n\t<array>\n\t\t<string>")
	xml.EscapeText(&b, []byte(keychainGroup))
	b.WriteString("</string>\n\t</array>\n")
	boolean("get-task-allow", development)
	if !development {
		boolean("beta-reports-active", true)
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// checkProfile verifies that the provisioning profile at path grants the
// entitlements iossign signs with, so a mismatch fails here with a clear
// error rather than as a vague launch failure on the device. If udid is
// non-empty, the profile must also list that device. tmpDir is for
// scratch files.
func checkProfile(path, tmpDir, teamID, bundleID, keychainGroup string, development bool, udid string) error {
	decoded := filepath.Join(tmpDir, "profile.plist")
	if _, err := output("security", "cms", "-D", "-i", path, "-o", decoded); err != nil {
		return err
	}
	// The whole profile can't be converted to JSON (it has dates and
	// data), but these parts can.
	var p profileInfo
	parts := map[string]any{"TeamIdentifier": &p.teams, "Entitlements": &p.ents}
	if udid != "" {
		parts["ProvisionedDevices"] = &p.devices
	}
	for key, dst := range parts {
		j, err := output("plutil", "-extract", key, "json", "-o", "-", decoded)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(j, dst); err != nil {
			return fmt.Errorf("profile %s: %w", key, err)
		}
	}
	return p.check(teamID, bundleID, keychainGroup, development, udid)
}

// profileInfo is the part of a provisioning profile that iossign checks.
type profileInfo struct {
	teams   []string
	ents    map[string]any
	devices []string
}

func (p profileInfo) check(teamID, bundleID, keychainGroup string, development bool, udid string) error {
	if udid != "" && !slices.Contains(p.devices, udid) {
		return fmt.Errorf("provisioning profile doesn't include device %s; it has %q", udid, p.devices)
	}
	return checkProfileEntitlements(p.teams, p.ents, teamID, bundleID, keychainGroup, development)
}

func checkProfileEntitlements(teams []string, ents map[string]any, teamID, bundleID, keychainGroup string, development bool) error {
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
	groups, _ := ents["keychain-access-groups"].([]any)
	if !slices.ContainsFunc(groups, func(g any) bool { return g == teamID+".*" || g == keychainGroup }) {
		return fmt.Errorf("provisioning profile's keychain-access-groups %v don't allow %q", groups, keychainGroup)
	}
	return nil
}

// extensionID returns the bundle ID to sign an app extension built with
// bundle ID builtExtID as, in an app built as builtAppID and signed as
// appID: appID with the extension's suffix, as Apple requires.
func extensionID(builtAppID, builtExtID, appID string) (string, error) {
	suffix, ok := strings.CutPrefix(builtExtID, builtAppID+".")
	if !ok || suffix == "" {
		return "", fmt.Errorf("extension's bundle ID %q isn't under the app's, %q", builtExtID, builtAppID)
	}
	return appID + "." + suffix, nil
}

// checkApp verifies that dir looks like an app bundle iossign knows how
// to sign, and returns its file list. The only nested code it may have
// is frameworks in Frameworks and app extensions in PlugIns, with none
// of their own.
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
		parts := strings.Split(rel, string(filepath.Separator))
		base := parts[len(parts)-1]
		inExt := len(parts) >= 2 && parts[0] == "PlugIns" && strings.HasSuffix(parts[1], ".appex")
		inFW := len(parts) >= 2 && parts[0] == "Frameworks" && strings.HasSuffix(parts[1], ".framework")
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s: symlinks not allowed", rel)
		case d.IsDir() && (rel == "PlugIns" || rel == "Frameworks" || len(parts) == 2 && (inExt || inFW)):
		case d.IsDir() && (base == "Frameworks" || base == "PlugIns" || strings.HasSuffix(base, ".appex") || strings.HasSuffix(base, ".framework")):
			return fmt.Errorf("%s: nested code other than app extensions isn't supported yet", rel)
		case base == "_CodeSignature" || base == "embedded.mobileprovision":
			return fmt.Errorf("%s: artifact is already signed; want an unsigned build", rel)
		case parts[0] == "PlugIns" && !inExt || parts[0] == "Frameworks" && !inFW:
			return fmt.Errorf("%s: unexpected file in %s", rel, parts[0])
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
	der    []byte // the certificate
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
			return signingIdentity{certID: cert.ID, sha1: h, name: name, der: cert.DER}, nil
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
	return signingIdentity{certID: cert.ID, sha1: strings.ToUpper(hex.EncodeToString(sum[:])), name: cert.Name, der: cert.DER}, nil
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

// ensureIssuer makes sure the keychain has a valid certificate for the
// intermediate CA (Apple WWDR) that issued leafDER. Without it codesign
// can't embed the chain in the signature, and iOS refuses to launch the
// app even though the signature verifies on the Mac. Xcode normally
// installs it, but there are several WWDR generations and old
// keychains often have only expired ones.
func ensureIssuer(leafDER []byte) error {
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	login := filepath.Join(home, "Library/Keychains/login.keychain-db")
	out, err := output("security", "find-certificate", "-a", "-p", login, "/Library/Keychains/System.keychain")
	if err != nil {
		return err
	}
	if findIssuer(leaf, out, time.Now()) {
		return nil
	}
	if len(leaf.IssuingCertificateURL) == 0 {
		return fmt.Errorf("no valid certificate for %q in the keychain, and the signing certificate doesn't say where to get one", leaf.Issuer)
	}
	u := leaf.IssuingCertificateURL[0]
	fmt.Printf("Installing intermediate certificate %q from %s\n", leaf.Issuer.CommonName, u)
	res, err := http.Get(u)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	der, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("fetching %s: %s", u, res.Status)
	}
	issuer, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", u, err)
	}
	// It may have come over plain HTTP, so make sure it's really the issuer.
	if err := leaf.CheckSignatureFrom(issuer); err != nil {
		return fmt.Errorf("certificate from %s didn't issue the signing certificate: %w", u, err)
	}
	dir, err := os.MkdirTemp("", "iossign-ca")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	f := filepath.Join(dir, "issuer.cer")
	if err := os.WriteFile(f, der, 0o644); err != nil {
		return err
	}
	return run("security", "import", f, "-k", login)
}

// findIssuer reports whether pemCerts contains a certificate that's
// valid at now and issued leaf.
func findIssuer(leaf *x509.Certificate, pemCerts []byte, now time.Time) bool {
	for rest := pemCerts; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return false
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil || now.Before(c.NotBefore) || now.After(c.NotAfter) {
			continue
		}
		if leaf.CheckSignatureFrom(c) == nil {
			return true
		}
	}
}

// signatureInfo returns codesign's description of app's signature.
func signatureInfo(app string) (string, error) {
	// codesign -d writes to stderr.
	out, err := exec.Command("codesign", "-d", "-vv", app).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("codesign -d: %v: %s", err, out)
	}
	return string(out), nil
}

// checkChain verifies that codesign's description of a signature (from
// signatureInfo) shows a certificate chain up to Apple's root, as iOS
// requires.
func checkChain(info string) error {
	var chain []string
	for line := range strings.Lines(info) {
		if a, ok := strings.CutPrefix(strings.TrimSpace(line), "Authority="); ok {
			chain = append(chain, a)
		}
	}
	if len(chain) < 3 || chain[len(chain)-1] != "Apple Root CA" {
		return fmt.Errorf("signature's certificate chain is %q; want signing cert, Apple WWDR intermediate, Apple Root CA", chain)
	}
	return nil
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
