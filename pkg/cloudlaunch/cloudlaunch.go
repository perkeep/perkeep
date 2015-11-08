/*
Copyright 2015 The Camlistore Authors

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

// Package cloudlaunch helps binaries run themselves on The Cloud, copying
// themselves to GCE.
package cloudlaunch

import (
	"flag"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	storageapi "google.golang.org/api/storage/v1"
)

func readFile(v string) string {
	slurp, err := ioutil.ReadFile(v)
	if err != nil {
		log.Fatalf("Error reading %s: %v", v, err)
	}
	return strings.TrimSpace(string(slurp))
}

const baseConfig = `#cloud-config
coreos:
  update:
    group: stable
    reboot-strategy: off
  units:
    - name: $NAME.service
      command: start
      content: |
        [Unit]
        Description=$NAME service
        After=network.target
        
        [Service]
        Type=simple
        ExecStartPre=/bin/sh -c 'mkdir -p /opt/bin && /usr/bin/curl -f -o /opt/bin/$NAME $URL?$(date +%s) && chmod +x /opt/bin/$NAME'
        ExecStart=/opt/bin/$NAME
        RestartSec=10
        Restart=always
        StartLimitInterval=0
        
        [Install]
        WantedBy=network-online.target
`

type Config struct {
	// Name is the name of a service to run.
	// This is the name of the systemd service (without .service)
	// and the name of the GCE instance.
	Name string

	// BinaryURL is the URL of the Linux binary to download on
	// boot and occasionally run. This binary must be public (at
	// least for now).
	BinaryURL string

	GCEProjectID string
	Zone         string // defaults to us-central1-f

	Scopes []string // any additional scopes

	MachineType  string
	InstanceName string
}

func (c *Config) zone() string        { return strDefault(c.Zone, "us-central1-f") }
func (c *Config) machineType() string { return strDefault(c.MachineType, "g1-small") }

func strDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var (
	doLaunch = flag.Bool("cloudlaunch", false, "Deploy or update this binary to the cloud. Must be on Linux, for now.")
)

func (c *Config) MaybeDeploy() {
	flag.Parse()
	if !*doLaunch {
		return
	}
	defer os.Exit(1) // backup, in case we return without Fatal or os.Exit later

	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		log.Fatal("Can only use --cloudlaunch on linux/amd64, for now.")
	}

	if c.GCEProjectID == "" {
		log.Fatal("cloudconfig.GCEProjectID is empty")
	}
	filename := filepath.Join(os.Getenv("HOME"), "keys", c.GCEProjectID+".key.json")
	log.Printf("Using OAuth config from JSON service file: %s", filename)
	oauthConfig, err := google.ConfigFromJSON([]byte(readFile(filename)), append([]string{
		storageapi.DevstorageFullControlScope,
		compute.ComputeScope,
		"https://www.googleapis.com/auth/cloud-platform",
	}, c.Scopes...)...)
	if err != nil {
		log.Fatal(err)
	}
	prefix := "https://www.googleapis.com/compute/v1/projects/" + c.GCEProjectID
	machType := prefix + "/zones/" + c.zone() + "/machineTypes/" + c.machineType()
	_ = machType

	oauthClient := oauthConfig.Client(oauth2.NoContext, nil)
	computeService, _ := compute.New(oauthClient)

	// Try to find it by name.
	aggAddrList, err := computeService.Addresses.AggregatedList(c.GCEProjectID).Do()
	if err != nil {
		log.Fatal(err)
	}
	// https://godoc.org/google.golang.org/api/compute/v1#AddressAggregatedList
	log.Printf("Addr list: %v", aggAddrList.Items)
	var ip string
IPLoop:
	for _, asl := range aggAddrList.Items {
		for _, addr := range asl.Addresses {
			log.Printf("  addr: %#v", addr)
			if addr.Name == c.Name+"-ip" && addr.Status == "RESERVED" {
				ip = addr.Address
				break IPLoop
			}
		}
	}
	log.Printf("Found IP: %v", ip)

	// TODO: copy binary to GCE

	os.Exit(0)
}

/*
	cloudConfig := strings.Replace(baseConfig, "$COORDINATOR", *coordinator, 1)
	if *sshPub != "" {
		key := strings.TrimSpace(readFile(*sshPub))
		cloudConfig += fmt.Sprintf("\nssh_authorized_keys:\n    - %s\n", key)
	}
	if os.Getenv("USER") == "bradfitz" {
		cloudConfig += fmt.Sprintf("\nssh_authorized_keys:\n    - %s\n", "ssh-rsa AAAAB3NzaC1yc2EAAAABIwAAAIEAwks9dwWKlRC+73gRbvYtVg0vdCwDSuIlyt4z6xa/YU/jTDynM4R4W10hm2tPjy8iR1k8XhDv4/qdxe6m07NjG/By1tkmGpm1mGwho4Pr5kbAAy/Qg+NLCSdAYnnE00FQEcFOC15GFVMOW2AzDGKisReohwH9eIzHPzdYQNPRWXE= bradfitz@papag.bradfitz.com")
	}
	const maxCloudConfig = 32 << 10 // per compute API docs
	if len(cloudConfig) > maxCloudConfig {
		log.Fatalf("cloud config length of %d bytes is over %d byte limit", len(cloudConfig), maxCloudConfig)
	}

	instance := &compute.Instance{
		Name:        *instName,
		Description: "Go Builder",
		MachineType: machType,
		Disks:       []*compute.AttachedDisk{instanceDisk(computeService)},
		Tags: &compute.Tags{
			Items: []string{"http-server", "https-server", "allow-ssh"},
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "user-data",
					Value: googleapi.String(cloudConfig),
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			&compute.NetworkInterface{
				AccessConfigs: []*compute.AccessConfig{
					&compute.AccessConfig{
						Type:  "ONE_TO_ONE_NAT",
						Name:  "External NAT",
						NatIP: natIP,
					},
				},
				Network: prefix + "/global/networks/default",
			},
		},
		ServiceAccounts: []*compute.ServiceAccount{
			{
				Email: "default",
				Scopes: []string{
					compute.DevstorageFullControlScope,
					compute.ComputeScope,
				},
			},
		},
	}

	log.Printf("Creating instance...")
	op, err := computeService.Instances.Insert(*proj, *zone, instance).Do()
	if err != nil {
		log.Fatalf("Failed to create instance: %v", err)
	}
	opName := op.Name
	log.Printf("Created. Waiting on operation %v", opName)
OpLoop:
	for {
		time.Sleep(2 * time.Second)
		op, err := computeService.ZoneOperations.Get(*proj, *zone, opName).Do()
		if err != nil {
			log.Fatalf("Failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			log.Printf("Waiting on operation %v", opName)
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					log.Printf("Error: %+v", operr)
				}
				log.Fatalf("Failed to start.")
			}
			log.Printf("Success. %+v", op)
			break OpLoop
		default:
			log.Fatalf("Unknown status %q: %+v", op.Status, op)
		}
	}

	inst, err := computeService.Instances.Get(*proj, *zone, *instName).Do()
	if err != nil {
		log.Fatalf("Error getting instance after creation: %v", err)
	}
	ij, _ := json.MarshalIndent(inst, "", "    ")
	log.Printf("Instance: %s", ij)
}

func instanceDisk(svc *compute.Service) *compute.AttachedDisk {
	const imageURL = "https://www.googleapis.com/compute/v1/projects/coreos-cloud/global/images/coreos-stable-723-3-0-v20150804"
	diskName := *instName + "-coreos-stateless-pd"

	if *reuseDisk {
		dl, err := svc.Disks.List(*proj, *zone).Do()
		if err != nil {
			log.Fatalf("Error listing disks: %v", err)
		}
		for _, disk := range dl.Items {
			if disk.Name != diskName {
				continue
			}
			return &compute.AttachedDisk{
				AutoDelete: false,
				Boot:       true,
				DeviceName: diskName,
				Type:       "PERSISTENT",
				Source:     disk.SelfLink,
				Mode:       "READ_WRITE",

				// The GCP web UI's "Show REST API" link includes a
				// "zone" parameter, but it's not in the API
				// description. But it wants this form (disk.Zone, a
				// full zone URL, not *zone):
				// Zone: disk.Zone,
				// ... but it seems to work without it.  Keep this
				// comment here until I file a bug with the GCP
				// people.
			}
		}
	}

	diskType := ""
	if *ssd {
		diskType = "https://www.googleapis.com/compute/v1/projects/" + *proj + "/zones/" + *zone + "/diskTypes/pd-ssd"
	}

	return &compute.AttachedDisk{
		AutoDelete: !*reuseDisk,
		Boot:       true,
		Type:       "PERSISTENT",
		InitializeParams: &compute.AttachedDiskInitializeParams{
			DiskName:    diskName,
			SourceImage: imageURL,
			DiskSizeGb:  50,
			DiskType:    diskType,
		},
	}
}
*/
