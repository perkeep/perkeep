// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package webdav

import (
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// PropSystem manages the properties of named resources. It allows finding
// and setting properties as defined in RFC 4918.
//
// The elements in a resource name are separated by slash ('/', U+002F)
// characters, regardless of host operating system convention.
type PropSystem interface {
	// Find returns the status of properties named propnames for resource name.
	//
	// Each Propstat must have a unique status and each property name must
	// only be part of one Propstat element.
	Find(name string, propnames []xml.Name) ([]Propstat, error)

	// TODO(rost) PROPPATCH.
	// TODO(nigeltao) merge Find and Allprop?

	// Allprop returns the properties defined for resource name and the
	// properties named in include. The returned Propstats are handled
	// as in Find.
	//
	// Note that RFC 4918 defines 'allprop' to return the DAV: properties
	// defined within the RFC plus dead properties. Other live properties
	// should only be returned if they are named in 'include'.
	//
	// See http://www.webdav.org/specs/rfc4918.html#METHOD_PROPFIND
	Allprop(name string, include []xml.Name) ([]Propstat, error)

	// Propnames returns the property names defined for resource name.
	Propnames(name string) ([]xml.Name, error)

	// TODO(rost) COPY/MOVE/DELETE.
}

// Propstat describes a XML propstat element as defined in RFC 4918.
// See http://www.webdav.org/specs/rfc4918.html#ELEMENT_propstat
type Propstat struct {
	// Props contains the properties for which Status applies.
	Props []Property

	// Status defines the HTTP status code of the properties in Prop.
	// Allowed values include, but are not limited to the WebDAV status
	// code extensions for HTTP/1.1.
	// http://www.webdav.org/specs/rfc4918.html#status.code.extensions.to.http11
	Status int

	// XMLError contains the XML representation of the optional error element.
	// XML content within this field must not rely on any predefined
	// namespace declarations or prefixes. If empty, the XML error element
	// is omitted.
	XMLError string

	// ResponseDescription contains the contents of the optional
	// responsedescription field. If empty, the XML element is omitted.
	ResponseDescription string
}

// memPS implements an in-memory PropSystem. It supports all of the mandatory
// live properties of RFC 4918.
type memPS struct {
	// TODO(rost) memPS will get writeable in the next CLs.
	fs FileSystem
	ls LockSystem
}

// NewMemPS returns a new in-memory PropSystem implementation.
func NewMemPS(fs FileSystem, ls LockSystem) PropSystem {
	return &memPS{fs: fs, ls: ls}
}

// davProps contains all supported DAV: properties and their optional
// propfind functions. A nil findFn indicates a hidden, protected property.
// The dir field indicates if the property applies to directories in addition
// to regular files.
var davProps = map[xml.Name]struct {
	findFn func(*memPS, string, os.FileInfo) (string, error)
	dir    bool
}{
	xml.Name{Space: "DAV:", Local: "resourcetype"}: {
		findFn: (*memPS).findResourceType,
		dir:    true,
	},
	xml.Name{Space: "DAV:", Local: "displayname"}: {
		findFn: (*memPS).findDisplayName,
		dir:    true,
	},
	xml.Name{Space: "DAV:", Local: "getcontentlength"}: {
		findFn: (*memPS).findContentLength,
		dir:    true,
	},
	xml.Name{Space: "DAV:", Local: "getlastmodified"}: {
		findFn: (*memPS).findLastModified,
		dir:    true,
	},
	xml.Name{Space: "DAV:", Local: "creationdate"}: {
		findFn: nil,
		dir:    true,
	},
	xml.Name{Space: "DAV:", Local: "getcontentlanguage"}: {
		findFn: nil,
		dir:    true,
	},
	xml.Name{Space: "DAV:", Local: "getcontenttype"}: {
		findFn: (*memPS).findContentType,
		dir:    true,
	},
	// memPS implements ETag as the concatenated hex values of a file's
	// modification time and size. This is not a reliable synchronization
	// mechanism for directories, so we do not advertise getetag for
	// DAV collections.
	xml.Name{Space: "DAV:", Local: "getetag"}: {
		findFn: (*memPS).findETag,
		dir:    false,
	},

	// TODO(nigeltao) Lock properties will be defined later.
	// xml.Name{Space: "DAV:", Local: "lockdiscovery"}
	// xml.Name{Space: "DAV:", Local: "supportedlock"}
}

func (ps *memPS) Find(name string, propnames []xml.Name) ([]Propstat, error) {
	fi, err := ps.fs.Stat(name)
	if err != nil {
		return nil, err
	}

	pm := make(map[int]Propstat)
	for _, pn := range propnames {
		p := Property{XMLName: pn}
		s := http.StatusNotFound
		if prop := davProps[pn]; prop.findFn != nil && (prop.dir || !fi.IsDir()) {
			xmlvalue, err := prop.findFn(ps, name, fi)
			if err != nil {
				return nil, err
			}
			s = http.StatusOK
			p.InnerXML = []byte(xmlvalue)
		}
		pstat := pm[s]
		pstat.Props = append(pstat.Props, p)
		pm[s] = pstat
	}

	pstats := make([]Propstat, 0, len(pm))
	for s, pstat := range pm {
		pstat.Status = s
		pstats = append(pstats, pstat)
	}
	return pstats, nil
}

func (ps *memPS) Propnames(name string) ([]xml.Name, error) {
	fi, err := ps.fs.Stat(name)
	if err != nil {
		return nil, err
	}
	propnames := make([]xml.Name, 0, len(davProps))
	for pn, prop := range davProps {
		if prop.findFn != nil && (prop.dir || !fi.IsDir()) {
			propnames = append(propnames, pn)
		}
	}
	return propnames, nil
}

func (ps *memPS) Allprop(name string, include []xml.Name) ([]Propstat, error) {
	propnames, err := ps.Propnames(name)
	if err != nil {
		return nil, err
	}
	// Add names from include if they are not already covered in propnames.
	nameset := make(map[xml.Name]bool)
	for _, pn := range propnames {
		nameset[pn] = true
	}
	for _, pn := range include {
		if !nameset[pn] {
			propnames = append(propnames, pn)
		}
	}
	return ps.Find(name, propnames)
}

func (ps *memPS) findResourceType(name string, fi os.FileInfo) (string, error) {
	if fi.IsDir() {
		return `<collection xmlns="DAV:"/>`, nil
	}
	return "", nil
}

func (ps *memPS) findDisplayName(name string, fi os.FileInfo) (string, error) {
	if slashClean(name) == "/" {
		// Hide the real name of a possibly prefixed root directory.
		return "", nil
	}
	return fi.Name(), nil
}

func (ps *memPS) findContentLength(name string, fi os.FileInfo) (string, error) {
	return strconv.FormatInt(fi.Size(), 10), nil
}

func (ps *memPS) findLastModified(name string, fi os.FileInfo) (string, error) {
	return fi.ModTime().Format(http.TimeFormat), nil
}

func (ps *memPS) findContentType(name string, fi os.FileInfo) (string, error) {
	f, err := ps.fs.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// This implementation is based on serveContent's code in the standard net/http package.
	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype == "" {
		// Read a chunk to decide between utf-8 text and binary.
		var buf [512]byte
		n, _ := io.ReadFull(f, buf[:])
		ctype = http.DetectContentType(buf[:n])
		// Rewind file.
		_, err = f.Seek(0, os.SEEK_SET)
	}
	return ctype, err
}

func (ps *memPS) findETag(name string, fi os.FileInfo) (string, error) {
	return detectETag(fi), nil
}

// detectETag determines the ETag for the file described by fi.
func detectETag(fi os.FileInfo) string {
	// The Apache http 2.4 web server by default concatenates the
	// modification time and size of a file. We replicate the heuristic
	// with nanosecond granularity.
	return fmt.Sprintf(`"%x%x"`, fi.ModTime().UnixNano(), fi.Size())
}
