/*
Copyright 2011 Google Inc.

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

package memcache

import (
	"hash/crc32"
	"net"
	"strings"
	"sync"
)

// ServerSelector is the interface that selects a memcache server
// as a function of the item's key.
//
// All ServerSelector implementations must be threadsafe.
type ServerSelector interface {
	// PickServer returns the server address that a given item
	// should be shared onto.
	PickServer(key string) (net.Addr, error)
}

// ServerList is a simple ServerSelector. Its zero value is usable.
type ServerList struct {
	lk    sync.RWMutex
	addrs []net.Addr
}

// SetServers changes a ServerList's set of servers at runtime and is
// threadsafe.
//
// Each server is given equal weight. A server is given more weight
// if it's listed multiple times.
//
// SetServers returns an error if any of the server names fail to
// resolve. No attempt is made to connect to the server. If any error
// is returned, no changes are made to the ServerList.
func (ss *ServerList) SetServers(servers ...string) error {
	naddr := make([]net.Addr, len(servers))
	for i, server := range servers {
		if strings.Contains(server, "/") {
			addr, err := net.ResolveUnixAddr("unix", server)
			if err != nil {
				return err
			}
			naddr[i] = addr
		} else {
			tcpaddr, err := net.ResolveTCPAddr("tcp", server)
			if err != nil {
				return err
			}
			naddr[i] = tcpaddr
		}
	}

	ss.lk.Lock()
	defer ss.lk.Unlock()
	ss.addrs = naddr
	return nil
}

func (ss *ServerList) PickServer(key string) (net.Addr, error) {
	ss.lk.RLock()
	defer ss.lk.RUnlock()
	if len(ss.addrs) == 0 {
		return nil, ErrNoServers
	}
	// TODO-GO: remove this copy
	cs := crc32.ChecksumIEEE([]byte(key))
	return ss.addrs[cs%uint32(len(ss.addrs))], nil
}
