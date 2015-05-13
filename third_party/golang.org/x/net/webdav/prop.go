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
	"sync"
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

	// Patch patches the properties of resource name.
	//
	// If all patches can be applied without conflict, Patch returns a slice
	// of length one and a Propstat element of status 200, naming all patched
	// properties. In case of conflict, Patch returns an arbitrary long slice
	// and no Propstat element must have status 200. In either case, properties
	// in Propstat must not have values.
	//
	// Note that the WebDAV RFC requires either all patches to succeed or none.
	Patch(name string, patches []Proppatch) ([]Propstat, error)

	// TODO(rost) COPY/MOVE/DELETE.
}

// Proppatch describes a property update instruction as defined in RFC 4918.
// See http://www.webdav.org/specs/rfc4918.html#METHOD_PROPPATCH
type Proppatch struct {
	// Remove specifies whether this patch removes properties. If it does not
	// remove them, it sets them.
	Remove bool
	// Props contains the properties to be set or removed.
	Props []Property
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
	fs FileSystem
	ls LockSystem
	m  Mutability

	mu    sync.RWMutex
	nodes map[string]*memPSNode
}

// memPSNode stores the dead properties of a resource.
type memPSNode struct {
	mu        sync.RWMutex
	deadProps map[xml.Name]Property
}

// BUG(rost): In this development version, the in-memory property system does
// not handle COPY/MOVE/DELETE requests. As a result, dead properties are not
// released if the according DAV resource is deleted or moved. It is not
// recommended to use a read-writeable property system in production.

// Mutability indicates the mutability of a property system.
type Mutability bool

const (
	ReadOnly  = Mutability(false)
	ReadWrite = Mutability(true)
)

// NewMemPS returns a new in-memory PropSystem implementation. A read-only
// property system rejects all patches. A read-writeable property system
// stores arbitrary properties but refuses to change any DAV: property
// specified in RFC 4918. It imposes no limit on the size of property values.
func NewMemPS(fs FileSystem, ls LockSystem, m Mutability) PropSystem {
	return &memPS{
		fs:    fs,
		ls:    ls,
		m:     m,
		nodes: make(map[string]*memPSNode),
	}
}

// liveProps contains all supported, protected DAV: properties.
var liveProps = map[xml.Name]struct {
	// findFn implements the propfind function of this property. If nil,
	// it indicates a hidden property.
	findFn func(*memPS, string, os.FileInfo) (string, error)
	// dir is true if the property applies to directories.
	dir bool
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
	xml.Name{Space: "DAV:", Local: "getetag"}: {
		findFn: (*memPS).findETag,
		// memPS implements ETag as the concatenated hex values of a file's
		// modification time and size. This is not a reliable synchronization
		// mechanism for directories, so we do not advertise getetag for
		// DAV collections.
		dir: false,
	},

	// TODO(nigeltao) Lock properties will be defined later.
	xml.Name{Space: "DAV:", Local: "lockdiscovery"}: {},
	xml.Name{Space: "DAV:", Local: "supportedlock"}: {},
}

func (ps *memPS) Find(name string, propnames []xml.Name) ([]Propstat, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	fi, err := ps.fs.Stat(name)
	if err != nil {
		return nil, err
	}

	// Lookup the dead properties of this resource. It's OK if there are none.
	n, ok := ps.nodes[name]
	if ok {
		n.mu.RLock()
		defer n.mu.RUnlock()
	}

	pm := make(map[int]Propstat)
	for _, pn := range propnames {
		// If this node has dead properties, check if they contain pn.
		if n != nil {
			if dp, ok := n.deadProps[pn]; ok {
				pstat := pm[http.StatusOK]
				pstat.Props = append(pstat.Props, dp)
				pm[http.StatusOK] = pstat
				continue
			}
		}
		// Otherwise, it must either be a live property or we don't know it.
		p := Property{XMLName: pn}
		s := http.StatusNotFound
		if prop := liveProps[pn]; prop.findFn != nil && (prop.dir || !fi.IsDir()) {
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

	propnames := make([]xml.Name, 0, len(liveProps))
	for pn, prop := range liveProps {
		if prop.findFn != nil && (prop.dir || !fi.IsDir()) {
			propnames = append(propnames, pn)
		}
	}

	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if n, ok := ps.nodes[name]; ok {
		n.mu.RLock()
		defer n.mu.RUnlock()
		for pn := range n.deadProps {
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

func (ps *memPS) Patch(name string, patches []Proppatch) ([]Propstat, error) {
	// A DELETE/COPY/MOVE might fly in, so we need to keep all nodes locked until
	// the end of this PROPPATCH.
	ps.mu.Lock()
	defer ps.mu.Unlock()
	n, ok := ps.nodes[name]
	if !ok {
		n = &memPSNode{deadProps: make(map[xml.Name]Property)}
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	_, err := ps.fs.Stat(name)
	if err != nil {
		return nil, err
	}

	// Perform a dry-run to identify any patch conflicts. A read-only property
	// system always fails at this stage.
	pm := make(map[int]Propstat)
	for _, patch := range patches {
		for _, p := range patch.Props {
			s := http.StatusOK
			if _, ok := liveProps[p.XMLName]; ok || ps.m == ReadOnly {
				s = http.StatusForbidden
			}
			pstat := pm[s]
			pstat.Props = append(pstat.Props, Property{XMLName: p.XMLName})
			pm[s] = pstat
		}
	}
	// Based on the dry-run, either apply the patches or handle conflicts.
	if _, ok = pm[http.StatusOK]; ok {
		if len(pm) == 1 {
			for _, patch := range patches {
				for _, p := range patch.Props {
					if patch.Remove {
						delete(n.deadProps, p.XMLName)
					} else {
						n.deadProps[p.XMLName] = p
					}
				}
			}
			ps.nodes[name] = n
		} else {
			pm[StatusFailedDependency] = pm[http.StatusOK]
			delete(pm, http.StatusOK)
		}
	}

	pstats := make([]Propstat, 0, len(pm))
	for s, pstat := range pm {
		pstat.Status = s
		pstats = append(pstats, pstat)
	}
	return pstats, nil
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
