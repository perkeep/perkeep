// The mgo ("mango") rich MongoDB driver for Go.
//
// The mgo project (pronounced as "mango") is a rich MongoDB driver for
// the Go language.  Some information about the project may be found at
// its web page:
//
//     http://labix.org/mgo
//
// Usage of the driver revolves around the concept of sessions.  To
// get started, obtain a session using the Mongo function:
//
//     session, err := mgo.Mongo(url)
//
// This will establish one or more connections with the cluster of
// servers defined by the url parameter.  From then on, the cluster
// may be queried with multiple consistency rules (see SetMode) and
// documents trivially retrieved with statements such as:
//
//     c := session.DB(database).C(collection)
//     err := c.Find(query).One(&result)
//
// New sessions may be created by calling New, Copy, or Clone on an
// initial session.  These spawned sessions will share the same cluster
// information and connection cache, and may be easily passed into other
// methods and functions for organizing logic.  Every session created
// must have its Close method called at the end of its use.
//
// For more details, see the documentation for the types and methods.
//
package mgo
