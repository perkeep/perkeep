// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*

Package dbm (experimental/WIP) implements a simple database engine, a hybrid of
a hierarchical[1] and/or a key-value one[2].

A dbm database stores arbitrary data in named multidimensional arrays and/or
named flat Files. It aims more for small DB footprint rather than for access
speed. Dbm was written for a project running on an embedded ARM Linux system.

Experimental release notes

This is an experimental release. However, it is now nearly feature complete.

Key collating respecting client supplied locale is not yet implemented. Planned
when exp/locale materializes. Because of this, the dbm API doesn't yet allow
to really define other than default collating of keys. At least some sort
of client defined collating will be incorporated after Go 1.1 release.

No serious attempts to profile and/or improve performance were made (TODO).

	WARNING: THE DBM API IS SUBJECT TO CHANGE.
	WARNING: THE DBM FILE FORMAT IS SUBJECT TO CHANGE.
	WARNING: NOT READY FOR USE IN PRODUCTION.

Targeted use cases

ATM using disk based dbm DBs with 2PC/WAL/recovery enabled is supposed to be
safe (modulo any unknown bugs).

Concurrent access

All of the dbm API is (intended to be) safe for concurrent use by multiple
goroutines. However, data races stemming from, for example, one goroutine
seeing a value in a tree and another deleting it before the first one gets back
to process it, must be handled outside of dbm.  Still any CRUD operations, as
in this date race example, are atomic and safe per se and will not corrupt the
database structural integrity.  Non coordinated updates of a DB may corrupt its
semantic and/or schema integrity, though. Failed DB updates performed not
within a structural transaction may corrupt the DB.

Also please note that passing racy arguments to an otherwise concurrent safe
API makes that API act racy as well.

Scalars

Keys and values of an Array are multi-valued and every value must be a
"scalar". Types called "scalar" are:

     nil (the typeless one)
     bool
     all integral types: [u]int8, [u]int16, [u]int32, [u]int, [u]int64
     all floating point types: float32, float64
     all complex types: complex64, complex128
     []byte (64kB max)
     string (64kb max)

Collating

Values in an Array are always ordered in the collating order of the respective
keys. For details about the collating order please see lldb.Collate. There's a
plan for a mechanism respecting user-supplied locale applied to string
collating, but the required API differences call for a whole different package
perhaps emerging in the future.

Multidimensional sparse arrays

A multidimensional array can have many subscripts. Each subscript must be one
of the bellow types:

	nil (typeless)
	bool
	int int8 int16 int32 int64
	uint byte uint8 uint16 uint32 uint64
	float32 float64
	complex64 complex128
	[]byte
	string

The "outer" ordering is: nil, bool, number, []byte, string. IOW, nil is
"smaller" than anything else except other nil, numbers collate before []byte,
[]byte collate before strings, etc.

By using single item subscripts the multidimensional array "degrades" to a
plain key-value map. As the arrays are named, both models can coexist in the
same database.  Dbm arrays are modeled after those of MUMPS[3], so the acronym
is for DB/M instead of Data Base Manager[4]. For a more detailed discussion of
multidimensional arrays please see [5]. Some examples from the same source
rewritten and/or modified for dbm. Note: Error values and error checking is not
present in the bellow examples.

This is a MUMPS statement
	^Stock("slip dress", 4, "blue", "floral") = 3

This is its dbm equivalent
	db.Set(3, "Stock", "slip dress", 4, "blue", "floral")

Dump of "Stock"
	                "slip dress", 4, "blue", "floral" → 3
	----
	db.Get("Stock", "slip dress", 4, "blue", "floral") → 3

Or for the same effect:
	stock := db.Array("Stock")
	stock.Set(3, "slip dress", 4, "blue", "floral")

Dump of "Stock"
	                "slip dress", 4, "blue", "floral" → 3
	----
	db.Get("Stock", "slip dress", 4, "blue", "floral") → 3
	      stock.Get("slip dress", 4, "blue", "floral") → 3

Or
	blueDress := db.Array("Stock", "slip dress", 4, "blue")
	blueDress.Set(3, "floral")

Dump of "Stock"
	                "slip dress", 4, "blue", "floral" → 3
	----
	db.Get("Stock", "slip dress", 4, "blue", "floral") → 3
	                           blueDress.Get("floral") → 3

Similarly:
	invoiceNum := 314159
	customer := "Google"
	when := time.Now().UnixNano()
	parts := []struct{ num, qty, price int }{
		{100001, 2, 300},
		{100004, 5, 600},
	}

	invoice := db.Array("Invoice")
	invoice.Set(when, invoiceNum, "Date")
	invoice.Set(customer, invoiceNum, "Customer")
	invoice.Set(len(parts), invoiceNum, "Items")	// # of Items in the invoice
	for i, part := range parts {
		invoice.Set(part.num, invoiceNum, "Items", i, "Part")
		invoice.Set(part.qty, invoiceNum, "Items", i, "Quantity")
		invoice.Set(part.price, invoiceNum, "Items", i, "Price")
	}

Dump of "Invoice"
	                      314159, "Customer" → "Google"
	                      314159, "Date" → 1363864307518685049
	                      314159, "Items" → 2
	                      314159, "Items", 0, "Part" → 100001
	                      314159, "Items", 0, "Price" → 300
	                      314159, "Items", 0, "Quantity" → 2
	                      314159, "Items", 1, "Part" → 100004
	                      314159, "Items", 1, "Price" → 600
	                      314159, "Items", 1, "Quantity" → 5
	----
	db.Get("Invoice", invoiceNum, "Customer") → customer
	db.Get("Invoice", invoiceNum, "Date") → when
	...
	      invoice.Get(invoiceNum, "Customer") → customer
	      invoice.Get(invoiceNum, "Date") → time.Then().UnixName
	      invoice.Get(invoiceNum, "Items") → len(parts)
	      invoice.Get(invoiceNum, "Items", 0, "Part") → parts[0].part
	      invoice.Get(invoiceNum, "Items", 0, "Quantity") → parts[0].qty
	      invoice.Get(invoiceNum, "Items", 0, "Price") → parts[0].price
	      invoice.Get(invoiceNum, "Items", 1, "Part") → parts[1].part
	      ...

Or for the same effect
	invoice := db.Array("Invoice", invoiceNum)
	invoice.Set(when, "Date")
	invoice.Set(customer, "Customer")
	items := invoice.Array("Items")
	items.Set(len(parts))	// # of Items in the invoice
	for i, part := range parts {
		items.Set(part.num, i, "Part")
		items.Set(part.qty, i, "Quantity")
		items.Set(part.price, i, "Price")
	}

Dump of "Invoice"
	                      314159, "Customer" → "Google"
	                      314159, "Date" → 1363865032036475263
	                      314159, "Items" → 2
	                      314159, "Items", 0, "Part" → 100001
	                      314159, "Items", 0, "Price" → 300
	                      314159, "Items", 0, "Quantity" → 2
	                      314159, "Items", 1, "Part" → 100004
	                      314159, "Items", 1, "Price" → 600
	                      314159, "Items", 1, "Quantity" → 5
	----
	db.Get("Invoice", invoiceNum, "Customer") → customer
	...
	                  invoice.Get("Customer") → customer
	                  invoice.Get("Date") → time.Then().UnixName
	                    items.Get() → len(parts)
	                             items.Get(0, "Part") → parts[0].part
	                             items.Get(0, "Quantity") → parts[0].qty
	                             items.Get(0, "Price") → parts[0].price
	                             items.Get(1, "Part") → parts[1].part
	                             ...

Values are not limited to a single item. The DB "schema" used above can be
changed to use a "record" for the invoice item details:
	invoice := db.Array("Invoice", invoiceNum)
	invoice.Set(when, "Date")
	invoice.Set(customer, "Customer")
	items := invoice.Array("Items")
	items.Set(len(parts))	// # of Items in the invoice
	for i, part := range parts {
		items.Set([]interface{}{part.num, part.qty, part.price}, i)
	}

Dump of "Invoice"
	314159, "Customer" → "Google"
	314159, "Date" → 1363865958506983228
	314159, "Items" → 2
	314159, "Items", 0 → []interface{100001, 2, 300}
	314159, "Items", 1 → []interface{100004, 5, 600}
	----
        items.Get() → len(parts)
                  items.Get(0) → []interface{parts[0].num, parts[0].qty, parts[O].price}
                  items.Get(1) → []interface{parts[1].num, parts[1].qty, parts[1].price}
                  ...


Naming issues

Array and File names can by any string value, including en empty string or a
non UTF-8 string.  Names are limited in size to approximately 64 kB. For
compatibility with future dbm versions and/or with other dbm based products, it
is recommended to use only array names which are a valid and exported[6] Go
identifier or rooted names.

Rooted names

Rooted name is a pathname beginning in a slash ('/'). The base name of such
path should be (by recommendation) again a valid and exported Go identifier.

Name spaces

Arrays namespace and Files namespace are disjoint. Entities in any namespace
having a rooted name with prefix '/tmp/' are removed from the DB automatically
on Open.

Access denied errors

Attemtps to mutate Arrays or Files or any other forbidden action return
lldb.ErrPERM.

ACID Finite State Machine

For Options.ACID == ACIDFull and GracePeriod != 0 the state transition table
for transaction collecting is:

	+------------+-----------------+---------------+-----------------+
	|\  Event    |                 |               |                 |
	| \--------\ |     enter       |     leave     |     timeout     |
	|   State   \|                 |               |                 |
	+------------+-----------------+---------------+-----------------+
	| idle       | BeginUpdate     | panic         | panic           |
	|            | nest = 1        |               |                 |
	|            | start timer     |               |                 |
	|            | S = collecting  |               |                 |
	+------------+-----------------+---------------+-----------------+
	| collecting | nest++          | nest--        | S = collecting- |
	|            |                 | if nest == 0  |     triggered   |
	|            |                 |     S = idle- |                 |
	|            |                 |         armed |                 |
	+------------+-----------------+---------------+-----------------+
	| idle-      | nest = 1        | panic         | EndUpdate       |
	| aremd      | S = collecting- |               | S = idle        |
	|            |     armed       |               |                 |
	+------------+-----------------+---------------+-----------------+
	| collecting-| nest++          | nest--        | S = collecting- |
	| armed      |                 | if nest == 0  |     triggered   |
	|            |                 |     S = idle- |                 |
	|            |                 |         armed |                 |
	+------------+-----------------+---------------+-----------------+
	| collecting-| nest++          | nest--        | panic           |
	| triggered  |                 | if nest == 0  |                 |
	|            |                 |     EndUpdate |                 |
	|            |                 |     S = idle  |                 |
	+------------+-----------------+---------------+-----------------+

	'enter': Invoking any DB state mutating operation.
	'leave': Returning from any DB state mutating operation.

NOTE: The collecting "interval" can be modified by invoking db.BeginUpdate and
db.EndUpdate.

References

Links fom the above godocs.

  [1]: http://en.wikipedia.org/wiki/Hierarchical_database_model
  [2]: http://en.wikipedia.org/wiki/NoSQL#Key.E2.80.93value_store
  [3]: http://en.wikipedia.org/wiki/MUMPS
  [4]: http://en.wikipedia.org/wiki/Dbm
  [5]: http://www.intersystems.com/cache/technology/techguide/cache_tech-guide_02.html
  [6]: http://golang.org/pkg/go/ast/#IsExported

*/
package dbm
