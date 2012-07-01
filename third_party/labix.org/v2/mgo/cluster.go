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
	"sync"
	"time"
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
	references   int
	syncing      bool
	direct       bool
	cachedIndex  map[string]bool
	sync         chan bool
}

func newCluster(userSeeds []string, direct bool) *mongoCluster {
	cluster := &mongoCluster{userSeeds: userSeeds, references: 1, direct: direct}
	cluster.serverSynced.L = cluster.RWMutex.RLocker()
	cluster.sync = make(chan bool, 1)
	go cluster.syncServersLoop()
	return cluster
}

// Acquire increases the reference count for the cluster.
func (cluster *mongoCluster) Acquire() {
	cluster.Lock()
	cluster.references++
	debugf("Cluster %p acquired (refs=%d)", cluster, cluster.references)
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
	debugf("Cluster %p released (refs=%d)", cluster, cluster.references)
	if cluster.references == 0 {
		for _, server := range cluster.servers.Slice() {
			server.Close()
		}
		// Wake up the sync loop so it can die.
		cluster.syncServers()
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
	cluster.masters.Remove(server)
	other := cluster.servers.Remove(server)
	cluster.Unlock()
	if other != nil {
		other.Close()
		log("Removed server ", server.Addr, " from cluster.")
	}
	server.Close()
}

type isMasterResult struct {
	IsMaster  bool
	Secondary bool
	Primary   string
	Hosts     []string
	Passives  []string
}

func (cluster *mongoCluster) syncServer(server *mongoServer) (hosts []string, err error) {
	addr := server.Addr
	log("SYNC Processing ", addr, "...")

	var result isMasterResult
	var tryerr error
	for retry := 0; ; retry++ {
		if retry == 3 {
			return nil, tryerr
		}

		socket, err := server.AcquireSocket(0)
		if err != nil {
			tryerr = err
			logf("SYNC Failed to get socket to %s: %v", addr, err)
			continue
		}

		// Monotonic will let us talk to a slave and still hold the socket.
		session := newSession(Monotonic, cluster, socket, 10 * time.Second)

		// session holds the socket now.
		socket.Release()

		err = session.Run("ismaster", &result)
		session.Close()
		if err != nil {
			tryerr = err
			logf("SYNC Command 'ismaster' to %s failed: %v", addr, err)
			continue
		}
		debugf("SYNC Result of 'ismaster' from %s: %#v", addr, result)
		break
	}

	if result.IsMaster {
		debugf("SYNC %s is a master.", addr)
		// Made an incorrect assumption above, so fix stats.
		stats.conn(-1, false)
		server.SetMaster(true)
		stats.conn(+1, true)
	} else if result.Secondary {
		debugf("SYNC %s is a slave.", addr)
	} else {
		logf("SYNC %s is neither a master nor a slave.", addr)
		// Made an incorrect assumption above, so fix stats.
		stats.conn(-1, false)
		return nil, errors.New(addr + " is not a master nor slave")
	}

	hosts = make([]string, 0, 1+len(result.Hosts)+len(result.Passives))
	if result.Primary != "" {
		// First in the list to speed up master discovery.
		hosts = append(hosts, result.Primary)
	}
	hosts = append(hosts, result.Hosts...)
	hosts = append(hosts, result.Passives...)

	debugf("SYNC %s knows about the following peers: %#v", addr, hosts)
	return hosts, nil
}

func (cluster *mongoCluster) mergeServer(server *mongoServer) {
	cluster.Lock()
	previous := cluster.servers.Search(server)
	isMaster := server.IsMaster()
	if previous == nil {
		cluster.servers.Add(server)
		if isMaster {
			cluster.masters.Add(server)
			log("SYNC Adding ", server.Addr, " to cluster as a master.")
		} else {
			log("SYNC Adding ", server.Addr, " to cluster as a slave.")
		}
	} else {
		if isMaster != previous.IsMaster() {
			if isMaster {
				log("SYNC Server ", server.Addr, " is now a master.")
				cluster.masters.Add(previous)
			} else {
				log("SYNC Server ", server.Addr, " is now a slave.")
				cluster.masters.Remove(previous)
			}
		}
		previous.Merge(server)
	}
	debugf("SYNC Broadcasting availability of server %s", server.Addr)
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

// syncServers injects a value into the cluster.sync channel to force
// an iteration of the syncServersLoop function.
func (cluster *mongoCluster) syncServers() {
	select {
	case cluster.sync <- true:
	default:
	}
}

// How long to wait for a checkup of the cluster topology if nothing
// else kicks a synchronization before that.
const syncServersDelay = 3 * time.Minute

// syncServersLoop loops while the cluster is alive to keep its idea of
// the server topology up-to-date. It must be called just once from
// newCluster.  The loop iterates once syncServersDelay has passed, or
// if somebody injects a value into the cluster.sync channel to force a
// synchronization.  A loop iteration will contact all servers in
// parallel, ask them about known peers and their own role within the
// cluster, and then attempt to do the same with all the peers
// retrieved.
func (cluster *mongoCluster) syncServersLoop() {
	for {
		debugf("SYNC Cluster %p is starting a sync loop iteration.", cluster)

		cluster.Lock()
		if cluster.references == 0 {
			cluster.Unlock()
			break
		}
		cluster.references++ // Keep alive while syncing.
		direct := cluster.direct
		cluster.Unlock()

		cluster.syncServersIteration(direct)

		// We just synchronized, so consume any outstanding requests.
		select {
		case <-cluster.sync:
		default:
		}

		cluster.Release()

		// Hold off before allowing another sync. No point in
		// burning CPU looking for down servers.
		time.Sleep(5e8)

		cluster.Lock()
		if cluster.references == 0 {
			cluster.Unlock()
			break
		}
		// Poke all waiters so they have a chance to timeout or
		// restart syncing if they wish to.
		cluster.serverSynced.Broadcast()
		// Check if we have to restart immediately either way.
		restart := !direct && cluster.masters.Empty() || cluster.servers.Empty()
		cluster.Unlock()

		if restart {
			log("SYNC No masters found. Will synchronize again.")
			continue
		}

		debugf("SYNC Cluster %p waiting for next requested or scheduled sync.", cluster)

		// Hold off until somebody explicitly requests a synchronization
		// or it's time to check for a cluster topology change again.
		select {
		case <-cluster.sync:
		case <-time.After(syncServersDelay):
		}
	}
	debugf("SYNC Cluster %p is stopping its sync loop.", cluster)
}

func (cluster *mongoCluster) syncServersIteration(direct bool) {
	log("SYNC Starting full topology synchronization...")

	var wg sync.WaitGroup
	var m sync.Mutex
	mergePending := make(map[string]*mongoServer)
	mergeRequested := make(map[string]bool)
	seen := make(map[string]bool)
	goodSync := false

	var spawnSync func(addr string, byMaster bool)
	spawnSync = func(addr string, byMaster bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()

			server, err := newServer(addr, cluster.sync)
			if err != nil {
				log("SYNC Failed to start sync of ", addr, ": ", err.Error())
				return
			}

			m.Lock()
			if byMaster {
				if s, found := mergePending[server.ResolvedAddr]; found {
					delete(mergePending, server.ResolvedAddr)
					m.Unlock()
					cluster.mergeServer(s)
					return
				}
				mergeRequested[server.ResolvedAddr] = true
			}
			if seen[server.ResolvedAddr] {
				m.Unlock()
				return
			}
			seen[server.ResolvedAddr] = true
			m.Unlock()

			hosts, err := cluster.syncServer(server)
			if err == nil {
				isMaster := server.IsMaster()
				if !direct {
					for _, addr := range hosts {
						spawnSync(addr, isMaster)
					}
				}

				m.Lock()
				merge := direct || isMaster
				if mergeRequested[server.ResolvedAddr] {
					merge = true
				} else if !merge {
					mergePending[server.ResolvedAddr] = server
				}
				if merge {
					goodSync = true
				}
				m.Unlock()
				if merge {
					cluster.mergeServer(server)
				}
			}
		}()
	}

	for _, addr := range cluster.getKnownAddrs() {
		spawnSync(addr, false)
	}
	wg.Wait()

	for _, server := range mergePending {
		if goodSync {
			cluster.removeServer(server)
		} else {
			server.Close()
		}
	}

	cluster.Lock()
	ml := cluster.masters.Len()
	logf("SYNC Synchronization completed: %d master(s) and %d slave(s) alive.", ml, cluster.servers.Len()-ml)

	// Update dynamic seeds, but only if we have any good servers. Otherwise,
	// leave them alone for better chances of a successful sync in the future.
	if goodSync {
		dynaSeeds := make([]string, cluster.servers.Len())
		for i, server := range cluster.servers.Slice() {
			dynaSeeds[i] = server.Addr
		}
		cluster.dynaSeeds = dynaSeeds
		debugf("SYNC New dynamic seeds: %#v\n", dynaSeeds)
	}
	cluster.Unlock()
}

var socketsPerServer = 4096

// AcquireSocket returns a socket to a server in the cluster.  If slaveOk is
// true, it will attempt to return a socket to a slave server.  If it is
// false, the socket will necessarily be to a master server.
func (cluster *mongoCluster) AcquireSocket(slaveOk bool, syncTimeout time.Duration) (s *mongoSocket, err error) {
	var started time.Time
	warnedLimit := false
	for {
		cluster.RLock()
		for {
			ml := cluster.masters.Len()
			sl := cluster.servers.Len()
			debugf("Cluster has %d known masters and %d known slaves.", ml, sl-ml)
			if ml > 0 || slaveOk && sl > 0 {
				break
			}
			if started.IsZero() {
				started = time.Now() // Initialize after fast path above.
			} else if syncTimeout != 0 && started.Before(time.Now().Add(-syncTimeout)) {
				cluster.RUnlock()
				return nil, errors.New("no reachable servers")
			}
			log("Waiting for servers to synchronize...")
			cluster.syncServers()

			// Remember: this will release and reacquire the lock.
			cluster.serverSynced.Wait()
		}

		var server *mongoServer
		if slaveOk {
			server = cluster.servers.MostAvailable()
		} else {
			server = cluster.masters.MostAvailable()
		}
		cluster.RUnlock()

		s, err = server.AcquireSocket(socketsPerServer)
		if err == errSocketLimit {
			if !warnedLimit {
				log("WARNING: Per-server connection limit reached.")
			}
			time.Sleep(1e8)
			continue
		}
		if err != nil {
			cluster.removeServer(server)
			cluster.syncServers()
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
	if exists {
		cluster.cachedIndex[cacheKey] = true
	} else {
		delete(cluster.cachedIndex, cacheKey)
	}
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
