package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	compute "camlistore.org/third_party/code.google.com/p/google-api-go-client/compute/v1"
	storage "camlistore.org/third_party/code.google.com/p/google-api-go-client/storage/v1"
)

var (
	proj     = flag.String("project", "", "name of Project")
	zone     = flag.String("zone", "us-central1-a", "GCE zone")
	mach     = flag.String("machinetype", "g1-small", "e.g. n1-standard-1, f1-micro, g1-small")
	instance = flag.String("instance_name", "camlistore-server", "Name of VM instance.")
	sshPub   = flag.String("ssh_public_key", "", "ssh public key file to authorize. Can modify later in Google's web UI anyway.")
)

func readFile(v string) string {
	slurp, err := ioutil.ReadFile(v)
	if err != nil {
		log.Fatalf("Error reading %s: %v", v, err)
	}
	return strings.TrimSpace(string(slurp))
}

var config = &oauth.Config{
	// The client-id and secret should be for an "Installed Application" when using
	// the CLI. Later we'll use a web application with a callback.
	ClientId:     readFile("client-id.dat"),
	ClientSecret: readFile("client-secret.dat"),
	Scope: strings.Join([]string{
		compute.DevstorageFull_controlScope,
		compute.ComputeScope,
		"https://www.googleapis.com/auth/sqlservice",
		"https://www.googleapis.com/auth/sqlservice.admin",
	}, " "),
	AuthURL:     "https://accounts.google.com/o/oauth2/auth",
	TokenURL:    "https://accounts.google.com/o/oauth2/token",
	RedirectURL: "urn:ietf:wg:oauth:2.0:oob",
}

func main() {
	flag.Parse()
	if *proj == "" {
		log.Fatalf("Missing --project flag")
	}
	prefix := "https://www.googleapis.com/compute/v1/projects/" + *proj
	imageURL := "https://www.googleapis.com/compute/v1/projects/coreos-cloud/global/images/coreos-alpha-402-2-0-v20140807"
	machType := prefix + "/zones/" + *zone + "/machineTypes/" + *mach

	tr := &oauth.Transport{
		Config: config,
	}

	cloudConfig := `#cloud-config
coreos:
  units:
    - name: camlistored.service
      command: start
      content: |
        [Unit]
        Description=Camlistore
        After=docker.service
        Requires=docker.service
        
        [Service]
        ExecStart=/usr/bin/docker run -p 80:80 -p 443:443 camlistore/camlistored
        RestartSec=500ms
        Restart=always
        
        [Install]
        WantedBy=multi-user.target
`

	if *sshPub != "" {
		key := strings.TrimSpace(readFile(*sshPub))
		cloudConfig += fmt.Sprintf("\nssh_authorized_keys:\n    - %s\n", key)
	}

	tokenCache := oauth.CacheFile("token.dat")
	token, err := tokenCache.Token()
	if err != nil {
		log.Printf("Error getting token from %s: %v", string(tokenCache), err)
		log.Printf("Get auth code from %v", config.AuthCodeURL("my-state"))
		os.Stdout.Write([]byte("\nEnter auth code: "))
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		authCode := strings.TrimSpace(sc.Text())
		token, err = tr.Exchange(authCode)
		if err != nil {
			log.Fatalf("Error exchanging auth code for a token: %v", err)
		}
		tokenCache.PutToken(token)
	}

	tr.Token = token
	computeService, _ := compute.New(&http.Client{Transport: tr})
	storageService, _ := storage.New(&http.Client{Transport: tr})

	blobBucket := *proj + "-camlistore-blobs"
	configBucket := *proj + "-camlistore-config"
	needBucket := map[string]bool{
		blobBucket:   true,
		configBucket: true,
	}

	buckets, err := storageService.Buckets.List(*proj).Do()
	if err != nil {
		log.Fatalf("Error listing buckets: %v", err)
	}
	for _, it := range buckets.Items {
		delete(needBucket, it.Name)
	}
	if len(needBucket) > 0 {
		log.Printf("Need to create buckets: %v", needBucket)
		var waitBucket sync.WaitGroup
		for name := range needBucket {
			name := name
			waitBucket.Add(1)
			go func() {
				defer waitBucket.Done()
				log.Printf("Creating bucket %s", name)
				b, err := storageService.Buckets.Insert(*proj, &storage.Bucket{
					Id:   name,
					Name: name,
				}).Do()
				if err != nil {
					log.Fatalf("Error creating bucket %s: %v", name, err)
				}
				log.Printf("Created bucket %s: %+v", name, b)
			}()
		}
		waitBucket.Wait()
	}

	instance := &compute.Instance{
		Name:        *instance,
		Description: "Camlistore server",
		MachineType: machType,
		Disks: []*compute.AttachedDisk{
			{
				AutoDelete: true,
				Boot:       true,
				Type:       "PERSISTENT",
				InitializeParams: &compute.AttachedDiskInitializeParams{
					DiskName:    "camlistore-coreos-stateless-pd",
					SourceImage: imageURL,
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
					Key:   "camlistore-blob-bucket",
					Value: "gs://" + blobBucket,
				},
				{
					Key:   "camlistore-config-bucket",
					Value: "gs://" + configBucket,
				},
				{
					Key:   "user-data",
					Value: cloudConfig,
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

	log.Printf("Creating instance...")
	op, err := computeService.Instances.Insert(*proj, *zone, instance).Do()
	if err != nil {
		log.Fatalf("Failed to create instance: %v", err)
	}
	opName := op.Name
	log.Printf("Created. Waiting on operation %v", opName)
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
			return
		default:
			log.Fatalf("Unknown status %q: %+v", op.Status, op)
		}
	}
}
