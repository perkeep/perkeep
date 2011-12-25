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
	. "launchpad.net/gocheck"
	"launchpad.net/mgo"
	"os"
	"strings"
	"time"
)

func (s *S) TestNewSession(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	// Do a dummy operation to wait for connection.
	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, IsNil)

	// Tweak safety and query settings to ensure other has copied those.
	session.SetSafe(nil)
	session.SetBatch(-1)
	other := session.New()
	defer other.Close()
	session.SetSafe(&mgo.Safe{})

	// Clone was copied while session was unsafe, so no errors.
	otherColl := other.DB("mydb").C("mycoll")
	err = otherColl.Insert(M{"_id": 1})
	c.Assert(err, IsNil)

	// Original session was made safe again.
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, NotNil)

	// With New(), each session has its own socket now.
	stats := mgo.GetStats()
	c.Assert(stats.MasterConns, Equals, 2)
	c.Assert(stats.SocketsInUse, Equals, 2)

	// Ensure query parameters were cloned.
	err = otherColl.Insert(M{"_id": 2})
	c.Assert(err, IsNil)

	// Ping the database to ensure the nonce has been received already.
	c.Assert(other.Ping(), IsNil)

	mgo.ResetStats()

	iter := otherColl.Find(M{}).Iter()
	c.Assert(err, IsNil)

	m := M{}
	ok := iter.Next(m)
	c.Assert(ok, Equals, true)
	err = iter.Err()
	c.Assert(err, IsNil)

	// If Batch(-1) is in effect, a single document must have been received.
	stats = mgo.GetStats()
	c.Assert(stats.ReceivedDocs, Equals, 1)
}

func (s *S) TestCloneSession(c *C) {
	session, err := mgo.Mongo("localhost:40001")
	c.Assert(err, IsNil)
	defer session.Close()

	// Do a dummy operation to wait for connection.
	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, IsNil)

	// Tweak safety and query settings to ensure clone is copying those.
	session.SetSafe(nil)
	session.SetBatch(-1)
	clone := session.Clone()
	defer clone.Close()
	session.SetSafe(&mgo.Safe{})

	// Clone was copied while session was unsafe, so no errors.
	cloneColl := clone.DB("mydb").C("mycoll")
	err = cloneColl.Insert(M{"_id": 1})
	c.Assert(err, IsNil)

	// Original session was made safe again.
	err = coll.Insert(M{"_id": 1})
	c.Assert(err, NotNil)

	// With Clone(), same socket is shared between sessions now.
	stats := mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 1)
	c.Assert(stats.SocketRefs, Equals, 2)

	// Refreshing one of them should let the original socket go,
	// while preserving the safety settings.
	clone.Refresh()
	err = cloneColl.Insert(M{"_id": 1})
	c.Assert(err, IsNil)

	// Must have used another connection now.
	stats = mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 2)
	c.Assert(stats.SocketRefs, Equals, 2)

	// Ensure query parameters were cloned.
	err = cloneColl.Insert(M{"_id": 2})
	c.Assert(err, IsNil)

	// Ping the database to ensure the nonce has been received already.
	c.Assert(clone.Ping(), IsNil)

	mgo.ResetStats()

	iter := cloneColl.Find(M{}).Iter()
	c.Assert(err, IsNil)

	m := M{}
	ok := iter.Next(m)
	c.Assert(ok, Equals, true)
	err = iter.Err()
	c.Assert(err, IsNil)

	// If Batch(-1) is in effect, a single document must have been received.
	stats = mgo.GetStats()
	c.Assert(stats.ReceivedDocs, Equals, 1)
}

func (s *S) TestSetModeStrong(c *C) {
	session, err := mgo.Mongo("localhost:40012")
	c.Assert(err, IsNil)
	defer session.Close()

	session.SetMode(mgo.Monotonic, false)
	session.SetMode(mgo.Strong, false)

	c.Assert(session.Mode(), Equals, mgo.Strong)

	result := M{}
	cmd := session.DB("admin").C("$cmd")
	err = cmd.Find(M{"ismaster": 1}).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, true)

	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	// Wait since the sync also uses sockets.
	for len(session.LiveServers()) != 3 {
		c.Log("Waiting for cluster sync to finish...")
		time.Sleep(5e8)
	}

	stats := mgo.GetStats()
	c.Assert(stats.MasterConns, Equals, 1)
	c.Assert(stats.SlaveConns, Equals, 2)
	c.Assert(stats.SocketsInUse, Equals, 1)

	session.SetMode(mgo.Strong, true)

	stats = mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestSetModeMonotonic(c *C) {
	// Must necessarily connect to a slave, otherwise the
	// master connection will be available first.
	session, err := mgo.Mongo("localhost:40012")
	c.Assert(err, IsNil)
	defer session.Close()

	session.SetMode(mgo.Monotonic, false)

	c.Assert(session.Mode(), Equals, mgo.Monotonic)

	result := M{}
	cmd := session.DB("admin").C("$cmd")
	err = cmd.Find(M{"ismaster": 1}).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, false)

	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	result = M{}
	err = cmd.Find(M{"ismaster": 1}).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, true)

	// Wait since the sync also uses sockets.
	for len(session.LiveServers()) != 3 {
		c.Log("Waiting for cluster sync to finish...")
		time.Sleep(5e8)
	}

	stats := mgo.GetStats()
	c.Assert(stats.MasterConns, Equals, 1)
	c.Assert(stats.SlaveConns, Equals, 2)
	c.Assert(stats.SocketsInUse, Equals, 1)

	session.SetMode(mgo.Monotonic, true)

	stats = mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestSetModeMonotonicAfterStrong(c *C) {
	// Test that a strong session shifting to a monotonic
	// one preserves the socket untouched.

	session, err := mgo.Mongo("localhost:40012")
	c.Assert(err, IsNil)
	defer session.Close()

	// Insert something to force a connection to the master.
	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	session.SetMode(mgo.Monotonic, false)

	// Wait since the sync also uses sockets.
	for len(session.LiveServers()) != 3 {
		c.Log("Waiting for cluster sync to finish...")
		time.Sleep(5e8)
	}

	// Master socket should still be reserved.
	stats := mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 1)

	// Confirm it's the master even though it's Monotonic by now.
	result := M{}
	cmd := session.DB("admin").C("$cmd")
	err = cmd.Find(M{"ismaster": 1}).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, true)
}

func (s *S) TestSetModeStrongAfterMonotonic(c *C) {
	// Test that shifting from Monotonic to Strong while
	// using a slave socket will keep the socket reserved
	// until the master socket is necessary, so that no
	// switch over occurs unless it's actually necessary.

	// Must necessarily connect to a slave, otherwise the
	// master connection will be available first.
	session, err := mgo.Mongo("localhost:40012")
	c.Assert(err, IsNil)
	defer session.Close()

	session.SetMode(mgo.Monotonic, false)

	// Ensure we're talking to a slave, and reserve the socket.
	result := M{}
	err = session.Run("ismaster", &result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, false)

	// Switch to a Strong session.
	session.SetMode(mgo.Strong, false)

	// Wait since the sync also uses sockets.
	for len(session.LiveServers()) != 3 {
		c.Log("Waiting for cluster sync to finish...")
		time.Sleep(5e8)
	}

	// Slave socket should still be reserved.
	stats := mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 1)

	// But any operation will switch it to the master.
	result = M{}
	err = session.Run("ismaster", &result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, true)
}

func (s *S) TestSetModeEventual(c *C) {
	// Must necessarily connect to a slave, otherwise the
	// master connection will be available first.
	session, err := mgo.Mongo("localhost:40012")
	c.Assert(err, IsNil)
	defer session.Close()

	session.SetMode(mgo.Eventual, false)

	c.Assert(session.Mode(), Equals, mgo.Eventual)

	result := M{}
	err = session.Run("ismaster", &result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, false)

	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	result = M{}
	err = session.Run("ismaster", &result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, false)

	// Wait since the sync also uses sockets.
	for len(session.LiveServers()) != 3 {
		c.Log("Waiting for cluster sync to finish...")
		time.Sleep(5e8)
	}

	stats := mgo.GetStats()
	c.Assert(stats.MasterConns, Equals, 1)
	c.Assert(stats.SlaveConns, Equals, 2)
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestSetModeEventualAfterStrong(c *C) {
	// Test that a strong session shifting to an eventual
	// one preserves the socket untouched.

	session, err := mgo.Mongo("localhost:40012")
	c.Assert(err, IsNil)
	defer session.Close()

	// Insert something to force a connection to the master.
	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	session.SetMode(mgo.Eventual, false)

	// Wait since the sync also uses sockets.
	for len(session.LiveServers()) != 3 {
		c.Log("Waiting for cluster sync to finish...")
		time.Sleep(5e8)
	}

	// Master socket should still be reserved.
	stats := mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 1)

	// Confirm it's the master even though it's Eventual by now.
	result := M{}
	cmd := session.DB("admin").C("$cmd")
	err = cmd.Find(M{"ismaster": 1}).One(&result)
	c.Assert(err, IsNil)
	c.Assert(result["ismaster"], Equals, true)

	session.SetMode(mgo.Eventual, true)

	stats = mgo.GetStats()
	c.Assert(stats.SocketsInUse, Equals, 0)
}

func (s *S) TestPrimaryShutdownStrong(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	session, err := mgo.Mongo("localhost:40021")
	c.Assert(err, IsNil)
	defer session.Close()

	// With strong consistency, this will open a socket to the master.
	result := &struct{ Host string }{}
	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)

	// Kill the master.
	host := result.Host
	s.Stop(host)

	// This must fail, since the connection was broken.
	err = session.Run("serverStatus", result)
	c.Assert(err, Equals, os.EOF)

	// With strong consistency, it fails again until reset.
	err = session.Run("serverStatus", result)
	c.Assert(err, Equals, os.EOF)

	session.Refresh()

	// Now we should be able to talk to the new master.
	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)
	c.Assert(result.Host, Not(Equals), host)
}

func (s *S) TestPrimaryShutdownMonotonic(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	session, err := mgo.Mongo("localhost:40021")
	c.Assert(err, IsNil)
	defer session.Close()

	session.SetMode(mgo.Monotonic, true)

	// Insert something to force a switch to the master.
	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	result := &struct{ Host string }{}
	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)

	// Kill the master.
	host := result.Host
	s.Stop(host)

	// This must fail, since the connection was broken.
	err = session.Run("serverStatus", result)
	c.Assert(err, Equals, os.EOF)

	// With monotonic consistency, it fails again until reset.
	err = session.Run("serverStatus", result)
	c.Assert(err, Equals, os.EOF)

	session.Refresh()

	// Now we should be able to talk to the new master.
	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)
	c.Assert(result.Host, Not(Equals), host)
}

func (s *S) TestPrimaryShutdownMonotonicWithSlave(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	session, err := mgo.Mongo("localhost:40021")
	c.Assert(err, IsNil)
	defer session.Close()

	ssresult := &struct{ Host string }{}
	imresult := &struct{ IsMaster bool }{}

	// Figure the master while still using the strong session.
	err = session.Run("serverStatus", ssresult)
	c.Assert(err, IsNil)
	err = session.Run("isMaster", imresult)
	c.Assert(err, IsNil)
	master := ssresult.Host
	c.Assert(imresult.IsMaster, Equals, true, Bug("%s is not the master", master))

	// Create new monotonic session with an explicit address to ensure
	// a slave is synchronized before the master, otherwise a connection
	// with the master may be used below for lack of other options.
	var addr string
	switch {
	case strings.HasSuffix(ssresult.Host, ":40021"):
		addr = "localhost:40022"
	case strings.HasSuffix(ssresult.Host, ":40022"):
		addr = "localhost:40021"
	case strings.HasSuffix(ssresult.Host, ":40023"):
		addr = "localhost:40021"
	default:
		c.Fatal("Unknown host: ", ssresult.Host)
	}

	session, err = mgo.Mongo(addr)
	c.Assert(err, IsNil)
	defer session.Close()

	session.SetMode(mgo.Monotonic, true)

	// Check the address of the socket associated with the monotonic session.
	c.Log("Running serverStatus and isMaster with monotonic session")
	err = session.Run("serverStatus", ssresult)
	c.Assert(err, IsNil)
	err = session.Run("isMaster", imresult)
	c.Assert(err, IsNil)
	slave := ssresult.Host
	c.Assert(imresult.IsMaster, Equals, false, Bug("%s is not a slave", slave))

	c.Assert(master, Not(Equals), slave)

	// Kill the master.
	s.Stop(master)

	// Session must still be good, since we were talking to a slave.
	err = session.Run("serverStatus", ssresult)
	c.Assert(err, IsNil)

	c.Assert(ssresult.Host, Equals, slave,
		Bug("Monotonic session moved from %s to %s", slave, ssresult.Host))

	// If we try to insert something, it'll have to hold until the new
	// master is available to move the connection, and work correctly.
	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	// Must now be talking to the new master.
	err = session.Run("serverStatus", ssresult)
	c.Assert(err, IsNil)
	err = session.Run("isMaster", imresult)
	c.Assert(err, IsNil)
	c.Assert(imresult.IsMaster, Equals, true, Bug("%s is not the master", master))

	// ... which is not the old one, since it's still dead.
	c.Assert(ssresult.Host, Not(Equals), master)
}

func (s *S) TestPrimaryShutdownEventual(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	session, err := mgo.Mongo("localhost:40021")
	c.Assert(err, IsNil)
	defer session.Close()

	result := &struct{ Host string }{}
	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)
	master := result.Host

	session.SetMode(mgo.Eventual, true)

	// Should connect to the master when needed.
	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	// Kill the master.
	s.Stop(master)

	// Should still work, with the new master now.
	coll = session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"a": 1})
	c.Assert(err, IsNil)

	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)
	c.Assert(result.Host, Not(Equals), master)
}

func (s *S) TestPreserveSocketCountOnSync(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	session, err := mgo.Mongo("localhost:40011")
	c.Assert(err, IsNil)
	defer session.Close()

	stats := mgo.GetStats()
	for stats.MasterConns+stats.SlaveConns != 3 {
		stats = mgo.GetStats()
		c.Log("Waiting for all connections to be established...")
		time.Sleep(5e8)
	}

	c.Assert(stats.SocketsAlive, Equals, 3)

	// Kill the master (with rs1, 'a' is always the master).
	s.Stop("localhost:40011")

	// Wait for the logic to run for a bit and bring it back.
	go func() {
		time.Sleep(5e9)
		s.StartAll()
	}()

	// Do an action to kick the resync logic in, and also to
	// wait until the cluster recognizes the server is back.
	result := struct{ Ok bool }{}
	err = session.Run("getLastError", &result)
	c.Assert(err, IsNil)
	c.Assert(result.Ok, Equals, true)

	for i := 0; i != 20; i++ {
		stats = mgo.GetStats()
		if stats.SocketsAlive == 3 {
			break
		}
		c.Logf("Waiting for 3 sockets alive, have %d", stats.SocketsAlive)
		time.Sleep(5e8)
	}

	// Ensure the number of sockets is preserved after syncing.
	stats = mgo.GetStats()
	c.Assert(stats.SocketsAlive, Equals, 3)
	c.Assert(stats.SocketsInUse, Equals, 1)
	c.Assert(stats.SocketRefs, Equals, 1)
}

func (s *S) TestSyncTimeout(c *C) {
	if *fast {
		c.Skip("-fast")
	}

	// 40009 isn't used by the test servers.
	session, err := mgo.Mongo("localhost:40009")
	c.Assert(err, IsNil)
	defer session.Close()

	timeout := int64(3e9)

	session.SetSyncTimeout(timeout)

	started := time.Nanoseconds()

	// Do something.
	result := struct{ Ok bool }{}
	err = session.Run("getLastError", &result)
	c.Assert(err, Matches, "no reachable servers")
	c.Assert(time.Nanoseconds()-started > timeout, Equals, true)
	c.Assert(time.Nanoseconds()-started < timeout*2, Equals, true)
}

func (s *S) TestDirect(c *C) {
	session, err := mgo.Mongo("localhost:40012?connect=direct")
	c.Assert(err, IsNil)
	defer session.Close()

	// We know that server is a slave.
	session.SetMode(mgo.Monotonic, true)

	result := &struct{ Host string }{}
	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)
	c.Assert(strings.HasSuffix(result.Host, ":40012"), Equals, true)

	stats := mgo.GetStats()
	c.Assert(stats.SocketsAlive, Equals, 1)
	c.Assert(stats.SocketsInUse, Equals, 1)
	c.Assert(stats.SocketRefs, Equals, 1)

	// We've got no master, so it'll timeout.
	session.SetSyncTimeout(5e8)

	coll := session.DB("mydb").C("mycoll")
	err = coll.Insert(M{"test": 1})
	c.Assert(err, Matches, "no reachable servers")

	// Slave is still reachable.
	result.Host = ""
	err = session.Run("serverStatus", result)
	c.Assert(err, IsNil)
	c.Assert(strings.HasSuffix(result.Host, ":40012"), Equals, true)
}

type OpCounters struct {
	Insert  int
	Query   int
	Update  int
	Delete  int
	GetMore int
	Command int
}

func getOpCounters(server string) (c *OpCounters, err os.Error) {
	session, err := mgo.Mongo(server + "?connect=direct")
	if err != nil {
		return nil, err
	}
	defer session.Close()
	session.SetMode(mgo.Monotonic, true)
	result := struct{ OpCounters }{}
	err = session.Run("serverStatus", &result)
	return &result.OpCounters, err
}

func (s *S) TestMonotonicSlaveOkFlagWithMongos(c *C) {
	session, err := mgo.Mongo("localhost:40021")
	c.Assert(err, IsNil)
	defer session.Close()

	ssresult := &struct{ Host string }{}
	imresult := &struct{ IsMaster bool }{}

	// Figure the master while still using the strong session.
	err = session.Run("serverStatus", ssresult)
	c.Assert(err, IsNil)
	err = session.Run("isMaster", imresult)
	c.Assert(err, IsNil)
	master := ssresult.Host
	c.Assert(imresult.IsMaster, Equals, true, Bug("%s is not the master", master))

	// Collect op counters for everyone.
	opc21a, err := getOpCounters("localhost:40021")
	c.Assert(err, IsNil)
	opc22a, err := getOpCounters("localhost:40022")
	c.Assert(err, IsNil)
	opc23a, err := getOpCounters("localhost:40023")
	c.Assert(err, IsNil)

	// Do a SlaveOk query through MongoS

	mongos, err := mgo.Mongo("localhost:40202")
	c.Assert(err, IsNil)
	defer mongos.Close()

	mongos.SetMode(mgo.Monotonic, true)

	coll := mongos.DB("mydb").C("mycoll")
	result := &struct{}{}
	for i := 0; i != 5; i++ {
		err := coll.Find(nil).One(result)
		c.Assert(err, Equals, mgo.NotFound)
	}

	// Collect op counters for everyone again.
	opc21b, err := getOpCounters("localhost:40021")
	c.Assert(err, IsNil)
	opc22b, err := getOpCounters("localhost:40022")
	c.Assert(err, IsNil)
	opc23b, err := getOpCounters("localhost:40023")
	c.Assert(err, IsNil)

	masterPort := master[strings.Index(master, ":")+1:]

	var masterDelta, slaveDelta int
	switch masterPort {
	case "40021":
		masterDelta = opc21b.Query - opc21a.Query
		slaveDelta = (opc22b.Query - opc22a.Query) + (opc23b.Query - opc23a.Query)
	case "40022":
		masterDelta = opc22b.Query - opc22a.Query
		slaveDelta = (opc21b.Query - opc21a.Query) + (opc23b.Query - opc23a.Query)
	case "40023":
		masterDelta = opc23b.Query - opc23a.Query
		slaveDelta = (opc21b.Query - opc21a.Query) + (opc22b.Query - opc22a.Query)
	default:
		c.Fatal("Uh?")
	}

	c.Check(masterDelta, Equals, 0) // Just the counting itself.
	c.Check(slaveDelta, Equals, 5)  // The counting for both, plus 5 queries above.
}
