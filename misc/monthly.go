/*
Copyright 2016 The Camlistore Authors

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

// Command monthly builds the tarballs and zip archives for all the monthly
// released Camlistore downloads. That is: source zip, linux and darwin tarballs,
// and windows zip. These files are then uploaded to the dedicated repository, as
// well as a file with their checksum, for each of them. Finally, the template page
// to serve these downloads with camweb is generated.
package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/osutil"

	"cloud.google.com/go/storage"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

var (
	flagRev     = flag.String("rev", "", "Camlistore revision to build (tag or commit hash). For development purposes, you can instead specify the path to a local Camlistore source tree from which to build, with the form \"WIP:/path/to/dir\".")
	flagUpload  = flag.Bool("upload", true, "Upload all the generated tarballs and zip archives.")
	flagSkipGen = flag.Bool("skipgen", false, "Do not recreate the release tarballs, and directly use the ones found in camlistore.org/misc/docker/release. Use -upload=true and -skipgen=true to only generate the monthly release page.")
	// TODO(mpl): make sanity run the tests too, once they're more reliable.
	flagSanity = flag.Bool("sanity", true, "Verify 'go run make.go' succeeds when building the source tarball. Abort everything if not.")
)

var camDir string

const (
	project = "camlistore-website"
	bucket  = "camlistore-release"
)

func isWIP() bool {
	return strings.HasPrefix(*flagRev, "WIP")
}

func rev() string {
	if isWIP() {
		return "WORKINPROGRESS"
	}
	return (*flagRev)[0:10]
}

// genDownloads creates all the zips and tarballs, and uploads them.
func genDownloads() error {
	dockDotGo := filepath.Join(camDir, "misc", "docker", "dock.go")
	releaseDir := filepath.Join(camDir, "misc", "docker", "release")
	var wg sync.WaitGroup
	if !*flagSkipGen {
		// Gen the source zip:
		args := []string{
			"run",
			dockDotGo,
			"-build_image=false",
			"-zip_source=true",
			"-rev=" + *flagRev,
			"-sanity=" + fmt.Sprintf("%t", *flagSanity),
		}
		cmd := exec.Command("go", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		upload(filepath.Join(releaseDir, "camlistore-src.zip"))
	}()

	// gen the binaries tarballs:
	for _, platform := range []string{"linux", "darwin", "windows"} {
		if !*flagSkipGen {
			args := []string{
				"run",
				dockDotGo,
				"-build_image=false",
				"-build_release=true",
				"-rev=" + *flagRev,
				"-os=" + platform,
			}
			cmd := exec.Command("go", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return err
			}
		}
		wg.Add(1)
		go func(osType string) {
			defer wg.Done()
			filename := "camlistore-" + osType + ".tar.gz"
			if osType == "windows" {
				filename = strings.Replace(filename, ".tar.gz", ".zip", 1)
			}
			upload(filepath.Join(releaseDir, filename))
		}(platform)
	}
	wg.Wait()
	return nil
}

func upload(srcPath string) {
	if !*flagUpload {
		return
	}
	destName := strings.Replace(filepath.Base(srcPath), "camlistore", "camlistore-"+rev(), 1)
	versionedTarball := "monthly/" + destName

	log.Printf("Uploading %s/%s ...", bucket, versionedTarball)

	ts, err := tokenSource(bucket)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	stoClient, err := storage.NewClient(ctx, option.WithTokenSource(ts), option.WithHTTPClient(oauth2.NewClient(ctx, ts)))
	if err != nil {
		log.Fatal(err)
	}
	w := stoClient.Bucket(bucket).Object(versionedTarball).NewWriter(ctx)
	w.ACL = publicACL(project)
	w.CacheControl = "no-cache" // TODO: remove for non-tip releases? set expirations?
	contentType := "application/x-gtar"
	if strings.HasSuffix(versionedTarball, ".zip") {
		contentType = "application/zip"
	}
	w.ContentType = contentType
	csw := sha256.New()
	mw := io.MultiWriter(w, csw)

	src, err := os.Open(srcPath)
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()

	if _, err := io.Copy(mw, src); err != nil {
		log.Fatalf("io.Copy: %v", err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
	log.Printf("Uploaded monthly tarball to %s", versionedTarball)

	// And upload the corresponding checksum
	checkSumFile := versionedTarball + ".sha256"
	sum := fmt.Sprintf("%x", csw.Sum(nil))
	w = stoClient.Bucket(bucket).Object(checkSumFile).NewWriter(ctx)
	w.ACL = publicACL(project)
	w.CacheControl = "no-cache" // TODO: remove for non-tip releases? set expirations?
	w.ContentType = "text/plain"
	if _, err := io.Copy(w, strings.NewReader(sum)); err != nil {
		log.Fatalf("error uploading checksum %v: %v", checkSumFile, err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
	log.Printf("Uploaded monthly tarball checksum to %s", checkSumFile)
}

type DownloadData struct {
	Filename string
	Platform string
	Checksum string
}

type ReleaseData struct {
	Date     string
	Download []DownloadData
}

// Note: the space trimming in the range loop is important. Since all of our
// html still goes through a markdown engine, newlines in between items would make
// markdown wrap the items in <p></p>, which breaks the page's style.
var monthlyTemplate = `
<h1>Monthly Release: {{.Date}}</h1>

<h2>Downloads</h2>

<center>
{{- range $d := .Download -}}
<a class="downloadBox" href="/dl/monthly/{{$d.Filename}}">
<div class="platform">{{$d.Platform}}</div>
<div>
	<span class="filename">{{$d.Filename}}</span>
</div>
<div class="checksum">SHA256: {{$d.Checksum}}</div>
</a>
{{- end -}}
</center>
`

// listDownloads lists all the files found in the monthly repo, and from them,
// builds the data that we'll feed to the template to generate the monthly
// downloads camweb page.
func listDownloads() (*ReleaseData, error) {
	ts, err := tokenSource(bucket)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	stoClient, err := storage.NewClient(ctx, option.WithTokenSource(ts), option.WithHTTPClient(oauth2.NewClient(ctx, ts)))
	if err != nil {
		return nil, err
	}
	objList, err := stoClient.Bucket(bucket).List(ctx, &storage.Query{Prefix: "monthly/"})
	if err != nil {
		return nil, err
	}

	platformBySuffix := map[string]string{
		"src.zip":       "Source",
		"linux.tar.gz":  "Linux",
		"darwin.tar.gz": "Darwin",
		"windows.zip":   "Windows",
	}
	getPlatform := func(name string) string {
		for suffix, platform := range platformBySuffix {
			if strings.HasSuffix(name, suffix) {
				return platform
			}
		}
		return ""
	}
	getChecksum := func(name string) (string, error) {
		r, err := stoClient.Bucket(bucket).Object(name).NewReader(ctx)
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	var date time.Time
	checkDate := func(objDate time.Time) error {
		if date.IsZero() {
			date = objDate
			return nil
		}
		d := date.Sub(objDate)
		if d < 0 {
			d = -d
		}
		if d < 24*time.Hour {
			return nil
		}
		return fmt.Errorf("objects in monthly have not been uploaded or updated the same day")
	}

	var (
		downloadData []DownloadData
		nameToSum    = make(map[string]string)
	)
	for _, attrs := range objList.Results {
		if !strings.Contains(attrs.Name, rev()) {
			continue
		}
		if err := checkDate(attrs.Updated); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(attrs.Name, ".sha256") {
			continue
		}
		sum, err := getChecksum(attrs.Name)
		if err != nil {
			return nil, err
		}
		nameToSum[strings.TrimSuffix(attrs.Name, ".sha256")] = sum
	}
	for _, attrs := range objList.Results {
		if !strings.Contains(attrs.Name, rev()) {
			continue
		}
		if strings.HasSuffix(attrs.Name, ".sha256") {
			continue
		}
		sum, ok := nameToSum[attrs.Name]
		if !ok {
			return nil, fmt.Errorf("%v has no checksum file!", attrs.Name)
		}
		downloadData = append(downloadData, DownloadData{
			Filename: filepath.Base(attrs.Name),
			Platform: getPlatform(attrs.Name),
			Checksum: sum,
		})
	}

	return &ReleaseData{
		Date:     date.Format("2006-01-02"),
		Download: downloadData,
	}, nil
}

func genMonthlyPage(releaseData *ReleaseData) error {
	tpl, err := template.New("monthly").Parse(monthlyTemplate)
	if err != nil {
		return fmt.Errorf("could not parse template: %v", err)
	}

	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "monthly", releaseData); err != nil {
		return fmt.Errorf("could not execute template: %v", err)
	}

	monthlyDocDir := filepath.Join(camDir, filepath.FromSlash("doc/release"))
	if err := os.MkdirAll(monthlyDocDir, 0755); err != nil {
		return err
	}
	monthlyDocPage := filepath.Join(monthlyDocDir, "monthly.html")
	if err := ioutil.WriteFile(monthlyDocPage, buf.Bytes(), 0700); err != nil {
		return fmt.Errorf("could not write template to file %v: %v", monthlyDocPage, err)
	}
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s [-rev camlistore_revision | -rev WIP:/path/to/camli/source]\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(1)
}

func checkFlags() {
	if flag.NArg() != 0 {
		usage()
	}
	if *flagRev == "" {
		fmt.Fprintf(os.Stderr, "Usage error: --rev is required.\n")
		usage()
	}
}

func main() {
	flag.Usage = usage
	flag.Parse()
	checkFlags()

	var err error
	camDir, err = osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatalf("Error looking up camlistore.org dir: %v", err)
	}

	if err := genDownloads(); err != nil {
		log.Fatal(err)
	}

	releaseData, err := listDownloads()
	if err != nil {
		log.Fatal(err)
	}

	if err := genMonthlyPage(releaseData); err != nil {
		log.Fatal(err)
	}
}

// TODO(mpl): refactor in a common place so that dock.go and this program here can use the helpers below.

func homedir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
	}
	return os.Getenv("HOME")
}

// ProjectTokenSource returns an OAuth2 TokenSource for the given Google Project ID.
func ProjectTokenSource(proj string, scopes ...string) (oauth2.TokenSource, error) {
	// TODO(bradfitz): try different strategies too, like
	// three-legged flow if the service account doesn't exist, and
	// then cache the token file on disk somewhere. Or maybe that should be an
	// option, for environments without stdin/stdout available to the user.
	// We'll figure it out as needed.
	fileName := filepath.Join(homedir(), "keys", proj+".key.json")
	jsonConf, err := ioutil.ReadFile(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Missing JSON key configuration. Download the Service Account JSON key from https://console.developers.google.com/project/%s/apiui/credential and place it at %s", proj, fileName)
		}
		return nil, err
	}
	conf, err := google.JWTConfigFromJSON(jsonConf, scopes...)
	if err != nil {
		return nil, fmt.Errorf("reading JSON config from %s: %v", fileName, err)
	}
	return conf.TokenSource(oauth2.NoContext), nil
}

var bucketProject = map[string]string{
	"camlistore-release": "camlistore-website",
}

func tokenSource(bucket string) (oauth2.TokenSource, error) {
	proj, ok := bucketProject[bucket]
	if !ok {
		return nil, fmt.Errorf("unknown project for bucket %q", bucket)
	}
	return ProjectTokenSource(proj, storage.ScopeReadWrite)
}

func publicACL(proj string) []storage.ACLRule {
	return []storage.ACLRule{
		// If you don't give the owners access, the web UI seems to
		// have a bug and doesn't have access to see that it's public, so
		// won't render the "Shared Publicly" link. So we do that, even
		// though it's dumb and unnecessary otherwise:
		{Entity: storage.ACLEntity("project-owners-" + proj), Role: storage.RoleOwner},
		{Entity: storage.AllUsers, Role: storage.RoleReader},
	}
}
