/*
Copyright 2014 The Camlistore Authors

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

// Package gce provides tools to deploy Camlistore on Google Compute Engine.
package gce

// TODO: we want to host our own docker images under gs://camlistore-release/docker, so we should make a
// list. For the purposes of this package, we should add mysql to the list.

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/constants/google"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"
	"golang.org/x/net/context"

	"go4.org/cloud/google/gceutil"
	"go4.org/syncutil"
	"golang.org/x/oauth2"
	// TODO(mpl): switch to google.golang.org/cloud/compute
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"
	"google.golang.org/cloud"
	"google.golang.org/cloud/logging"
	cloudstorage "google.golang.org/cloud/storage"
)

const (
	DefaultInstanceName = "camlistore-server"
	DefaultMachineType  = "g1-small"
	DefaultRegion       = "us-central1"

	projectsAPIURL = "https://www.googleapis.com/compute/v1/projects/"

	fallbackZone = "us-central1-a"

	camliUsername = "camlistore" // directly set in compute metadata, so not user settable.

	configDir = "config"

	ConsoleURL          = "https://console.developers.google.com"
	HelpCreateProject   = "Go to " + ConsoleURL + " to create a new Google Cloud project"
	HelpEnableAPIs      = `Enable the project APIs: in your project console, navigate to "APIs and auth", "APIs". In the list, enable "Google Cloud Storage", "Google Cloud Storage JSON API", and "Google Compute Engine".`
	helpDeleteInstance  = `To delete an existing Compute Engine instance: in your project console, navigate to "Compute", "Compute Engine", and "VM instances". Select your instance and click "Delete".`
	HelpManageSSHKeys   = `To manage/add SSH keys: in your project console, navigate to "Compute", "Compute Engine", and "VM instances". Click on your instance name. Scroll down to the SSH Keys section.`
	HelpManageHTTPCreds = `To change your login and password: in your project console, navigate to "Compute", "Compute Engine", and "VM instances". Click on your instance name. Set camlistore-username and/or camlistore-password in the custom metadata section.`
)

var (
	// Verbose enables more info to be printed.
	Verbose bool
)

// certFilename returns the HTTPS certificate file name
func certFilename() string {
	return filepath.Base(osutil.DefaultTLSCert())
}

// keyFilename returns the HTTPS key name
func keyFilename() string {
	return filepath.Base(osutil.DefaultTLSKey())
}

// NewOAuthConfig returns an OAuth configuration template.
func NewOAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		Scopes: []string{
			logging.Scope,
			compute.DevstorageFullControlScope,
			compute.ComputeScope,
			"https://www.googleapis.com/auth/sqlservice",
			"https://www.googleapis.com/auth/sqlservice.admin",
		},
		Endpoint:     google.Endpoint,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}

// InstanceConf is the configuration for the Google Compute Engine instance that will be deployed.
type InstanceConf struct {
	Name     string // Name given to the virtual machine instance.
	Project  string // Google project ID where the instance is created.
	Machine  string // Machine type.
	Zone     string // GCE zone; see https://cloud.google.com/compute/docs/zones
	SSHPub   string // SSH public key.
	CertFile string // HTTPS certificate file.
	KeyFile  string // HTTPS key file.
	Hostname string // Fully qualified domain name.
	Password string // HTTP basic auth password for user "camlistore"; empty means random

	configDir string // bucketBase() + "/config"
	blobDir   string // bucketBase() + "/blobs"

	Ctime time.Time // Timestamp for this configuration.

	WIP bool // Whether to use the camlistored-WORKINPROGRESS.tar.gz tarball instead of the "production" one
}

func (conf *InstanceConf) bucketBase() string {
	return conf.Project + "-camlistore"
}

// Deployer creates and starts an instance such as defined in Conf.
type Deployer struct {
	// Client is an OAuth2 client, authenticated for working with
	// the user's Google Cloud resources.
	Client *http.Client

	Conf *InstanceConf

	// SHA-1 and SHA-256 fingerprints of the HTTPS certificate created during setupHTTPS, if any.
	// Keyed by hash name: "SHA-1", and "SHA-256".
	certFingerprints map[string]string

	*log.Logger // Cannot be nil.
}

// Get returns the Instance corresponding to the Project, Zone, and Name defined in the
// Deployer's Conf.
func (d *Deployer) Get() (*compute.Instance, error) {
	computeService, err := compute.New(d.Client)
	if err != nil {
		return nil, err
	}
	return computeService.Instances.Get(d.Conf.Project, d.Conf.Zone, d.Conf.Name).Do()
}

type instanceExistsError struct {
	project string
	zone    string
	name    string
}

func (e instanceExistsError) Error() string {
	if e.project == "" {
		panic("instanceExistsErr has no project")
	}
	msg := "some instance(s) already exist as (" + e.project
	if e.zone != "" {
		msg += ", " + e.zone
	}
	if e.name != "" {
		msg += ", " + e.name
	}
	msg += "), you need to delete them first."
	return msg
}

// projectHasInstance checks for all the possible zones if there's already an instance for the project.
// It returns the name of the zone at the first instance it finds, if any.
func (d *Deployer) projectHasInstance() (zone string, err error) {
	s, err := compute.New(d.Client)
	if err != nil {
		return "", err
	}
	// TODO(mpl): make use of the handler's cached zones.
	zl, err := compute.NewZonesService(s).List(d.Conf.Project).Do()
	if err != nil {
		return "", fmt.Errorf("could not get a list of zones: %v", err)
	}
	computeService, _ := compute.New(d.Client)
	var zoneOnce sync.Once
	var grp syncutil.Group
	errc := make(chan error, 1)
	zonec := make(chan string, 1)
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for _, z := range zl.Items {
		z := z
		grp.Go(func() error {
			list, err := computeService.Instances.List(d.Conf.Project, z.Name).Do()
			if err != nil {
				return fmt.Errorf("could not list existing instances: %v", err)
			}
			if len(list.Items) > 0 {
				zoneOnce.Do(func() {
					zonec <- z.Name
				})
			}
			return nil
		})
	}
	go func() {
		errc <- grp.Err()
	}()
	// We block until either an instance was found in a zone, or all the instance
	// listing is done. Or we timed-out.
	select {
	case err = <-errc:
		return "", err
	case zone = <-zonec:
		// We voluntarily ignore any listing error if we found at least one instance
		// because that's what we primarily want to report about.
		return zone, nil
	case <-timeout.C:
		return "", errors.New("timed out")
	}
}

type projectIDError struct {
	id    string
	cause error
}

func (e projectIDError) Error() string {
	if e.id == "" {
		panic("projectIDError without an id")
	}
	if e.cause != nil {
		return fmt.Sprintf("project ID error for %v: %v", e.id, e.cause)
	}
	return fmt.Sprintf("project ID error for %v", e.id)
}

func (d *Deployer) checkProjectID() error {
	// TODO(mpl): cache the computeService in Deployer, instead of recreating a new one everytime?
	s, err := compute.New(d.Client)
	if err != nil {
		return projectIDError{
			id:    d.Conf.Project,
			cause: err,
		}
	}
	project, err := compute.NewProjectsService(s).Get(d.Conf.Project).Do()
	if err != nil {
		return projectIDError{
			id:    d.Conf.Project,
			cause: err,
		}
	}
	if project.Name != d.Conf.Project {
		return projectIDError{
			id:    d.Conf.Project,
			cause: fmt.Errorf("project ID do not match: got %q, wanted %q", project.Name, d.Conf.Project),
		}
	}
	return nil
}

// Create sets up and starts a Google Compute Engine instance as defined in d.Conf. It
// creates the necessary Google Storage buckets beforehand.
func (d *Deployer) Create(ctx context.Context) (*compute.Instance, error) {
	if err := d.checkProjectID(); err != nil {
		return nil, err
	}

	computeService, _ := compute.New(d.Client)
	storageService, _ := storage.New(d.Client)

	fwc := make(chan error, 1)
	go func() {
		fwc <- d.setFirewall(ctx, computeService)
	}()

	config := cloudConfig(d.Conf)
	const maxCloudConfig = 32 << 10 // per compute API docs
	if len(config) > maxCloudConfig {
		return nil, fmt.Errorf("cloud config length of %d bytes is over %d byte limit", len(config), maxCloudConfig)
	}

	// TODO(mpl): maybe add a wipe mode where we erase other instances before attempting to create.
	if zone, err := d.projectHasInstance(); zone != "" {
		return nil, instanceExistsError{
			project: d.Conf.Project,
			zone:    zone,
		}
	} else if err != nil {
		return nil, fmt.Errorf("could not scan project for existing instances: %v", err)
	}

	if err := d.setBuckets(storageService, ctx); err != nil {
		return nil, fmt.Errorf("could not create buckets: %v", err)
	}

	if err := d.setupHTTPS(storageService); err != nil {
		return nil, fmt.Errorf("could not setup HTTPS: %v", err)
	}

	if err := d.createInstance(computeService, ctx); err != nil {
		return nil, fmt.Errorf("could not create compute instance: %v", err)
	}

	inst, err := computeService.Instances.Get(d.Conf.Project, d.Conf.Zone, d.Conf.Name).Do()
	if err != nil {
		return nil, fmt.Errorf("error getting instance after creation: %v", err)
	}
	if Verbose {
		ij, _ := json.MarshalIndent(inst, "", "    ")
		d.Printf("Instance: %s", ij)
	}

	if err = <-fwc; err != nil {
		return nil, fmt.Errorf("could not create firewall rules: %v", err)
	}
	return inst, nil
}

// TODO(bradfitz,mpl): stop generating a password on camlistore.org
// and have the instance create its own password on first boot
// instead. We shouldn't even ask users for the initial optional
// password, though. But let's not block the 0.9 release on this
// because regardless the users are already trusting us with their GCE
// OAuth scope to create their instance (so if we were evil we could
// do whatever already). But it raises unnecessary questions. Once we do
// ACME (LetsEncrypt.org) on their instance, we also don't need to
// generate their TLS certs for them.
func randPassword() string {
	buf := make([]byte, 5)
	if n, err := rand.Read(buf); err != nil || n != len(buf) {
		log.Fatalf("crypto/rand.Read = %v, %v", n, err)
	}
	return fmt.Sprintf("%x", buf)
}

// LooksLikeRegion reports whether s looks like a GCE region.
func LooksLikeRegion(s string) bool {
	return strings.Count(s, "-") == 1
}

// createInstance starts the creation of the Compute Engine instance and waits for the
// result of the creation operation. It should be called after setBuckets and setupHTTPS.
func (d *Deployer) createInstance(computeService *compute.Service, ctx context.Context) error {
	coreosImgURL, err := gceutil.CoreOSImageURL(d.Client)
	if err != nil {
		return fmt.Errorf("error looking up latest CoreOS stable image: %v", err)
	}
	prefix := projectsAPIURL + d.Conf.Project
	machType := prefix + "/zones/" + d.Conf.Zone + "/machineTypes/" + d.Conf.Machine
	config := cloudConfig(d.Conf)
	password := d.Conf.Password
	if password == "" {
		password = randPassword()
	}
	instance := &compute.Instance{
		Name:        d.Conf.Name,
		Description: "Camlistore server",
		MachineType: machType,
		Disks: []*compute.AttachedDisk{
			{
				AutoDelete: true,
				Boot:       true,
				Type:       "PERSISTENT",
				InitializeParams: &compute.AttachedDiskInitializeParams{
					DiskName:    d.Conf.Name + "-coreos-stateless-pd",
					SourceImage: coreosImgURL,
				},
			},
		},
		Tags: &compute.Tags{
			Items: []string{"http-server", "https-server"},
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "camlistore-username",
					Value: googleapi.String(camliUsername),
				},
				{
					Key:   "camlistore-password",
					Value: googleapi.String(password),
				},
				{
					Key:   "camlistore-blob-dir",
					Value: googleapi.String("gs://" + d.Conf.blobDir),
				},
				{
					Key:   "camlistore-config-dir",
					Value: googleapi.String("gs://" + d.Conf.configDir),
				},
				{
					Key:   "user-data",
					Value: googleapi.String(config),
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			&compute.NetworkInterface{
				AccessConfigs: []*compute.AccessConfig{
					&compute.AccessConfig{
						Type: "ONE_TO_ONE_NAT",
						Name: "External NAT",
					},
				},
				Network: prefix + "/global/networks/default",
			},
		},
		ServiceAccounts: []*compute.ServiceAccount{
			{
				Email: "default",
				Scopes: []string{
					logging.Scope,
					compute.DevstorageFullControlScope,
					compute.ComputeScope,
					"https://www.googleapis.com/auth/sqlservice",
					"https://www.googleapis.com/auth/sqlservice.admin",
				},
			},
		},
	}
	if d.Conf.Hostname != "" && d.Conf.Hostname != "localhost" {
		instance.Metadata.Items = append(instance.Metadata.Items, &compute.MetadataItems{
			Key:   "camlistore-hostname",
			Value: googleapi.String(d.Conf.Hostname),
		})
	}
	const localMySQL = false // later
	if localMySQL {
		instance.Disks = append(instance.Disks, &compute.AttachedDisk{
			AutoDelete: false,
			Boot:       false,
			Type:       "PERSISTENT",
			InitializeParams: &compute.AttachedDiskInitializeParams{
				DiskName:   "camlistore-mysql-index-pd",
				DiskSizeGb: 4,
			},
		})
	}

	if Verbose {
		d.Print("Creating instance...")
	}
	op, err := computeService.Instances.Insert(d.Conf.Project, d.Conf.Zone, instance).Do()
	if err != nil {
		return fmt.Errorf("failed to create instance: %v", err)
	}
	opName := op.Name
	if Verbose {
		d.Printf("Created. Waiting on operation %v", opName)
	}
OpLoop:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		time.Sleep(2 * time.Second)
		op, err := computeService.ZoneOperations.Get(d.Conf.Project, d.Conf.Zone, opName).Do()
		if err != nil {
			return fmt.Errorf("failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			if Verbose {
				d.Printf("Waiting on operation %v", opName)
			}
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					d.Printf("Error: %+v", operr)
				}
				return fmt.Errorf("failed to start.")
			}
			if Verbose {
				d.Printf("Success. %+v", op)
			}
			break OpLoop
		default:
			return fmt.Errorf("unknown status %q: %+v", op.Status, op)
		}
	}
	return nil
}

func cloudConfig(conf *InstanceConf) string {
	config := strings.Replace(baseInstanceConfig, "INNODB_BUFFER_POOL_SIZE=NNN", "INNODB_BUFFER_POOL_SIZE="+strconv.Itoa(innodbBufferPoolSize(conf.Machine)), -1)
	camlistoredTarball := "https://storage.googleapis.com/camlistore-release/docker/"
	if conf.WIP {
		camlistoredTarball += "camlistored-WORKINPROGRESS.tar.gz"
	} else {
		camlistoredTarball += "camlistored.tar.gz"
	}
	config = strings.Replace(config, "CAMLISTORED_TARBALL", camlistoredTarball, 1)
	if conf.SSHPub != "" {
		config += fmt.Sprintf("\nssh_authorized_keys:\n    - %s\n", conf.SSHPub)
	}
	return config
}

// getInstalledTLS returns the TLS certificate and key stored on Google Cloud Storage for the
// instance defined in d.Conf.
//
// If either the TLS keypair doesn't exist, the error is os.ErrNotExist.
func (d *Deployer) getInstalledTLS() (certPEM, keyPEM []byte, err error) {
	ctx := context.Background()
	stoClient, err := cloudstorage.NewClient(ctx, cloud.WithBaseHTTP(d.Client))
	if err != nil {
		return nil, nil, fmt.Errorf("error creating Cloud Storage client to fetch TLS cert & key from new instance: %v", err)
	}
	getFile := func(name string) ([]byte, error) {
		sr, err := stoClient.Bucket(d.Conf.bucketBase()).Object(path.Join(configDir, name)).NewReader(ctx)
		if err == cloudstorage.ErrObjectNotExist {
			return nil, os.ErrNotExist
		}
		if err != nil {
			return nil, err
		}
		defer sr.Close()
		return ioutil.ReadAll(sr)
	}
	var grp syncutil.Group
	grp.Go(func() (err error) {
		certPEM, err = getFile(certFilename())
		return
	})
	grp.Go(func() (err error) {
		keyPEM, err = getFile(keyFilename())
		return
	})
	err = grp.Err()
	return
}

// setBuckets defines the buckets needed by the instance and creates them.
func (d *Deployer) setBuckets(storageService *storage.Service, ctx context.Context) error {
	projBucket := d.Conf.Project + "-camlistore"

	needBucket := map[string]bool{
		projBucket: true,
	}

	buckets, err := storageService.Buckets.List(d.Conf.Project).Do()
	if err != nil {
		return fmt.Errorf("error listing buckets: %v", err)
	}
	for _, it := range buckets.Items {
		delete(needBucket, it.Name)
	}
	if len(needBucket) > 0 {
		if Verbose {
			d.Printf("Need to create buckets: %v", needBucket)
		}
		var waitBucket sync.WaitGroup
		var bucketErr error
		for name := range needBucket {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			name := name
			waitBucket.Add(1)
			go func() {
				defer waitBucket.Done()
				if Verbose {
					d.Printf("Creating bucket %s", name)
				}
				b, err := storageService.Buckets.Insert(d.Conf.Project, &storage.Bucket{
					Id:   name,
					Name: name,
				}).Do()
				if err != nil && bucketErr == nil {
					bucketErr = fmt.Errorf("error creating bucket %s: %v", name, err)
					return
				}
				if Verbose {
					d.Printf("Created bucket %s: %+v", name, b)
				}
			}()
		}
		waitBucket.Wait()
		if bucketErr != nil {
			return bucketErr
		}
	}

	d.Conf.configDir = path.Join(projBucket, configDir)
	d.Conf.blobDir = path.Join(projBucket, "blobs")
	return nil
}

// setFirewall adds the firewall rules needed for ports 80 & 433 to the default network.
func (d *Deployer) setFirewall(ctx context.Context, computeService *compute.Service) error {
	defaultNet, err := computeService.Networks.Get(d.Conf.Project, "default").Do()
	if err != nil {
		return fmt.Errorf("error getting default network: %v", err)
	}

	needRules := map[string]compute.Firewall{
		"default-allow-http": compute.Firewall{
			Name:         "default-allow-http",
			SourceRanges: []string{"0.0.0.0/0"},
			SourceTags:   []string{"http-server"},
			Allowed:      []*compute.FirewallAllowed{{IPProtocol: "tcp", Ports: []string{"80"}}},
			Network:      defaultNet.SelfLink,
		},
		"default-allow-https": compute.Firewall{
			Name:         "default-allow-https",
			SourceRanges: []string{"0.0.0.0/0"},
			SourceTags:   []string{"https-server"},
			Allowed:      []*compute.FirewallAllowed{{IPProtocol: "tcp", Ports: []string{"443"}}},
			Network:      defaultNet.SelfLink,
		},
	}

	rules, err := computeService.Firewalls.List(d.Conf.Project).Do()
	if err != nil {
		return fmt.Errorf("error listing rules: %v", err)
	}
	for _, it := range rules.Items {
		delete(needRules, it.Name)
	}
	if len(needRules) == 0 {
		return nil
	}

	if Verbose {
		d.Printf("Need to create rules: %v", needRules)
	}
	var wg syncutil.Group
	for name, rule := range needRules {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name, rule := name, rule
		wg.Go(func() error {
			if Verbose {
				d.Printf("Creating rule %s", name)
			}
			r, err := computeService.Firewalls.Insert(d.Conf.Project, &rule).Do()
			if err != nil {
				return fmt.Errorf("error creating rule %s: %v", name, err)
			}
			if Verbose {
				d.Printf("Created rule %s: %+v", name, r)
			}
			return nil
		})
	}
	return wg.Err()
}

// setupHTTPS uploads to the configuration bucket the certificate and key used by the
// instance for HTTPS. It generates them if d.Conf.CertFile or d.Conf.KeyFile is not defined.
// It should be called after setBuckets.
func (d *Deployer) setupHTTPS(storageService *storage.Service) error {
	installedCert, _, err := d.getInstalledTLS()
	if err != nil && err != os.ErrNotExist {
		return err
	}
	if err == nil {
		sigs, err := httputil.CertFingerprints(installedCert)
		if err != nil {
			return fmt.Errorf("could not get fingerprints of certificate: %v", err)
		}
		d.certFingerprints = sigs
		if Verbose {
			d.Printf("Reusing existing certificate with fingerprint %v", sigs["SHA-256"])
		}
		return nil
	}
	var cert, key io.ReadCloser
	if d.Conf.CertFile != "" && d.Conf.KeyFile != "" {
		// Note: it is not a bug that we do not set d.certFingerprint in that case, because only
		// the wizard template cares about d.certFingerprint, and we never get here with the wizard
		// - but only with camdeploy.
		cert, err = os.Open(d.Conf.CertFile)
		if err != nil {
			return err
		}
		defer cert.Close()
		key, err = os.Open(d.Conf.KeyFile)
		if err != nil {
			return err
		}
		defer key.Close()
	} else {

		if Verbose {
			d.Printf("Generating self-signed certificate for %v ...", d.Conf.Hostname)
		}
		certBytes, keyBytes, err := httputil.GenSelfTLS(d.Conf.Hostname)
		if err != nil {
			return fmt.Errorf("error generating certificates: %v", err)
		}
		sigs, err := httputil.CertFingerprints(certBytes)
		if err != nil {
			return fmt.Errorf("could not get fingerprints of certificate: %v", err)
		}
		d.certFingerprints = sigs
		if Verbose {
			d.Printf("Wrote certificate with SHA-256 fingerprint %s", sigs["SHA-256"])
		}
		cert = ioutil.NopCloser(bytes.NewReader(certBytes))
		key = ioutil.NopCloser(bytes.NewReader(keyBytes))
	}

	if Verbose {
		d.Print("Uploading certificate and key...")
	}
	_, err = storageService.Objects.Insert(d.Conf.bucketBase(),
		&storage.Object{Name: path.Join(configDir, certFilename())}).Media(cert).Do()
	if err != nil {
		return fmt.Errorf("cert upload failed: %v", err)
	}
	_, err = storageService.Objects.Insert(d.Conf.bucketBase(),
		&storage.Object{Name: path.Join(configDir, keyFilename())}).Media(key).Do()
	if err != nil {
		return fmt.Errorf("key upload failed: %v", err)
	}
	return nil
}

// returns the MySQL InnoDB buffer pool size (in bytes) as a function
// of the GCE machine type.
func innodbBufferPoolSize(machine string) int {
	// Totally arbitrary. We don't need much here because
	// camlistored slurps this all into its RAM on start-up
	// anyway. So this is all prety overkill and more than the
	// 8MB default.
	switch machine {
	case "f1-micro":
		return 32 << 20
	case "g1-small":
		return 64 << 20
	default:
		return 128 << 20
	}
}

const baseInstanceConfig = `#cloud-config
write_files:
  - path: /var/lib/camlistore/tmp/README
    permissions: 0644
    content: |
      This is the Camlistore /tmp directory.
  - path: /var/lib/camlistore/mysql/README
    permissions: 0644
    content: |
      This is the Camlistore MySQL data directory.
coreos:
  units:
    - name: cam-journal-gatewayd.service
      content: |
        [Unit]
        Description=Journal Gateway Service
        Requires=cam-journal-gatewayd.socket

        [Service]
        ExecStart=/usr/lib/systemd/systemd-journal-gatewayd
        User=systemd-journal-gateway
        Group=systemd-journal-gateway
        SupplementaryGroups=systemd-journal
        PrivateTmp=yes
        PrivateDevices=yes
        PrivateNetwork=yes
        ProtectSystem=full
        ProtectHome=yes

        [Install]
        Also=cam-journal-gatewayd.socket
    - name: cam-journal-gatewayd.socket
      command: start
      content: |
        [Unit]
        Description=Journal Gateway Service Socket

        [Socket]
        ListenStream=/run/camjournald.sock

        [Install]
        WantedBy=sockets.target
    - name: mysql.service
      command: start
      content: |
        [Unit]
        Description=MySQL
        After=docker.service
        Requires=docker.service

        [Service]
        ExecStartPre=/bin/bash -c '/usr/bin/curl https://storage.googleapis.com/camlistore-release/docker/systemd-docker.tar.gz | /bin/gunzip -c | /usr/bin/docker load'
        ExecStartPre=/usr/bin/docker run --rm -v /opt/bin:/opt/bin camlistore/systemd-docker
        ExecStart=/opt/bin/systemd-docker run --rm --name %n -v /var/lib/camlistore/mysql:/mysql -e INNODB_BUFFER_POOL_SIZE=NNN camlistore/mysql
        RestartSec=1s
        Restart=always
        Type=notify
        NotifyAccess=all

        [Install]
        WantedBy=multi-user.target
    - name: camlistored.service
      command: start
      content: |
        [Unit]
        Description=Camlistore
        After=docker.service mysql.service
        Requires=docker.service mysql.service

        [Service]
        ExecStartPre=/usr/bin/docker run --rm -v /opt/bin:/opt/bin camlistore/systemd-docker
        ExecStartPre=/bin/bash -c '/usr/bin/curl CAMLISTORED_TARBALL | /bin/gunzip -c | /usr/bin/docker load'
        ExecStart=/opt/bin/systemd-docker run --rm -p 80:80 -p 443:443 --name %n -v /run/camjournald.sock:/run/camjournald.sock -v /var/lib/camlistore/tmp:/tmp --link=mysql.service:mysqldb camlistore/server
        RestartSec=1s
        Restart=always
        Type=notify
        NotifyAccess=all

        [Install]
        WantedBy=multi-user.target
`
