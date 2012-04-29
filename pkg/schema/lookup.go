/*
Copyright 2012 Google Inc.

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

package schema

import (
	"bufio"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"
)

type intBool struct {
	int
	bool
}

var (
	lookupMu sync.RWMutex // guards rest
	uidName  = map[int]string{}
	gidName  = map[int]string{}
	userUid  = map[string]intBool{}
	groupGid = map[string]intBool{}

	parsedGroups, parsedPasswd bool
)

func getUserFromUid(id int) string {
	return cachedName(id, uidName, lookupUserid)
}

func getGroupFromGid(id int) string {
	return cachedName(id, gidName, lookupGroupId)
}

func getUidFromName(user string) (int, bool) {
	return cachedId(user, userUid, lookupUserToId)
}

func getGidFromName(group string) (int, bool) {
	return cachedId(group, groupGid, lookupGroupToId)
}

func cachedName(id int, m map[int]string, fn func(int) string) string {
	// TODO: use singleflight library here, keyed by 'id', rather than this lookupMu lock,
	// which is too coarse.
	lookupMu.RLock()
	name, ok := m[id]
	lookupMu.RUnlock()
	if ok {
		return name
	}
	lookupMu.Lock()
	defer lookupMu.Unlock()
	name, ok = m[id]
	if ok {
		return name // lost race, already populated
	}
	m[id] = fn(id)
	return m[id]
}

func cachedId(name string, m map[string]intBool, fn func(string) (int, bool)) (int, bool) {
	// TODO: use singleflight library here, keyed by 'name', rather than this lookupMu lock,
	// which is too coarse.
	lookupMu.RLock()
	intb, ok := m[name]
	lookupMu.RUnlock()
	if ok {
		return intb.int, intb.bool
	}
	lookupMu.Lock()
	defer lookupMu.Unlock()
	intb, ok = m[name]
	if ok {
		return intb.int, intb.bool // lost race, already populated
	}
	id, ok := fn(name)
	m[name] = intBool{id, ok}
	return id, ok
}

func lookupUserToId(name string) (uid int, ok bool) {
	u, err := user.Lookup(name)
	if err == nil {
		uid, err := strconv.Atoi(u.Uid)
		if err == nil {
			return uid, true
		}
	}
	return
}

func lookupGroupToId(group string) (gid int, ok bool) {
	if !parsedGroups {
		lookupGroupId(0) // force them to be loaded
	}
	intb := groupGid[group]
	return intb.int, intb.bool
}

// lookupMu is held
func lookupGroupId(id int) string {
	if parsedGroups {
		return ""
	}
	parsedGroups = true
	populateMap(gidName, groupGid, "/etc/group")
	return gidName[id]
}

// lookupMu is held
func lookupUserid(id int) string {
	u, err := user.LookupId(strconv.Itoa(id))
	if err == nil {
		return u.Username
	}
	if _, ok := err.(user.UnknownUserIdError); ok {
		return ""
	}
	if parsedPasswd {
		return ""
	}
	parsedPasswd = true
	populateMap(uidName, nil, "/etc/passwd")
	return uidName[id]
}

// Lame fallback parsing /etc/password for non-cgo systems where os/user doesn't work,
// and used for groups (which also happens to work on OS X, generally)
// nameMap may be nil.
func populateMap(m map[int]string, nameMap map[string]intBool, file string) {
	f, err := os.Open(file)
	if err != nil {
		return
	}
	defer f.Close()
	bufr := bufio.NewReader(f)
	for {
		line, err := bufr.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.SplitN(line, ":", 4)
		if len(parts) >= 3 {
			idstr := parts[2]
			id, err := strconv.Atoi(idstr)
			if err == nil {
				m[id] = parts[0]
				if nameMap != nil {
					nameMap[parts[0]] = intBool{id, true}
				}
			}
		}
	}
}
