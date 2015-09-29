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

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/deploy/gce"
	"camlistore.org/pkg/oauthutil"

	"golang.org/x/oauth2"
)

type gceCmd struct {
	project  string
	zone     string
	machine  string
	instName string
	hostname string
	certFile string
	keyFile  string
	sshPub   string
	verbose  bool
}

func init() {
	cmdmain.RegisterCommand("gce", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(gceCmd)
		flags.StringVar(&cmd.project, "project", "", "Name of Project.")
		flags.StringVar(&cmd.zone, "zone", gce.Zone, "GCE zone.")
		flags.StringVar(&cmd.machine, "machine", gce.Machine, "e.g. n1-standard-1, f1-micro, g1-small")
		flags.StringVar(&cmd.instName, "instance_name", gce.InstanceName, "Name of VM instance.")
		flags.StringVar(&cmd.hostname, "hostname", "", "Hostname for the instance and self-signed certificates. Must be given if generating self-signed certs.")
		flags.StringVar(&cmd.certFile, "cert", "", "Certificate file for TLS. A self-signed one will be generated if this flag is omitted.")
		flags.StringVar(&cmd.keyFile, "key", "", "Key file for the TLS certificate. Must be given with --cert")
		flags.StringVar(&cmd.sshPub, "ssh_public_key", "", "SSH public key file to authorize. Can modify later in Google's web UI anyway.")
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		return cmd
	})
}

const (
	clientIdDat     = "client-id.dat"
	clientSecretDat = "client-secret.dat"
	helpEnableAuth  = `Enable authentication: in your project console, navigate to "APIs and auth", "Credentials", click on "Create new Client ID", and pick "Installed application", with type "Other". Copy the CLIENT ID to ` + clientIdDat + `, and the CLIENT SECRET to ` + clientSecretDat
)

func (c *gceCmd) Describe() string {
	return "Deploy Camlistore on Google Compute Engine."
}

func (c *gceCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n\n    %s\n    %s\n\n",
		"camdeploy gce --project=<project> --hostname=<hostname> [options]",
		"camdeploy gce --project=<project> --cert=<cert file> --key=<key file> [options]")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\nTo get started:\n")
	printHelp()
}

func printHelp() {
	for _, v := range []string{gce.HelpCreateProject, helpEnableAuth, gce.HelpEnableAPIs} {
		fmt.Fprintf(os.Stderr, "%v\n", v)
	}
}

func (c *gceCmd) RunCommand(args []string) error {
	if c.verbose {
		gce.Verbose = true
	}
	if c.project == "" {
		return cmdmain.UsageError("Missing --project flag.")
	}
	if (c.certFile == "") != (c.keyFile == "") {
		return cmdmain.UsageError("--cert and --key must both be given together.")
	}
	if c.certFile == "" && c.hostname == "" {
		return cmdmain.UsageError("Either --hostname, or --cert & --key must provided.")
	}
	config := gce.NewOAuthConfig(readFile(clientIdDat), readFile(clientSecretDat))
	config.RedirectURL = "urn:ietf:wg:oauth:2.0:oob"

	instConf := &gce.InstanceConf{
		Name:     c.instName,
		Project:  c.project,
		Machine:  c.machine,
		Zone:     c.zone,
		CertFile: c.certFile,
		KeyFile:  c.keyFile,
		Hostname: c.hostname,
	}
	if c.sshPub != "" {
		instConf.SSHPub = strings.TrimSpace(readFile(c.sshPub))
	}

	depl := &gce.Deployer{
		Client: oauth2.NewClient(oauth2.NoContext, oauth2.ReuseTokenSource(nil, &oauthutil.TokenSource{
			Config:    config,
			CacheFile: c.project + "-token.json",
			AuthCode: func() string {
				fmt.Println("Get auth code from:")
				fmt.Printf("%v\n", config.AuthCodeURL("my-state", oauth2.AccessTypeOffline, oauth2.ApprovalForce))
				fmt.Println("Enter auth code:")
				sc := bufio.NewScanner(os.Stdin)
				sc.Scan()
				return strings.TrimSpace(sc.Text())
			},
		})),
		Conf: instConf,
	}
	inst, err := depl.Create(context.TODO())
	if err != nil {
		return err
	}

	log.Printf("Instance is up at %s", inst.NetworkInterfaces[0].AccessConfigs[0].NatIP)
	return nil
}

func readFile(v string) string {
	slurp, err := ioutil.ReadFile(v)
	if err != nil {
		if os.IsNotExist(err) {
			msg := fmt.Sprintf("%v does not exist.", v)
			if v == clientIdDat || v == clientSecretDat {
				msg = fmt.Sprintf("%v\n%s", msg, helpEnableAuth)
			}
			log.Fatal(msg)
		}
		log.Fatalf("Error reading %s: %v", v, err)
	}
	return strings.TrimSpace(string(slurp))
}
