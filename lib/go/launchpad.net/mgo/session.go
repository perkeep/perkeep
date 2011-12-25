// mgo - MongoDB driver for Go
// 
// Copyright (c) 2010-2011 - Gustavo Niemeyer <gustavo@niemeyer.net>
// 
// All rights reserved.
// 
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
// 
//     * Redistributions of source code must retain the above copyright notice,
//       this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above copyright notice,
//       this list of conditions and the following disclaimer in the documentation
//       and/or other materials provided with the distribution.
//     * Neither the name of the copyright holder nor the names of its
//       contributors may be used to endorse or promote products derived from
//       this software without specific prior written permission.
// 
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR
// CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package mgo

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"launchpad.net/gobson/bson"
	"sync"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type mode int

const (
	Eventual  mode = 0
	Monotonic mode = 1
	Strong    mode = 2
)

// When changing the Session type, check if newSession and copySession
// need to be updated too.

type Session struct {
	m              sync.RWMutex
	cluster_       *mongoCluster
	socket         *mongoSocket
	socketIsMaster bool
	slaveOk        bool
	consistency    mode
	queryConfig    query
	safeOp         *queryOp
	syncTimeout    int64
	urlauth        *authInfo
	auth           []authInfo
}

type Database struct {
	Session *Session
	Name    string
}

type Collection struct {
	DB       Database
	Name     string // "collection"
	FullName string // "db.collection"
}

type Query struct {
	m       sync.Mutex
	session *Session
	query   // Enables default settings in session.
}

type query struct {
	op       queryOp
	prefetch float64
	limit    int32
}

type getLastError struct {
	CmdName  int         "getLastError"
	W        interface{} "w,omitempty"
	WTimeout int         "wtimeout,omitempty"
	FSync    bool        "fsync,omitempty"
	J        bool        "j,omitempty"
}

type Iter struct {
	m              sync.Mutex
	gotReply       sync.Cond
	session        *Session
	docData        queue
	err            os.Error
	op             getMoreOp
	prefetch       float64
	limit          int32
	pendingDocs    int
	docsBeforeMore int
	timeout        int
	timedout       bool
}

var NotFound = os.NewError("Document not found")

const defaultPrefetch = 0.25

// Mongo establishes a new session to the cluster identified by the given seed
// server(s).  The session will enable communication with all of the servers in
// the cluster, so the seed servers are used only to find out about the cluster
// topology.
//
// This method is generally called just once for a given cluster.  Further
// sessions to the same cluster are then established using the New method on
// the obtained session.  This will make them share the underlying cluster,
// and manage the pool of connections appropriately.
//
// Once the session is not useful anymore, Close must be called to release the
// resources appropriately.
//
// The seed servers must be provided in the following format:
//
//     [mongodb://][user:pass@]host1[:port1][,host2[:port2],...][/database][?options]
//
// For example, it may be as simple as:
//
//     localhost
//
// Or more involved like:
//
//     mongodb://myuser:mypass@localhost:40001,otherhost:40001/mydb
//
// If the port number is not provided for a server, it defaults to 27017.
//
// The username and password provided in the URL will be used to authenticate
// into the database named after the slash at the end of the host names, or
// into the "admin" database if none is provided.  The authentication information
// will persist in sessions obtained through the New method as well.
//
// The following connection options are supported after the question mark:
//
//     connect=direct
//
//         This option will disable the automatic replica set server
//         discovery logic, and will only use the servers provided.
//         This enables forcing the communication with a specific
//         server or set of servers (even if they are slaves).  Note
//         that to talk to a slave you'll need to relax the consistency
//         requirements via the Monotonic or Eventual session methods.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Connections
//
func Mongo(url string) (session *Session, err os.Error) {
	servers, auth, options, err := parseURL(url)
	if err != nil {
		return nil, err
	}
	direct := false
	for k, v := range options {
		switch k {
		case "connect":
			if v == "direct" {
				direct = true
				break
			}
			if v == "replicaSet" {
				break
			}
			fallthrough
		default:
			err = os.NewError("Unsupported connection URL option: " + k + "=" + v)
			return
		}
	}
	cluster := newCluster(servers, direct)
	session = newSession(Strong, cluster, nil)
	if auth.user != "" {
		session.urlauth = &auth
		session.auth = []authInfo{auth}
	}
	cluster.Release()
	return session, nil
}

func parseURL(url string) (servers []string, auth authInfo, options map[string]string, err os.Error) {
	if strings.HasPrefix(url, "mongodb://") {
		url = url[10:]
	}
	options = make(map[string]string)
	if c := strings.Index(url, "?"); c != -1 {
		for _, pair := range strings.Split(url[c+1:], ";") {
			l := strings.SplitN(pair, "=", 2)
			if len(l) != 2 || l[0] == "" || l[1] == "" {
				err = os.NewError("Connection option must be key=value: " + pair)
				return
			}
			options[l[0]] = l[1]
		}
		url = url[:c]
	}
	if c := strings.Index(url, "@"); c != -1 {
		pair := strings.SplitN(url[:c], ":", 2)
		if len(pair) != 2 || pair[0] == "" {
			err = os.NewError("Credentials must be provided as user:pass@host")
			return
		}
		auth.user = pair[0]
		auth.pass = pair[1]
		url = url[c+1:]
		auth.db = "admin"
	}
	if c := strings.Index(url, "/"); c != -1 {
		if c != len(url)-1 {
			auth.db = url[c+1:]
		}
		url = url[:c]
	}
	if auth.user == "" {
		if auth.db != "" {
			err = os.NewError("Database name only makes sense with credentials")
			return
		}
	} else if auth.db == "" {
		auth.db = "admin"
	}
	servers = strings.Split(url, ",")
	// XXX This is untested. The test suite doesn't use the standard port.
	for i, server := range servers {
		p := strings.LastIndexAny(server, "]:")
		if p == -1 || server[p] != ':' {
			servers[i] = server + ":27017"
		}
	}
	return
}

func newSession(consistency mode, cluster *mongoCluster, socket *mongoSocket) (session *Session) {
	cluster.Acquire()
	session = &Session{cluster_: cluster}
	session.SetMode(consistency, true)
	session.SetSafe(&Safe{})
	session.setSocket(socket)
	session.queryConfig.prefetch = defaultPrefetch
	runtime.SetFinalizer(session, finalizeSession)
	return session
}

func copySession(session *Session, keepAuth bool) (s *Session) {
	session.cluster().Acquire()
	if session.socket != nil {
		session.socket.Acquire()
	}
	var auth []authInfo
	if keepAuth {
		auth = make([]authInfo, len(session.auth))
		copy(auth, session.auth)
	} else if session.urlauth != nil {
		auth = []authInfo{*session.urlauth}
	}
	// Copy everything but the mutex.
	s = &Session{
		cluster_:       session.cluster_,
		socket:         session.socket,
		socketIsMaster: session.socketIsMaster,
		slaveOk:        session.slaveOk,
		consistency:    session.consistency,
		queryConfig:    session.queryConfig,
		safeOp:         session.safeOp,
		syncTimeout:    session.syncTimeout,
		urlauth:        session.urlauth,
		auth:           auth,
	}
	runtime.SetFinalizer(s, finalizeSession)
	return s
}

func finalizeSession(session *Session) {
	session.Close()
}

// LiveServers returns a list of server addresses which are
// currently known to be alive.
func (s *Session) LiveServers() (addrs []string) {
	s.m.RLock()
	addrs = s.cluster().LiveServers()
	s.m.RUnlock()
	return addrs
}

// DB returns a database object, which allows further accessing any
// collections within it, or performing any database-level operations.
// Creating this object is a very lightweight operation, and involves
// no network communication.
func (s *Session) DB(name string) Database {
	return Database{s, name}
}

// C returns a collection object for the given database, which enables querying
// and modifying the content contained in the given collection. Creating this
// object is a very lightweight operation, and involves no network
// communication.
func (db Database) C(name string) Collection {
	return Collection{db, name, db.Name + "." + name}
}

// GridFS returns a GridFS value for interacting with collections in the
// database which follow the standard GridFS specification.  The provided
// prefix (sometimes known as root) will determine which collections to
// use, and is usually set to "fs" when there is a single GridFS in the
// database.
//
// See the GridFS Create, Open, and OpenId methods for more details.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/GridFS
//     http://www.mongodb.org/display/DOCS/GridFS+Tools
//     http://www.mongodb.org/display/DOCS/GridFS+Specification
//
func (db Database) GridFS(prefix string) *GridFS {
	return newGridFS(db, prefix)
}

// Run issues the provided command against the database and unmarshals
// its result in the respective argument. The cmd argument may be either
// a string with the command name itself, in which case an empty document of
// the form bson.M{cmd: 1} will be used, or it may be a full command document.
//
// Note that MongoDB considers the first marshalled key as the command
// name, so when providing a command with options, it's important to
// use an ordering-preserving document, such as a struct value or an
// instance of bson.D.  For instance:
//
//     db.Run(bson.D{{"create", "mycollection"}, {"size", 1024}})
//
// For privilleged commands typically run against the "admin" database, see
// the Run method in the Session type.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Commands
//     http://www.mongodb.org/display/DOCS/List+of+Database+CommandSkips
//
func (db Database) Run(cmd interface{}, result interface{}) os.Error {
	if name, ok := cmd.(string); ok {
		cmd = bson.D{{name, 1}}
	}
	return db.C("$cmd").Find(cmd).One(result)
}

// Login authenticates against MongoDB with the provided credentials.  The
// authentication is valid for the whole session and will stay valid until
// Logout is explicitly called for the same database, or the session is
// closed.
//
// Concurrent Login calls will work correctly.
func (db Database) Login(user, pass string) (err os.Error) {
	session := db.Session
	dbname := db.Name

	socket, err := session.acquireSocket(false)
	if err != nil {
		return err
	}
	defer socket.Release()

	err = socket.Login(dbname, user, pass)
	if err != nil {
		return err
	}

	session.m.Lock()
	defer session.m.Unlock()

	for _, a := range session.auth {
		if a.db == dbname {
			a.user = user
			a.pass = pass
			return nil
		}
	}
	session.auth = append(session.auth, authInfo{dbname, user, pass})
	return nil
}

// Logout removes any established authentication credentials for the database.
func (db Database) Logout() {
	session := db.Session
	dbname := db.Name
	session.m.Lock()
	found := false
	for i, a := range session.auth {
		if a.db == dbname {
			copy(session.auth[i:], session.auth[i+1:])
			session.auth = session.auth[:len(session.auth)-1]
			found = true
			break
		}
	}
	if found && session.socket != nil {
		session.socket.Logout(dbname)
	}
	session.m.Unlock()
}

// LogoutAll removes all established authentication credentials for the session.
func (s *Session) LogoutAll() {
	s.m.Lock()
	for _, a := range s.auth {
		s.socket.Logout(a.db)
	}
	s.auth = s.auth[0:0]
	s.m.Unlock()
}

// AddUser creates or updates the authentication credentials of user within
// the database.
func (db Database) AddUser(user, pass string, readOnly bool) os.Error {
	psum := md5.New()
	psum.Write([]byte(user + ":mongo:" + pass))
	digest := hex.EncodeToString(psum.Sum())
	c := db.C("system.users")
	_, err := c.Upsert(bson.M{"user": user}, bson.M{"$set": bson.M{"user": user, "pwd": digest, "readOnly": readOnly}})
	return err
}

// RemoveUser removes the authentication credentials of user from the database.
func (db Database) RemoveUser(user string) os.Error {
	c := db.C("system.users")
	return c.Remove(bson.M{"user": user})
}

type indexSpec struct {
	Name, NS       string
	Key            bson.D
	Unique         bool ",omitempty"
	DropDups       bool "dropDups,omitempty"
	Background     bool ",omitempty"
	Sparse         bool ",omitempty"
	Bits, Min, Max int  ",omitempty"
}

type Index struct {
	Key        []string // Index key fields; prefix name with dash (-) for descending order
	Unique     bool     // Prevent two documents from having the same index key
	DropDups   bool     // Drop documents with the same index key as a previously indexed one
	Background bool     // Build index in background and return immediately
	Sparse     bool     // Only index documents containing the Key fields

	Name string // Index name, computed by EnsureIndex

	Bits, Min, Max int // Properties for spatial indexes
}

func parseIndexKey(key []string) (name string, realKey bson.D, err os.Error) {
	var order interface{}
	for _, field := range key {
		if name != "" {
			name += "_"
		}
		if field != "" {
			switch field[0] {
			case '@':
				order = "2d"
				field = field[1:]
				name += field + "_" // Why don't they put 2d here?
			case '-':
				order = -1
				field = field[1:]
				name += field + "_-1"
			case '+':
				field = field[1:]
				fallthrough
			default:
				order = 1
				name += field + "_1"
			}
		}
		if field == "" {
			return "", nil, os.NewError("Invalid index key: empty field name")
		}
		realKey = append(realKey, bson.DocElem{field, order})
	}
	if name == "" {
		return "", nil, os.NewError("Invalid index key: no fields provided")
	}
	return
}

// EnsureIndexKey ensures an index with the given key exists, creating it
// if necessary.
//
// This example:
//
//     err := collection.EnsureIndexKey([]string{"a", "b"})
//
// Is equivalent to:
//
//     err := collection.EnsureIndex(mgo.Index{Key: []string{"a", "b"}})
//
// See the EnsureIndex method for more details.
func (c Collection) EnsureIndexKey(key []string) os.Error {
	return c.EnsureIndex(Index{Key: key})
}

// EnsureIndex ensures an index with the given key exists, creating it with
// the provided parameters if necessary.
//
// Once EnsureIndex returns successfully, following requests for the same index
// will not contact the server unless Collection.DropIndex is used to drop the
// same index, or Session.ResetIndexCache is called.
//
// For example:
//
//     index := Index{
//         Key: []string{"lastname", "firstname"},
//         Unique: true,
//         DropDups: true,
//         Background: true,
//         Sparse: true,
//     }
//     err := collection.EnsureIndex(index)
//
// The Key value determines which fields compose the index. The index ordering
// will be ascending by default.  To obtain an index with a descending order,
// the field name should be prefixed by a dash (e.g. []string{"-time"}).
//
// If Unique is true, the index must necessarily contain only a single
// document per Key.  With DropDups set to true, documents with the same key
// as a previously indexed one will be dropped rather than an error returned.
//
// If Background is true, the operation will return immediately and will
// continue in background.  The index won't be used for queries until the build
// is complete.
//
// If Sparse is true, only documents containing the provided Key fields will be
// included in the index.  When using a sparse index for sorting, only indexed
// documents will be returned.
//
// Spatial indexes are also supported through that API.  Here is an example:
//
//     index := Index{
//         Key: []string{"@loc"},
//         Bits: 26,
//     }
//     err := collection.EnsureIndex(index)
//
// The "@" prefix in the field name will request the creation of a "2d" index
// for the given field.
//
// The 2D index bounds may be changed using the Min and Max attributes of the
// Index value.  The default bound setting of (-180, 180) is suitable for
// latitude/longitude pairs.
//
// The Bits parameter sets the precision of the 2D geohash values.  If not
// provided, 26 bits are used, which is roughly equivalent to 1 foot of
// precision for the default (-180, 180) index bounds.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Indexes
//     http://www.mongodb.org/display/DOCS/Indexing+Advice+and+FAQ
//     http://www.mongodb.org/display/DOCS/Indexing+as+a+Background+Operation
//     http://www.mongodb.org/display/DOCS/Geospatial+Indexing
//     http://www.mongodb.org/display/DOCS/Multikeys
//
func (c Collection) EnsureIndex(index Index) os.Error {
	name, realKey, err := parseIndexKey(index.Key)
	if err != nil {
		return err
	}

	db := c.DB
	session := db.Session
	cacheKey := c.FullName + "\x00" + name
	if session.cluster().HasCachedIndex(cacheKey) {
		return nil
	}

	spec := indexSpec{
		Name:       name,
		NS:         c.FullName,
		Key:        realKey,
		Unique:     index.Unique,
		DropDups:   index.DropDups,
		Background: index.Background,
		Sparse:     index.Sparse,
		Bits:       index.Bits,
		Min:        index.Min,
		Max:        index.Max,
	}

	session = session.Clone()
	defer session.Close()
	session.SetMode(Strong, false)
	session.EnsureSafe(&Safe{})

	db.Session = session
	err = db.C("system.indexes").Insert(&spec)
	if err == nil {
		session.cluster().CacheIndex(cacheKey, true)
	}
	return err
}

// DropIndex removes the index with key from the collection.
//
// The key value determines which fields compose the index. The index ordering
// will be ascending by default.  To obtain an index with a descending order,
// the field name should be prefixed by a dash (e.g. []string{"-time"}).
//
// For example:
//
//     err := collection.DropIndex([]string{"lastname", "firstname"})
//
// See the EnsureIndex method for more details on indexes.
func (c Collection) DropIndex(key []string) os.Error {
	name, _, err := parseIndexKey(key)
	if err != nil {
		return err
	}

	db := c.DB
	session := db.Session
	cacheKey := c.FullName + "\x00" + name
	session.cluster().CacheIndex(cacheKey, false)

	session = session.Clone()
	defer session.Close()
	session.SetMode(Strong, false)

	db.Session = session
	defer db.Session.Close()
	result := struct {
		ErrMsg string
		Ok     bool
	}{}
	err = db.Run(bson.D{{"dropIndexes", c.Name}, {"index", name}}, &result)
	if err != nil {
		return err
	}
	if !result.Ok {
		return os.NewError(result.ErrMsg)
	}
	return nil
}

// Indexes returns a list of all indexes for the collection.
//
// For example, this snippet would drop all available indexes:
//
//   indexes, err := collection.Indexes()
//   if err != nil {
//       panic(err)
//   }
//   for _, index := range indexes {
//       err = collection.DropIndex(index.Key)
//       if err != nil {
//           panic(err)
//       }
//   }
//
// See the EnsureIndex method for more details on indexes.
func (c Collection) Indexes() (indexes []Index, err os.Error) {
	query := c.DB.C("system.indexes").Find(bson.M{"ns": c.FullName})
	iter := query.Sort(bson.D{{"name", 1}}).Iter()
	for {
		var spec indexSpec
		if !iter.Next(&spec) {
			break
		}
		index := Index{
			Name:       spec.Name,
			Key:        simpleIndexKey(spec.Key),
			Unique:     spec.Unique,
			DropDups:   spec.DropDups,
			Background: spec.Background,
			Sparse:     spec.Sparse,
		}
		indexes = append(indexes, index)
	}
	err = iter.Err()
	return
}

func simpleIndexKey(realKey bson.D) (key []string) {
	for i := range realKey {
		field := realKey[i].Name
		i, _ := realKey[i].Value.(int)
		if i == 1 {
			key = append(key, field)
			continue
		}
		if i == -1 {
			key = append(key, "-"+field)
			continue
		}
		s, _ := realKey[i].Value.(string)
		if s == "2d" {
			key = append(key, "@"+field)
			continue
		}
		panic("Got unknown index key type for field " + field)
	}
	return
}

// ResetIndexCache() clears the cache of previously ensured indexes.
// Following requests to EnsureIndex will contact the server.
func (s *Session) ResetIndexCache() {
	s.cluster().ResetIndexCache()
}

// New creates a new session with the same parameters as the original
// session, including consistency, batch size, prefetching, safety mode,
// etc. The returned session will use sockets from the poll, so there's
// a chance that writes just performed in another session may not yet
// be visible.
//
// Login information from the original session will not be copied over
// into the new session unless it was provided through the initial URL
// for the Mongo function.
//
// See the Copy and Clone methods.
//
func (s *Session) New() *Session {
	s.m.Lock()
	scopy := copySession(s, false)
	s.m.Unlock()
	scopy.Refresh()
	return scopy
}

// Copy works just like New, but preserves the exact authentication
// information from the original session.
func (s *Session) Copy() *Session {
	s.m.Lock()
	scopy := copySession(s, true)
	s.m.Unlock()
	scopy.Refresh()
	return scopy
}

// Clone works just like Copy, but also reuses the same socket as the original
// session, in case it had already reserved one due to its consistency
// guarantees.  This behavior ensures that writes performed in the old session
// are necessarily observed when using the new session, as long as it was a
// strong or monotonic session.  That said, it also means that long operations
// may cause other goroutines using the original session to wait.
func (s *Session) Clone() *Session {
	s.m.Lock()
	scopy := copySession(s, true)
	s.m.Unlock()
	return scopy
}

// Close terminates the session.  It's a runtime error to use a session
// after it has been closed.
func (s *Session) Close() {
	s.m.Lock()
	if s.cluster_ != nil {
		s.setSocket(nil)
		s.cluster_.Release()
		s.cluster_ = nil
	}
	s.m.Unlock()
}

func (s *Session) cluster() *mongoCluster {
	if s.cluster_ == nil {
		panic("Session already closed")
	}
	return s.cluster_
}

// Refresh puts back any reserved sockets in use and restarts the consistency
// guarantees according to the current consistency setting for the session.
func (s *Session) Refresh() {
	s.m.Lock()
	s.slaveOk = s.consistency != Strong
	s.setSocket(nil)
	s.m.Unlock()
}

// SetMode changes the consistency mode for the session.
//
// In the Strong consistency mode reads and writes will always be made to
// the master server using a unique connection so that reads and writes are
// fully consistent, ordered, and observing the most up-to-date data.
// This offers the least benefits in terms of distributing load, but the
// most guarantees.  See also Monotonic and Eventual.
//
// In the Monotonic consistency mode reads may not be entirely up-to-date,
// but they will always see the history of changes moving forward, the data
// read will be consistent across sequential queries in the same session,
// and modifications made within the session will be observed in following
// queries (read-your-writes).
//
// In practice, the Monotonic mode is obtained by performing initial reads
// against a unique connection to an arbitrary slave, if one is available,
// and once the first write happens, the session connection is switched over
// to the master server.  This manages to distribute some of the reading
// load with slaves, while maintaining some useful guarantees.
//
// In the Eventual consistency mode reads will be made to any slave in the
// cluster, if one is available, and sequential reads will not necessarily
// be made with the same connection.  This means that data may be observed
// out of order.  Writes will of course be issued to the master, but
// independent writes in the same Eventual session may also be made with
// independent connections, so there are also no guarantees in terms of
// write ordering (no read-your-writes guarantees either).
//
// The Eventual mode is the fastest and most resource-friendly, but is
// also the one offering the least guarantees about ordering of the data
// read and written.
//
// If refresh is true, in addition to ensuring the session is in the given
// consistency mode, the consistency guarantees will also be reset (e.g.
// a Monotonic session will be allowed to read from slaves again).  This is
// equivalent to calling the Refresh function.
//
// Shifting between Monotonic and Strong modes will keep a previously
// reserved connection for the session unless refresh is true or the
// connection is unsuitable (to a slave server in a Strong session).
func (s *Session) SetMode(consistency mode, refresh bool) {
	s.m.Lock()
	debugf("Session %p: setting mode %d with refresh=%v (socket=%p)", s, consistency, refresh, s.socket)
	s.consistency = consistency
	if refresh {
		s.slaveOk = s.consistency != Strong
		s.setSocket(nil)
	} else if s.consistency == Strong {
		s.slaveOk = false
	} else if s.socket == nil {
		s.slaveOk = true
	}
	s.m.Unlock()
}

// Mode returns the current consistency mode for the session.
func (s *Session) Mode() mode {
	s.m.RLock()
	mode := s.consistency
	s.m.RUnlock()
	return mode
}

// SetSyncTimeout sets the amount of time an operation with this session
// will wait before returning an error in case a connection to a usable
// server can't be established. Set it to zero to wait forever. This is
// the default.
func (s *Session) SetSyncTimeout(nsec int64) {
	s.m.Lock()
	s.syncTimeout = nsec
	s.m.Unlock()
}

// SetBatch sets the default batch size used when fetching documents from the
// database. It's possible to change this setting on a per-query basis as
// well, using the Query.Batch method.
//
// The default batch size is defined by the database itself.  As of this
// writing, MongoDB will use an initial size of min(100 docs, 4MB) on the
// first batch, and 4MB on remaining ones.
func (s *Session) SetBatch(n int) {
	s.m.Lock()
	s.queryConfig.op.limit = int32(n)
	s.m.Unlock()
}

// SetPrefetch sets the default point at which the next batch of results will be
// requested.  When there are p*batch_size remaining documents cached in an
// Iter, the next batch will be requested in background. For instance, when
// using this:
//
//     session.SetBatch(200)
//     session.SetPrefetch(0.25)
//
// and there are only 50 documents cached in the Iter to be processed, the
// next batch of 200 will be requested. It's possible to change this setting on
// a per-query basis as well, using the Prefetch method of Query.
//
// The default prefetch value is 0.25.
func (s *Session) SetPrefetch(p float64) {
	s.m.Lock()
	s.queryConfig.prefetch = p
	s.m.Unlock()
}

// See SetSafe for details on the Safe type.
type Safe struct {
	W        int    // Min # of servers to ack before success
	WMode    string // Write mode for MongoDB 2.0+ (e.g. "majority")
	WTimeout int    // Milliseconds to wait for W before timing out
	FSync    bool   // Should servers sync to disk before returning success
	J        bool   // Wait for next group commit if journaling; no effect otherwise
}

// Safe returns the current safety mode for the session.
func (s *Session) Safe() (safe *Safe) {
	s.m.Lock()
	defer s.m.Unlock()
	if s.safeOp != nil {
		cmd := s.safeOp.query.(*getLastError)
		safe = &Safe{WTimeout: cmd.WTimeout, FSync: cmd.FSync, J: cmd.J}
		switch w := cmd.W.(type) {
		case string:
			safe.WMode = w
		case int:
			safe.W = w
		}
	}
	return
}

// SetSafe changes the session safety mode.
//
// If the safe parameter is nil, the session is put in unsafe mode, and writes
// become fire-and-forget, without error checking.  The unsafe mode is faster
// since operations won't hold on waiting for a confirmation.
//
// If the safe parameter is not nil, any changing query (insert, update, ...)
// will be followed by a getLastError command with the specified parameters,
// to ensure the request was correctly processed.
//
// The safe.W parameter determines how many servers should confirm a write
// before the operation is considered successful.  If set to 0 or 1, the
// command will return as soon as the master is done with the request.
// If safe.WTimeout is greater than zero, it determines how many milliseconds
// to wait for the safe.W servers to respond before returning an error.
//
// Starting with MongoDB 2.0.0 the safe.WMode parameter can be used instead
// of W to request for richer semantics. If set to "majority" the server will
// wait for a majority of members from the replica set to respond before
// returning. Custom modes may also be defined within the server to create
// very detailed placement schemas. See the data awareness documentation in
// the links below for more details (note that MongoDB internally reuses the
// "w" field name for WMode).
//
// If safe.FSync is true and journaling is disabled, the servers will be
// forced to sync all files to disk immediately before returning. If the
// same option is true but journaling is enabled, the server will instead
// await for the next group commit before returning.
//
// Since MongoDB 2.0.0, the safe.J option can also be used instead of FSync
// to force the server to wait for a group commit in case journaling is
// enabled. The option has no effect if the server has journaling disabled.
//
// For example, the following statement will make the session check for
// errors, without imposing further constraints:
//
//     session.SetSafe(&mgo.Safe{})
//
// The following statement will force the server to wait for a majority of
// members of a replica set to return (MongoDB 2.0+ only):
//
//     session.SetSafe(&mgo.Safe{WMode: "majority"})
//
// The following statement, on the other hand, ensures that at least two
// servers have flushed the change to disk before confirming the success
// of operations:
//
//     session.EnsureSafe(&mgo.Safe{W: 2, FSync: true})
//
// The following statement, on the other hand, disables the verification
// of errors entirely:
//
//     session.SetSafe(nil)
//
// See also the EnsureSafe method.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/getLastError+Command
//     http://www.mongodb.org/display/DOCS/Verifying+Propagation+of+Writes+with+getLastError
//     http://www.mongodb.org/display/DOCS/Data+Center+Awareness
//
func (s *Session) SetSafe(safe *Safe) {
	s.m.Lock()
	s.safeOp = nil
	s.ensureSafe(safe)
	s.m.Unlock()
}

// EnsureSafe compares the provided safety parameters with the ones
// currently in use by the session and picks the most conservative
// choice for each setting.
//
// That is:
//
//     - safe.WMode is always used if set.
//     - safe.W is used if larger than the current W and WMode is empty.
//     - safe.FSync is always used if true.
//     - safe.J is used if FSync is false.
//     - safe.WTimeout is used if set and smaller than the current WTimeout.
//
// For example, the following statement will ensure the session is
// at least checking for errors, without enforcing further constraints.
// If a more conservative SetSafe or EnsureSafe call was previously done,
// the following call will be ignored.
//
//     session.EnsureSafe(&mgo.Safe{})
//
// See also the SetSafe method for details on what each option means.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/getLastError+Command
//     http://www.mongodb.org/display/DOCS/Verifying+Propagation+of+Writes+with+getLastError
//     http://www.mongodb.org/display/DOCS/Data+Center+Awareness
//
func (s *Session) EnsureSafe(safe *Safe) {
	s.m.Lock()
	s.ensureSafe(safe)
	s.m.Unlock()
}

func (s *Session) ensureSafe(safe *Safe) {
	if safe == nil {
		return
	}

	var w interface{}
	if safe.WMode != "" {
		w = safe.WMode
	} else if safe.W > 0 {
		w = safe.W
	}

	var cmd getLastError
	if s.safeOp == nil {
		cmd = getLastError{1, w, safe.WTimeout, safe.FSync, safe.J}
	} else {
		// Copy.  We don't want to mutate the existing query.
		cmd = *(s.safeOp.query.(*getLastError))
		if cmd.W == nil {
			cmd.W = w
		} else if safe.WMode != "" {
			cmd.W = safe.WMode
		} else if i, ok := cmd.W.(int); ok && safe.W > i {
			cmd.W = safe.W
		}
		if safe.WTimeout > 0 && safe.WTimeout < cmd.WTimeout {
			cmd.WTimeout = safe.WTimeout
		}
		if safe.FSync {
			cmd.FSync = true
			cmd.J = false
		} else if safe.J && !cmd.FSync {
			cmd.J = true
		}
	}
	s.safeOp = &queryOp{
		query:      &cmd,
		collection: "admin.$cmd",
		limit:      -1,
	}
}

// Run issues the provided command against the "admin" database and
// and unmarshals its result in the respective argument. The cmd
// argument may be either a string with the command name itself, in
// which case an empty document of the form bson.M{cmd: 1} will be used,
// or it may be a full command document.
//
// Note that MongoDB considers the first marshalled key as the command
// name, so when providing a command with options, it's important to
// use an ordering-preserving document, such as a struct value or an
// instance of bson.D.  For instance:
//
//     db.Run(bson.D{{"create", "mycollection"}, {"size", 1024}})
//
// For commands against arbitrary databases, see the Run method in
// the Database type.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Commands
//     http://www.mongodb.org/display/DOCS/List+of+Database+CommandSkips
//
func (s *Session) Run(cmd interface{}, result interface{}) os.Error {
	return s.DB("admin").Run(cmd, result)
}

// Ping runs a trivial ping command just to get in touch with the server.
func (s *Session) Ping() os.Error {
	result := struct{}{} // We don't care.
	return s.Run("ping", &result)
}

// Find prepares a query using the provided document.  The document may be a
// map or a struct value capable of being marshalled with bson.  The map
// may be a generic one using interface{} for its key and/or values, such as
// bson.M, or it may be a properly typed map.  Providing nil as the document
// is equivalent to providing an empty document such as bson.M{}.
//
// Further details of the query may be tweaked using the resulting Query value,
// and then executed to retrieve results using methods such as One, For,
// Iter, or Tail.
//
// In case the resulting document includes a field named $err or errmsg, which
// are standard ways for MongoDB to return query errors, the returned err will
// be set to a *QueryError value including the Err message and the Code.  In
// those cases, the result argument is still unmarshalled into with the
// received document so that any other custom values may be obtained if
// desired.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Querying
//     http://www.mongodb.org/display/DOCS/Advanced+Queries
//
func (c Collection) Find(query interface{}) *Query {
	session := c.DB.Session
	session.m.RLock()
	q := &Query{session: session, query: session.queryConfig}
	session.m.RUnlock()
	q.op.query = query
	q.op.collection = c.FullName
	return q
}

type LastError struct {
	Err             string
	Code, N, Waited int
	FSyncFiles      int "fsyncFiles"
	WTimeout        bool
	Updated         bool        "updatedExisting"
	UpsertedId      interface{} "upserted"
}

func (err *LastError) String() string {
	return err.Err
}

type queryError struct {
	Err           string "$err"
	ErrMsg        string
	Assertion     string
	Code          int
	AssertionCode int "assertionCode"
}

type QueryError struct {
	Code      int
	Message   string
	Assertion bool
}

func (err *QueryError) String() string {
	return err.Message
}

// Insert inserts one or more documents in the respective collection.  In
// case the session is in safe mode (see the SetSafe method) and an error
// happens while inserting the provided documents, the returned error will
// be of type *LastError.
func (c Collection) Insert(docs ...interface{}) os.Error {
	_, err := c.DB.Session.writeQuery(&insertOp{c.FullName, docs})
	return err
}

// Update finds a single document matching the provided selector document
// and modifies it according to the change document.  In case the session
// is in safe mode (see the SetSafe method) a getLastError command will
// follow the update request and NotFound will be returned in case no
// documents are updated, or a value of type *LastError in case some other
// error is detected.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Updating
//     http://www.mongodb.org/display/DOCS/Atomic+Operations
//
func (c Collection) Update(selector interface{}, change interface{}) os.Error {
	lerr, err := c.DB.Session.writeQuery(&updateOp{c.FullName, selector, change, 0})
	if err == nil && lerr != nil && !lerr.Updated {
		return NotFound
	}
	return err

}

type idType struct {
	Id interface{} "_id"
}

// UpdateAll finds all documents matching the provided selector document
// and modifies them according to the change document.  In case the session
// is in safe mode (see the SetSafe method) a getLastError command will
// follow the update request and NotFound will be returned in case no
// documents are updated, or a value of type *LastError in case some other
// error is detected.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Updating
//     http://www.mongodb.org/display/DOCS/Atomic+Operations
//
func (c Collection) UpdateAll(selector interface{}, change interface{}) os.Error {
	lerr, err := c.DB.Session.writeQuery(&updateOp{c.FullName, selector, change, 2})
	if err == nil && lerr != nil && !lerr.Updated {
		return NotFound
	}
	return err
}

// Upsert finds a single document matching the provided selector document
// and modifies it according to the change document.  If no document matching
// the selector is found, the change document is newly inserted and the
// returned id value is set to the id of the inserted document.  In case the
// session is in safe mode (see the SetSafe method) a getLastError command will
// follow the upsert request and a value of type *LastError in case some error
// is detected.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Updating
//     http://www.mongodb.org/display/DOCS/Atomic+Operations
//
func (c Collection) Upsert(selector interface{}, change interface{}) (id interface{}, err os.Error) {
	data, err := bson.Marshal(change)
	if err != nil {
		return nil, err
	}
	change = bson.Raw{0x03, data}
	lerr, err := c.DB.Session.writeQuery(&updateOp{c.FullName, selector, change, 1})
	if lerr != nil {
		id = lerr.UpsertedId
		if id == nil && !lerr.Updated {
			// Sad. Will unmarhsal the document to make the interface even.
			var doc idType
			err = bson.Unmarshal(data, &doc)
			if err != nil {
				return nil, err
			}
			id = doc.Id
		}
	}
	return id, err
}

// Remove finds a single document matching the provided selector document
// and removes it from the database.  In case the session is in safe mode
// (see the SetSafe method) and an error happens when attempting the change,
// the returned error will be of type *LastError.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Removing
//
func (c Collection) Remove(selector interface{}) os.Error {
	_, err := c.DB.Session.writeQuery(&deleteOp{c.FullName, selector, 1})
	return err
}

// RemoveAll finds all documents matching the provided selector document
// and removes them from the database.  In case the session is in safe mode
// (see the SetSafe method) and an error happens when attempting the change,
// the returned error will be of type *LastError.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Removing
//
func (c Collection) RemoveAll(selector interface{}) os.Error {
	_, err := c.DB.Session.writeQuery(&deleteOp{c.FullName, selector, 0})
	return err
}

// DropDatabase removes the entire database including all of its collections.
func (db Database) DropDatabase() os.Error {
	return db.Run(bson.D{{"dropDatabase", 1}}, nil)
}

// DropCollection removes the entire collection including all of its documents.
func (c Collection) DropCollection() os.Error {
	return c.DB.Run(bson.D{{"drop", c.Name}}, nil)
}

// Batch sets the batch size used when fetching documents from the database.
// It's possible to change this setting on a per-session basis as well, using
// the Batch method of Session.
//
// The default batch size is defined by the database itself.  As of this
// writing, MongoDB will use an initial size of min(100 docs, 4MB) on the
// first batch, and 4MB on remaining ones.
func (q *Query) Batch(n int) *Query {
	q.m.Lock()
	q.op.limit = int32(n)
	q.m.Unlock()
	return q
}

// Prefetch sets the point at which the next batch of results will be requested.
// When there are p*batch_size remaining documents cached in an Iter, the next
// batch will be requested in background. For instance, when using this:
//
//     query.Batch(200).Prefetch(0.25)
//
// and there are only 50 documents cached in the Iter to be processed, the
// next batch of 200 will be requested. It's possible to change this setting on
// a per-session basis as well, using the SetPrefetch method of Session.
//
// The default prefetch value is 0.25.
func (q *Query) Prefetch(p float64) *Query {
	q.m.Lock()
	q.prefetch = p
	q.m.Unlock()
	return q
}

// Skip skips over the n initial documents from the query results.  Note that
// this only makes sense with capped collections where documents are naturally
// ordered by insertion time, or with sorted results.
func (q *Query) Skip(n int) *Query {
	q.m.Lock()
	q.op.skip = int32(n)
	q.m.Unlock()
	return q
}

// Limit restricts the maximum number of documents retrieved to n, and also
// changes the batch size to the same value.  Once n documents have been
// returned by Next, the following call will return NotFound.
func (q *Query) Limit(n int) *Query {
	q.m.Lock()
	q.limit = int32(n)
	q.op.limit = -int32(n)
	q.m.Unlock()
	return q
}

// Select enables selecting which fields should be retrieved for the results
// found. For example, the following query would only retrieve the name field:
//
//     err := collection.Find(nil).Select(bson.M{"name": 1}).One(&result)
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Retrieving+a+Subset+of+Fields
//
func (q *Query) Select(selector interface{}) *Query {
	q.m.Lock()
	q.op.selector = selector
	q.m.Unlock()
	return q
}

type queryWrapper struct {
	Query   interface{} "$query"
	OrderBy interface{} "$orderby,omitempty"
	Hint    interface{} "$hint,omitempty"
	Explain bool        "$explain,omitempty"
}

func (q *Query) wrap() *queryWrapper {
	w, ok := q.op.query.(*queryWrapper)
	if !ok {
		if q.op.query == nil {
			var empty bson.D
			w = &queryWrapper{Query: empty}
		} else {
			w = &queryWrapper{Query: q.op.query}
		}
		q.op.query = w
	}
	return w
}

// Sort asks the database to order returned documents according to the rules
// provided in the given document.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Sorting+and+Natural+Order
//
func (q *Query) Sort(order interface{}) *Query {
	q.m.Lock()
	w := q.wrap()
	w.OrderBy = order
	q.m.Unlock()
	return q
}

// Explain returns a number of details about how the MongoDB server would
// execute the requested query, such as the number of objects examined,
// the number of time the read lock was yielded to allow writes to go in,
// and so on.
//
// For example:
//
//     m := bson.M{}
//     err := collection.Find(bson.M{"filename": name}).Explain(m)
//     if err == nil {
//         fmt.Printf("Explain: %#v\n", m)
//     }
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Optimization
//     http://www.mongodb.org/display/DOCS/Query+Optimizer
//     
func (q *Query) Explain(result interface{}) os.Error {
	q.m.Lock()
	clone := &Query{session: q.session, query: q.query}
	q.m.Unlock()
	w := clone.wrap()
	w.Explain = true
	if clone.op.limit > 0 {
		clone.op.limit = -q.op.limit
	}
	iter := clone.Iter()
	if iter.Next(result) {
		return nil
	}
	return iter.Err()
}

// Hint will include an explicit "hint" in the query to force the server
// to use a specified index, potentially improving performance in some
// situations.  The indexKey parameter must be set to the key of the
// index to be used.  For details on how the indexKey may be built, see
// the EnsureIndex method.
//
// For example:
//
//     query := collection.Find(bson.M{"a": 4, "b": 5, "c": 6}).Hint([]string{"a", "b"})
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Optimization
//     http://www.mongodb.org/display/DOCS/Query+Optimizer
//     
func (q *Query) Hint(indexKey []string) *Query {
	q.m.Lock()
	_, realKey, err := parseIndexKey(indexKey)
	w := q.wrap()
	w.Hint = realKey
	q.m.Unlock()
	if err != nil {
		panic(err)
	}
	return q
}

func checkQueryError(d []byte) os.Error {
	found := false
	l := len(d)
	for i := 0; i < l; i++ {
		if d[i] != '\x02' || l-i < 6 {
			continue
		}
		if d[i+1] == '$' && d[i+2] == 'e' && d[i+3] == 'r' && d[i+4] == 'r' && d[i+5] == '\x00' {
			found = true
			break
		}
		if l-i < 8 {
			continue
		}
		if d[i+1] == 'e' && d[i+2] == 'r' && d[i+3] == 'r' && d[i+4] == 'm' && d[i+5] == 's' && d[i+6] == 'g' && d[i+7] == '\x00' {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	result := &queryError{}
	bson.Unmarshal(d, result)
	if result.Err == "" && result.ErrMsg == "" {
		return nil
	}
	if result.AssertionCode != 0 && result.Assertion != "" {
		return &QueryError{Code: result.AssertionCode, Message: result.Assertion, Assertion: true}
	}
	if result.Err != "" {
		return &QueryError{Code: result.Code, Message: result.Err}
	}
	return &QueryError{Code: result.Code, Message: result.ErrMsg}
}

// One executes the query and unmarshals the first obtained document into the
// result argument.  The result must be a struct or map value capable of being
// unmarshalled into by gobson.  This function blocks until either a result
// is available or an error happens.  For example:
//
//     err := collection.Find(bson.M{"a", 1}).One(&result)
//
// In case the resulting document includes a field named $err or errmsg, which
// are standard ways for MongoDB to return query errors, the returned err will
// be set to a *QueryError value including the Err message and the Code.  In
// those cases, the result argument is still unmarshalled into with the
// received document so that any other custom values may be obtained if
// desired.
//
func (q *Query) One(result interface{}) (err os.Error) {
	q.m.Lock()
	session := q.session
	op := q.op // Copy.
	q.m.Unlock()

	socket, err := session.acquireSocket(true)
	if err != nil {
		return err
	}
	defer socket.Release()

	op.flags |= session.slaveOkFlag()
	op.limit = -1

	data, err := socket.SimpleQuery(&op)
	if err != nil {
		return err
	}
	if data == nil {
		return NotFound
	}
	if result == nil {
		return nil
	}

	err = bson.Unmarshal(data, result)
	if err == nil {
		debugf("Query %p document unmarshaled: %#v", q, result)
	} else {
		debugf("Query %p document unmarshaling failed: %#v", q, err)
		return err
	}

	return checkQueryError(data)
}

// The DBRef type implements support for the database reference MongoDB
// convention as supported by multiple drivers.  This convention enables
// cross-referencing documents between collections and databases using
// a structure which includes a collection name, a document id, and
// optionally a database name.
//
// See the FindRef methods on Session and on Database.
// 
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Database+References
//
type DBRef struct {
	C  string      `bson:"$ref"`
	ID interface{} `bson:"$id"`
	DB string      `bson:"$db,omitempty"`
}

type id struct {
	Id interface{} "_id"
}

// FindRef retrieves the document in the provided reference and stores it
// in result.  If the reference includes the DB field, the document will
// be retrieved from the respective database.
//
// See also the DBRef type and the FindRef method on Session.
//
// Relevant documentation:
// 
//     http://www.mongodb.org/display/DOCS/Database+References
//
func (db Database) FindRef(ref DBRef, result interface{}) os.Error {
	if ref.DB == "" {
		return db.C(ref.C).Find(id{ref.ID}).One(result)
	}
	return db.Session.DB(ref.DB).C(ref.C).Find(id{ref.ID}).One(result)
}

// FindRef retrieves the document in the provided reference and stores it
// in result.  For a DBRef to be resolved correctly at the session level
// it must necessarily have the optional DB field defined.
//
// See also the DBRef type and the FindRef method on Database.
//
// Relevant documentation:
// 
//     http://www.mongodb.org/display/DOCS/Database+References
//
func (s *Session) FindRef(ref DBRef, result interface{}) os.Error {
	if ref.DB == "" {
		return os.NewError(fmt.Sprintf("Can't resolve database for %#v", ref))
	}
	return s.DB(ref.DB).C(ref.C).Find(id{ref.ID}).One(result)
}

// CollectionNames returns the collection names present in database.
func (db Database) CollectionNames() (names []string, err os.Error) {
	c := len(db.Name) + 1
	var result *struct{ Name string }
	err = db.C("system.namespaces").Find(nil).For(&result, func() os.Error {
		if strings.Index(result.Name, "$") < 0 || strings.Index(result.Name, ".oplog.$") >= 0 {
			names = append(names, result.Name[c:])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.StringSlice(names).Sort()
	return names, nil
}

type dbNames struct {
	Databases []struct {
		Name  string
		Empty bool
	}
}

// DatabaseNames returns the names of non-empty databases present in the cluster.
func (s *Session) DatabaseNames() (names []string, err os.Error) {
	var result dbNames
	err = s.Run("listDatabases", &result)
	if err != nil {
		return nil, err
	}
	for _, db := range result.Databases {
		if !db.Empty {
			names = append(names, db.Name)
		}
	}
	return names, nil
}

// Iter executes the query and returns an iterator capable of going over all
// the results. Results will be returned in batches of configurable
// size (see the Batch method) and more documents will be requested when a
// configurable number of documents is iterated over (see the Prefetch method).
func (q *Query) Iter() *Iter {
	q.m.Lock()
	session := q.session
	op := q.op
	prefetch := q.prefetch
	limit := q.limit
	q.m.Unlock()

	iter := &Iter{session: session, prefetch: prefetch, limit: limit}
	iter.gotReply.L = &iter.m
	iter.op.collection = op.collection
	iter.op.limit = op.limit
	iter.op.replyFunc = iter.replyFunc()
	iter.pendingDocs++
	op.replyFunc = iter.op.replyFunc
	op.flags |= session.slaveOkFlag()

	socket, err := session.acquireSocket(true)
	if err != nil {
		iter.err = err
	} else {
		iter.err = socket.Query(&op)
		socket.Release()
	}
	return iter
}

// Tail returns a tailable iterator. Unlike a normal iterator, a
// tailable iterator may wait for new values to be inserted in the
// collection once the end of the current result set is reached,
// A tailable iterator may only be used with capped collections.
//
// The timeoutSecs parameter indicates how long Next will block
// waiting for a result before timing out.  If set to -1, Next will
// not timeout, and will continue waiting for a result for as long
// as the cursor is valid and the session is not closed. If set
// to 0, Next times out as soon as it reaches the end of the result
// set. Otherwise, Next will wait for at least the given number of
// seconds for a new document to be available before timing out.
//
// On timeouts, Next will unblock and return false, and the Timeout
// method will return true if called. In these cases, Next may still
// be called again on the same iterator to check if a new value is
// available at the current cursor position, and again it will block
// according to the specified timeoutSecs. If the cursor becomes
// invalid, though, both Next and Timeout will return false and
// the query must be restarted.
//
// The following example demonstrates timeout handling and query
// restarting:
//
//    iter := collection.Find(nil).Sort("$natural").Tail(5)
//    for {
//         for iter.Next(&result) {
//             fmt.Println(result.Id)
//             lastId = result.Id
//         }
//         if iter.Err() != nil {
//             panic(err)
//         }
//         if iter.Timeout() {
//             continue
//         }
//         query := collection.Find(bson.M{"_id", bson.M{"$gt", lastId}})
//         iter = query.Sort("$natural").Tail(5)
//    }
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Tailable+Cursors
//     http://www.mongodb.org/display/DOCS/Capped+Collections
//     http://www.mongodb.org/display/DOCS/Sorting+and+Natural+Order
//
func (q *Query) Tail(timeoutSecs int) *Iter {
	q.m.Lock()
	session := q.session
	op := q.op
	prefetch := q.prefetch
	q.m.Unlock()

	iter := &Iter{session: session, prefetch: prefetch}
	iter.gotReply.L = &iter.m
	iter.timeout = timeoutSecs
	iter.op.collection = op.collection
	iter.op.limit = op.limit
	iter.op.replyFunc = iter.replyFunc()
	iter.pendingDocs++
	op.replyFunc = iter.op.replyFunc
	op.flags |= 2 | 32 | session.slaveOkFlag() // Tailable | AwaitData [| SlaveOk]

	socket, err := session.acquireSocket(true)
	if err != nil {
		iter.err = err
	} else {
		iter.err = socket.Query(&op)
		socket.Release()
	}
	return iter
}

func (s *Session) slaveOkFlag() (flag uint32) {
	s.m.RLock()
	if s.slaveOk {
		flag = 4
	}
	s.m.RUnlock()
	return
}

// Err returns nil if no errors happened during iteration, or the actual
// error otherwise.
//
// In case a resulting document included a field named $err or errmsg, which are
// standard ways for MongoDB to report an improper query, the returned value has
// a *QueryError type, and includes the Err message and the Code.
func (iter *Iter) Err() os.Error {
	iter.m.Lock()
	err := iter.err
	iter.m.Unlock()
	if err == NotFound {
		return nil
	}
	return err
}

// Timeout returns true if Next returned false due to a timeout of
// a tailable cursor. In those cases, Next may be called again to continue
// the iteration at the previous cursor position.
func (iter *Iter) Timeout() bool {
	iter.m.Lock()
	result := iter.timedout
	iter.m.Unlock()
	return result
}

// Next retrieves the next document from the result set, blocking if necessary.
// This method will also automatically retrieve another batch of documents from
// the server when the current one is exhausted, or before that in background
// if pre-fetching is enabled (see the Query.Prefetch and Session.SetPrefetch
// methods).
//
// Next returns true if a document was successfully unmarshalled onto result,
// and false at the end of the result set or if an error happened.
// When Next returns false, the Err method should be called to verify if
// there was an error during iteration.
//
// For example:
//
//    iter := collection.Find(nil).Iter()
//    for iter.Next(&result) {
//        println(result.Id)
//    }
//    if iter.Err() != nil {
//        panic(iter.Err())
//    }
//
func (iter *Iter) Next(result interface{}) bool {
	timeout := int64(-1)
	if iter.timeout >= 0 {
		timeout = time.Nanoseconds() + int64(iter.timeout)*1e9
	}

	iter.m.Lock()
	iter.timedout = false
	for iter.err == nil && iter.docData.Len() == 0 && (iter.pendingDocs > 0 || iter.op.cursorId != 0) {
		if iter.pendingDocs == 0 && iter.op.cursorId != 0 {
			// Tailable cursor exhausted.
			if timeout >= 0 && time.Nanoseconds() > timeout {
				iter.timedout = true
				iter.m.Unlock()
				return false
			}
			iter.getMore()
		}
		iter.gotReply.Wait()
	}

	// Exhaust available data before reporting any errors.
	if docData, ok := iter.docData.Pop().([]byte); ok {
		iter.limit--
		if iter.limit == 0 {
			// XXX Must kill the cursor here.
			iter.err = NotFound
		}
		if iter.op.cursorId != 0 && iter.err == nil {
			iter.docsBeforeMore--
			if iter.docsBeforeMore == 0 {
				iter.getMore()
			}
		}
		iter.m.Unlock()
		err := bson.Unmarshal(docData, result)
		if err != nil {
			debugf("Iter %p document unmarshaling failed: %#v", iter, err)
			iter.err = err
			return false
		}
		debugf("Iter %p document unmarshaled: %#v", iter, result)
		// XXX Only have to check first document for a query error.
		err = checkQueryError(docData)
		if err != nil {
			iter.err = err
			return false
		}
		return true
	} else if iter.err != nil {
		debugf("Iter %p returning false: %s", iter, iter.err)
		iter.m.Unlock()
		return false
	} else if iter.op.cursorId == 0 {
		iter.err = NotFound
		debugf("Iter %p exhausted with cursor=0", iter)
		iter.m.Unlock()
		return false
	}

	panic("unreachable")
}

// All retrieves all documents from the result set into the provided slice.
//
// The result argument must necessarily be the address for a slice. The slice
// may be nil or previously allocated.
//
// WARNING: Obviously, All must not be used with result sets that may be
// potentially large, since it may consume all memory until the system
// crashes. Consider building the query with a Limit clause to ensure the
// result size is bounded.
// 
// For instance:
//
//    var result []struct{ Value int }
//    iter := collection.Find(nil).Limit(100).Iter()
//    err := iter.All(&result)
//    if err != nil {
//        panic(iter.Err())
//    }
//
func (iter *Iter) All(result interface{}) os.Error {
	resultv := reflect.ValueOf(result)
	if resultv.Kind() != reflect.Ptr || resultv.Elem().Kind() != reflect.Slice {
		panic("result argument must be a slice address")
	}
	slicev := resultv.Elem()
	slicev = slicev.Slice(0, slicev.Cap())
	elemt := slicev.Type().Elem()
	i := 0
	for {
		if slicev.Len() == i {
			elemp := reflect.New(elemt)
			if !iter.Next(elemp.Interface()) {
				break
			}
			slicev = reflect.Append(slicev, elemp.Elem())
			slicev = slicev.Slice(0, slicev.Cap())
		} else {
			if !iter.Next(slicev.Index(i).Addr().Interface()) {
				break
			}
		}
		i++
	}
	resultv.Elem().Set(slicev.Slice(0, i))
	return iter.Err()
}

// All calls All on an Iter for query. See Iter.All.
func (q *Query) All(result interface{}) os.Error {
	return q.Iter().All(result)
}

// The For method is obsolete and will be removed in a future release.
// See Iter as an elegant replacement.
func (q *Query) For(result interface{}, f func() os.Error) os.Error {
	return q.Iter().For(result, f)
}

// The For method is obsolete and will be removed in a future release.
// See Iter as an elegant replacement.
func (iter *Iter) For(result interface{}, f func() os.Error) (err os.Error) {
	valid := false
	v := reflect.ValueOf(result)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
		switch v.Kind() {
		case reflect.Map, reflect.Ptr, reflect.Interface, reflect.Slice:
			valid = v.IsNil()
		}
	}
	if !valid {
		panic("For needs a pointer to nil reference value.  See the documentation.")
	}
	zero := reflect.Zero(v.Type())
	for {
		v.Set(zero)
		if !iter.Next(result) {
			break
		}
		err = f()
		if err != nil {
			return err
		}
	}
	return iter.Err()
}

func (iter *Iter) getMore() {
	socket, err := iter.session.acquireSocket(true)
	if err != nil {
		iter.err = err
		return
	}
	defer socket.Release()

	debugf("Iter %p requesting more documents", iter)
	iter.pendingDocs++
	if iter.limit > 0 && iter.op.limit > iter.limit {
		iter.op.limit = -iter.limit
	}
	err = socket.Query(&iter.op)
	if err != nil {
		iter.err = err
	}
}

type countCmd struct {
	Count string
	Query interface{}
	Limit int32 ",omitempty"
	Skip  int32 ",omitempty"
}

// Count returns the total number of documents in the result set.
func (q *Query) Count() (n int, err os.Error) {
	q.m.Lock()
	session := q.session
	op := q.op
	limit := q.limit
	q.m.Unlock()

	c := strings.Index(op.collection, ".")
	if c < 0 {
		return 0, os.NewError("Bad collection name: " + op.collection)
	}

	dbname := op.collection[:c]
	cname := op.collection[c+1:]

	qdoc := op.query
	if wrapper, ok := qdoc.(*queryWrapper); ok {
		qdoc = wrapper.Query
	}

	result := struct{ N int }{}
	err = session.DB(dbname).Run(countCmd{cname, qdoc, limit, op.skip}, &result)
	return result.N, err
}

// Count returns the total number of documents in the collection.
func (c Collection) Count() (n int, err os.Error) {
	return c.Find(nil).Count()
}

type distinctCmd struct {
	Collection string "distinct"
	Key        string
	Query      interface{} ",omitempty"
}

// Distinct returns a list of distinct values for the given key within
// the result set.  The list of distinct values will be unmarshalled
// in the "values" key of the provided result parameter.
//
// For example:
//
//     var result []int
//     err := collection.Find(bson.M{"gender": "F"}).Distinct("age", &result)
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/Aggregation
//
func (q *Query) Distinct(key string, result interface{}) os.Error {
	q.m.Lock()
	session := q.session
	op := q.op // Copy.
	q.m.Unlock()

	c := strings.Index(op.collection, ".")
	if c < 0 {
		return os.NewError("Bad collection name: " + op.collection)
	}

	dbname := op.collection[:c]
	cname := op.collection[c+1:]

	qdoc := op.query
	if wrapper, ok := qdoc.(*queryWrapper); ok {
		qdoc = wrapper.Query
	}

	var doc struct{ Values bson.Raw }
	err := session.DB(dbname).Run(distinctCmd{cname, key, qdoc}, &doc)
	if err != nil {
		return err
	}
	return doc.Values.Unmarshal(result)
}

type mapReduceCmd struct {
	Collection string "mapreduce"
	Map        string ",omitempty"
	Reduce     string ",omitempty"
	Finalize   string ",omitempty"
	Limit      int32  ",omitempty"
	Out        interface{}
	Query      interface{} ",omitempty"
	Sort       interface{} ",omitempty"
	Scope      interface{} ",omitempty"
	Verbose    bool        ",omitempty"
}

type mapReduceResult struct {
	Results    bson.Raw
	Result     bson.Raw
	TimeMillis int64 "timeMillis"
	Counts     struct{ Input, Emit, Output int }
	Ok         bool
	Err        string
	Timing     *MapReduceTime
}

type MapReduce struct {
	Map      string      // Map Javascript function code (required)
	Reduce   string      // Reduce Javascript function code (required)
	Finalize string      // Finalize Javascript function code (optional)
	Out      interface{} // Output collection name or document. If nil, results are inlined into the result parameter.
	Scope    interface{} // Optional global scope for Javascript functions
	Verbose  bool
}

type MapReduceInfo struct {
	InputCount  int            // Number of documents mapped
	EmitCount   int            // Number of times reduce called emit
	OutputCount int            // Number of documents in resulting collection
	Database    string         // Output database, if results are not inlined
	Collection  string         // Output collection, if results are not inlined
	Time        int64          // Time to run the job, in nanoseconds
	VerboseTime *MapReduceTime // Only defined if Verbose was true
}

type MapReduceTime struct {
	Total    int64 // Total time, in nanoseconds
	Map      int64 "mapTime"  // Time within map function, in nanoseconds
	EmitLoop int64 "emitLoop" // Time within the emit/map loop, in nanoseconds
}

// MapReduce executes a map/reduce job for documents covered by the query.
// That kind of job is suitable for very flexible bulk aggregation of data
// performed at the server side via Javascript functions.
//
// Results from the job may be returned as a result of the query itself
// through the result parameter in case they'll certainly fit in memory
// and in a single document.  If there's the possibility that the amount
// of data might be too large, results must be stored back in an alternative
// collection or even a separate database, by setting the Out field of the
// provided MapReduce job.  In that case, provide nil as the result parameter.
//
// These are some of the ways to set Out:
//
//     nil
//         Inline results into the result parameter.
//
//     bson.M{"replace": "mycollection"}
//         The output will be inserted into a collection which replaces any
//         existing collection with the same name.
//
//     bson.M{"merge": "mycollection"}
//         This option will merge new data into the old output collection. In
//         other words, if the same key exists in both the result set and the
//         old collection, the new key will overwrite the old one.
//
//     bson.M{"reduce": "mycollection"}
//         If documents exist for a given key in the result set and in the old
//         collection, then a reduce operation (using the specified reduce
//         function) will be performed on the two values and the result will be
//         written to the output collection. If a finalize function was
//         provided, this will be run after the reduce as well.
//
//     bson.M{...., "db": "mydb"}
//         Any of the above options can have the "db" key included for doing
//         the respective action in a separate database.
//
// The following is a trivial example which will count the number of
// occurrences of a field named n on each document in a collection, and
// will return results inline:
//
//     job := mgo.MapReduce{
//             Map:      "function() { emit(this.n, 1) }",
//             Reduce:   "function(key, values) { return Array.sum(values) }",
//     }
//     var result []struct { Id int "_id"; Value int }
//     _, err := collection.Find(nil).MapReduce(job, &result)
//     if err != nil {
//         panic(err)
//     }
//     for _, item := range result {
//         fmt.Println(item.Value)
//     }
//     
// This function is compatible with MongoDB 1.7.4+.
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/MapReduce
//
func (q *Query) MapReduce(job MapReduce, result interface{}) (info *MapReduceInfo, err os.Error) {
	q.m.Lock()
	session := q.session
	op := q.op // Copy.
	limit := q.limit
	q.m.Unlock()

	c := strings.Index(op.collection, ".")
	if c < 0 {
		return nil, os.NewError("Bad collection name: " + op.collection)
	}

	dbname := op.collection[:c]
	cname := op.collection[c+1:]

	qdoc := op.query
	var sort interface{}
	if wrapper, ok := qdoc.(*queryWrapper); ok {
		qdoc = wrapper.Query
		sort = wrapper.OrderBy
	}

	cmd := mapReduceCmd{
		Collection: cname,
		Map:        job.Map,
		Reduce:     job.Reduce,
		Finalize:   job.Finalize,
		Out:        job.Out,
		Scope:      job.Scope,
		Verbose:    job.Verbose,
		Query:      qdoc,
		Sort:       sort,
		Limit:      limit,
	}

	if cmd.Out == nil {
		cmd.Out = bson.M{"inline": 1}
	}

	var doc mapReduceResult
	err = session.DB(dbname).Run(&cmd, &doc)
	if err != nil {
		return nil, err
	}
	if doc.Err != "" {
		return nil, os.NewError(doc.Err)
	}

	info = &MapReduceInfo{
		InputCount:  doc.Counts.Input,
		EmitCount:   doc.Counts.Emit,
		OutputCount: doc.Counts.Output,
		Time:        doc.TimeMillis * 1e6,
	}

	if doc.Result.Kind == 0x02 {
		err = doc.Result.Unmarshal(&info.Collection)
		info.Database = dbname
	} else if doc.Result.Kind == 0x03 {
		var v struct{ Collection, Db string }
		err = doc.Result.Unmarshal(&v)
		info.Collection = v.Collection
		info.Database = v.Db
	}

	if doc.Timing != nil {
		info.VerboseTime = doc.Timing
		info.VerboseTime.Total *= 1e6
		info.VerboseTime.Map *= 1e6
		info.VerboseTime.EmitLoop *= 1e6
	}

	if err != nil {
		return nil, err
	}
	if result != nil {
		return info, doc.Results.Unmarshal(result)
	}
	return info, nil
}

type Change struct {
	Update interface{} // The change document
	Upsert bool        // Whether to insert in case the document isn't found
	Remove bool        // Whether to remove the document found rather than updating
	New    bool        // Should the modified document be returned rather than the old one
}

type findModifyCmd struct {
	Collection                  string      "findAndModify"
	Query, Update, Sort, Fields interface{} ",omitempty"
	Upsert, Remove, New         bool        ",omitempty"
}

type valueResult struct {
	Value bson.Raw
}

// Modify allows updating, upserting or removing a document matching the query
// and atomically returning either the old version (the default) or the new
// version of the document (when change.New is true).  In case no objects are
// found Modify returns NotFound.
//
// The Sort and Select query methods affect the result of Modify.  In case
// multiple documents match the query, Sort enables selecting which document to
// act upon by ordering it first.  Select enables retrieving only a selection
// of fields of the new or old document.
//
// This simple example increments a counter and prints its new value:
//
//     change := mgo.Change{Update: bson.M{"$inc": bson.M{"n": 1}}, New: true}
//     err = col.Find(M{"_id": id}).Modify(change, &doc)
//     fmt.Println(doc.N)
//
// Relevant documentation:
//
//     http://www.mongodb.org/display/DOCS/findAndModify+Command
//     http://www.mongodb.org/display/DOCS/Updating
//     http://www.mongodb.org/display/DOCS/Atomic+Operations
//
func (q *Query) Modify(change Change, result interface{}) (err os.Error) {
	q.m.Lock()
	session := q.session
	op := q.op // Copy.
	q.m.Unlock()

	c := strings.Index(op.collection, ".")
	if c < 0 {
		return os.NewError("Bad collection name: " + op.collection)
	}

	dbname := op.collection[:c]
	cname := op.collection[c+1:]

	qdoc := op.query
	var sort interface{}
	if wrapper, ok := qdoc.(*queryWrapper); ok {
		qdoc = wrapper.Query
		sort = wrapper.OrderBy
	}

	cmd := findModifyCmd{
		Collection: cname,
		Update:     change.Update,
		Upsert:     change.Upsert,
		Remove:     change.Remove,
		New:        change.New,
		Query:      qdoc,
		Sort:       sort,
		Fields:     op.selector,
	}

	var doc valueResult
	err = session.DB(dbname).Run(&cmd, &doc)
	if err != nil {
		if qerr, ok := err.(*QueryError); ok && qerr.Message == "No matching object found" {
			return NotFound
		}
		return err
	}
	if doc.Value.Kind == 0x0A {
		return NotFound
	}
	return doc.Value.Unmarshal(result)
}

// The BuildInfo type encapsulates details about the running MongoDB server.
//
// Note that the VersionArray field was introduced in MongoDB 2.0+, but it is
// internally assembled from the Version information for previous versions.
// In both cases, VersionArray is guaranteed to have at least 4 entries.
type BuildInfo struct {
	Version       string
	VersionArray  []int  `bson:"versionArray"` // On MongoDB 2.0+; assembled from Version otherwise
	GitVersion    string `bson:"gitVersion"`
	SysInfo       string `bson:"sysInfo"`
	Bits          int
	Debug         bool
	MaxObjectSize int `bson:"maxBsonObjectSize"`
}

// BuildInfo retrieves the version and other details about the
// running MongoDB server.
func (s *Session) BuildInfo() (info BuildInfo, err os.Error) {
	err = s.Run(bson.D{{"buildInfo", "1"}}, &info)
	if len(info.VersionArray) == 0 {
		for _, a := range strings.Split(info.Version, ".") {
			i, err := strconv.Atoi(a)
			if err != nil {
				break
			}
			info.VersionArray = append(info.VersionArray, i)
		}
	}
	for len(info.VersionArray) < 4 {
		info.VersionArray = append(info.VersionArray, 0)
	}
	return
}

// ---------------------------------------------------------------------------
// Internal session handling helpers.

func (s *Session) acquireSocket(slaveOk bool) (*mongoSocket, os.Error) {

	// Try to use a previously reserved socket, with a fast read-only lock.
	s.m.RLock()
	sock := s.socket
	sockIsGood := sock != nil && (slaveOk && s.slaveOk || s.socketIsMaster)
	s.m.RUnlock()

	if sockIsGood {
		sock.Acquire()
		return sock, nil
	}

	// No go.  We may have to request a new socket and change the session,
	// so try again but with an exclusive lock now.
	s.m.Lock()
	defer s.m.Unlock()

	sock = s.socket
	sockIsGood = sock != nil && (slaveOk && s.slaveOk || s.socketIsMaster)

	if sockIsGood {
		sock.Acquire()
		return sock, nil
	}

	// Still not good.  We need a new socket.
	sock, err := s.cluster().AcquireSocket(slaveOk && s.slaveOk, s.syncTimeout)
	if err != nil {
		return nil, err
	}

	// Authenticate the new socket.
	for _, a := range s.auth {
		err = sock.Login(a.db, a.user, a.pass)
		if err != nil {
			sock.Release()
			return nil, err
		}
	}

	// Keep track of the new socket, if necessary.
	// Note that, as a special case, if the Eventual session was
	// not refreshed (socket != nil), it means the developer asked
	// to preserve an existing reserved socket, so we'll keep the
	// master one around too before a Refresh happens.
	if s.consistency != Eventual || s.socket != nil {
		s.setSocket(sock)
	}

	// Switch over a Monotonic session to the master.
	if !slaveOk && s.consistency == Monotonic {
		s.slaveOk = false
	}

	return sock, nil
}

// Set the socket bound to this session.  With a bound socket, all operations
// with this session will use the given socket if possible. When not possible
// (e.g. attempting to write to a slave) acquireSocket will replace the
// current socket.  Note that this method will properly refcount the socket up
// and down when setting/releasing.
func (s *Session) setSocket(socket *mongoSocket) {
	if socket != nil {
		s.socketIsMaster = socket.Acquire()
	} else {
		s.socketIsMaster = false
	}
	if s.socket != nil {
		s.socket.Release()
	}
	s.socket = socket
}

func (iter *Iter) replyFunc() replyFunc {
	return func(err os.Error, op *replyOp, docNum int, docData []byte) {
		iter.m.Lock()
		iter.pendingDocs--
		if err != nil {
			iter.err = err
			debugf("Iter %p received an error: %s", iter, err.String())
		} else if docNum == -1 {
			debugf("Iter %p received no documents (cursor=%d).", iter, op.cursorId)
			if op != nil && op.cursorId != 0 {
				// It's a tailable cursor.
				iter.op.cursorId = op.cursorId
			} else {
				iter.err = NotFound
			}
		} else {
			rdocs := int(op.replyDocs)
			if docNum == 0 {
				iter.pendingDocs += rdocs - 1
				iter.docsBeforeMore = rdocs - int(iter.prefetch*float64(rdocs))
				iter.op.cursorId = op.cursorId
			}
			// XXX Handle errors and flags.
			debugf("Iter %p received reply document %d/%d (cursor=%d)", iter, docNum+1, rdocs, op.cursorId)
			iter.docData.Push(docData)
		}
		iter.gotReply.Broadcast()
		iter.m.Unlock()
	}
}

// writeQuery runs the given modifying operation, potentially followed up
// by a getLastError command in case the session is in safe mode.  The
// LastError result is made available in lerr, and if lerr.Err is set it
// will also be returned as err.
func (s *Session) writeQuery(op interface{}) (lerr *LastError, err os.Error) {
	socket, err := s.acquireSocket(false)
	if err != nil {
		return nil, err
	}
	defer socket.Release()

	s.m.RLock()
	safeOp := s.safeOp
	s.m.RUnlock()

	if safeOp == nil {
		return nil, socket.Query(op)
	} else {
		var mutex sync.Mutex
		var replyData []byte
		var replyErr os.Error
		mutex.Lock()
		query := *safeOp // Copy the data.
		query.replyFunc = func(err os.Error, reply *replyOp, docNum int, docData []byte) {
			replyData = docData
			replyErr = err
			mutex.Unlock()
		}
		err = socket.Query(op, &query)
		if err != nil {
			return nil, err
		}
		mutex.Lock() // Wait.
		if replyErr != nil {
			return nil, replyErr // XXX TESTME
		}
		result := &LastError{}
		bson.Unmarshal(replyData, &result)
		debugf("Result from writing query: %#v", result)
		if result.Err != "" {
			return result, result
		}
		return result, nil
	}
	panic("unreachable")
}
