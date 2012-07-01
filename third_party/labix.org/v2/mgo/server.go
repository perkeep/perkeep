// mgo - MongoDB driver for Go
// 
// Copyright (c) 2010-2012 - Gustavo Niemeyer <gustavo@niemeyer.net>
// 
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met: 
// 
// 1. Redistributions of source code must retain the above copyright notice, this
//    list of conditions and the following disclaimer. 
// 2. Redistributions in binary form must reproduce the above copyright notice,
//    this list of conditions and the following disclaimer in the documentation
//    and/or other materials provided with the distribution. 
// 
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
// ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
// WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR
// ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
// (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
// LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND
// ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package mgo

import (
	"errors"
	"net"
	"sort"
	"sync"
)

// ---------------------------------------------------------------------------
// Mongo server encapsulation.

type mongoServer struct {
	sync.RWMutex
	Addr          string
	ResolvedAddr  string
	tcpaddr       *net.TCPAddr
	unusedSockets []*mongoSocket
	liveSockets   []*mongoSocket
	closed        bool
	master        bool
	sync          chan bool
}

func newServer(addr string, sync chan bool) (server *mongoServer, err error) {
	tcpaddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		log("Failed to resolve ", addr, ": ", err.Error())
		return nil, err
	}

	resolvedAddr := tcpaddr.String()
	if resolvedAddr != addr {
		debug("Address ", addr, " resolved as ", resolvedAddr)
	}
	server = &mongoServer{
		Addr:         addr,
		ResolvedAddr: resolvedAddr,
		tcpaddr:      tcpaddr,
		sync:         sync,
	}
	return
}

var errSocketLimit = errors.New("per-server connection limit reached")

// AcquireSocket returns a socket for communicating with the server.
// This will attempt to reuse an old connection, if one is available. Otherwise,
// it will establish a new one. The returned socket is owned by the call site,
// and will return to the cache when the socket has its Release method called
// the same number of times as AcquireSocket + Acquire were called for it.
// If the limit argument is not zero, a socket will only be returned if the
// number of sockets in use for this server is under the provided limit.
func (server *mongoServer) AcquireSocket(limit int) (socket *mongoSocket, err error) {
	for {
		server.Lock()
		n := len(server.unusedSockets)
		if limit > 0 && len(server.liveSockets)-n >= limit {
			server.Unlock()
			return nil, errSocketLimit
		}
		if n > 0 {
			socket = server.unusedSockets[n-1]
			server.unusedSockets[n-1] = nil // Help GC.
			server.unusedSockets = server.unusedSockets[:n-1]
			server.Unlock()
			err = socket.InitialAcquire()
			if err != nil {
				continue
			}
		} else {
			server.Unlock()
			socket, err = server.Connect()
			if err == nil {
				server.Lock()
				server.liveSockets = append(server.liveSockets, socket)
				server.Unlock()
			}
		}
		return
	}
	panic("unreached")
}

// Connect establishes a new connection to the server. This should
// generally be done through server.AcquireSocket().
func (server *mongoServer) Connect() (*mongoSocket, error) {
	server.RLock()
	addr := server.Addr
	tcpaddr := server.tcpaddr
	master := server.master
	server.RUnlock()

	log("Establishing new connection to ", addr, "...")
	conn, err := net.DialTCP("tcp", nil, tcpaddr)
	if err != nil {
		log("Connection to ", addr, " failed: ", err.Error())
		return nil, err
	}
	log("Connection to ", addr, " established.")

	stats.conn(+1, master)
	return newSocket(server, conn), nil
}

// Close forces closing all sockets that are alive, whether
// they're currently in use or not.
func (server *mongoServer) Close() {
	server.Lock()
	server.closed = true
	liveSockets := server.liveSockets
	unusedSockets := server.unusedSockets
	server.liveSockets = nil
	server.unusedSockets = nil
	addr := server.Addr
	server.Unlock()
	logf("Connections to %s closing (%d live sockets).", addr, len(liveSockets))
	for i, s := range liveSockets {
		s.Close()
		liveSockets[i] = nil
	}
	for i := range unusedSockets {
		unusedSockets[i] = nil
	}
}

// RecycleSocket puts socket back into the unused cache.
func (server *mongoServer) RecycleSocket(socket *mongoSocket) {
	server.Lock()
	if !server.closed {
		server.unusedSockets = append(server.unusedSockets, socket)
	}
	server.Unlock()
}

func removeSocket(sockets []*mongoSocket, socket *mongoSocket) []*mongoSocket {
	for i, s := range sockets {
		if s == socket {
			copy(sockets[i:], sockets[i+1:])
			n := len(sockets) - 1
			sockets[n] = nil
			sockets = sockets[:n]
			break
		}
	}
	return sockets
}

// AbendSocket notifies the server that the given socket has terminated
// abnormally, and thus should be discarded rather than cached.
func (server *mongoServer) AbendSocket(socket *mongoSocket) {
	server.Lock()
	if server.closed {
		server.Unlock()
		return
	}
	server.liveSockets = removeSocket(server.liveSockets, socket)
	server.unusedSockets = removeSocket(server.unusedSockets, socket)
	server.Unlock()
	// Maybe just a timeout, but suggest a cluster sync up just in case.
	select {
	case server.sync <- true:
	default:
	}
}

// Merge other into server, which must both be communicating with
// the same server address.
func (server *mongoServer) Merge(other *mongoServer) {
	server.Lock()
	server.master = other.master
	server.Unlock()
	// Sockets of other are ignored for the moment. Merging them
	// would mean a large number of sockets being cached on longer
	// recovering situations.
	other.Close()
}

func (server *mongoServer) SetMaster(isMaster bool) {
	server.Lock()
	server.master = isMaster
	server.Unlock()
}

func (server *mongoServer) IsMaster() bool {
	server.RLock()
	result := server.master
	server.RUnlock()
	return result
}

type mongoServerSlice []*mongoServer

func (s mongoServerSlice) Len() int {
	return len(s)
}

func (s mongoServerSlice) Less(i, j int) bool {
	return s[i].ResolvedAddr < s[j].ResolvedAddr
}

func (s mongoServerSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s mongoServerSlice) Sort() {
	sort.Sort(s)
}

func (s mongoServerSlice) Search(other *mongoServer) (i int, ok bool) {
	resolvedAddr := other.ResolvedAddr
	n := len(s)
	i = sort.Search(n, func(i int) bool {
		return s[i].ResolvedAddr >= resolvedAddr
	})
	return i, i != n && s[i].ResolvedAddr == resolvedAddr
}

type mongoServers struct {
	slice mongoServerSlice
}

func (servers *mongoServers) Search(other *mongoServer) (server *mongoServer) {
	if i, ok := servers.slice.Search(other); ok {
		return servers.slice[i]
	}
	return nil
}

func (servers *mongoServers) Add(server *mongoServer) {
	servers.slice = append(servers.slice, server)
	servers.slice.Sort()
}

func (servers *mongoServers) Remove(other *mongoServer) (server *mongoServer) {
	if i, found := servers.slice.Search(other); found {
		server = servers.slice[i]
		copy(servers.slice[i:], servers.slice[i+1:])
		n := len(servers.slice) - 1
		servers.slice[n] = nil // Help GC.
		servers.slice = servers.slice[:n]
	}
	return
}

func (servers *mongoServers) Slice() []*mongoServer {
	return ([]*mongoServer)(servers.slice)
}

func (servers *mongoServers) Get(i int) *mongoServer {
	return servers.slice[i]
}

func (servers *mongoServers) Len() int {
	return len(servers.slice)
}

func (servers *mongoServers) Empty() bool {
	return len(servers.slice) == 0
}

// MostAvailable returns the best guess of what would be the
// most interesting server to perform operations on at this
// point in time.
func (servers *mongoServers) MostAvailable() *mongoServer {
	if len(servers.slice) == 0 {
		panic("MostAvailable: can't be used on empty server list")
	}
	var best *mongoServer
	for i, next := range servers.slice {
		if i == 0 {
			best = next
			best.RLock()
			continue
		}
		next.RLock()
		swap := false
		switch {
		case next.master != best.master:
			// Prefer slaves.
			swap = best.master
		case len(next.liveSockets)-len(next.unusedSockets) < len(best.liveSockets)-len(best.unusedSockets):
			// Prefer servers with less connections.
			swap = true
		}
		if swap {
			best.RUnlock()
			best = next
		} else {
			next.RUnlock()
		}
	}
	best.RUnlock()
	return best
}
