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

// TODO: we want to host our own docker images under camlistore.org, so we should make a
// list. For the purposes of this package, we should add to the list camlistore/camlistored
// and camlistore/mysql.

import (
	"bytes"
	"encoding/json"
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
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"

	compute "camlistore.org/third_party/code.google.com/p/google-api-go-client/compute/v1"
	storage "camlistore.org/third_party/code.google.com/p/google-api-go-client/storage/v1"
	"camlistore.org/third_party/golang.org/x/oauth2"
)

const (
	projectsAPIURL = "https://www.googleapis.com/compute/v1/projects/"
	coreosImgURL   = "https://www.googleapis.com/compute/v1/projects/coreos-cloud/global/images/coreos-stable-444-5-0-v20141016"

	// default instance configuration values.
	InstanceName = "camlistore-server"
	Machine      = "g1-small"
	Zone         = "us-central1-a"

	configDir = "config"

	ConsoleURL         = "https://console.developers.google.com"
	HelpCreateProject  = "Go to " + ConsoleURL + " to create a new Google Cloud project."
	HelpEnableAPIs     = `Enable the project APIs: in your project console, navigate to "APIs and auth", "APIs". In the list, enable "Google Cloud Storage", "Google Cloud Storage JSON API", and "Google Compute Engine".`
	HelpDeleteInstance = `Delete an existing Compute Engine instance: in your project console, navigate to "Compute", "Compute Engine", and "VM instances". Select your instance and click "Delete".`
)

// Verbose enables more info to be printed.
var Verbose bool

// NewOAuthConfig returns an OAuth configuration template.
func NewOAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		Scopes: []string{
			compute.DevstorageFull_controlScope,
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
	Zone     string // Geographic zone.
	SSHPub   string // SSH public key.
	CertFile string // HTTPS certificate file.
	KeyFile  string // HTTPS key file.
	Hostname string // Fully qualified domain name.

	bucketBase string // Project + "-camlistore"
	configDir  string // bucketBase + "/config"
	blobDir    string // bucketBase + "/blobs"

	Ctime time.Time // Timestamp for this configuration.
}

// Deployer creates and starts an instance such as defined in Conf.
type Deployer struct {
	Cl              *http.Client
	Conf            *InstanceConf
	certFingerprint string // SHA-256 fingerprint of the HTTPS certificate created during setupHTTPS, if any.
}

// Get returns the Instance corresponding to the Project, Zone, and Name defined in the
// Deployer's Conf.
func (d *Deployer) Get(ctx *context.Context) (*compute.Instance, error) {
	computeService, err := compute.New(d.Cl)
	if err != nil {
		return nil, err
	}
	return computeService.Instances.Get(d.Conf.Project, d.Conf.Zone, d.Conf.Name).Do()
}

// Create sets up and starts a Google Compute Engine instance as defined in d.Conf. It
// creates the necessary Google Storage buckets beforehand.
func (d *Deployer) Create(ctx *context.Context) (*compute.Instance, error) {
	computeService, _ := compute.New(d.Cl)
	storageService, _ := storage.New(d.Cl)

	config := cloudConfig(d.Conf)
	const maxCloudConfig = 32 << 10 // per compute API docs
	if len(config) > maxCloudConfig {
		return nil, fmt.Errorf("cloud config length of %d bytes is over %d byte limit", len(config), maxCloudConfig)
	}

	if err := d.setBuckets(storageService, ctx); err != nil {
		return nil, fmt.Errorf("could not create buckets: %v", err)
	}

	if err := d.setupHTTPS(storageService); err != nil {
		return nil, fmt.Errorf("could not setup HTTPS: %v", err)
	}

	if err := d.createInstance(storageService, computeService, ctx); err != nil {
		return nil, fmt.Errorf("could not create compute instance: %v", err)
	}

	inst, err := computeService.Instances.Get(d.Conf.Project, d.Conf.Zone, d.Conf.Name).Do()
	if err != nil {
		return nil, fmt.Errorf("error getting instance after creation: %v", err)
	}
	if Verbose {
		ij, _ := json.MarshalIndent(inst, "", "    ")
		log.Printf("Instance: %s", ij)
	}
	return inst, nil
}

// createInstance starts the creation of the Compute Engine instance and waits for the
// result of the creation operation. It should be called after setBuckets and setupHTTPS.
func (d *Deployer) createInstance(storageService *storage.Service, computeService *compute.Service, ctx *context.Context) error {
	prefix := projectsAPIURL + d.Conf.Project
	machType := prefix + "/zones/" + d.Conf.Zone + "/machineTypes/" + d.Conf.Machine
	config := cloudConfig(d.Conf)
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
					Value: "test",
				},
				{
					Key:   "camlistore-password",
					Value: "insecure", // TODO: this won't be cleartext later
				},
				{
					Key:   "camlistore-blob-dir",
					Value: "gs://" + d.Conf.blobDir,
				},
				{
					Key:   "camlistore-config-dir",
					Value: "gs://" + d.Conf.configDir,
				},
				{
					Key:   "user-data",
					Value: config,
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
					compute.DevstorageFull_controlScope,
					compute.ComputeScope,
					"https://www.googleapis.com/auth/sqlservice",
					"https://www.googleapis.com/auth/sqlservice.admin",
				},
			},
		},
	}
	if d.Conf.Hostname != "" {
		instance.Metadata.Items = append(instance.Metadata.Items, &compute.MetadataItems{
			Key:   "camlistore-hostname",
			Value: d.Conf.Hostname,
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
		log.Print("Creating instance...")
	}
	op, err := computeService.Instances.Insert(d.Conf.Project, d.Conf.Zone, instance).Do()
	if err != nil {
		return fmt.Errorf("failed to create instance: %v", err)
	}
	opName := op.Name
	if Verbose {
		log.Printf("Created. Waiting on operation %v", opName)
	}
OpLoop:
	for {
		if ctx.IsCanceled() {
			// TODO(mpl): should we destroy any created instance (and attached
			// resources) at this point? So that a subsequent run does not fail
			// with something like:
			// 2014/11/25 17:08:58 Error: &{Code:RESOURCE_ALREADY_EXISTS Location: Message:The resource 'projects/camli-mpl/zones/europe-west1-b/disks/camlistore-server-coreos-stateless-pd' already exists}
			return context.ErrCanceled
		}
		time.Sleep(2 * time.Second)
		op, err := computeService.ZoneOperations.Get(d.Conf.Project, d.Conf.Zone, opName).Do()
		if err != nil {
			return fmt.Errorf("failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			if Verbose {
				log.Printf("Waiting on operation %v", opName)
			}
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					log.Printf("Error: %+v", operr)
				}
				return fmt.Errorf("failed to start.")
			}
			if Verbose {
				log.Printf("Success. %+v", op)
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
	if conf.SSHPub != "" {
		config += fmt.Sprintf("\nssh_authorized_keys:\n    - %s\n", conf.SSHPub)
	}
	return config
}

// setBuckets defines the buckets needed by the instance and creates them.
func (d *Deployer) setBuckets(storageService *storage.Service, ctx *context.Context) error {
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
			log.Printf("Need to create buckets: %v", needBucket)
		}
		var waitBucket sync.WaitGroup
		var bucketErr error
		for name := range needBucket {
			if ctx.IsCanceled() {
				return context.ErrCanceled
			}
			name := name
			waitBucket.Add(1)
			go func() {
				defer waitBucket.Done()
				if Verbose {
					log.Printf("Creating bucket %s", name)
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
					log.Printf("Created bucket %s: %+v", name, b)
				}
			}()
		}
		waitBucket.Wait()
		if bucketErr != nil {
			return bucketErr
		}
	}
	d.Conf.bucketBase = projBucket
	d.Conf.configDir = path.Join(projBucket, configDir)
	d.Conf.blobDir = path.Join(projBucket, "blobs")
	return nil
}

// setupHTTPS uploads to the configuration bucket the certificate and key used by the
// instance for HTTPS. It generates them if d.Conf.CertFile or d.Conf.KeyFile is not defined.
// It should be called after setBuckets.
func (d *Deployer) setupHTTPS(storageService *storage.Service) error {
	var cert, key io.ReadCloser
	var err error
	if d.Conf.CertFile != "" && d.Conf.KeyFile != "" {
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
			log.Printf("Generating self-signed certificate for %v ...", d.Conf.Hostname)
		}
		certBytes, keyBytes, err := httputil.GenSelfTLS(d.Conf.Hostname)
		if err != nil {
			return fmt.Errorf("error generating certificates: %v", err)
		}
		sig, err := httputil.CertFingerprint(certBytes)
		if err != nil {
			return fmt.Errorf("could not get sha256 fingerprint of certificate: %v", err)
		}
		d.certFingerprint = sig
		if Verbose {
			log.Printf("Wrote certificate with fingerprint %s", sig)
		}
		cert = ioutil.NopCloser(bytes.NewReader(certBytes))
		key = ioutil.NopCloser(bytes.NewReader(keyBytes))
	}

	if Verbose {
		log.Print("Uploading certificate and key...")
	}
	_, err = storageService.Objects.Insert(d.Conf.bucketBase,
		&storage.Object{Name: path.Join(configDir, filepath.Base(osutil.DefaultTLSCert()))}).Media(cert).Do()
	if err != nil {
		return fmt.Errorf("cert upload failed: %v", err)
	}
	_, err = storageService.Objects.Insert(d.Conf.bucketBase,
		&storage.Object{Name: path.Join(configDir, filepath.Base(osutil.DefaultTLSKey()))}).Media(key).Do()
	if err != nil {
		return fmt.Errorf("key upload failed: %v", err)
	}
	return nil
}

// CertFingerprint returns the SHA-256 fingerprint of the HTTPS certificate that is
// generated when the instance is created, if any.
func (d *Deployer) CertFingerprint() string {
	return d.certFingerprint
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

// TODO(mpl): investigate why `curl file.tar | docker load` is not working.
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
        ExecStartPre=/usr/bin/docker run --rm -v /opt/bin:/opt/bin ibuildthecloud/systemd-docker
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
        After=docker.service
        Requires=docker.service mysql.service

        [Service]
        ExecStartPre=/usr/bin/docker run --rm -v /opt/bin:/opt/bin ibuildthecloud/systemd-docker
        ExecStartPre=/bin/bash -c '/usr/bin/curl https://storage.googleapis.com/camlistore-release/docker/camlistored.tar.gz | /bin/gunzip -c | /usr/bin/docker load'
        ExecStart=/opt/bin/systemd-docker run --rm -p 80:80 -p 443:443 --name %n -v /run/camjournald.sock:/run/camjournald.sock -v /var/lib/camlistore/tmp:/tmp --link=mysql.service:mysqldb camlistored
        RestartSec=1s
        Restart=always
        Type=notify
        NotifyAccess=all

        [Install]
        WantedBy=multi-user.target
`
