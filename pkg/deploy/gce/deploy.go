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
package gce // import "camlistore.org/pkg/deploy/gce"

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/osutil"

	"cloud.google.com/go/logging"
	"go4.org/cloud/google/gceutil"
	"go4.org/syncutil"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	servicemanagement "google.golang.org/api/servicemanagement/v1"
	storage "google.golang.org/api/storage/v1"
)

const (
	DefaultInstanceName = "camlistore-server"
	DefaultMachineType  = "g1-small"
	DefaultRegion       = "us-central1"

	projectsAPIURL = "https://www.googleapis.com/compute/v1/projects/"

	fallbackZone = "us-central1-a"

	camliUsername = "camlistore" // directly set in compute metadata, so not user settable.

	configDir = "config"

	ConsoleURL         = "https://console.developers.google.com"
	helpDeleteInstance = `To delete an existing Compute Engine instance: in your project console, navigate to "Compute", "Compute Engine", and "VM instances". Select your instance and click "Delete".`
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
			logging.WriteScope,
			compute.DevstorageFullControlScope,
			compute.ComputeScope,
			cloudresourcemanager.CloudPlatformScope,
			servicemanagement.CloudPlatformScope,
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
	Name          string // Name given to the virtual machine instance.
	Project       string // Google project ID where the instance is created.
	CreateProject bool   // CreateProject defines whether to first create project.
	Machine       string // Machine type.
	Zone          string // GCE zone; see https://cloud.google.com/compute/docs/zones
	Hostname      string // Fully qualified domain name.

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

// CreateProject creates a new Google Cloud Project. It returns the project ID,
// which is a random number in (0,1e10), prefixed with "camlistore-launcher-".
func (d *Deployer) CreateProject(ctx context.Context) (string, error) {
	s, err := cloudresourcemanager.New(d.Client)
	if err != nil {
		return "", err
	}
	// Allow for a few retries, when we generated an already taken project ID
	creationTimeout := time.Now().Add(time.Minute)
	var projectID, projectName string
	for {
		if d.Conf.Project != "" {
			projectID = d.Conf.Project
			projectName = projectID
		} else {
			projectID = genRandomProjectID()
			projectName = strings.Replace(projectID, "camlistore-launcher-", "Camlistore ", 1)
		}
		project := cloudresourcemanager.Project{
			Name:      projectName,
			ProjectId: projectID,
		}
		if time.Now().After(creationTimeout) {
			return "", errors.New("timeout while trying to create project")
		}
		d.Printf("Trying to create project %v", projectID)
		op, err := cloudresourcemanager.NewProjectsService(s).Create(&project).Do()
		if err != nil {
			gerr, ok := err.(*googleapi.Error)
			if !ok {
				return "", fmt.Errorf("could not create project: %v", err)
			}
			if gerr.Code != 409 {
				return "", fmt.Errorf("could not create project: %v", gerr.Message)
			}
			// it's ok using time.Sleep, and no backoff, as the
			// timeout is pretty short, and we only retry on a 409.
			time.Sleep(time.Second)
			// retry if project ID already exists.
			d.Printf("Project %v already exists, will retry with a new project ID", project.ProjectId)
			continue
		}

		// as per
		// https://cloud.google.com/resource-manager/reference/rest/v1/projects/create
		// recommendation
		timeout := time.Now().Add(30 * time.Second)
		backoff := time.Second
		startPolling := 5 * time.Second
		time.Sleep(startPolling)
		for {
			if time.Now().After(timeout) {
				return "", fmt.Errorf("timeout while trying to check project creation")
			}
			if !op.Done {
				// it's ok to just sleep, as our timeout is pretty short.
				time.Sleep(backoff)
				backoff *= 2
				op, err = cloudresourcemanager.NewOperationsService(s).Get(op.Name).Do()
				if err != nil {
					return "", fmt.Errorf("could not check project creation status: %v", err)
				}
				continue
			}
			if op.Error != nil {
				// TODO(mpl): ghetto logging for now. detect at least the quota errors.
				var details string
				for _, v := range op.Error.Details {
					details += string(v)
				}
				return "", fmt.Errorf("could not create project: %v, %v", op.Error.Message, details)
			}
			break
		}
		break
	}
	d.Printf("Success creating project %v", projectID)
	return projectID, nil
}

func genRandomProjectID() string {
	// we're allowed up to 30 characters, and we already consume 20 with
	// "camlistore-launcher-", so we've got 10 chars left of randomness. Should
	// be plenty enough I think.
	var n *big.Int
	var err error
	zero := big.NewInt(0)
	for {
		n, err = rand.Int(rand.Reader, big.NewInt(1e10)) // max is 1e10 - 1
		if err != nil {
			panic(fmt.Sprintf("rand.Int error: %v", err))
		}
		if n.Cmp(zero) > 0 {
			break
		}
	}
	return fmt.Sprintf("camlistore-launcher-%d", n)
}

func (d *Deployer) enableAPIs() error {
	// TODO(mpl): For now we're lucky enough that servicemanagement seems to
	// work even when the Service Management API hasn't been enabled for the
	// project. If/when it does not anymore, then we should use serviceuser
	// instead. http://stackoverflow.com/a/43503392/1775619
	s, err := servicemanagement.New(d.Client)
	if err != nil {
		return err
	}

	list, err := servicemanagement.NewServicesService(s).List().ConsumerId("project:" + d.Conf.Project).Do()
	if err != nil {
		return err
	}

	requiredServices := map[string]string{
		"storage-component.googleapis.com": "Google Cloud Storage",
		"storage-api.googleapis.com":       "Google Cloud Storage JSON",
		"logging.googleapis.com":           "Stackdriver Logging",
		"compute-component.googleapis.com": "Google Compute Engine",
	}
	enabledServices := make(map[string]bool)
	for _, v := range list.Services {
		enabledServices[v.ServiceName] = true
	}
	errc := make(chan error, len(requiredServices))
	var wg sync.WaitGroup
	for k, v := range requiredServices {
		if _, ok := enabledServices[k]; ok {
			continue
		}
		d.Printf("%v API not enabled; enabling it with Service Management", v)
		op, err := servicemanagement.NewServicesService(s).
			Enable(k, &servicemanagement.EnableServiceRequest{ConsumerId: "project:" + d.Conf.Project}).Do()
		if err != nil {
			gerr, ok := err.(*googleapi.Error)
			if !ok {
				return err
			}
			if gerr.Code != 400 {
				return err
			}
			for _, v := range gerr.Errors {
				if v.Reason == "failedPrecondition" && strings.Contains(v.Message, "billing-enabled") {
					return fmt.Errorf("you need to enabling billing for project %v: https://console.cloud.google.com/billing/?project=%v", d.Conf.Project, d.Conf.Project)
				}
			}
			return err
		}

		wg.Add(1)
		go func(service, opName string) {
			defer wg.Done()
			timeout := time.Now().Add(2 * time.Minute)
			backoff := time.Second
			startPolling := 5 * time.Second
			time.Sleep(startPolling)
			for {
				if time.Now().After(timeout) {
					errc <- fmt.Errorf("timeout while trying to enable service: %v", service)
					return
				}
				op, err := servicemanagement.NewOperationsService(s).Get(opName).Do()
				if err != nil {
					errc <- fmt.Errorf("could not check service enabling status: %v", err)
					return
				}
				if !op.Done {
					// it's ok to just sleep, as our timeout is pretty short.
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
				if op.Error != nil {
					errc <- fmt.Errorf("could not enable service %v: %v", service, op.Error.Message)
					return
				}
				d.Printf("%v service successfully enabled", service)
				return
			}
		}(k, op.Name)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			return err
		}
	}
	return nil
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
			cause: fmt.Errorf("project IDs do not match: got %q, wanted %q", project.Name, d.Conf.Project),
		}
	}
	return nil
}

var errAttrNotFound = errors.New("attribute not found")

// getInstanceAttribute returns the value for attr in the custom metadata of the
// instance. It returns errAttrNotFound is such a metadata attributed does not
// exist.
func (d *Deployer) getInstanceAttribute(attr string) (string, error) {
	s, err := compute.New(d.Client)
	if err != nil {
		return "", fmt.Errorf("error getting compute service: %v", err)
	}
	inst, err := compute.NewInstancesService(s).Get(d.Conf.Project, d.Conf.Zone, d.Conf.Name).Do()
	if err != nil {
		return "", fmt.Errorf("error getting instance: %v", err)
	}
	for _, v := range inst.Metadata.Items {
		if v.Key == attr {
			return *(v.Value), nil
		}
	}
	return "", errAttrNotFound
}

// Create sets up and starts a Google Compute Engine instance as defined in d.Conf. It
// creates the necessary Google Storage buckets beforehand.
func (d *Deployer) Create(ctx context.Context) (*compute.Instance, error) {
	if err := d.enableAPIs(); err != nil {
		return nil, projectIDError{
			id:    d.Conf.Project,
			cause: err,
		}
	}
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
					Value: googleapi.String(randPassword()),
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
					logging.WriteScope,
					compute.DevstorageFullControlScope,
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
	return config
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
