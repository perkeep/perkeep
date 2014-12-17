package main

import (
	"io/ioutil"
	"log"
	"path"
	"strings"
	"syscall"

	"camlistore.org/pkg/wkfs"
	_ "camlistore.org/pkg/wkfs/gcs"

	"camlistore.org/third_party/github.com/bradfitz/gce"
)

func downloadFile(dst, src string) error {
	f, err := wkfs.Open(src)
	if err != nil {
		return err
	}
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(dst, b, 555)
}

func main() {
	v, err := gce.InstanceAttributeValue("camlistore-config-dir")
	if err != nil {
		log.Fatalf("Error getting config bucket: %v", err)
	}
	cfgPath := path.Clean("/gcs/" + strings.TrimPrefix(v, "gs://"))

	err = downloadFile("/etc/nginx/tls.crt", cfgPath+"/tls.crt")
	if err != nil {
		log.Fatalf("Error getting TLS certificate: %v", err)
	}
	log.Print("Wrote /etc/nginx/tls.crt")

	err = downloadFile("/etc/nginx/tls.key", cfgPath+"/tls.key")
	if err != nil {
		log.Fatalf("Error getting TLS key: %v", err)
	}
	log.Print("Wrote /etc/nginx/tls.key")

	log.Print("Launching nginx")
	syscall.Exec("/usr/sbin/nginx", []string{"nginx"}, []string{})
}
