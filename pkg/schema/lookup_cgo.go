//go:build !nocgo
// +build !nocgo

/*
Copyright 2016 The Perkeep Authors.

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
	"os/user"
	"strconv"
)

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
