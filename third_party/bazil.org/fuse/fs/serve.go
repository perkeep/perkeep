// FUSE service loop, for servers that wish to use it.

package fs

import (
	"fmt"
	"hash/fnv"
	"io"
	"path"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"
)

import (
	"camlistore.org/third_party/bazil.org/fuse"
	"camlistore.org/third_party/bazil.org/fuse/fuseutil"
)

const (
	attrValidTime  = 1 * time.Minute
	entryValidTime = 1 * time.Minute
)

// TODO: FINISH DOCS

// An Intr is a channel that signals that a request has been interrupted.
// Being able to receive from the channel means the request has been
// interrupted.
type Intr chan struct{}

func (Intr) String() string { return "fuse.Intr" }

// An FS is the interface required of a file system.
//
// Other FUSE requests can be handled by implementing methods from the
// FS* interfaces, for example FSIniter.
type FS interface {
	// Root is called to obtain the Node for the file system root.
	Root() (Node, fuse.Error)
}

type FSIniter interface {
	// Init is called to initialize the FUSE connection.
	// It can inspect the request and adjust the response as desired.
	// The default response sets MaxReadahead to 0 and MaxWrite to 4096.
	// Init must return promptly.
	Init(*fuse.InitRequest, *fuse.InitResponse, Intr) fuse.Error
}

type FSStatfser interface {
	// Statfs is called to obtain file system metadata.
	// It should write that data to resp.
	Statfs(*fuse.StatfsRequest, *fuse.StatfsResponse, Intr) fuse.Error
}

type FSDestroyer interface {
	// Destroy is called when the file system is shutting down.
	//
	// Linux only sends this request for block device backed (fuseblk)
	// filesystems, to allow them to flush writes to disk before the
	// unmount completes.
	//
	// On normal FUSE filesystems, use Forget of the root Node to
	// do actions at unmount time.
	Destroy()
}

// A Node is the interface required of a file or directory.
// See the documentation for type FS for general information
// pertaining to all methods.
//
// Other FUSE requests can be handled by implementing methods from the
// Node* interfaces, for example NodeOpener.
type Node interface {
	Attr() fuse.Attr
}

type NodeGetattrer interface {
	// Getattr obtains the standard metadata for the receiver.
	// It should store that metadata in resp.
	//
	// If this method is not implemented, the attributes will be
	// generated based on Attr(), with zero values filled in.
	Getattr(*fuse.GetattrRequest, *fuse.GetattrResponse, Intr) fuse.Error
}

type NodeSetattrer interface {
	// Setattr sets the standard metadata for the receiver.
	Setattr(*fuse.SetattrRequest, *fuse.SetattrResponse, Intr) fuse.Error
}

type NodeSymlinker interface {
	// Symlink creates a new symbolic link in the receiver, which must be a directory.
	//
	// TODO is the above true about directories?
	Symlink(*fuse.SymlinkRequest, Intr) (Node, fuse.Error)
}

// This optional request will be called only for symbolic link nodes.
type NodeReadlinker interface {
	// Readlink reads a symbolic link.
	Readlink(*fuse.ReadlinkRequest, Intr) (string, fuse.Error)
}

type NodeLinker interface {
	// Link creates a new directory entry in the receiver based on an
	// existing Node. Receiver must be a directory.
	Link(r *fuse.LinkRequest, old Node, intr Intr) (Node, fuse.Error)
}

type NodeRemover interface {
	// Remove removes the entry with the given name from
	// the receiver, which must be a directory.  The entry to be removed
	// may correspond to a file (unlink) or to a directory (rmdir).
	Remove(*fuse.RemoveRequest, Intr) fuse.Error
}

type NodeAccesser interface {
	// Access checks whether the calling context has permission for
	// the given operations on the receiver. If so, Access should
	// return nil. If not, Access should return EPERM.
	//
	// Note that this call affects the result of the access(2) system
	// call but not the open(2) system call. If Access is not
	// implemented, the Node behaves as if it always returns nil
	// (permission granted), relying on checks in Open instead.
	Access(*fuse.AccessRequest, Intr) fuse.Error
}

type NodeStringLookuper interface {
	// Lookup looks up a specific entry in the receiver,
	// which must be a directory.  Lookup should return a Node
	// corresponding to the entry.  If the name does not exist in
	// the directory, Lookup should return nil, err.
	//
	// Lookup need not to handle the names "." and "..".
	Lookup(string, Intr) (Node, fuse.Error)
}

type NodeRequestLookuper interface {
	// Lookup looks up a specific entry in the receiver.
	// See NodeStringLookuper for more.
	Lookup(*fuse.LookupRequest, *fuse.LookupResponse, Intr) (Node, fuse.Error)
}

type NodeMkdirer interface {
	Mkdir(*fuse.MkdirRequest, Intr) (Node, fuse.Error)
}

type NodeOpener interface {
	// Open opens the receiver.
	// XXX note about access.  XXX OpenFlags.
	// XXX note that the Node may be a file or directory.
	Open(*fuse.OpenRequest, *fuse.OpenResponse, Intr) (Handle, fuse.Error)
}

type NodeCreater interface {
	// Create creates a new directory entry in the receiver, which
	// must be a directory.
	Create(*fuse.CreateRequest, *fuse.CreateResponse, Intr) (Node, Handle, fuse.Error)
}

type NodeForgetter interface {
	Forget()
}

type NodeRenamer interface {
	Rename(r *fuse.RenameRequest, newDir Node, intr Intr) fuse.Error
}

type NodeMknoder interface {
	Mknod(r *fuse.MknodRequest, intr Intr) (Node, fuse.Error)
}

// TODO this should be on Handle not Node
type NodeFsyncer interface {
	Fsync(r *fuse.FsyncRequest, intr Intr) fuse.Error
}

type NodeGetxattrer interface {
	// Getxattr gets an extended attribute by the given name from the
	// node.
	//
	// If there is no xattr by that name, returns fuse.ENODATA. This
	// will be translated to the platform-specific correct error code
	// by the framework.
	Getxattr(req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse, intr Intr) fuse.Error
}

type NodeListxattrer interface {
	// Listxattr lists the extended attributes recorded for the node.
	Listxattr(req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse, intr Intr) fuse.Error
}

type NodeSetxattrer interface {
	// Setxattr sets an extended attribute with the given name and
	// value for the node.
	Setxattr(req *fuse.SetxattrRequest, intr Intr) fuse.Error
}

type NodeRemovexattrer interface {
	// Removexattr removes an extended attribute for the name.
	//
	// If there is no xattr by that name, returns fuse.ENODATA. This
	// will be translated to the platform-specific correct error code
	// by the framework.
	Removexattr(req *fuse.RemovexattrRequest, intr Intr) fuse.Error
}

var startTime = time.Now()

func nodeAttr(n Node) (attr fuse.Attr) {
	attr = n.Attr()
	if attr.Nlink == 0 {
		attr.Nlink = 1
	}
	if attr.Atime.IsZero() {
		attr.Atime = startTime
	}
	if attr.Mtime.IsZero() {
		attr.Mtime = startTime
	}
	if attr.Ctime.IsZero() {
		attr.Ctime = startTime
	}
	if attr.Crtime.IsZero() {
		attr.Crtime = startTime
	}
	return
}

// A Handle is the interface required of an opened file or directory.
// See the documentation for type FS for general information
// pertaining to all methods.
//
// Other FUSE requests can be handled by implementing methods from the
// Node* interfaces. The most common to implement are
// HandleReader, HandleReadDirer, and HandleWriter.
//
// TODO implement methods: Getlk, Setlk, Setlkw
type Handle interface {
}

type HandleFlusher interface {
	// Flush is called each time the file or directory is closed.
	// Because there can be multiple file descriptors referring to a
	// single opened file, Flush can be called multiple times.
	Flush(*fuse.FlushRequest, Intr) fuse.Error
}

type HandleReadAller interface {
	ReadAll(Intr) ([]byte, fuse.Error)
}

type HandleReadDirer interface {
	ReadDir(Intr) ([]fuse.Dirent, fuse.Error)
}

type HandleReader interface {
	Read(*fuse.ReadRequest, *fuse.ReadResponse, Intr) fuse.Error
}

type HandleWriter interface {
	Write(*fuse.WriteRequest, *fuse.WriteResponse, Intr) fuse.Error
}

type HandleReleaser interface {
	Release(*fuse.ReleaseRequest, Intr) fuse.Error
}

// Serve serves the FUSE connection by making calls to the methods
// of fs and the Nodes and Handles it makes available.  It returns only
// when the connection has been closed or an unexpected error occurs.
func Serve(c *fuse.Conn, fs FS) error {
	sc := serveConn{}
	sc.req = map[fuse.RequestID]*serveRequest{}

	root, err := fs.Root()
	if err != nil {
		return fmt.Errorf("cannot obtain root node: %v", syscall.Errno(err.(fuse.Errno)).Error())
	}
	sc.node = append(sc.node, nil, &serveNode{name: "/", node: root, refs: 1})
	sc.handle = append(sc.handle, nil)

	for {
		req, err := c.ReadRequest()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		go sc.serve(fs, req)
	}
	return nil
}

type nothing struct{}

type serveConn struct {
	meta       sync.Mutex
	req        map[fuse.RequestID]*serveRequest
	node       []*serveNode
	handle     []*serveHandle
	freeNode   []fuse.NodeID
	freeHandle []fuse.HandleID
	nodeGen    uint64
}

type serveRequest struct {
	Request fuse.Request
	Intr    Intr
}

type serveNode struct {
	name string
	node Node
	refs uint64
}

func (sn *serveNode) attr() (attr fuse.Attr) {
	attr = nodeAttr(sn.node)
	if attr.Inode == 0 {
		attr.Inode = hash(sn.name)
	}
	return
}

func hash(s string) uint64 {
	f := fnv.New64()
	f.Write([]byte(s))
	return f.Sum64()
}

type serveHandle struct {
	handle   Handle
	readData []byte
	nodeID   fuse.NodeID
}

// NodeRef can be embedded in a Node to recognize the same Node being
// returned from multiple Lookup, Create etc calls.
//
// Without this, each Node will get a new NodeID, causing spurious
// cache invalidations, extra lookups and aliasing anomalies. This may
// not matter for a simple, read-only filesystem.
type NodeRef struct {
	id         fuse.NodeID
	generation uint64
}

// nodeRef is only ever accessed while holding serveConn.meta
func (n *NodeRef) nodeRef() *NodeRef {
	return n
}

type nodeRef interface {
	nodeRef() *NodeRef
}

func (c *serveConn) saveNode(name string, node Node) (id fuse.NodeID, gen uint64, sn *serveNode) {
	c.meta.Lock()
	defer c.meta.Unlock()

	var ref *NodeRef
	if nodeRef, ok := node.(nodeRef); ok {
		ref = nodeRef.nodeRef()

		if ref.id != 0 {
			// dropNode guarantees that NodeRef is zeroed at the same
			// time as the NodeID is removed from serveConn.node, as
			// guarded by c.meta; this means sn cannot be nil here
			sn = c.node[ref.id]
			sn.refs++
			return ref.id, ref.generation, sn
		}
	}

	sn = &serveNode{name: name, node: node, refs: 1}
	if n := len(c.freeNode); n > 0 {
		id = c.freeNode[n-1]
		c.freeNode = c.freeNode[:n-1]
		c.node[id] = sn
		c.nodeGen++
	} else {
		id = fuse.NodeID(len(c.node))
		c.node = append(c.node, sn)
	}
	gen = c.nodeGen
	if ref != nil {
		ref.id = id
		ref.generation = gen
	}
	return
}

func (c *serveConn) saveHandle(handle Handle, nodeID fuse.NodeID) (id fuse.HandleID) {
	c.meta.Lock()
	shandle := &serveHandle{handle: handle, nodeID: nodeID}
	if n := len(c.freeHandle); n > 0 {
		id = c.freeHandle[n-1]
		c.freeHandle = c.freeHandle[:n-1]
		c.handle[id] = shandle
	} else {
		id = fuse.HandleID(len(c.handle))
		c.handle = append(c.handle, shandle)
	}
	c.meta.Unlock()
	return
}

type nodeRefcountDropBug struct {
	N    uint64
	Refs uint64
	Node fuse.NodeID
}

func (n *nodeRefcountDropBug) String() string {
	return fmt.Sprintf("bug: trying to drop %d of %d references to %v", n.N, n.Refs, n.Node)
}

func (c *serveConn) dropNode(id fuse.NodeID, n uint64) (forget bool) {
	c.meta.Lock()
	defer c.meta.Unlock()
	snode := c.node[id]

	if snode == nil {
		// this should only happen if refcounts kernel<->us disagree
		// *and* two ForgetRequests for the same node race each other;
		// this indicates a bug somewhere
		fuse.Debug(nodeRefcountDropBug{N: n, Node: id})

		// we may end up triggering Forget twice, but that's better
		// than not even once, and that's the best we can do
		return true
	}

	if n > snode.refs {
		fuse.Debug(nodeRefcountDropBug{N: n, Refs: snode.refs, Node: id})
		n = snode.refs
	}

	snode.refs -= n
	if snode.refs == 0 {
		c.node[id] = nil
		if nodeRef, ok := snode.node.(nodeRef); ok {
			ref := nodeRef.nodeRef()
			*ref = NodeRef{}
		}
		c.freeNode = append(c.freeNode, id)
		return true
	}
	return false
}

func (c *serveConn) dropHandle(id fuse.HandleID) {
	c.meta.Lock()
	c.handle[id] = nil
	c.freeHandle = append(c.freeHandle, id)
	c.meta.Unlock()
}

type missingHandle struct {
	Handle    fuse.HandleID
	MaxHandle fuse.HandleID
}

func (m missingHandle) String() string {
	return fmt.Sprint("missing handle", m.Handle, m.MaxHandle)
}

// Returns nil for invalid handles.
func (c *serveConn) getHandle(id fuse.HandleID) (shandle *serveHandle) {
	c.meta.Lock()
	defer c.meta.Unlock()
	if id < fuse.HandleID(len(c.handle)) {
		shandle = c.handle[uint(id)]
	}
	if shandle == nil {
		fuse.Debug(missingHandle{
			Handle:    id,
			MaxHandle: fuse.HandleID(len(c.handle)),
		})
	}
	return
}

type request struct {
	Op      string
	Request *fuse.Header
	In      interface{} `json:",omitempty"`
}

func (r request) String() string {
	return fmt.Sprintf("<- %s", r.In)
}

type logResponseHeader struct {
	ID fuse.RequestID
}

func (m logResponseHeader) String() string {
	return fmt.Sprintf("ID=%#x", m.ID)
}

type response struct {
	Op      string
	Request logResponseHeader
	Out     interface{} `json:",omitempty"`
	Error   fuse.Error  `json:",omitempty"`
}

func (r response) String() string {
	switch {
	case r.Error != nil && r.Out != nil:
		return fmt.Sprintf("-> %s error=%s %s", r.Request, r.Error, r.Out)
	case r.Error != nil:
		return fmt.Sprintf("-> %s error=%s", r.Request, r.Error)
	case r.Out != nil:
		return fmt.Sprintf("-> %s %s", r.Request, r.Out)
	default:
		return fmt.Sprintf("-> %s", r.Request)
	}
}

type logMissingNode struct {
	MaxNode fuse.NodeID
}

func opName(req fuse.Request) string {
	t := reflect.Indirect(reflect.ValueOf(req)).Type()
	s := t.Name()
	s = strings.TrimSuffix(s, "Request")
	return s
}

type logLinkRequestOldNodeNotFound struct {
	Request *fuse.Header
	In      *fuse.LinkRequest
}

func (m *logLinkRequestOldNodeNotFound) String() string {
	return fmt.Sprintf("In LinkRequest (request %#x), node %d not found", m.Request.Hdr().ID, m.In.OldNode)
}

type renameNewDirNodeNotFound struct {
	Request *fuse.Header
	In      *fuse.RenameRequest
}

func (m *renameNewDirNodeNotFound) String() string {
	return fmt.Sprintf("In RenameRequest (request %#x), node %d not found", m.Request.Hdr().ID, m.In.NewDir)
}

func (c *serveConn) serve(fs FS, r fuse.Request) {
	intr := make(Intr)
	req := &serveRequest{Request: r, Intr: intr}

	fuse.Debug(request{
		Op:      opName(r),
		Request: r.Hdr(),
		In:      r,
	})
	var node Node
	var snode *serveNode
	c.meta.Lock()
	hdr := r.Hdr()
	if id := hdr.Node; id != 0 {
		if id < fuse.NodeID(len(c.node)) {
			snode = c.node[uint(id)]
		}
		if snode == nil {
			c.meta.Unlock()
			fuse.Debug(response{
				Op:      opName(r),
				Request: logResponseHeader{ID: hdr.ID},
				Error:   fuse.ESTALE,
				// this is the only place that sets both Error and
				// Out; not sure if i want to do that; might get rid
				// of len(c.node) things altogether
				Out: logMissingNode{
					MaxNode: fuse.NodeID(len(c.node)),
				},
			})
			r.RespondError(fuse.ESTALE)
			return
		}
		node = snode.node
	}
	if c.req[hdr.ID] != nil {
		// This happens with OSXFUSE.  Assume it's okay and
		// that we'll never see an interrupt for this one.
		// Otherwise everything wedges.  TODO: Report to OSXFUSE?
		//
		// TODO this might have been because of missing done() calls
		intr = nil
	} else {
		c.req[hdr.ID] = req
	}
	c.meta.Unlock()

	// Call this before responding.
	// After responding is too late: we might get another request
	// with the same ID and be very confused.
	done := func(resp interface{}) {
		msg := response{
			Op:      opName(r),
			Request: logResponseHeader{ID: hdr.ID},
		}
		if err, ok := resp.(fuse.Error); ok {
			msg.Error = err
		} else {
			msg.Out = resp
		}
		fuse.Debug(msg)

		c.meta.Lock()
		delete(c.req, hdr.ID)
		c.meta.Unlock()
	}

	switch r := r.(type) {
	default:
		// Note: To FUSE, ENOSYS means "this server never implements this request."
		// It would be inappropriate to return ENOSYS for other operations in this
		// switch that might only be unavailable in some contexts, not all.
		done(fuse.ENOSYS)
		r.RespondError(fuse.ENOSYS)

	// FS operations.
	case *fuse.InitRequest:
		s := &fuse.InitResponse{
			MaxWrite: 4096,
		}
		if fs, ok := fs.(FSIniter); ok {
			if err := fs.Init(r, s, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
		}
		done(s)
		r.Respond(s)

	case *fuse.StatfsRequest:
		s := &fuse.StatfsResponse{}
		if fs, ok := fs.(FSStatfser); ok {
			if err := fs.Statfs(r, s, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
		}
		done(s)
		r.Respond(s)

	// Node operations.
	case *fuse.GetattrRequest:
		s := &fuse.GetattrResponse{}
		if n, ok := node.(NodeGetattrer); ok {
			if err := n.Getattr(r, s, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
		} else {
			s.AttrValid = attrValidTime
			s.Attr = snode.attr()
		}
		done(s)
		r.Respond(s)

	case *fuse.SetattrRequest:
		s := &fuse.SetattrResponse{}
		if n, ok := node.(NodeSetattrer); ok {
			if err := n.Setattr(r, s, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
			done(s)
			r.Respond(s)
			break
		}

		if s.AttrValid == 0 {
			s.AttrValid = attrValidTime
		}
		s.Attr = snode.attr()
		done(s)
		r.Respond(s)

	case *fuse.SymlinkRequest:
		s := &fuse.SymlinkResponse{}
		n, ok := node.(NodeSymlinker)
		if !ok {
			done(fuse.EIO) // XXX or EPERM like Mkdir?
			r.RespondError(fuse.EIO)
			break
		}
		n2, err := n.Symlink(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		c.saveLookup(&s.LookupResponse, snode, r.NewName, n2)
		done(s)
		r.Respond(s)

	case *fuse.ReadlinkRequest:
		n, ok := node.(NodeReadlinker)
		if !ok {
			done(fuse.EIO) /// XXX or EPERM?
			r.RespondError(fuse.EIO)
			break
		}
		target, err := n.Readlink(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		done(target)
		r.Respond(target)

	case *fuse.LinkRequest:
		n, ok := node.(NodeLinker)
		if !ok {
			done(fuse.EIO) /// XXX or EPERM?
			r.RespondError(fuse.EIO)
			break
		}
		c.meta.Lock()
		var oldNode *serveNode
		if int(r.OldNode) < len(c.node) {
			oldNode = c.node[r.OldNode]
		}
		c.meta.Unlock()
		if oldNode == nil {
			fuse.Debug(logLinkRequestOldNodeNotFound{
				Request: r.Hdr(),
				In:      r,
			})
			done(fuse.EIO)
			r.RespondError(fuse.EIO)
			break
		}
		n2, err := n.Link(r, oldNode.node, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		s := &fuse.LookupResponse{}
		c.saveLookup(s, snode, r.NewName, n2)
		done(s)
		r.Respond(s)

	case *fuse.RemoveRequest:
		n, ok := node.(NodeRemover)
		if !ok {
			done(fuse.EIO) /// XXX or EPERM?
			r.RespondError(fuse.EIO)
			break
		}
		err := n.Remove(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		done(nil)
		r.Respond()

	case *fuse.AccessRequest:
		if n, ok := node.(NodeAccesser); ok {
			if err := n.Access(r, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
		}
		done(nil)
		r.Respond()

	case *fuse.LookupRequest:
		var n2 Node
		var err fuse.Error
		s := &fuse.LookupResponse{}
		if n, ok := node.(NodeStringLookuper); ok {
			n2, err = n.Lookup(r.Name, intr)
		} else if n, ok := node.(NodeRequestLookuper); ok {
			n2, err = n.Lookup(r, s, intr)
		} else {
			done(fuse.ENOENT)
			r.RespondError(fuse.ENOENT)
			break
		}
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		c.saveLookup(s, snode, r.Name, n2)
		done(s)
		r.Respond(s)

	case *fuse.MkdirRequest:
		s := &fuse.MkdirResponse{}
		n, ok := node.(NodeMkdirer)
		if !ok {
			done(fuse.EPERM)
			r.RespondError(fuse.EPERM)
			break
		}
		n2, err := n.Mkdir(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		c.saveLookup(&s.LookupResponse, snode, r.Name, n2)
		done(s)
		r.Respond(s)

	case *fuse.OpenRequest:
		s := &fuse.OpenResponse{Flags: fuse.OpenDirectIO}
		var h2 Handle
		if n, ok := node.(NodeOpener); ok {
			hh, err := n.Open(r, s, intr)
			if err != nil {
				done(err)
				r.RespondError(err)
				break
			}
			h2 = hh
		} else {
			h2 = node
		}
		s.Handle = c.saveHandle(h2, hdr.Node)
		done(s)
		r.Respond(s)

	case *fuse.CreateRequest:
		n, ok := node.(NodeCreater)
		if !ok {
			// If we send back ENOSYS, FUSE will try mknod+open.
			done(fuse.EPERM)
			r.RespondError(fuse.EPERM)
			break
		}
		s := &fuse.CreateResponse{OpenResponse: fuse.OpenResponse{Flags: fuse.OpenDirectIO}}
		n2, h2, err := n.Create(r, s, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		c.saveLookup(&s.LookupResponse, snode, r.Name, n2)
		s.Handle = c.saveHandle(h2, hdr.Node)
		done(s)
		r.Respond(s)

	case *fuse.GetxattrRequest:
		n, ok := node.(NodeGetxattrer)
		if !ok {
			done(fuse.ENOTSUP)
			r.RespondError(fuse.ENOTSUP)
			break
		}
		s := &fuse.GetxattrResponse{}
		err := n.Getxattr(r, s, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		if r.Size != 0 && uint64(len(s.Xattr)) > uint64(r.Size) {
			done(fuse.ERANGE)
			r.RespondError(fuse.ERANGE)
			break
		}
		done(s)
		r.Respond(s)

	case *fuse.ListxattrRequest:
		n, ok := node.(NodeListxattrer)
		if !ok {
			done(fuse.ENOTSUP)
			r.RespondError(fuse.ENOTSUP)
			break
		}
		s := &fuse.ListxattrResponse{}
		err := n.Listxattr(r, s, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		if r.Size != 0 && uint64(len(s.Xattr)) > uint64(r.Size) {
			done(fuse.ERANGE)
			r.RespondError(fuse.ERANGE)
			break
		}
		done(s)
		r.Respond(s)

	case *fuse.SetxattrRequest:
		n, ok := node.(NodeSetxattrer)
		if !ok {
			done(fuse.ENOTSUP)
			r.RespondError(fuse.ENOTSUP)
			break
		}
		err := n.Setxattr(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		done(nil)
		r.Respond()

	case *fuse.RemovexattrRequest:
		n, ok := node.(NodeRemovexattrer)
		if !ok {
			done(fuse.ENOTSUP)
			r.RespondError(fuse.ENOTSUP)
			break
		}
		err := n.Removexattr(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		done(nil)
		r.Respond()

	case *fuse.ForgetRequest:
		forget := c.dropNode(hdr.Node, r.N)
		if forget {
			n, ok := node.(NodeForgetter)
			if ok {
				n.Forget()
			}
		}
		done(nil)
		r.Respond()

	// Handle operations.
	case *fuse.ReadRequest:
		shandle := c.getHandle(r.Handle)
		if shandle == nil {
			done(fuse.ESTALE)
			r.RespondError(fuse.ESTALE)
			return
		}
		handle := shandle.handle

		s := &fuse.ReadResponse{Data: make([]byte, 0, r.Size)}
		if r.Dir {
			if h, ok := handle.(HandleReadDirer); ok {
				if shandle.readData == nil {
					dirs, err := h.ReadDir(intr)
					if err != nil {
						done(err)
						r.RespondError(err)
						break
					}
					var data []byte
					for _, dir := range dirs {
						if dir.Inode == 0 {
							dir.Inode = hash(path.Join(snode.name, dir.Name))
						}
						data = fuse.AppendDirent(data, dir)
					}
					shandle.readData = data
				}
				fuseutil.HandleRead(r, s, shandle.readData)
				done(s)
				r.Respond(s)
				break
			}
		} else {
			if h, ok := handle.(HandleReadAller); ok {
				if shandle.readData == nil {
					data, err := h.ReadAll(intr)
					if err != nil {
						done(err)
						r.RespondError(err)
						break
					}
					if data == nil {
						data = []byte{}
					}
					shandle.readData = data
				}
				fuseutil.HandleRead(r, s, shandle.readData)
				done(s)
				r.Respond(s)
				break
			}
			h, ok := handle.(HandleReader)
			if !ok {
				fmt.Printf("NO READ FOR %T\n", handle)
				done(fuse.EIO)
				r.RespondError(fuse.EIO)
				break
			}
			if err := h.Read(r, s, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
		}
		done(s)
		r.Respond(s)

	case *fuse.WriteRequest:
		shandle := c.getHandle(r.Handle)
		if shandle == nil {
			done(fuse.ESTALE)
			r.RespondError(fuse.ESTALE)
			return
		}

		s := &fuse.WriteResponse{}
		if h, ok := shandle.handle.(HandleWriter); ok {
			if err := h.Write(r, s, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
			done(s)
			r.Respond(s)
			break
		}
		done(fuse.EIO)
		r.RespondError(fuse.EIO)

	case *fuse.FlushRequest:
		shandle := c.getHandle(r.Handle)
		if shandle == nil {
			done(fuse.ESTALE)
			r.RespondError(fuse.ESTALE)
			return
		}
		handle := shandle.handle

		if h, ok := handle.(HandleFlusher); ok {
			if err := h.Flush(r, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
		}
		done(nil)
		r.Respond()

	case *fuse.ReleaseRequest:
		shandle := c.getHandle(r.Handle)
		if shandle == nil {
			done(fuse.ESTALE)
			r.RespondError(fuse.ESTALE)
			return
		}
		handle := shandle.handle

		// No matter what, release the handle.
		c.dropHandle(r.Handle)

		if h, ok := handle.(HandleReleaser); ok {
			if err := h.Release(r, intr); err != nil {
				done(err)
				r.RespondError(err)
				break
			}
		}
		done(nil)
		r.Respond()

	case *fuse.DestroyRequest:
		if fs, ok := fs.(FSDestroyer); ok {
			fs.Destroy()
		}
		done(nil)
		r.Respond()

	case *fuse.RenameRequest:
		c.meta.Lock()
		var newDirNode *serveNode
		if int(r.NewDir) < len(c.node) {
			newDirNode = c.node[r.NewDir]
		}
		c.meta.Unlock()
		if newDirNode == nil {
			fuse.Debug(renameNewDirNodeNotFound{
				Request: r.Hdr(),
				In:      r,
			})
			done(fuse.EIO)
			r.RespondError(fuse.EIO)
			break
		}
		n, ok := node.(NodeRenamer)
		if !ok {
			done(fuse.EIO) // XXX or EPERM like Mkdir?
			r.RespondError(fuse.EIO)
			break
		}
		err := n.Rename(r, newDirNode.node, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		done(nil)
		r.Respond()

	case *fuse.MknodRequest:
		n, ok := node.(NodeMknoder)
		if !ok {
			done(fuse.EIO)
			r.RespondError(fuse.EIO)
			break
		}
		n2, err := n.Mknod(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		s := &fuse.LookupResponse{}
		c.saveLookup(s, snode, r.Name, n2)
		done(s)
		r.Respond(s)

	case *fuse.FsyncRequest:
		n, ok := node.(NodeFsyncer)
		if !ok {
			done(fuse.EIO)
			r.RespondError(fuse.EIO)
			break
		}
		err := n.Fsync(r, intr)
		if err != nil {
			done(err)
			r.RespondError(err)
			break
		}
		done(nil)
		r.Respond()

	case *fuse.InterruptRequest:
		c.meta.Lock()
		ireq := c.req[r.IntrID]
		if ireq != nil && ireq.Intr != nil {
			close(ireq.Intr)
			ireq.Intr = nil
		}
		c.meta.Unlock()
		done(nil)
		r.Respond()

		/*	case *FsyncdirRequest:
				done(ENOSYS)
				r.RespondError(ENOSYS)

			case *GetlkRequest, *SetlkRequest, *SetlkwRequest:
				done(ENOSYS)
				r.RespondError(ENOSYS)

			case *BmapRequest:
				done(ENOSYS)
				r.RespondError(ENOSYS)

			case *SetvolnameRequest, *GetxtimesRequest, *ExchangeRequest:
				done(ENOSYS)
				r.RespondError(ENOSYS)
		*/
	}
}

func (c *serveConn) saveLookup(s *fuse.LookupResponse, snode *serveNode, elem string, n2 Node) {
	name := path.Join(snode.name, elem)
	var sn *serveNode
	s.Node, s.Generation, sn = c.saveNode(name, n2)
	if s.EntryValid == 0 {
		s.EntryValid = entryValidTime
	}
	if s.AttrValid == 0 {
		s.AttrValid = attrValidTime
	}
	s.Attr = sn.attr()
}

// DataHandle returns a read-only Handle that satisfies reads
// using the given data.
func DataHandle(data []byte) Handle {
	return &dataHandle{data}
}

type dataHandle struct {
	data []byte
}

func (d *dataHandle) ReadAll(intr Intr) ([]byte, fuse.Error) {
	return d.data, nil
}
