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

package mongo

import (
	"camlistore.org/pkg/index"
)

func NewMongoIndex(mgw *MongoWrapper) (*index.Index, error) {
	return newMongoIndex(mgw)
}

// AddUser creates a new user in mgw.Database so the mongo indexer
// tests can be run as authenticated with this user.
func AddUser(mgw *MongoWrapper, user, password string) error {
	ses, err := mgw.getConnection()
	if err != nil {
		return err
	}
	defer ses.Close()
	return ses.DB(mgw.Database).AddUser(user, password, false)
}
