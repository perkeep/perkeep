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

package mgo_test

import (
	"launchpad.net/gobson/bson"
	. "launchpad.net/gocheck"
	"launchpad.net/mgo"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Connect to the master of a deployment with a single server,
// run an insert, and then ensure the insert worked and that a
// single connection was established.
func (s *S) TestTopologySyncWithSingleMaster(c *C) {
	// Use hostname here rather than IP, to make things trickier.
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1, "b": 2})
	c.Assert(err, IsNil)

	// One connection used for discovery. Master socket recycled for
	// insert. Socket is reserved after insert.
	stats := mgo.GetStats()
	c.Assert(stats.MasterConns, Equals, 1)
	c.Assert(stats.SlaveConns, Equals, 0)
	c.Assert(stats.SocketsInUse, Equals, 1)

	// Refresh session and socket must be released.
	session.Refresh()
	stats = mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestTopologySyncWithSlaveSeed(c *C) {
	// That's supposed to be a slave. Must run discovery
	// and find out master to insert successfully.
	session, err := mgo.Mongo("localhost:40012")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.Insert(M{"a": 1, "b": 2})

	result := struct{ Ok bool }{}
	err = session.Run("getLastError", &result)
	c.Assert(err, IsNil)
	c.Assert(result.Ok, Equals, true)

	// One connection to each during discovery. Master
	// socket recycled for insert. 
	stats := mgo.GetStats()
	c.Assert(stats.MasterConns, Equals, 1)
	c.Assert(stats.SlaveConns, Equals, 2)

	// Only one socket reference alive, in the master socket owned
	// by the above session.
	c.Assert(stats.SocketsInUse, Equals, 1)

	// Refresh it, and it must be gone.
	session.Refresh()
	stats = mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestRunString(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	result := struct{ Ok int }{}
	err = session.Run("ping", &result)
	c.Assert(err, IsNil)
	c.Assert(result.Ok, Equals, 1)
}

func (s *S) TestRunValue(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	result := struct{ Ok int }{}
	err = session.Run(M{"ping": 1}, &result)
	c.Assert(err, IsNil)
	c.Assert(result.Ok, Equals, 1)
}

func (s *S) TestPing(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	// Just ensure the nonce has been received.
	result := struct{}{}
	err = session.Run("ping", &result)

	mgo.ResetStats()

	err = session.Ping()
	c.Assert(err, IsNil)

	// Pretty boring.
	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 1)
	c.Assert(stats.ReceivedOps, Equals, 1)
}

func (s *S) TestURLSingle(c *C) {
	session, err := mgo.Mongo("mongodb://localhost:40001/")
	c.Assert(err, IsNil)
	defer session.Close()

	result := struct{ Ok int }{}
	err = session.Run("ping", &result)
	c.Assert(err, IsNil)
	c.Assert(result.Ok, Equals, 1)
}

func (s *S) TestURLMany(c *C) {
	session, err := mgo.Mongo("mongodb://localhost:40011,localhost:40012/")
	c.Assert(err, IsNil)
	defer session.Close()

	result := struct{ Ok int }{}
	err = session.Run("ping", &result)
	c.Assert(err, IsNil)
	c.Assert(result.Ok, Equals, 1)
}

func (s *S) TestInsertFindOne(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.Insert(M{"a": 1, "b": 2})

	result := struct{ A, B int }{}

	err = coll.Find(M{"a": 1}).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result.A, Equals, 1)
	c.Assert(result.B, Equals, 2)
}

func (s *S) TestInsertFindOneMap(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.Insert(M{"a": 1, "b": 2})
	result := make(M)
	err = coll.Find(M{"a": 1}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["a"], Equals, 1)
	c.Assert(result["b"], Equals, 2)
}

func (s *S) TestInsertFindAll(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.Insert(M{"a": 1, "b": 2})
	coll.Insert(M{"a": 3, "b": 4})

	type R struct{ A, B int }
	var result []R

	assertResult := func() {
		c.Assert(len(result), Equals, 2)
		c.Assert(result[0].A, Equals, 1)
		c.Assert(result[0].B, Equals, 2)
		c.Assert(result[1].A, Equals, 3)
		c.Assert(result[1].B, Equals, 4)
	}

	// nil slice
	err = coll.Find(nil).Sort(M{"a": 1}).All(&result)
	c.Assert(err, IsNil)
	assertResult()

	// Previously allocated slice
	allocd := make([]R, 5)
	result = allocd
	err = coll.Find(nil).Sort(M{"a": 1}).All(&result)
	c.Assert(err, IsNil)
	assertResult()

	// Ensure result is backed by the originally allocated array
	c.Assert(&result[0], Equals, &allocd[0])

	// Non-pointer slice error
	f := func() { coll.Find(nil).All(result) }
	c.Assert(f, Panics, "result argument must be a slice address")

	// Non-slice error
	f = func() { coll.Find(nil).All(new(int)) }
	c.Assert(f, Panics, "result argument must be a slice address")
}

func (s *S) TestFindRef(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	db1 := session.DB("db1")
	db1col1 := db1.C("col1")

	db2 := session.DB("db2")
	db2col1 := db2.C("col1")

	db1col1.Insert(M{"_id": 1, "n": 1})
	db1col1.Insert(M{"_id": 2, "n": 2})
	db2col1.Insert(M{"_id": 2, "n": 3})

	result := struct{ N int }{}

	ref1 := mgo.DBRef{C: "col1", ID: 1}
	ref2 := mgo.DBRef{C: "col1", ID: 2, DB: "db2"}

	err = db1.FindRef(ref1, &result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 1)

	err = db1.FindRef(ref2, &result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 3)

	err = db2.FindRef(ref1, &result)
	c.Assert(err, Equals, mgo.NotFound)

	err = db2.FindRef(ref2, &result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 3)

	err = session.FindRef(ref2, &result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 3)

	err = session.FindRef(ref1, &result)
	c.Assert(err, Matches, "Can't resolve database for mgo.DBRef{C:\"col1\", ID:1, DB:\"\"}")
}

func (s *S) TestDatabaseAndCollectionNames(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	db1 := session.DB("db1")
	db1col1 := db1.C("col1")
	db1col2 := db1.C("col2")

	db2 := session.DB("db2")
	db2col1 := db2.C("col3")

	db1col1.Insert(M{"_id": 1})
	db1col2.Insert(M{"_id": 1})
	db2col1.Insert(M{"_id": 1})

	names, err := session.DatabaseNames()
	c.Assert(err, IsNil)
	c.Assert(names, Equals, []string{"db1", "db2"})

	names, err = db1.CollectionNames()
	c.Assert(err, IsNil)
	c.Assert(names, Equals, []string{"col1", "col2", "system.indexes"})

	names, err = db2.CollectionNames()
	c.Assert(err, IsNil)
	c.Assert(names, Equals, []string{"col3", "system.indexes"})
}

func (s *S) TestSelect(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.Insert(M{"a": 1, "b": 2})

	result := struct{ A, B int }{}

	err = coll.Find(M{"a": 1}).Select(M{"b": 1}).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result.A, Equals, 0)
	c.Assert(result.B, Equals, 2)
}

func (s *S) TestUpdate(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		err := coll.Insert(M{"k": n, "n": n})
		c.Assert(err, IsNil)
	}

	err = coll.Update(M{"k": 42}, M{"$inc": M{"n": 1}})
	c.Assert(err, IsNil)

	result := make(M)
	err = coll.Find(M{"k": 42}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 43)

	err = coll.Update(M{"k": 47}, M{"k": 47, "n": 47})
	c.Assert(err, Equals, mgo.NotFound)

	err = coll.Find(M{"k": 47}).One(result)
	c.Assert(err, Equals, mgo.NotFound)
}

func (s *S) TestUpdateNil(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	err = coll.Insert(M{"k": 42, "n": 42})
	c.Assert(err, IsNil)
	err = coll.Update(nil, M{"$inc": M{"n": 1}})
	c.Assert(err, IsNil)

	result := make(M)
	err = coll.Find(M{"k": 42}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 43)

	err = coll.Insert(M{"k": 45, "n": 45})
	c.Assert(err, IsNil)
	err = coll.UpdateAll(nil, M{"$inc": M{"n": 1}})
	c.Assert(err, IsNil)

	err = coll.Find(M{"k": 42}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 44)
	err = coll.Find(M{"k": 45}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 46)

}

func (s *S) TestUpsert(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		err := coll.Insert(M{"k": n, "n": n})
		c.Assert(err, IsNil)
	}

	id, err := coll.Upsert(M{"k": 42}, M{"k": 42, "n": 24})
	c.Assert(err, IsNil)
	c.Assert(id, IsNil)

	result := make(M)
	err = coll.Find(M{"k": 42}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 24)

	// Insert with internally created id.
	id, err = coll.Upsert(M{"k": 47}, M{"k": 47, "n": 47})
	c.Assert(err, IsNil)
	c.Assert(id, NotNil)

	err = coll.Find(M{"k": 47}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 47)

	result = make(M)
	err = coll.Find(M{"_id": id}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 47)

	// Insert with provided id.
	id, err = coll.Upsert(M{"k": 48}, M{"k": 48, "n": 48, "_id": 48})
	c.Assert(err, IsNil)
	c.Assert(id, NotNil)

	err = coll.Find(M{"k": 48}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 48)

	c.Assert(id, Equals, 48)
}

func (s *S) TestUpdateAll(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		err := coll.Insert(M{"k": n, "n": n})
		c.Assert(err, IsNil)
	}

	err = coll.UpdateAll(M{"k": M{"$gt": 42}}, M{"$inc": M{"n": 1}})
	c.Assert(err, IsNil)

	result := make(M)
	err = coll.Find(M{"k": 42}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 42)

	err = coll.Find(M{"k": 43}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 44)

	err = coll.Find(M{"k": 44}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 45)

	err = coll.UpdateAll(M{"k": 47}, M{"k": 47, "n": 47})
	c.Assert(err, Equals, mgo.NotFound)
}

func (s *S) TestRemove(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		err := coll.Insert(M{"n": n})
		c.Assert(err, IsNil)
	}

	err = coll.Remove(M{"n": M{"$gt": 42}})
	c.Assert(err, IsNil)

	result := &struct{ N int }{}
	err = coll.Find(M{"n": 42}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 42)

	err = coll.Find(M{"n": 43}).One(result)
	c.Assert(err, Equals, mgo.NotFound)

	err = coll.Find(M{"n": 44}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 44)
}

func (s *S) TestRemoveAll(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		err := coll.Insert(M{"n": n})
		c.Assert(err, IsNil)
	}

	err = coll.RemoveAll(M{"n": M{"$gt": 42}})
	c.Assert(err, IsNil)

	result := &struct{ N int }{}
	err = coll.Find(M{"n": 42}).One(result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 42)

	err = coll.Find(M{"n": 43}).One(result)
	c.Assert(err, Equals, mgo.NotFound)

	err = coll.Find(M{"n": 44}).One(result)
	c.Assert(err, Equals, mgo.NotFound)
}

func (s *S) TestDropDatabase(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	db1 := session.DB("db1")
	db1.C("col").Insert(M{"_id": 1})

	db2 := session.DB("db2")
	db2.C("col").Insert(M{"_id": 1})

	err = db1.DropDatabase()
	c.Assert(err, IsNil)

	names, err := session.DatabaseNames()
	c.Assert(err, IsNil)
	c.Assert(names, Equals, []string{"db2"})

	err = db2.DropDatabase()
	c.Assert(err, IsNil)

	names, err = session.DatabaseNames()
	c.Assert(err, IsNil)
	c.Assert(names, Equals, []string{})
}

func (s *S) TestDropCollection(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	db := session.DB("db1")
	db.C("col1").Insert(M{"_id": 1})
	db.C("col2").Insert(M{"_id": 1})

	err = db.C("col1").DropCollection()
	c.Assert(err, IsNil)

	names, err := db.CollectionNames()
	c.Assert(err, IsNil)
	c.Assert(names, Equals, []string{"col2", "system.indexes"})

	err = db.C("col2").DropCollection()
	c.Assert(err, IsNil)

	names, err = db.CollectionNames()
	c.Assert(err, IsNil)
	c.Assert(names, Equals, []string{"system.indexes"})
}

func (s *S) TestFindAndModify(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	err = coll.Insert(M{"n": 42})

	result := make(M)
	err = coll.Find(M{"n": 42}).Modify(mgo.Change{Update: M{"$inc": M{"n": 1}}}, result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 42)

	result = make(M)
	err = coll.Find(M{"n": 43}).Modify(mgo.Change{Update: M{"$inc": M{"n": 1}}, New: true}, result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 44)

	result = make(M)
	err = coll.Find(M{"n": 50}).Modify(mgo.Change{Upsert: true, Update: M{"n": 51, "o": 52}}, result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], IsNil)

	result = make(M)
	err = coll.Find(nil).Sort(M{"n": -1}).Modify(mgo.Change{Update: M{"$inc": M{"n": 1}}, New: true}, result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], Equals, 52)

	result = make(M)
	err = coll.Find(M{"n": 52}).Select(M{"o": 1}).Modify(mgo.Change{Remove: true}, result)
	c.Assert(err, IsNil)
	c.Assert(result["n"], IsNil)
	c.Assert(result["o"], Equals, 52)

	result = make(M)
	err = coll.Find(M{"n": 60}).Modify(mgo.Change{Remove: true}, result)
	c.Assert(err, Equals, mgo.NotFound)
	c.Assert(len(result), Equals, 0)
}

func (s *S) TestCountCollection(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42}
	for _, n := range ns {
		err := coll.Insert(M{"n": n})
		c.Assert(err, IsNil)
	}

	n, err := coll.Count()
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 3)
}

func (s *S) TestCountQuery(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42}
	for _, n := range ns {
		err := coll.Insert(M{"n": n})
		c.Assert(err, IsNil)
	}

	n, err := coll.Find(M{"n": M{"$gt": 40}}).Count()
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 2)
}

func (s *S) TestCountQuerySorted(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42}
	for _, n := range ns {
		err := coll.Insert(M{"n": n})
		c.Assert(err, IsNil)
	}

	n, err := coll.Find(M{"n": M{"$gt": 40}}).Sort(M{"n": 1}).Count()
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 2)
}

func (s *S) TestCountSkipLimit(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44}
	for _, n := range ns {
		err := coll.Insert(M{"n": n})
		c.Assert(err, IsNil)
	}

	n, err := coll.Find(nil).Skip(1).Limit(3).Count()
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 3)

	n, err = coll.Find(nil).Skip(1).Limit(5).Count()
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 4)
}

func (s *S) TestQueryExplain(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42}
	for _, n := range ns {
		err := coll.Insert(M{"n": n})
		c.Assert(err, IsNil)
	}

	m := M{}
	query := coll.Find(nil).Batch(1).Limit(2)
	err = query.Batch(2).Explain(m)
	c.Assert(err, IsNil)
	c.Assert(m["cursor"], Equals, "BasicCursor")
	c.Assert(m["nscanned"], Equals, 2)
	c.Assert(m["n"], Equals, 2)

	n := 0
	var result M
	err = query.For(&result, func() os.Error {
		n++
		return nil
	})
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 2)
}

func (s *S) TestQueryHint(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.EnsureIndexKey([]string{"a"})

	m := M{}
	err = coll.Find(nil).Hint([]string{"a"}).Explain(m)
	c.Assert(err, IsNil)
	c.Assert(m["indexBounds"], NotNil)
	c.Assert(m["indexBounds"].(bson.M)["a"], NotNil)
}

func (s *S) TestFindOneNotFound(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	result := struct{ A, B int }{}
	err = coll.Find(M{"a": 1}).One(&result)
	c.Assert(err, Equals, mgo.NotFound)
	c.Assert(err, Matches, "Document not found")
	c.Assert(err == mgo.NotFound, Equals, true)
}

func (s *S) TestFindNil(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.Insert(M{"n": 1})

	result := struct{ N int }{}

	err = coll.Find(nil).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result.N, Equals, 1)
}

func (s *S) TestFindIterAll(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	session.Refresh() // Release socket.

	mgo.ResetStats()

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Prefetch(0).Batch(2)
	iter := query.Iter()
	result := struct{ N int }{}
	for i := 2; i < 7; i++ {
		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)
		c.Assert(result.N, Equals, ns[i])
		if i == 1 {
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
	}

	ok := iter.Next(&result)
	c.Assert(ok, Equals, false)
	c.Assert(iter.Err(), IsNil)

	session.Refresh() // Release socket.

	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 3)     // 1*QUERY_OP + 2*GET_MORE_OP
	c.Assert(stats.ReceivedOps, Equals, 3) // and their REPLY_OPs.
	c.Assert(stats.ReceivedDocs, Equals, 5)
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestFindIterTwiceWithSameQuery(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	for i := 40; i != 47; i++ {
		coll.Insert(M{"n": i})
	}

	query := coll.Find(M{}).Sort(M{"n": 1})

	result1 := query.Skip(1).Iter()
	result2 := query.Skip(2).Iter()

	result := struct{ N int }{}
	ok := result2.Next(&result)
	c.Assert(ok, Equals, true)
	c.Assert(result.N, Equals, 42)
	ok = result1.Next(&result)
	c.Assert(ok, Equals, true)
	c.Assert(result.N, Equals, 41)
}

func (s *S) TestFindIterWithoutResults(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")
	coll.Insert(M{"n": 42})

	iter := coll.Find(M{"n": 0}).Iter()

	result := struct{ N int }{}
	ok := iter.Next(&result)
	c.Assert(ok, Equals, false)
	c.Assert(iter.Err(), IsNil)
	c.Assert(result.N, Equals, 0)
}

func (s *S) TestFindIterLimit(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	session.Refresh() // Release socket.

	mgo.ResetStats()

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Limit(3)
	iter := query.Iter()

	result := struct{ N int }{}
	for i := 2; i < 5; i++ {
		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)
		c.Assert(result.N, Equals, ns[i])
	}

	ok := iter.Next(&result)
	c.Assert(ok, Equals, false)
	c.Assert(iter.Err(), IsNil)

	session.Refresh() // Release socket.

	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 1)     // 1*QUERY_OP
	c.Assert(stats.ReceivedOps, Equals, 1) // and its REPLY_OP
	c.Assert(stats.ReceivedDocs, Equals, 3)
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestFindIterLimitWithBatch(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	// Ping the database to ensure the nonce has been received already.
	c.Assert(session.Ping(), IsNil)

	session.Refresh() // Release socket.

	mgo.ResetStats()

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Limit(3).Batch(2)
	iter := query.Iter()
	result := struct{ N int }{}
	for i := 2; i < 5; i++ {
		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)
		c.Assert(result.N, Equals, ns[i])
		if i == 3 {
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
	}

	ok := iter.Next(&result)
	c.Assert(ok, Equals, false)
	c.Assert(iter.Err(), IsNil)

	session.Refresh() // Release socket.

	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 2)     // 1*QUERY_OP + 1*GET_MORE_OP
	c.Assert(stats.ReceivedOps, Equals, 2) // and its REPLY_OPs
	c.Assert(stats.ReceivedDocs, Equals, 3)
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestFindIterSortWithBatch(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	// Without this, the logic above breaks because Mongo refuses to
	// return a cursor with an in-memory sort.
	coll.EnsureIndexKey([]string{"n"})

	// Ping the database to ensure the nonce has been received already.
	c.Assert(session.Ping(), IsNil)

	session.Refresh() // Release socket.

	mgo.ResetStats()

	query := coll.Find(M{"n": M{"$lte": 44}}).Sort(M{"n": -1}).Batch(2)
	iter := query.Iter()
	ns = []int{46, 45, 44, 43, 42, 41, 40}
	result := struct{ N int }{}
	for i := 2; i < len(ns); i++ {
		c.Logf("i=%d", i)
		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)
		c.Assert(result.N, Equals, ns[i])
		if i == 3 {
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
	}

	ok := iter.Next(&result)
	c.Assert(ok, Equals, false)
	c.Assert(iter.Err(), IsNil)

	session.Refresh() // Release socket.

	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 3)     // 1*QUERY_OP + 2*GET_MORE_OP
	c.Assert(stats.ReceivedOps, Equals, 3) // and its REPLY_OPs
	c.Assert(stats.ReceivedDocs, Equals, 5)
	c.Assert(stats.SocketsInUse, Equals, 0)
}


// Test tailable cursors in a situation where Next has to sleep to
// respect the timeout requested on Tail.
func (s *S) TestFindTailTimeoutWithSleep(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	cresult := struct{ ErrMsg string }{}

	db := session.DB("mydb")
	err = db.Run(bson.D{{"create", "mycoll"}, {"capped", true}, {"size", 1024}}, &cresult)
	c.Assert(err, IsNil)
	c.Assert(cresult.ErrMsg, Equals, "")
	coll := db.C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	session.Refresh() // Release socket.

	mgo.ResetStats()

	const timeout = 3

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Prefetch(0).Batch(2)
	iter := query.Tail(timeout)

	n := len(ns)
	result := struct{ N int }{}
	for i := 2; i != n; i++ {
		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)
		c.Assert(iter.Err(), IsNil)
		c.Assert(iter.Timeout(), Equals, false)
		c.Assert(result.N, Equals, ns[i])
		if i == 3 { // The batch boundary.
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
	}

	mgo.ResetStats()

	// The following call to Next will block.
	go func() {
		// The internal AwaitData timing of MongoDB is around 2 seconds,
		// so this should force mgo to sleep at least once by itself to
		// respect the requested timeout.
		time.Sleep(timeout*1e9 + 5e8)
		session := session.New()
		defer session.Close()
		coll := session.DB("mydb").C("mycoll")
		coll.Insert(M{"n": 47})
	}()

	c.Log("Will wait for Next with N=47...")
	ok := iter.Next(&result)
	c.Assert(ok, Equals, true)
	c.Assert(iter.Err(), IsNil)
	c.Assert(iter.Timeout(), Equals, false)
	c.Assert(result.N, Equals, 47)
	c.Log("Got Next with N=47!")

	// The following may break because it depends a bit on the internal
	// timing used by MongoDB's AwaitData logic.  If it does, the problem
	// will be observed as more GET_MORE_OPs than predicted:
	// 1*QUERY for nonce + 1*GET_MORE_OP on Next + 1*GET_MORE_OP on Next after sleep +
	// 1*INSERT_OP + 1*QUERY_OP for getLastError on insert of 47
	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 5)
	c.Assert(stats.ReceivedOps, Equals, 4)  // REPLY_OPs for 1*QUERY_OP for nonce + 2*GET_MORE_OPs + 1*QUERY_OP
	c.Assert(stats.ReceivedDocs, Equals, 3) // nonce + N=47 result + getLastError response

	c.Log("Will wait for a result which will never come...")

	started := time.Nanoseconds()
	ok = iter.Next(&result)
	c.Assert(ok, Equals, false)
	c.Assert(iter.Err(), IsNil)
	c.Assert(iter.Timeout(), Equals, true)
	c.Assert(time.Nanoseconds()-started > timeout*1e9, Equals, true)

	c.Log("Will now reuse the timed out tail cursor...")

	coll.Insert(M{"n": 48})
	ok = iter.Next(&result)
	c.Assert(ok, Equals, true)
	c.Assert(iter.Err(), IsNil)
	c.Assert(iter.Timeout(), Equals, false)
	c.Assert(result.N, Equals, 48)
}

// Test tailable cursors in a situation where Next never gets to sleep once
// to respect the timeout requested on Tail.
func (s *S) TestFindTailTimeoutNoSleep(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	cresult := struct{ ErrMsg string }{}

	db := session.DB("mydb")
	err = db.Run(bson.D{{"create", "mycoll"}, {"capped", true}, {"size", 1024}}, &cresult)
	c.Assert(err, IsNil)
	c.Assert(cresult.ErrMsg, Equals, "")
	coll := db.C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	session.Refresh() // Release socket.

	mgo.ResetStats()

	const timeout = 1

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Prefetch(0).Batch(2)
	iter := query.Tail(timeout)

	n := len(ns)
	result := struct{ N int }{}
	for i := 2; i != n; i++ {
		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)
		c.Assert(iter.Err(), IsNil)
		c.Assert(iter.Timeout(), Equals, false)
		c.Assert(result.N, Equals, ns[i])
		if i == 3 { // The batch boundary.
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
	}

	mgo.ResetStats()

	// The following call to Next will block.
	go func() {
		// The internal AwaitData timing of MongoDB is around 2 seconds,
		// so this item should arrive within the AwaitData threshold.
		time.Sleep(5e8)
		session := session.New()
		defer session.Close()
		coll := session.DB("mydb").C("mycoll")
		coll.Insert(M{"n": 47})
	}()

	c.Log("Will wait for Next with N=47...")
	ok := iter.Next(&result)
	c.Assert(ok, Equals, true)
	c.Assert(iter.Err(), IsNil)
	c.Assert(iter.Timeout(), Equals, false)
	c.Assert(result.N, Equals, 47)
	c.Log("Got Next with N=47!")

	// The following may break because it depends a bit on the internal
	// timing used by MongoDB's AwaitData logic.  If it does, the problem
	// will be observed as more GET_MORE_OPs than predicted:
	// 1*QUERY_OP for nonce + 1*GET_MORE_OP on Next +
	// 1*INSERT_OP + 1*QUERY_OP for getLastError on insert of 47
	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 4)
	c.Assert(stats.ReceivedOps, Equals, 3)  // REPLY_OPs for 1*QUERY_OP for nonce + 1*GET_MORE_OPs and 1*QUERY_OP
	c.Assert(stats.ReceivedDocs, Equals, 3) // nonce + N=47 result + getLastError response

	c.Log("Will wait for a result which will never come...")

	started := time.Nanoseconds()
	ok = iter.Next(&result)
	c.Assert(ok, Equals, false)
	c.Assert(iter.Err(), IsNil)
	c.Assert(iter.Timeout(), Equals, true)
	c.Assert(time.Nanoseconds()-started > timeout*1e9, Equals, true)

	c.Log("Will now reuse the timed out tail cursor...")

	coll.Insert(M{"n": 48})
	ok = iter.Next(&result)
	c.Assert(ok, Equals, true)
	c.Assert(iter.Err(), IsNil)
	c.Assert(iter.Timeout(), Equals, false)
	c.Assert(result.N, Equals, 48)
}

// Test tailable cursors in a situation where Next never gets to sleep once
// to respect the timeout requested on Tail.
func (s *S) TestFindTailNoTimeout(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	cresult := struct{ ErrMsg string }{}

	db := session.DB("mydb")
	err = db.Run(bson.D{{"create", "mycoll"}, {"capped", true}, {"size", 1024}}, &cresult)
	c.Assert(err, IsNil)
	c.Assert(cresult.ErrMsg, Equals, "")
	coll := db.C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	session.Refresh() // Release socket.

	mgo.ResetStats()

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Prefetch(0).Batch(2)
	iter := query.Tail(-1)
	c.Assert(err, IsNil)

	n := len(ns)
	result := struct{ N int }{}
	for i := 2; i != n; i++ {
		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)
		c.Assert(result.N, Equals, ns[i])
		if i == 3 { // The batch boundary.
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
	}

	mgo.ResetStats()

	// The following call to Next will block.
	go func() {
		time.Sleep(5e8)
		session := session.New()
		defer session.Close()
		coll := session.DB("mydb").C("mycoll")
		coll.Insert(M{"n": 47})
	}()

	c.Log("Will wait for Next with N=47...")
	ok := iter.Next(&result)
	c.Assert(ok, Equals, true)
	c.Assert(iter.Err(), IsNil)
	c.Assert(iter.Timeout(), Equals, false)
	c.Assert(result.N, Equals, 47)
	c.Log("Got Next with N=47!")

	// The following may break because it depends a bit on the internal
	// timing used by MongoDB's AwaitData logic.  If it does, the problem
	// will be observed as more GET_MORE_OPs than predicted:
	// 1*QUERY_OP for nonce + 1*GET_MORE_OP on Next +
	// 1*INSERT_OP + 1*QUERY_OP for getLastError on insert of 47
	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 4)
	c.Assert(stats.ReceivedOps, Equals, 3)  // REPLY_OPs for 1*QUERY_OP for nonce + 1*GET_MORE_OPs and 1*QUERY_OP
	c.Assert(stats.ReceivedDocs, Equals, 3) // nonce + N=47 result + getLastError response

	c.Log("Will wait for a result which will never come...")

	gotNext := make(chan bool)
	go func() {
		ok := iter.Next(&result)
		gotNext <- ok
	}()

	select {
	case ok := <-gotNext:
		c.Fatalf("Next returned: %v", ok)
	case <-time.After(3e9):
		// Good. Should still be sleeping at that point.
	}

	// Closing the session should cause Next to return.
	session.Close()

	select {
	case ok := <-gotNext:
		c.Assert(ok, Equals, false)
		c.Assert(iter.Err(), Matches, "Closed explicitly")
		c.Assert(iter.Timeout(), Equals, false)
	case <-time.After(1e9):
		c.Fatal("Closing the session did not unblock Next")
	}
}

func (s *S) TestFindForOnIter(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	session.Refresh() // Release socket.

	mgo.ResetStats()

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Prefetch(0).Batch(2)
	iter := query.Iter()

	i := 2
	var result *struct{ N int }
	err = iter.For(&result, func() os.Error {
		c.Assert(i < 7, Equals, true)
		c.Assert(result.N, Equals, ns[i])
		if i == 1 {
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
		i++
		return nil
	})
	c.Assert(err, IsNil)

	session.Refresh() // Release socket.

	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 3)     // 1*QUERY_OP + 2*GET_MORE_OP
	c.Assert(stats.ReceivedOps, Equals, 3) // and their REPLY_OPs.
	c.Assert(stats.ReceivedDocs, Equals, 5)
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestFindFor(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	session.Refresh() // Release socket.

	mgo.ResetStats()

	query := coll.Find(M{"n": M{"$gte": 42}}).Sort(M{"$natural": 1}).Prefetch(0).Batch(2)

	i := 2
	var result *struct{ N int }
	err = query.For(&result, func() os.Error {
		c.Assert(i < 7, Equals, true)
		c.Assert(result.N, Equals, ns[i])
		if i == 1 {
			stats := mgo.GetStats()
			c.Assert(stats.ReceivedDocs, Equals, 2)
		}
		i++
		return nil
	})
	c.Assert(err, IsNil)

	session.Refresh() // Release socket.

	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 3)     // 1*QUERY_OP + 2*GET_MORE_OP
	c.Assert(stats.ReceivedOps, Equals, 3) // and their REPLY_OPs.
	c.Assert(stats.ReceivedDocs, Equals, 5)
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestFindForStopOnError(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{40, 41, 42, 43, 44, 45, 46}
	for _, n := range ns {
		coll.Insert(M{"n": n})
	}

	query := coll.Find(M{"n": M{"$gte": 42}})
	i := 2
	var result *struct{ N int }
	err = query.For(&result, func() os.Error {
		c.Assert(i < 4, Equals, true)
		c.Assert(result.N, Equals, ns[i])
		if i == 3 {
			return os.NewError("stop!")
		}
		i++
		return nil
	})
	c.Assert(err, Matches, "stop!")
}

func (s *S) TestFindForResetsResult(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	ns := []int{1, 2, 3}
	for _, n := range ns {
		coll.Insert(M{"n" + strconv.Itoa(n): n})
	}

	query := coll.Find(nil).Sort(M{"$natural": 1})

	i := 0
	var sresult *struct{ N1, N2, N3 int }
	err = query.For(&sresult, func() os.Error {
		switch i {
		case 0:
			c.Assert(sresult.N1, Equals, 1)
			c.Assert(sresult.N2+sresult.N3, Equals, 0)
		case 1:
			c.Assert(sresult.N2, Equals, 2)
			c.Assert(sresult.N1+sresult.N3, Equals, 0)
		case 2:
			c.Assert(sresult.N3, Equals, 3)
			c.Assert(sresult.N1+sresult.N2, Equals, 0)
		}
		i++
		return nil
	})
	c.Assert(err, IsNil)

	i = 0
	var mresult M
	err = query.For(&mresult, func() os.Error {
		mresult["_id"] = nil, false
		switch i {
		case 0:
			c.Assert(mresult, Equals, M{"n1": 1})
		case 1:
			c.Assert(mresult, Equals, M{"n2": 2})
		case 2:
			c.Assert(mresult, Equals, M{"n3": 3})
		}
		i++
		return nil
	})
	c.Assert(err, IsNil)

	i = 0
	var iresult interface{}
	err = query.For(&iresult, func() os.Error {
		mresult, ok := iresult.(bson.M)
		c.Assert(ok, Equals, true, Bug("%#v", iresult))
		mresult["_id"] = nil, false
		switch i {
		case 0:
			c.Assert(mresult, Equals, bson.M{"n1": 1})
		case 1:
			c.Assert(mresult, Equals, bson.M{"n2": 2})
		case 2:
			c.Assert(mresult, Equals, bson.M{"n3": 3})
		}
		i++
		return nil
	})
	c.Assert(err, IsNil)
}

func (s *S) TestSort(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	coll.Insert(M{"a": 1, "b": 1})
	coll.Insert(M{"a": 2, "b": 2})
	coll.Insert(M{"a": 2, "b": 1})
	coll.Insert(M{"a": 0, "b": 1})
	coll.Insert(M{"a": 2, "b": 0})
	coll.Insert(M{"a": 0, "b": 2})
	coll.Insert(M{"a": 1, "b": 2})
	coll.Insert(M{"a": 0, "b": 0})
	coll.Insert(M{"a": 1, "b": 0})

	query := coll.Find(M{})
	query.Sort(bson.D{{"a", -1}}) // Should be ignored.
	iter := query.Sort(bson.D{{"b", -1}, {"a", 1}}).Iter()

	l := make([]int, 18)
	r := struct{ A, B int }{}
	for i := 0; i != len(l); i += 2 {
		ok := iter.Next(&r)
		c.Assert(ok, Equals, true)
		c.Assert(err, IsNil)
		l[i] = r.A
		l[i+1] = r.B
	}

	c.Assert(l, Equals, []int{0, 2, 1, 2, 2, 2, 0, 1, 1, 1, 2, 1, 0, 0, 1, 0, 2, 0})
}

func (s *S) TestPrefetching(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	docs := make([]interface{}, 200)
	for i := 0; i != 200; i++ {
		docs[i] = M{"n": i}
	}
	coll.Insert(docs...)

	// Same test three times.  Once with prefetching via query, then with the
	// default prefetching, and a third time tweaking the default settings in
	// the session.
	for testi := 0; testi != 3; testi++ {
		mgo.ResetStats()

		var iter *mgo.Iter
		var nextn int

		switch testi {
		case 0: // First, using query methods.
			iter = coll.Find(M{}).Prefetch(0.27).Batch(100).Iter()
			nextn = 73

		case 1: // Then, the default session value.
			session.SetBatch(100)
			iter = coll.Find(M{}).Iter()
			nextn = 75

		case 2: // Then, tweaking the session value.
			session.SetBatch(100)
			session.SetPrefetch(0.27)
			iter = coll.Find(M{}).Iter()
			nextn = 73
		}

		result := struct{ N int }{}
		for i := 0; i != nextn; i++ {
			ok := iter.Next(&result)
			c.Assert(ok, Equals, true)
		}

		stats := mgo.GetStats()
		c.Assert(stats.ReceivedDocs, Equals, 100)

		ok := iter.Next(&result)
		c.Assert(ok, Equals, true)

		// Ping the database just to wait for the fetch above
		// to get delivered.
		session.Run("ping", M{}) // XXX Should support nil here.

		stats = mgo.GetStats()
		c.Assert(stats.ReceivedDocs, Equals, 201) // 200 + the ping result
	}
}

func (s *S) TestSafeSetting(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	// Check the default
	safe := session.Safe()
	c.Assert(safe.W, Equals, 0)
	c.Assert(safe.WMode, Equals, "")
	c.Assert(safe.WTimeout, Equals, 0)
	c.Assert(safe.FSync, Equals, false)
	c.Assert(safe.J, Equals, false)

	// Tweak it
	session.SetSafe(&mgo.Safe{W: 1, WTimeout: 2, FSync: true})
	safe = session.Safe()
	c.Assert(safe.W, Equals, 1)
	c.Assert(safe.WMode, Equals, "")
	c.Assert(safe.WTimeout, Equals, 2)
	c.Assert(safe.FSync, Equals, true)
	c.Assert(safe.J, Equals, false)

	// Reset it again.
	session.SetSafe(&mgo.Safe{})
	safe = session.Safe()
	c.Assert(safe.W, Equals, 0)
	c.Assert(safe.WMode, Equals, "")
	c.Assert(safe.WTimeout, Equals, 0)
	c.Assert(safe.FSync, Equals, false)
	c.Assert(safe.J, Equals, false)

	// Ensure safety to something more conservative.
	session.SetSafe(&mgo.Safe{W: 5, WTimeout: 6, J: true})
	safe = session.Safe()
	c.Assert(safe.W, Equals, 5)
	c.Assert(safe.WMode, Equals, "")
	c.Assert(safe.WTimeout, Equals, 6)
	c.Assert(safe.FSync, Equals, false)
	c.Assert(safe.J, Equals, true)

	// Ensure safety to something less conservative won't change it.
	session.EnsureSafe(&mgo.Safe{W: 4, WTimeout: 7})
	safe = session.Safe()
	c.Assert(safe.W, Equals, 5)
	c.Assert(safe.WMode, Equals, "")
	c.Assert(safe.WTimeout, Equals, 6)
	c.Assert(safe.FSync, Equals, false)
	c.Assert(safe.J, Equals, true)

	// But to something more conservative will.
	session.EnsureSafe(&mgo.Safe{W: 6, WTimeout: 4, FSync: true})
	safe = session.Safe()
	c.Assert(safe.W, Equals, 6)
	c.Assert(safe.WMode, Equals, "")
	c.Assert(safe.WTimeout, Equals, 4)
	c.Assert(safe.FSync, Equals, true)
	c.Assert(safe.J, Equals, false)

	// Even more conservative.
	session.EnsureSafe(&mgo.Safe{WMode: "majority", WTimeout: 2})
	safe = session.Safe()
	c.Assert(safe.W, Equals, 0)
	c.Assert(safe.WMode, Equals, "majority")
	c.Assert(safe.WTimeout, Equals, 2)
	c.Assert(safe.FSync, Equals, true)
	c.Assert(safe.J, Equals, false)

	// WMode always overrides, whatever it is, but J doesn't.
	session.EnsureSafe(&mgo.Safe{WMode: "something", J: true})
	safe = session.Safe()
	c.Assert(safe.W, Equals, 0)
	c.Assert(safe.WMode, Equals, "something")
	c.Assert(safe.WTimeout, Equals, 2)
	c.Assert(safe.FSync, Equals, true)
	c.Assert(safe.J, Equals, false)

	// EnsureSafe with nil does nothing.
	session.EnsureSafe(nil)
	safe = session.Safe()
	c.Assert(safe.W, Equals, 0)
	c.Assert(safe.WMode, Equals, "something")
	c.Assert(safe.WTimeout, Equals, 2)
	c.Assert(safe.FSync, Equals, true)
	c.Assert(safe.J, Equals, false)

	// Changing the safety of a cloned session doesn't touch the original.
	clone := session.Clone()
	defer clone.Close()
	clone.EnsureSafe(&mgo.Safe{WMode: "foo"})
	safe = session.Safe()
	c.Assert(safe.WMode, Equals, "something")
}

func (s *S) TestSafeInsert(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	// Insert an element with a predefined key.
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, IsNil)

	mgo.ResetStats()

	// Session should be safe by default, so inserting it again must fail.
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, Matches, "E11000 duplicate.*")
	c.Assert(err.(*mgo.LastError).Code, Equals, 11000)

	// It must have sent two operations (INSERT_OP + getLastError QUERY_OP)
	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 2)

	mgo.ResetStats()

	// If we disable safety, though, it won't complain.
	session.SetSafe(nil)
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, IsNil)

	// Must have sent a single operation this time (just the INSERT_OP)
	stats = mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 1)
}

func (s *S) TestSafeParameters(c *C) {
	session, err := mgo.Mongo("localhost:40011")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	// Tweak the safety parameters to something unachievable.
	session.SetSafe(&mgo.Safe{W: 4, WTimeout: 100})
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, Matches, "timeout")
	c.Assert(err.(*mgo.LastError).WTimeout, Equals, true)
}

func (s *S) TestQueryErrorOne(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	result := struct {
		Err string "$err"
	}{}

	err = coll.Find(M{"a": 1}).Select(M{"a": M{"b": 1}}).One(&result)
	c.Assert(err, Matches, "Unsupported projection option: b")
	c.Assert(err.(*mgo.QueryError).Message, Matches, "Unsupported projection option: b")
	c.Assert(err.(*mgo.QueryError).Code, Equals, 13097)

	// The result should be properly unmarshalled with QueryError
	c.Assert(result.Err, Matches, "Unsupported projection option: b")
}

func (s *S) TestQueryErrorNext(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	result := struct {
		Err string "$err"
	}{}

	iter := coll.Find(M{"a": 1}).Select(M{"a": M{"b": 1}}).Iter()

	ok := iter.Next(&result)
	c.Assert(ok, Equals, false)

	err = iter.Err()
	c.Assert(err, Matches, "Unsupported projection option: b")
	c.Assert(err.(*mgo.QueryError).Message, Matches, "Unsupported projection option: b")
	c.Assert(err.(*mgo.QueryError).Code, Equals, 13097)

	// The result should be properly unmarshalled with QueryError
	c.Assert(result.Err, Matches, "Unsupported projection option: b")
}

func (s *S) TestEnsureIndex(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	index1 := mgo.Index{
		Key:        []string{"a"},
		Background: true,
	}

	index2 := mgo.Index{
		Key:      []string{"a", "-b"},
		Unique:   true,
		DropDups: true,
	}

	index3 := mgo.Index{
		Key:  []string{"@loc"},
		Min:  -500,
		Max:  500,
		Bits: 32,
	}

	coll := session.DB("mydb").C("mycoll")

	err = coll.EnsureIndex(index1)
	c.Assert(err, IsNil)

	err = coll.EnsureIndex(index2)
	c.Assert(err, IsNil)

	err = coll.EnsureIndex(index3)
	c.Assert(err, IsNil)

	sysidx := session.DB("mydb").C("system.indexes")

	result1 := M{}
	err = sysidx.Find(M{"name": "a_1"}).One(result1)
	c.Assert(err, IsNil)

	result2 := M{}
	err = sysidx.Find(M{"name": "a_1_b_-1"}).One(result2)
	c.Assert(err, IsNil)

	result3 := M{}
	err = sysidx.Find(M{"name": "loc_"}).One(result3)
	c.Assert(err, IsNil)

	result1["v"] = nil, false
	expected1 := M{
		"name":       "a_1",
		"key":        bson.M{"a": 1},
		"ns":         "mydb.mycoll",
		"background": true,
	}
	c.Assert(result1, Equals, expected1)

	result2["v"] = nil, false
	expected2 := M{
		"name":     "a_1_b_-1",
		"key":      bson.M{"a": 1, "b": -1},
		"ns":       "mydb.mycoll",
		"unique":   true,
		"dropDups": true,
	}
	c.Assert(result2, Equals, expected2)

	result3["v"] = nil, false
	expected3 := M{
		"name": "loc_",
		"key":  bson.M{"loc": "2d"},
		"ns":   "mydb.mycoll",
		"min":  -500,
		"max":  500,
		"bits": 32,
	}
	c.Assert(result3, Equals, expected3)

	// Ensure the index actually works for real.
	err = coll.Insert(M{"a": 1, "b": 1})
	c.Assert(err, IsNil)
	err = coll.Insert(M{"a": 1, "b": 1})
	c.Assert(err, Matches, ".*duplicate key error.*")
}

func (s *S) TestEnsureIndexWithBadInfo(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	err = coll.EnsureIndex(mgo.Index{})
	c.Assert(err, Matches, "Invalid index key:.*")

	err = coll.EnsureIndex(mgo.Index{Key: []string{""}})
	c.Assert(err, Matches, "Invalid index key:.*")
}

func (s *S) TestEnsureIndexWithUnsafeSession(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	session.SetSafe(nil)

	coll := session.DB("mydb").C("mycoll")

	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	// Should fail since there are duplicated entries.
	index := mgo.Index{
		Key:    []string{"a"},
		Unique: true,
	}

	err = coll.EnsureIndex(index)
	c.Assert(err, Matches, ".*duplicate key error.*")
}

func (s *S) TestEnsureIndexKey(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	err = coll.EnsureIndexKey([]string{"a"})
	c.Assert(err, IsNil)

	err = coll.EnsureIndexKey([]string{"a", "-b"})
	c.Assert(err, IsNil)

	sysidx := session.DB("mydb").C("system.indexes")

	result1 := M{}
	err = sysidx.Find(M{"name": "a_1"}).One(result1)
	c.Assert(err, IsNil)

	result2 := M{}
	err = sysidx.Find(M{"name": "a_1_b_-1"}).One(result2)
	c.Assert(err, IsNil)

	result1["v"] = nil, false
	expected1 := M{
		"name": "a_1",
		"key":  bson.M{"a": 1},
		"ns":   "mydb.mycoll",
	}
	c.Assert(result1, Equals, expected1)

	result2["v"] = nil, false
	expected2 := M{
		"name": "a_1_b_-1",
		"key":  bson.M{"a": 1, "b": -1},
		"ns":   "mydb.mycoll",
	}
	c.Assert(result2, Equals, expected2)
}

func (s *S) TestEnsureIndexDropIndex(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	err = coll.EnsureIndexKey([]string{"a"})
	c.Assert(err, IsNil)

	err = coll.EnsureIndexKey([]string{"-b"})
	c.Assert(err, IsNil)

	err = coll.DropIndex([]string{"-b"})
	c.Assert(err, IsNil)

	sysidx := session.DB("mydb").C("system.indexes")
	dummy := &struct{}{}

	err = sysidx.Find(M{"name": "a_1"}).One(dummy)
	c.Assert(err, IsNil)

	err = sysidx.Find(M{"name": "b_1"}).One(dummy)
	c.Assert(err, Equals, mgo.NotFound)

	err = coll.DropIndex([]string{"a"})
	c.Assert(err, IsNil)

	err = sysidx.Find(M{"name": "a_1"}).One(dummy)
	c.Assert(err, Equals, mgo.NotFound)

	err = coll.DropIndex([]string{"a"})
	c.Assert(err, Matches, "index not found")
}

func (s *S) TestEnsureIndexCaching(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	err = coll.EnsureIndexKey([]string{"a"})
	c.Assert(err, IsNil)

	mgo.ResetStats()

	// Second EnsureIndex should be cached and do nothing.
	err = coll.EnsureIndexKey([]string{"a"})
	c.Assert(err, IsNil)

	stats := mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 0)

	// Resetting the cache should make it contact the server again.
	session.ResetIndexCache()

	err = coll.EnsureIndexKey([]string{"a"})
	c.Assert(err, IsNil)

	stats = mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 2)

	// Dropping the index should also drop the cached index key.
	err = coll.DropIndex([]string{"a"})
	c.Assert(err, IsNil)

	mgo.ResetStats()

	err = coll.EnsureIndexKey([]string{"a"})
	c.Assert(err, IsNil)

	stats = mgo.GetStats()
	c.Assert(stats.SentOps, Equals, 2)
}

func (s *S) TestEnsureIndexGetIndexes(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	err = coll.EnsureIndexKey([]string{"-b"})
	c.Assert(err, IsNil)

	err = coll.EnsureIndexKey([]string{"a"})
	c.Assert(err, IsNil)

	err = coll.EnsureIndexKey([]string{"@c"})
	c.Assert(err, IsNil)

	indexes, err := coll.Indexes()

	c.Assert(indexes[0].Name, Equals, "_id_")
	c.Assert(indexes[1].Name, Equals, "a_1")
	c.Assert(indexes[1].Key, Equals, []string{"a"})
	c.Assert(indexes[2].Name, Equals, "b_-1")
	c.Assert(indexes[2].Key, Equals, []string{"-b"})
	c.Assert(indexes[3].Name, Equals, "c_")
	c.Assert(indexes[3].Key, Equals, []string{"@c"})
}

func (s *S) TestDistinct(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	for _, i := range []int{1, 4, 6, 2, 2, 3, 4} {
		coll.Insert(M{"n": i})
	}

	var result []int
	err = coll.Find(M{"n": M{"$gt": 2}}).Sort(M{"n": 1}).Distinct("n", &result)

	sort.IntSlice(result).Sort()
	c.Assert(result, Equals, []int{3, 4, 6})
}

func (s *S) TestMapReduce(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	for _, i := range []int{1, 4, 6, 2, 2, 3, 4} {
		coll.Insert(M{"n": i})
	}

	job := mgo.MapReduce{
		Map:    "function() { emit(this.n, 1); }",
		Reduce: "function(key, values) { return Array.sum(values); }",
	}
	var result []struct {
		Id    int "_id"
		Value int
	}

	info, err := coll.Find(M{"n": M{"$gt": 2}}).MapReduce(job, &result)
	c.Assert(err, IsNil)
	c.Assert(info.InputCount, Equals, 4)
	c.Assert(info.EmitCount, Equals, 4)
	c.Assert(info.OutputCount, Equals, 3)
	c.Assert(info.Time > 1e6, Equals, true)
	c.Assert(info.Time < 1e9, Equals, true)
	c.Assert(info.VerboseTime, IsNil)

	expected := map[int]int{3: 1, 4: 2, 6: 1}
	for _, item := range result {
		c.Logf("Item: %#v", &item)
		c.Assert(item.Value, Equals, expected[item.Id])
		expected[item.Id] = -1
	}

	// Weak attempt of testing that Sort gets delivered.
	_, err = coll.Find(nil).Sort(M{"n": -1}).MapReduce(job, &result)
	_, isQueryError := err.(*mgo.QueryError)
	c.Assert(isQueryError, Equals, true)
}

func (s *S) TestMapReduceFinalize(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	for _, i := range []int{1, 4, 6, 2, 2, 3, 4} {
		coll.Insert(M{"n": i})
	}

	job := mgo.MapReduce{
		Map:      "function() { emit(this.n, 1) }",
		Reduce:   "function(key, values) { return Array.sum(values) }",
		Finalize: "function(key, count) { return {count: count} }",
	}
	var result []struct {
		Id    int "_id"
		Value struct{ Count int }
	}
	_, err = coll.Find(nil).MapReduce(job, &result)
	c.Assert(err, IsNil)

	expected := map[int]int{1: 1, 2: 2, 3: 1, 4: 2, 6: 1}
	for _, item := range result {
		c.Logf("Item: %#v", &item)
		c.Assert(item.Value.Count, Equals, expected[item.Id])
		expected[item.Id] = -1
	}
}

func (s *S) TestMapReduceToCollection(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	for _, i := range []int{1, 4, 6, 2, 2, 3, 4} {
		coll.Insert(M{"n": i})
	}

	job := mgo.MapReduce{
		Map:    "function() { emit(this.n, 1); }",
		Reduce: "function(key, values) { return Array.sum(values); }",
		Out:    "mr",
	}

	info, err := coll.Find(nil).MapReduce(job, nil)
	c.Assert(err, IsNil)
	c.Assert(info.InputCount, Equals, 7)
	c.Assert(info.EmitCount, Equals, 7)
	c.Assert(info.OutputCount, Equals, 5)
	c.Assert(info.Time > 1e6, Equals, true)
	c.Assert(info.Time < 1e9, Equals, true)
	c.Assert(info.Collection, Equals, "mr")
	c.Assert(info.Database, Equals, "mydb")

	expected := map[int]int{1: 1, 2: 2, 3: 1, 4: 2, 6: 1}
	var item *struct {
		Id    int "_id"
		Value int
	}
	mr := session.DB("mydb").C("mr")
	err = mr.Find(nil).For(&item, func() os.Error {
		c.Logf("Item: %#v", &item)
		c.Assert(item.Value, Equals, expected[item.Id])
		expected[item.Id] = -1
		return nil
	})
	c.Assert(err, IsNil)
}

func (s *S) TestMapReduceToOtherDb(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	for _, i := range []int{1, 4, 6, 2, 2, 3, 4} {
		coll.Insert(M{"n": i})
	}

	job := mgo.MapReduce{
		Map:    "function() { emit(this.n, 1); }",
		Reduce: "function(key, values) { return Array.sum(values); }",
		Out:    bson.D{{"replace", "mr"}, {"db", "otherdb"}},
	}

	info, err := coll.Find(nil).MapReduce(job, nil)
	c.Assert(err, IsNil)
	c.Assert(info.InputCount, Equals, 7)
	c.Assert(info.EmitCount, Equals, 7)
	c.Assert(info.OutputCount, Equals, 5)
	c.Assert(info.Time > 1e6, Equals, true)
	c.Assert(info.Time < 1e9, Equals, true)
	c.Assert(info.Collection, Equals, "mr")
	c.Assert(info.Database, Equals, "otherdb")

	expected := map[int]int{1: 1, 2: 2, 3: 1, 4: 2, 6: 1}
	var item *struct {
		Id    int "_id"
		Value int
	}
	mr := session.DB("otherdb").C("mr")
	err = mr.Find(nil).For(&item, func() os.Error {
		c.Logf("Item: %#v", &item)
		c.Assert(item.Value, Equals, expected[item.Id])
		expected[item.Id] = -1
		return nil
	})
	c.Assert(err, IsNil)
}

func (s *S) TestMapReduceScope(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	coll.Insert(M{"n": 1})

	job := mgo.MapReduce{
		Map:    "function() { emit(this.n, x); }",
		Reduce: "function(key, values) { return Array.sum(values); }",
		Scope:  M{"x": 42},
	}

	var result []bson.M
	_, err = coll.Find(nil).MapReduce(job, &result)
	c.Assert(len(result), Equals, 1)
	c.Assert(result[0]["value"], Equals, 42.0)
}

func (s *S) TestMapReduceVerbose(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	coll.Insert(M{"n": 1})

	job := mgo.MapReduce{
		Map:     "function() { emit(this.n, 1); }",
		Reduce:  "function(key, values) { return Array.sum(values); }",
		Verbose: true,
	}

	info, err := coll.Find(nil).MapReduce(job, nil)
	c.Assert(err, IsNil)
	c.Assert(info.VerboseTime, NotNil)
	c.Assert(info.VerboseTime.Total > 1e6, Equals, true)
	c.Assert(info.VerboseTime.Total < 1e9, Equals, true)
}

func (s *S) TestMapReduceLimit(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	coll := session.DB("mydb").C("mycoll")

	for _, i := range []int{1, 4, 6, 2, 2, 3, 4} {
		coll.Insert(M{"n": i})
	}

	job := mgo.MapReduce{
		Map:    "function() { emit(this.n, 1); }",
		Reduce: "function(key, values) { return Array.sum(values); }",
	}

	var result []bson.M
	_, err = coll.Find(nil).Limit(3).MapReduce(job, &result)
	c.Assert(err, IsNil)
	c.Assert(len(result), Equals, 3)
}

func (s *S) TestBuildInfo(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	info, err := session.BuildInfo()
	c.Assert(err, IsNil)

	var v []int
	for _, a := range strings.Split(info.Version, ".") {
		i, err := strconv.Atoi(a)
		c.Assert(err, IsNil)
		v = append(v, i)
	}
	for len(v) < 4 {
		v = append(v, 0)
	}

	c.Assert(info.VersionArray, Equals, v)
	c.Assert(info.GitVersion, Matches, "[a-z0-9]+")
	c.Assert(info.SysInfo, Matches, ".*[0-9:]+.*")
	if info.Bits != 32 && info.Bits != 64 {
		c.Fatalf("info.Bits is %d", info.Bits)
	}
	if info.MaxObjectSize < 8192 {
		c.Fatalf("info.MaxObjectSize seems too small: %d", info.MaxObjectSize)
	}
}
