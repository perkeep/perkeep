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
	"sync"
	"time"
	"os"
	"rand"
)

// ---------------------------------------------------------------------------
// Mongo cluster encapsulation.
//
// A cluster enables the communication with one or more servers participating
// in a mongo cluster.  This works with individual servers, a replica set,
// a replica pair, one or multiple mongos routers, etc.

type mongoCluster struct {
	sync.RWMutex
	serverSynced sync.Cond
	userSeeds    []string
	dynaSeeds    []string
	servers      mongoServers
	masters      mongoServers
	slaves       mongoServers
	references   int
	syncing      bool
	direct       bool
	cachedIndex  map[string]bool
}

func newCluster(userSeeds []string, direct bool) *mongoCluster {
	cluster := &mongoCluster{userSeeds: userSeeds, references: 1, direct: direct}
	cluster.serverSynced.L = cluster.RWMutex.RLocker()
	go cluster.syncServers()
	return cluster
}

// Acquire increases the reference count for the cluster.
func (cluster *mongoCluster) Acquire() {
	cluster.Lock()
	cluster.references++
	cluster.Unlock()
}

// Release decreases the reference count for the cluster. Once
// it reaches zero, all servers will be closed.
func (cluster *mongoCluster) Release() {
	cluster.Lock()
	if cluster.references == 0 {
		panic("cluster.Release() with references == 0")
	}
	cluster.references--
	if cluster.references == 0 {
		for _, server := range cluster.servers.Slice() {
			server.Close()
		}
	}
	cluster.Unlock()
}

func (cluster *mongoCluster) LiveServers() (servers []string) {
	cluster.RLock()
	for _, serv := range cluster.servers.Slice() {
		servers = append(servers, serv.Addr)
	}
	cluster.RUnlock()
	return servers
}

func (cluster *mongoCluster) removeServer(server *mongoServer) {
	cluster.Lock()
	removed := cluster.servers.Remove(server) ||
		cluster.masters.Remove(server) ||
		cluster.slaves.Remove(server)
	if removed {
		log("Removing server ", server.Addr, " from cluster.")
	}
	cluster.Unlock()
}

type isMasterResult struct {
	IsMaster  bool
	Secondary bool
	Primary   string
	Hosts     []string
	Passives  []string
}

func (cluster *mongoCluster) syncServer(server *mongoServer) (hosts []string, err os.Error) {
	addr := server.Addr
	log("[sync] Processing ", addr, "...")

	defer func() {
		if err != nil {
			// XXX TESTME
			cluster.removeServer(server)
		}
	}()

	socket, err := server.AcquireSocket()
	if err != nil {
		log("[sync] Failed to get socket to ", addr, ": ", err.String())
		return
	}

	// Monotonic will let us talk to a slave and still hold the socket.
	session := newSession(Monotonic, cluster, socket)
	defer session.Close()

	socket.Release()

	result := isMasterResult{}
	err = session.Run("ismaster", &result)
	if err != nil {
		log("[sync] Command 'ismaster' to ", addr, " failed: ", err.String())
		return
	}
	debugf("[sync] Result of 'ismaster' from %s: %#v", addr, result)

	if result.IsMaster {
		log("[sync] ", addr, " is a master.")
		// Made an incorrect assumption above, so fix stats.
		stats.conn(-1, false)
		server.SetMaster(true)
		stats.conn(+1, true)
	} else if result.Secondary {
		log("[sync] ", addr, " is a slave.")
	} else {
		log("[sync] ", addr, " is neither a master nor a slave.")
	}

	hosts = make([]string, 0, 1+len(result.Hosts)+len(result.Passives))
	if result.Primary != "" {
		// First in the list to speed up master discovery.
		hosts = append(hosts, result.Primary)
	}
	hosts = append(hosts, result.Hosts...)
	hosts = append(hosts, result.Passives...)

	// Close the session ahead of time. This will release the socket being
	// used for synchronization so that it may be reused as soon as the
	// server is merged.
	session.Close()
	cluster.mergeServer(server)

	debugf("[sync] %s knows about the following peers: %#v", addr, hosts)
	return hosts, nil
}

func (cluster *mongoCluster) mergeServer(server *mongoServer) {
	cluster.Lock()
	previous := cluster.servers.Search(server)
	isMaster := server.IsMaster()
	if previous == nil {
		cluster.servers.Add(server)
		if isMaster {
			log("[sync] Adding ", server.Addr, " to cluster as a master.")
			cluster.masters.Add(server)
		} else {
			log("[sync] Adding ", server.Addr, " to cluster as a slave.")
			cluster.slaves.Add(server)
		}
	} else {
		if isMaster != previous.IsMaster() {
			if isMaster {
				log("[sync] Server ", server.Addr, " is now a master.")
				cluster.slaves.Remove(previous)
				cluster.masters.Add(previous)
			} else {
				log("[sync] Server ", server.Addr, " is now a slave.")
				cluster.masters.Remove(previous)
				cluster.slaves.Add(previous)
			}
		}
		previous.Merge(server)
	}
	debug("[sync] Broadcasting availability of server.")
	cluster.serverSynced.Broadcast()
	cluster.Unlock()
}

func (cluster *mongoCluster) getKnownAddrs() []string {
	cluster.RLock()
	max := len(cluster.userSeeds) + len(cluster.dynaSeeds) + cluster.servers.Len()
	seen := make(map[string]bool, max)
	known := make([]string, 0, max)

	add := func(addr string) {
		if _, found := seen[addr]; !found {
			seen[addr] = true
			known = append(known, addr)
		}
	}

	for _, addr := range cluster.userSeeds {
		add(addr)
	}
	for _, addr := range cluster.dynaSeeds {
		add(addr)
	}
	for _, serv := range cluster.servers.Slice() {
		add(serv.Addr)
	}
	cluster.RUnlock()

	return known
}


// Synchronize all servers in the cluster.  This will contact all servers in
// parallel, ask them about known peers and their own role within the cluster,
// and then attempt to do the same with all the peers retrieved.  This function
// will only return once the full synchronization is done.
func (cluster *mongoCluster) syncServers() {
	cluster.Lock()
	if cluster.syncing || cluster.references == 0 {
		cluster.Unlock()
		return
	}
	cluster.syncing = true
	cluster.Unlock()

restart:

	log("[sync] Starting full topology synchronization...")

	cluster.Lock()
	// Check again, so that ref=0 stops restarting.
	if cluster.references == 0 {
		cluster.syncing = false
		cluster.Unlock()
		return
	}
	cluster.references++ // Keep alive while syncing.
	direct := cluster.direct
	cluster.Unlock()

	known := cluster.getKnownAddrs()

	// Note that the logic below is lock free.  The locks below are
	// just to avoid race conditions internally and to wait for the
	// procedure to finish.

	var started, finished int
	var done sync.Mutex
	var m sync.Mutex

	done.Lock()
	seen := make(map[string]bool)

	var spawnSync func(addr string)
	spawnSync = func(addr string) {
		m.Lock()
		started++
		m.Unlock()

		go func() {
			defer func() {
				m.Lock()
				finished++
				if started == finished && finished >= len(known) {
					done.Unlock()
				}
				m.Unlock()
			}()

			server, err := newServer(addr)
			if err != nil {
				log("[sync] Failed to start sync of ", addr, ": ", err.String())
				return
			}

			if _, found := seen[server.ResolvedAddr]; found {
				return
			}
			seen[server.ResolvedAddr] = true

			hosts, err := cluster.syncServer(server)
			if !direct && err == nil {
				for _, addr := range hosts {
					spawnSync(addr)
				}
			}
		}()
	}

	for _, addr := range known {
		spawnSync(addr)
	}

	done.Lock()

	cluster.Lock()
	log("[sync] Synchronization completed: ", cluster.masters.Len(),
		" master(s) and, ", cluster.slaves.Len(), " slave(s) alive.")

	// Update dynamic seeds, but only if we have any good servers. Otherwise,
	// leave them alone for better chances of a successful sync in the future.
	if !cluster.servers.Empty() {
		dynaSeeds := make([]string, cluster.servers.Len())
		for i, server := range cluster.servers.Slice() {
			dynaSeeds[i] = server.Addr
		}
		cluster.dynaSeeds = dynaSeeds
		debugf("[sync] New dynamic seeds: %#v\n", dynaSeeds)
	}

	// Poke all waiters so they have a chance to timeout.
	cluster.serverSynced.Broadcast()

	if !direct && cluster.masters.Empty() || cluster.servers.Empty() {
		log("[sync] No masters found. Synchronize again.")

		cluster.Unlock()
		cluster.Release() // May stop resyncing with refs=0.
		time.Sleep(5e8)
		goto restart
	}

	// Reference is decreased after unlocking, so that
	// if refs=0, Release handles it.
	cluster.Unlock()
	cluster.Release()

	// Hold off before allowing another sync.  No point in
	// burning CPU looking for down servers.
	time.Sleep(5e8)
	cluster.Lock()
	cluster.syncing = false
	// Poke all waiters so they have a chance to timeout or
	// restart syncing if they wish to.
	cluster.serverSynced.Broadcast()
	cluster.Unlock()
}

// AcquireSocket returns a socket to a server in the cluster.  If slaveOk is
// true, it will attempt to return a socket to a slave server.  If it is
// false, the socket will necessarily be to a master server.
func (cluster *mongoCluster) AcquireSocket(slaveOk bool, syncTimeout int64) (s *mongoSocket, err os.Error) {
	started := time.Nanoseconds()
	for {
		cluster.RLock()
		for {
			debugf("Cluster has %d known masters and %d known slaves.", cluster.masters.Len(), cluster.slaves.Len())
			if !cluster.masters.Empty() || slaveOk && !cluster.slaves.Empty() {
				break
			}
			if syncTimeout > 0 && time.Nanoseconds()-started > syncTimeout {
				cluster.RUnlock()
				return nil, os.NewError("no reachable servers")
			}
			log("Waiting for servers to synchronize...")
			if !cluster.syncing {
				go cluster.syncServers()
			}
			cluster.serverSynced.Wait()
		}

		var server *mongoServer
		if !slaveOk || cluster.slaves.Empty() {
			i := rand.Intn(cluster.masters.Len())
			server = cluster.masters.Get(i)
		} else {
			i := rand.Intn(cluster.slaves.Len())
			server = cluster.slaves.Get(i)
		}
		cluster.RUnlock()

		s, err = server.AcquireSocket()
		if err != nil {
			cluster.removeServer(server)
			go cluster.syncServers()
			continue
		}
		return s, nil
	}
	panic("unreached")
}

func (cluster *mongoCluster) CacheIndex(cacheKey string, exists bool) {
	cluster.Lock()
	if cluster.cachedIndex == nil {
		cluster.cachedIndex = make(map[string]bool)
	}
	cluster.cachedIndex[cacheKey] = exists, exists
	cluster.Unlock()
}

func (cluster *mongoCluster) HasCachedIndex(cacheKey string) (result bool) {
	cluster.RLock()
	if cluster.cachedIndex != nil {
		result = cluster.cachedIndex[cacheKey]
	}
	cluster.RUnlock()
	return
}

func (cluster *mongoCluster) ResetIndexCache() {
	cluster.Lock()
	cluster.cachedIndex = make(map[string]bool)
	cluster.Unlock()
}
