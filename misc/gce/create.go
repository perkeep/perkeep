package main

import (
	"bufio"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	compute "camlistore.org/third_party/code.google.com/p/google-api-go-client/compute/v1"
)

var (
	projFlag     = flag.String("project", "", "name of Project")
	zoneFlag     = flag.String("zone", "us-central1-a", "GCE zone")
	machFlag     = flag.String("machinetype", "g1-small", "e.g. n1-standard-1, f1-micro, g1-small")
	instanceFlag = flag.String("instance_name", "camlistore-server", "Name of VM instance.")
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
	if *projFlag == "" {
		log.Fatalf("Missing --project flag")
	}
	proj := *projFlag
	prefix := "https://www.googleapis.com/compute/v1/projects/" + proj
	imageURL := "https://www.googleapis.com/compute/v1/projects/coreos-cloud/global/images/coreos-alpha-402-2-0-v20140807"
	machType := prefix + "/zones/" + *zoneFlag + "/machineTypes/" + *machFlag

	tr := &oauth.Transport{
		Config: config,
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
	service, _ := compute.New(&http.Client{Transport: tr})

	instance := &compute.Instance{
		Name:        *instanceFlag,
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

	op, err := service.Instances.Insert(*projFlag, *zoneFlag, instance).Do()
	if err != nil {
		log.Fatalf("Failed to create instance: %v", err)
	}
	opName := op.Name
	for {
		op, err := service.ZoneOperations.Get(*projFlag, *zoneFlag, opName).Do()
		if err != nil {
			log.Fatalf("Failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			time.Sleep(1 * time.Second)
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
