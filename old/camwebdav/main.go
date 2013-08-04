// +build THIS_IS_BROKEN

// The camwebdav binary is a WebDAV server to expose Camlistore as a
// filesystem that can be mounted from Windows (or other operating
// systems).
//
// It is currently broken and needs to be updated to use
// camlistore.org/pkg/fs.
package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/cacher"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/fs"

//	"camlistore.org/third_party/github.com/hanwen/go-fuse/fuse"
)

var (
	f       *fs.CamliFileSystem
	davaddr = flag.String("davaddr", "", "WebDAV service address")
)

// TODO(rh): tame copy/paste code from cammount
func main() {
	client.AddFlags()
	flag.Parse()
	cacheDir, err := ioutil.TempDir("", "camlicache")
	if err != nil {
		log.Fatalf("Error creating temp cache directory: %v", err)
	}
	defer os.RemoveAll(cacheDir)
	diskcache, err := localdisk.New(cacheDir)
	if err != nil {
		log.Fatalf("Error setting up local disk cache: %v", err)
	}
	if flag.NArg() != 1 {
		log.Fatal("usage: camwebdav <blobref>")
	}
	br := blobref.Parse(flag.Arg(0))
	if br == nil {
		log.Fatalf("%s was not a valid blobref.", flag.Arg(0))
	}
	client := client.NewOrFail()
	fetcher := cacher.NewCachingFetcher(diskcache, client)

	f = fs.NewCamliFileSystem(fetcher, br)
	http.HandleFunc("/", webdav)
	err = http.ListenAndServe(*davaddr, nil)
	if err != nil {
		log.Fatalf("Error starting WebDAV server: %v", err)
	}
}

func webdav(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		get(w, r)
	case "OPTIONS":
		w.Header().Set("DAV", "1")
	case "PROPFIND":
		propfind(w, r)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

// sendHTTPStatus is an HTTP status code.
type sendHTTPStatus int

// senderr, when deferred, recovers panics of
// type sendHTTPStatus and writes the corresponding
// HTTP status to the response.  If the value is not
// of type sendHTTPStatus, it re-panics.
func senderr(w http.ResponseWriter) {
	err := recover()
	if stat, ok := err.(sendHTTPStatus); ok {
		w.WriteHeader(int(stat))
	} else if err != nil {
		panic(err)
	}
}

// GET Method
func get(w http.ResponseWriter, r *http.Request) {
	defer senderr(w)
	file, stat := f.Open(url2path(r.URL), uint32(os.O_RDONLY))

	checkerr(stat)

	w.Header().Set("Content-Type", "application/octet-stream")
	ff, err := file.(*fs.CamliFile).GetReader()
	_, err = io.Copy(w, ff)
	if err != nil {
		log.Print("propfind: error writing response: %s", err)
	}
}

// 9.1 PROPFIND Method
func propfind(w http.ResponseWriter, r *http.Request) {
	defer senderr(w)
	depth := r.Header.Get("Depth")
	switch depth {
	case "0", "1":
	case /*implicit infinity*/ "", "infinity":
		log.Print("propfind: unsupported depth of infinity")
		panic(sendHTTPStatus(http.StatusForbidden))
	default:
		log.Print("propfind: invalid Depth of", depth)
		panic(sendHTTPStatus(http.StatusBadRequest))
	}

	// TODO(rh) Empty body == allprop

	// TODO(rh): allprop
	var propsToFind []string

	x := parsexml(r.Body)
	x.muststart("propfind")
	switch {
	case x.start("propname"):
		x.mustend("propname")
	case x.start("allprop"):
		x.mustend("allprop")
		if x.start("include") {
			// TODO(rh) parse elements
			x.mustend("include")
		}
	case x.start("prop"):
		propsToFind = parseprop(x)
		x.mustend("prop")
	}
	x.mustend("propfind")
	var files = []string{url2path(r.URL)}
	if depth == "1" {
		// TODO(rh) handle bad stat
		files = append(files, ls(files[0])...)
	}

	var ms multistatus
	for _, file := range files {
		resp := &response{href: (*href)(path2url(file))}
		attr, stat := f.GetAttr(file) // TODO(rh) better way?

		checkerr(stat)

		var props []xmler
		for _, p := range propsToFind {
			switch p {
			case "creationdate":
				props = append(props, creationdate(attr.Ctime))
			case "resourcetype":
				props = append(props, resourcetype(attr.Mode&fuse.S_IFDIR == fuse.S_IFDIR))
			case "getcontentlength":
				props = append(props, getcontentlength(attr.Size))
			case "getlastmodified":
				props = append(props, getlastmodified(attr.Mtime))
			}

			resp.body = propstats{{props, 200}}
		}
		ms = append(ms, resp)
	}

	var xmlbytes bytes.Buffer
	ms.XML(&xmlbytes)
	w.Header().Set("Content-Type", "application/xml; charset=UTF-8")
	w.WriteHeader(207) // 207 Multi-Status
	_, err := io.Copy(w, &xmlbytes)
	if err != nil {
		log.Print("propfind: error writing response: %s", err)
	}
}

func checkerr(stat fuse.Status) {
	switch stat {
	case fuse.ENOENT:
		panic(sendHTTPStatus(http.StatusNotFound))
	case fuse.OK:
	default:
		panic(sendHTTPStatus(http.StatusForbidden))
	}
}

func ls(path string) (paths []string) {
	dirs, err := f.OpenDir(path)

	checkerr(err)

	for d := range dirs {
		// TODO(rh) determine a proper way to join paths
		if path != "" {
			d.Name = path + "/" + d.Name
		}
		paths = append(paths, d.Name)
	}
	return
}

// TODO(rh) settle on an internal format for paths, and a better way to translate between paths and URLs
func url2path(url_ *url.URL) string {
	return strings.Trim(url_.Path, "/") // TODO(rh) make not suck
}

func path2url(path string) *url.URL {
	return &url.URL{Path: "/" + path} // TODO(rh) make not suck
}

func parseprop(x *xmlparser) (props []string) {
	for {
		el, ok := x.cur.(xml.StartElement)
		if !ok {
			break
		}
		props = append(props, el.Name.Local)
		x.muststart(el.Name.Local)
		x.mustend(el.Name.Local)
	}
	return
}
