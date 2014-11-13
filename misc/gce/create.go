package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	compute "camlistore.org/third_party/code.google.com/p/google-api-go-client/compute/v1"
	storage "camlistore.org/third_party/code.google.com/p/google-api-go-client/storage/v1"
)

var (
	proj     = flag.String("project", "", "Name of Project.")
	zone     = flag.String("zone", "us-central1-a", "GCE zone.")
	mach     = flag.String("machinetype", "g1-small", "e.g. n1-standard-1, f1-micro, g1-small")
	instName = flag.String("instance_name", "camlistore-server", "Name of VM instance.")
	hostname = flag.String("hostname", "", "Hostname for the instance and self-signed certificates. Must be given if generating self-signed certs.")
	certFile = flag.String("cert", "", "Certificate file for TLS. A self-signed one will be generated if this flag is omitted.")
	keyFile  = flag.String("key", "", "Key file for the TLS certificate. Must be given with --cert")
	sshPub   = flag.String("ssh_public_key", "", "SSH public key file to authorize. Can modify later in Google's web UI anyway.")
	verbose  = flag.Bool("verbose", false, "Be verbose.")
)

const (
	clientIdDat       = "client-id.dat"
	clientSecretDat   = "client-secret.dat"
	helpCreateProject = "Create new project: go to https://console.developers.google.com to create a new Project."
	helpEnableAuth    = `Enable authentication: in your project console, navigate to "APIs and auth", "Credentials", click on "Create new Client ID"¸ and pick "Installed application", with type "Other". Copy the CLIENT ID to ` + clientIdDat + `, and the CLIENT SECRET to ` + clientSecretDat
	helpEnableAPIs    = `Enable the project APIs: in your project console, navigate to "APIs and auth", "APIs". In the list, enable "Google Cloud Storage", "Google Cloud Storage JSON API", and "Google Compute Engine".`
)

func readFile(v string) string {
	slurp, err := ioutil.ReadFile(v)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("%v does not exist.\n%s", v, helpEnableAuth)
		}
		log.Fatalf("Error reading %s: %v", v, err)
	}
	return strings.TrimSpace(string(slurp))
}

func printHelp() {
	for _, v := range []string{helpCreateProject, helpEnableAuth, helpEnableAPIs} {
		fmt.Fprintf(os.Stderr, "%v\n", v)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n\n    %s\n    %s\n\n",
		"go run create.go --project=<project> --hostname=<hostname> [options]",
		"go run create.go --project=<project> --cert=<cert file> --key=<key file> [options]")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\nTo get started with this script:\n")
	printHelp()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if *proj == "" {
		log.Print("Missing --project flag.")
		usage()
		return
	}
	if (*certFile == "") != (*keyFile == "") {
		log.Print("--cert and --key must both be given together.")
		return
	}
	if *certFile == "" && *hostname == "" {
		log.Print("Either --hostname, or --cert & --key must provided.")
		return
	}
	prefix := "https://www.googleapis.com/compute/v1/projects/" + *proj
	imageURL := "https://www.googleapis.com/compute/v1/projects/coreos-cloud/global/images/coreos-alpha-402-2-0-v20140807"
	machType := prefix + "/zones/" + *zone + "/machineTypes/" + *mach

	config := &oauth.Config{
		// The client-id and secret should be for an "Installed Application" when using
		// the CLI. Later we'll use a web application with a callback.
		ClientId:     readFile(clientIdDat),
		ClientSecret: readFile(clientSecretDat),
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

	tr := &oauth.Transport{
		Config: config,
	}

	tokenCache := oauth.CacheFile(*proj + "-token.dat")
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
	oauthClient := &http.Client{Transport: tr}
	computeService, _ := compute.New(oauthClient)
	storageService, _ := storage.New(oauthClient)

	cloudConfig := `#cloud-config
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
        ExecStart=/opt/bin/systemd-docker run --rm -p 80:80 -p 443:443 --name %n -v /run/camjournald.sock:/run/camjournald.sock -v /var/lib/camlistore/tmp:/tmp --link=mysql.service:mysqldb camlistore/camlistored
        RestartSec=1s
        Restart=always
        Type=notify
        NotifyAccess=all
        
        [Install]
        WantedBy=multi-user.target
`
	cloudConfig = strings.Replace(cloudConfig, "INNODB_BUFFER_POOL_SIZE=NNN", "INNODB_BUFFER_POOL_SIZE="+strconv.Itoa(innodbBufferPoolSize(*mach)), -1)

	const maxCloudConfig = 32 << 10 // per compute API docs
	if len(cloudConfig) > maxCloudConfig {
		log.Fatalf("cloud config length of %d bytes is over %d byte limit", len(cloudConfig), maxCloudConfig)
	}
	if *sshPub != "" {
		key := strings.TrimSpace(readFile(*sshPub))
		cloudConfig += fmt.Sprintf("\nssh_authorized_keys:\n    - %s\n", key)
	}

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

	if *certFile == "" {
		// A bit paranoid since these are illigal GCE project name characters anyway but it doesn't hurt.
		r := strings.NewReplacer(".", "_", "/", "_", "\\", "_")
		*certFile = r.Replace(*proj) + ".crt"
		*keyFile = r.Replace(*proj) + ".key"

		_, errc := os.Stat(*certFile)
		_, errk := os.Stat(*keyFile)
		switch {
		case os.IsNotExist(errc) && os.IsNotExist(errk):
			log.Printf("Generating self-signed certificate for %v ...", *hostname)
			err, sig := httputil.GenSelfTLS(*hostname, *certFile, *keyFile)
			if err != nil {
				log.Fatalf("Error generating certificates: %v", err)
			}
			log.Printf("Wrote key to %s, and certificate to %s with fingerprint %s", *keyFile, *certFile, sig)
		case errc != nil:
			log.Fatalf("Couldn't stat cert: %v", errc)
		case errk != nil:
			log.Fatalf("Couldn't stat key: %v", errk)
		default:
			log.Printf("Using certificate %s and key %s", *certFile, *keyFile)
		}
	}

	log.Print("Uploading certificate and key...")
	err = uploadFile(storageService, *certFile, configBucket, filepath.Base(osutil.DefaultTLSCert()))
	if err != nil {
		log.Fatalf("Cert upload failed: %v", err)
	}
	err = uploadFile(storageService, *keyFile, configBucket, filepath.Base(osutil.DefaultTLSKey()))
	if err != nil {
		log.Fatalf("Key upload failed: %v", err)
	}

	instance := &compute.Instance{
		Name:        *instName,
		Description: "Camlistore server",
		MachineType: machType,
		Disks: []*compute.AttachedDisk{
			{
				AutoDelete: true,
				Boot:       true,
				Type:       "PERSISTENT",
				InitializeParams: &compute.AttachedDiskInitializeParams{
					DiskName:    *instName + "-coreos-stateless-pd",
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
	if *hostname != "" {
		instance.Metadata.Items = append(instance.Metadata.Items, &compute.MetadataItems{
			Key:   "camlistore-hostname",
			Value: *hostname,
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

	log.Print("Creating instance...")
	op, err := computeService.Instances.Insert(*proj, *zone, instance).Do()
	if err != nil {
		log.Fatalf("Failed to create instance: %v", err)
	}
	opName := op.Name
	if *verbose {
		log.Printf("Created. Waiting on operation %v", opName)
	}
OpLoop:
	for {
		time.Sleep(2 * time.Second)
		op, err := computeService.ZoneOperations.Get(*proj, *zone, opName).Do()
		if err != nil {
			log.Fatalf("Failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			if *verbose {
				log.Printf("Waiting on operation %v", opName)
			}
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					log.Printf("Error: %+v", operr)
				}
				log.Fatalf("Failed to start.")
			}
			if *verbose {
				log.Printf("Success. %+v", op)
			}
			break OpLoop
		default:
			log.Fatalf("Unknown status %q: %+v", op.Status, op)
		}
	}

	inst, err := computeService.Instances.Get(*proj, *zone, *instName).Do()
	if err != nil {
		log.Fatalf("Error getting instance after creation: %v", err)
	}

	if *verbose {
		ij, _ := json.MarshalIndent(inst, "", "    ")
		log.Printf("Instance: %s", ij)
	}

	addr := inst.NetworkInterfaces[0].AccessConfigs[0].NatIP
	log.Printf("Instance is up at %s", addr)
}

func uploadFile(service *storage.Service, localFilename, bucketName, objectName string) error {
	file, err := os.Open(localFilename)
	defer file.Close()
	if err != nil {
		return fmt.Errorf("Error opening %v: %v", localFilename, err)
	}
	_, err = service.Objects.Insert(bucketName, &storage.Object{Name: objectName}).Media(file).Do()
	if err != nil {
		return fmt.Errorf("Objects.Insert for %v failed: %v", localFilename, err)
	}
	return nil
}

// returns the MySQL InnoDB buffer pool size (in bytes) as a function
// of the GCE machine type.
func innodbBufferPoolSize(machType string) int {
	// Totally arbitrary. We don't need much here because
	// camlistored slurps this all into its RAM on start-up
	// anyway. So this is all prety overkill and more than the
	// 8MB default.
	switch machType {
	case "f1-micro":
		return 32 << 20
	case "g1-small":
		return 64 << 20
	default:
		return 128 << 20
	}
}
