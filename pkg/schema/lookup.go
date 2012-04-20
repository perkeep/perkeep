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

var (
	lookupMu sync.RWMutex // guards rest
	uidName  = map[int]string{}
	gidName  = map[int]string{}

	parsedGroups, parsedPasswd bool
)

func getUserFromUid(id int) string {
	return cachedName(id, uidName, lookupUserid)
}

func getGroupFromGid(id int) string {
	return cachedName(id, gidName, lookupGroupId)
}

func cachedName(id int, m map[int]string, fn func(int) string) string {
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

func lookupGroupId(id int) string {
	if parsedGroups {
		return ""
	}
	parsedGroups = true
	populateMap(gidName, "/etc/group")
	return gidName[id]
}

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
	populateMap(uidName, "/etc/passwd")
	return uidName[id]
}

// Lame fallback parsing /etc/password for non-cgo systems where os/user doesn't work,
// and used for groups (which also happens to work on OS X, generally)
func populateMap(m map[int]string, file string) {
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
			}
		}
	}
}
