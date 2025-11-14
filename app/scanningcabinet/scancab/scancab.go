/*
Copyright 2017 The Perkeep Authors.

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

// The scancab tool scans document and uploads them to the scanning cabinet
// Perkeep application.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/buildinfo"
)

var (
	flagVersion = flag.Bool("version", false, "Show version.")

	flagLoop    = flag.Bool("loop", false, "Loop forever, monitor the queue directory (~/scancab-queue), and upload.")
	flagADF     = flag.Bool("adf", false, "Scan using the automatic document feeder (with scanadf).")
	flagUpload  = flag.String("upload", "", "Upload this file, instead of using the queue directory.")
	flagNoQueue = flag.Bool("noqueue", false, "In (non-ADF) scanning mode, immediately upload instead of storing in the queue directory.")

	flagURL      = flag.String("url", "http://localhost:3179/scancab/", "Scanning cabinet app base URL.")
	flagUsername = flag.String("user", "camlistore", "HTTP Basic Auth username to access the scanning cabinet app.")
	flagPassword = flag.String("pass", "pass3179", "HTTP Basic Auth password to access the scanning cabinet app.")

	flagDevice  = flag.String("device", "", "Device name to pass to scanimage and scanadf.")
	flagDuplex  = flag.Bool("duplex", false, "Double-sided scanning. Implies -adf mode.")
	flagColor   = flag.Bool("color", false, "Color scanning.")
	flagLineart = flag.Bool("lineart", false, "Line art scanning.")
)

var (
	am       auth.AuthMode
	queueDir = path.Join(os.Getenv("HOME"), "scancab-queue")
	confDir  = path.Join(os.Getenv("HOME"), ".config", "scanningcabinet")
)

func usage() {
	pgName := os.Args[0]
	fmt.Fprint(os.Stderr, "\nUsage:\n")
	fmt.Fprint(os.Stderr, "(Load scanner full of documents)\n")
	fmt.Fprint(os.Stderr, pgName+" --loop # monitor ~/scancab-queue and uploads files in it\n")
	fmt.Fprint(os.Stderr, pgName+" --adf # start scanning and dumping image files to ~/scancab-queue\n")
	fmt.Fprint(os.Stderr, "\n")
	flag.PrintDefaults()
}

func usageAndDie(msg string) {
	fmt.Fprintf(os.Stderr, "%s\n", msg)
	usage()
	os.Exit(-1)
}

func getUploadURL() (string, error) {
	req, err := http.NewRequest("GET", *flagURL+"uploadurl", nil)
	if err != nil {
		return "", err
	}
	am.AddAuthHeader(req)
	cl := &http.Client{}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	uploadURL := string(body)
	if _, err := url.ParseRequestURI(uploadURL); err != nil {
		return "", fmt.Errorf("could not parse upload URL returned by server (%q): %v", uploadURL, err)
	}
	return uploadURL, nil
}

func uploadFile(filename string) error {
	uploadURL, err := getUploadURL()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	bw := multipart.NewWriter(&buf)
	fw, err := bw.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(fw, f); err != nil {
		return err
	}
	if err := bw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", uploadURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", bw.FormDataContentType())
	am.AddAuthHeader(req)
	_, err = (&http.Client{}).Do(req)
	return err
}

func uploadOne(filename string) {
	if _, err := os.Stat(filename); err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("File %v does not exist", filename)
		}
		log.Fatal(err)
	}
	fmt.Printf("Uploading %v ...\n", filename)
	if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		if err := uploadFile(filename); err != nil {
			log.Fatalf("Could not upload file %v: %v", filename, err)
		}
		return
	}
	var args []string
	var ext string
	if *flagColor {
		args = append(args, "-density", "300")
		ext = "jpg"
	} else {
		args = append(args, "-monochrome", "-density", "150", "-compress", "lzw")
		ext = "tiff"
	}
	cnt, err := pageCount(filename)
	if err != nil {
		log.Fatal(err)
	}
	tmpDir, err := os.MkdirTemp("", "scancabcli")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		// not using RemoveAll on purpose, so that it does not remove if some of the temp files are still in there
		os.Remove(tmpDir)
	}()
	for i := range cnt {
		fmt.Printf("	page %04d of %04d\n", i+1, cnt)
		converted := path.Join(tmpDir, fmt.Sprintf("page-%04d.%v", i+1, ext))
		pageArgs := append(args, fmt.Sprintf("%v[%d]", filename, i), converted)
		// TODO(mpl): how about using pdftk instead of convert, to get contents without rasterizing them ?
		cmd := exec.Command("convert", pageArgs...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Fatalf("could not convert page %d of %v: %v, %v", i, filename, err, string(out))
		}
		if err := uploadFile(converted); err != nil {
			log.Fatalf("Could not upload file %v: %v", converted, err)
		}
		if err := os.Remove(converted); err != nil {
			log.Printf("could not remove %v: %v", converted, err)
		}
	}
}

type imageNameByTime []struct {
	name     string
	nanoTime int
}

func (a imageNameByTime) Len() int           { return len(a) }
func (a imageNameByTime) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a imageNameByTime) Less(i, j int) bool { return a[i].nanoTime < a[j].nanoTime }

func uploadLoop() {
	toUploadPattern := regexp.MustCompile(`^image-.+-unx(\d+)\.(png|jpg)$`)
	if err := os.Chdir(queueDir); err != nil {
		log.Fatalf("Could not chdir to queue directory %v: %v", queueDir, err)
	}
	for {
		dir, err := os.Open(".")
		if err != nil {
			log.Fatalf("Could not open queue directory: %v", err)
		}
		names, err := dir.Readdirnames(-1)
		if err != nil {
			log.Fatalf("Could not read queue directory: %v", err)
		}
		dir.Close()
		var sortedNames imageNameByTime
		for _, v := range names {
			m := toUploadPattern.FindStringSubmatch(v)
			if len(m) > 2 {
				t, err := strconv.Atoi(m[1])
				if err != nil {
					log.Printf("unexpected pattern %q in filename, wanted unixnano time", m[1])
					continue
				}
				sortedNames = append(sortedNames, struct {
					name     string
					nanoTime int
				}{
					name:     v,
					nanoTime: t,
				})
			}
		}
		sort.Sort(sortedNames)
		for _, v := range sortedNames {
			if err := uploadFile(v.name); err != nil {
				log.Print("Upload error. Sleeping for 5 seconds...")
				break
			}
			if err := os.Remove(v.name); err != nil {
				log.Fatalf("Could not remove %v: %v", v.name, err)
			}

		}
		fmt.Println("Uploads complete. Waiting for new files.")
		time.Sleep(5 * time.Second)
	}
}

func batchScanHelper(img string) {
	imagennnPattern := regexp.MustCompile(`\bimage-\d\d\d\d$`)
	scanFormat := os.Getenv("SCAN_FORMAT")
	if scanFormat == "" {
		log.Fatal("no SCAN_FORMAT")
	}
	filebase := imagennnPattern.FindString(img)
	if filebase == "" {
		log.Fatal("Expected first argument to be image-nnnn")
	}
	pidfi, err := os.Lstat("/proc/self")
	if err != nil {
		log.Fatalf("Could not get self pid through /proc/self: %v", err)
	}
	pid := pidfi.Name()
	fmt.Printf("[%s] Got format: %v for %v", pid, scanFormat, filebase)

	ext := "jpg"
	if os.Getenv("SCAN_LINEART") != "" {
		ext = "png"
	}

	destFile := path.Join(queueDir, fmt.Sprintf("%v-unx%d.%v", filebase, time.Now().UnixNano(), ext))
	cmd := exec.Command("convert", "-quality", "95", img, destFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Failed to convert %v: %v, %v", destFile, err, string(out))
	}
	if err := os.Remove(img); err != nil {
		log.Fatalf("Could not remove %v: %v", img, err)
	}
}

func scan() {
	imagennnPattern := regexp.MustCompile(`^image-(\d\d\d\d)(\.tiff|\.jpg|\.png)?$`)
	di, err := os.Open(".")
	if err != nil {
		log.Fatal(err)
	}
	names, err := di.Readdirnames(-1)
	if err != nil {
		log.Fatal(err)
	}
	// TODO(mpl): I did that part a bit differently from the original code,
	// so extra testing will be needed.
	var sortedNames []string
	for _, name := range names {
		if imagennnPattern.MatchString(name) {
			sortedNames = append(sortedNames, name)
		}
	}
	sort.Strings(sortedNames)
	lastOne := sortedNames[len(sortedNames)-1]
	m := imagennnPattern.FindStringSubmatch(lastOne)
	if len(m) < 2 {
		panic(fmt.Sprintf("matched scan %q does not actually match after. probably wrong regexp.", lastOne))
	}
	lastPage, err := strconv.Atoi(m[1])
	if err != nil {
		log.Fatal(err)
	}
	lastPage++

	mode := "Gray"
	if *flagLineart {
		mode = "Lineart"
	} else if *flagColor {
		mode = "Color"
	}

	if *flagADF {
		args := []string{"-d", *flagDevice, "--mode", mode, "--resolution", "300"}
		if *flagDuplex {
			args = append(args, `--source="ADF Duplex"`)
		}
		args = append(args, "--scan-script", flag.Args()[0], "-s", fmt.Sprintf("%d", lastPage))
		cmd := exec.Command("scanadf", args...)
		env := os.Environ()
		if *flagLineart {
			env = append(env, "SCAN_LINEART=1")
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Fatalf("Failed to run scanadf: %v, %v", err, string(out))
		}
		return
	}

	baseName := fmt.Sprintf("image-%04d", lastPage)
	tiffName := baseName + ".tiff"
	ext := "jpg"
	imgName := baseName + ".jpg"
	if *flagLineart {
		ext = "png"
		imgName = baseName + ".png"
	}
	f, err := os.Create(tiffName)
	if err != nil {
		log.Fatal(err)
	}
	closedYet := false
	defer func() {
		if closedYet {
			return
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}()
	cmd := exec.Command("scanimage", "-d", *flagDevice, "--mode", mode, "--resolution", "300", "--format", "tiff")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start scanimage: %v", err)
	}
	if _, err := io.Copy(f, stdout); err != nil {
		log.Fatalf("Failed to write to %v: %v", tiffName, err)
	}
	if err := cmd.Wait(); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	closedYet = true
	fmt.Printf("Scanned. Converting %v -> %v\n", tiffName, imgName)

	cmd = exec.Command("convert", "-quality", "90", tiffName, imgName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Failed to run convert: %v, %v", err, string(out))
	}
	if err := os.Remove(tiffName); err != nil {
		log.Fatal(err)
	}
	if *flagNoQueue {
		if err := uploadFile(imgName); err != nil {
			log.Fatalf("Could not upload file %v: %v", imgName, err)
		}
		if err := os.Remove(imgName); err != nil {
			log.Fatal(err)
		}
		return
	}
	// TODO(mpl): original code had
	// my $qfile = "$whatever/$out-unx" . time() . substr($out, -4);
	// which would yield something like
	// queuedir/image-0007.png-unx1443798056.png
	// so I assumed there was a typo and we don't want the extension right in the middle of the name.
	queuedName := path.Join(queueDir, fmt.Sprintf("%v-unx%d.%v", baseName, time.Now().UnixNano(), ext))
	fmt.Printf("Moving file from %v to %v\n", imgName, queuedName)
	if err := os.Rename(imgName, queuedName); err != nil {
		log.Fatal(err)
	}
}

func checkSanity() {
	flag.Usage = usage
	if *flagColor && *flagLineart {
		usageAndDie("-color and -lineart are mutually exclusive")
	}
	if *flagDuplex {
		*flagADF = true
	}
	if *flagLoop && *flagUpload != "" {
		usageAndDie("-loop and -upload are mutually exclusive")
	}

	if !*flagLoop && *flagUpload == "" {
		if *flagDevice == "" {
			deviceFile := path.Join(confDir, "device")
			device, err := os.ReadFile(deviceFile)
			if err != nil {
				log.Printf("error reading device conf file %v: %v", deviceFile, err)
				usageAndDie(fmt.Sprintf("Please specify your scanning device with the -device option, or in %v", deviceFile))
			}
			*flagDevice = string(device)
		}
	}

	if *flagUpload == "" && !*flagNoQueue {
		if _, err := os.Stat(queueDir); err != nil {
			if !os.IsNotExist(err) {
				log.Fatal(err)
			}
			log.Fatalf("Queue directory %v doesn't exist; please create it", queueDir)
		}
	}

	if !*flagADF {
		if *flagURL == "" {
			urlFile := path.Join(confDir, "url")
			URL, err := os.ReadFile(urlFile)
			if err != nil {
				log.Printf("error reading url file %v: %v", urlFile, err)
				usageAndDie(fmt.Sprintf("Please specify the scanning cabinet app URL with the -url option, or in %v", urlFile))
			}
			*flagURL = string(URL)
		}
		if *flagUsername == "" {
			userFile := path.Join(confDir, "user")
			username, err := os.ReadFile(userFile)
			if err != nil {
				log.Printf("error reading username file %v: %v", userFile, err)
				usageAndDie(fmt.Sprintf("Please specify your username with the -user option, or in %v", userFile))
			}
			*flagUsername = string(username)
		}
		if *flagPassword == "" {
			passFile := path.Join(confDir, "password")
			password, err := os.ReadFile(passFile)
			if err != nil {
				log.Printf("error reading password file %v: %v", passFile, err)
				usageAndDie(fmt.Sprintf("Please specify your password with the -pass option, or in %v", passFile))
			}
			*flagPassword = string(password)
		}
	}

}

func main() {
	flag.Parse()

	checkSanity()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "scancab version: %s\n", buildinfo.Summary())
		return
	}

	if os.Getenv("SCAN_RES") != "" || os.Getenv("SCAN_FORMAT_ID") != "" {
		batchScanHelper(flag.Args()[0])
		os.Exit(0)
	}

	am = auth.NewBasicAuth(*flagUsername, *flagPassword)

	if *flagLoop {
		uploadLoop()
		os.Exit(0)
	}

	if *flagUpload != "" {
		uploadOne(*flagUpload)
		os.Exit(0)
	}

	scan()
}
