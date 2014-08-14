package main

import (
	"flag"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	compute "camlistore.org/third_party/code.google.com/p/google-api-go-client/compute/v1"
)

var (
	projFlag = flag.String("project", "", "name of Project")
	zoneFlag = flag.String("zone", "us-central1-a", "GCE zone")
	machFlag = flag.String("machinetype", "g1-small", "e.g. n1-standard-1, f1-micro, g1-small")
)

func readFile(v string) string {
	slurp, err := ioutil.ReadFile(v)
	if err != nil {
		log.Fatalf("Error reading %s: %v", v, err)
	}
	return strings.TrimSpace(string(slurp))
}

var config = &oauth.Config{
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

	if _, err := os.Stat("auth-code.dat"); err != nil {
		log.Printf("No auth code in file auth-code.dat. Get auth code from %v", config.AuthCodeURL("my-state"))
		return
	}
	authCode := readFile("auth-code.dat")

	tr := &oauth.Transport{
		Config: config,
	}
	t, err := tr.Exchange(authCode)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Got token: %#v", t)
	_, _ = machType, imageURL
}
