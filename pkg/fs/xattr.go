// +build linux darwin

/*
Copyright 2013 The Camlistore Authors

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

package fs

import (
	"encoding/base64"
	"log"
	"strings"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/third_party/bazil.org/fuse"
	"camlistore.org/third_party/bazil.org/fuse/fs"
)

// xattrPrefix is the permanode attribute prefix used for record
// extended attributes.
const xattrPrefix = "xattr:"

// xattr provides common support for extended attributes for various
// file and directory implementations (fuse.Node) within the FUSE services.
type xattr struct {
	typeName  string // for logging
	fs        *CamliFileSystem
	permanode blob.Ref

	// mu guards xattrs.  Both mu and the xattrs map are provided by the
	// fuse.Node when the struct is created.
	mu *sync.Mutex

	// This is a pointer to the particular fuse.Node's location of its
	// xattr map so that it can be initialized commonly when the fuse.Node
	// calls xattr.load(*search.DescribedPermanode)
	xattrs *map[string][]byte
}

// load is invoked after the creation of a fuse.Node that may contain extended
// attributes.  This creates the node's xattr map as well as fills it with any
// extended attributes found in the permanode's claims.
func (x *xattr) load(p *search.DescribedPermanode) {
	x.mu.Lock()
	defer x.mu.Unlock()

	*x.xattrs = map[string][]byte{}
	for k, v := range p.Attr {
		if strings.HasPrefix(k, xattrPrefix) {
			name := k[len(xattrPrefix):]
			val, err := base64.StdEncoding.DecodeString(v[0])
			if err != nil {
				log.Printf("Base64 decoding error on attribute %v: %v", name, err)
				continue
			}
			(*x.xattrs)[name] = val
		}
	}
}

func (x *xattr) set(req *fuse.SetxattrRequest) fuse.Error {
	log.Printf("%s.setxattr(%q) -> %q", x.typeName, req.Name, req.Xattr)

	claim := schema.NewSetAttributeClaim(x.permanode, xattrPrefix+req.Name,
		base64.StdEncoding.EncodeToString(req.Xattr))
	_, err := x.fs.client.UploadAndSignBlob(claim)
	if err != nil {
		log.Printf("Error setting xattr: %v", err)
		return fuse.EIO
	}

	val := make([]byte, len(req.Xattr))
	copy(val, req.Xattr)
	x.mu.Lock()
	(*x.xattrs)[req.Name] = val
	x.mu.Unlock()

	return nil
}

func (x *xattr) remove(req *fuse.RemovexattrRequest) fuse.Error {
	log.Printf("%s.Removexattr(%q)", x.typeName, req.Name)

	claim := schema.NewDelAttributeClaim(x.permanode, xattrPrefix+req.Name, "")
	_, err := x.fs.client.UploadAndSignBlob(claim)

	if err != nil {
		log.Printf("Error removing xattr: %v", err)
		return fuse.EIO
	}

	x.mu.Lock()
	delete(*x.xattrs, req.Name)
	x.mu.Unlock()

	return nil
}

func (x *xattr) get(req *fuse.GetxattrRequest, res *fuse.GetxattrResponse) fuse.Error {
	x.mu.Lock()
	defer x.mu.Unlock()

	val, found := (*x.xattrs)[req.Name]

	if !found {
		return fuse.ENODATA
	}

	res.Xattr = val

	return nil
}

func (x *xattr) list(req *fuse.ListxattrRequest, res *fuse.ListxattrResponse) fuse.Error {
	x.mu.Lock()
	defer x.mu.Unlock()

	for k := range *x.xattrs {
		res.Xattr = append(res.Xattr, k...)
		res.Xattr = append(res.Xattr, '\x00')
	}
	return nil
}

// noXattr provides default xattr methods for fuse nodes.  The fuse
// package itself defaults to ENOSYS which causes some systems (read:
// MacOSX) to assume that no extended attribute support is available
// anywhere in the filesystem.  This different set of defaults just
// returns no values for read requests and permission denied for write
// requests.
type noXattr struct{}

func (n noXattr) Getxattr(*fuse.GetxattrRequest, *fuse.GetxattrResponse, fs.Intr) fuse.Error {
	return fuse.ENODATA
}

func (n noXattr) Listxattr(*fuse.ListxattrRequest, *fuse.ListxattrResponse, fs.Intr) fuse.Error {
	return nil
}

func (n noXattr) Setxattr(*fuse.SetxattrRequest, fs.Intr) fuse.Error {
	return fuse.EPERM
}

func (n noXattr) Removexattr(*fuse.RemovexattrRequest, fs.Intr) fuse.Error {
	return fuse.EPERM
}
