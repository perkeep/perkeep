Sorry for my poor English. If you can help in improving English in this
documentation, please contact me.

## MyMySQL v0.4.4 (2012-03-09)

This package contains MySQL client API written entirely in Go. It works with
the MySQL protocol version 4.1 or greater. It definitely works well with MySQL
5.0 and 5.1 (I use these versions of MySQL servers for my applications).

## Changelog

#### v0.4.4

1. Row.Int, Row.Uint, Row.Int64, ... methods now panic in case of error.
2. New Row.Float method.

#### v0.4.3

1. Fixed issue with panic when server returns MYSQL_TYPE_NEWDECIMAL.
2. Decimals are returned as float64 (previous they were returned as []byte).

#### v0.4.2

1. A lot of changes in MySQL time handling:

    Datetime type replaced by time.Time.

    Time type replaced by time.Duration.

    Support for time.Time type added to godrv.

2. row.Int64/row.Uint64 methods added.

3. Rename BindParams to Bind.

#### v0.4.1

BindParams supports Go bool type. 

#### v0.4

1. Modular design:

    MySQL wire protocol handling moved to *mymysql/native*

    Thread safe wrapper of *native* engine in separate *mymysql/thrsafe*

    *mymysql/mysql* package contains definitions of interfaces to engines and
    common (engine-independent) functions.

    Automatic reconnect interface moved to *mymysql/autorc*.

2. *mysql.New* and other functions returns mostly interface types. So all
previously exported members were converted to methods (with except *mysql.Row*
and *mysql.Field* - they deffinition didn't changed).

3. Transactions added. If you use *mymysql/thrsafe" engine transactions are
full thread safe.

4. Driver for *exp/sql*.

#### v0.3.8

1. Package name changed to *mysql*.
2. Connection handler name changed from *MySQL* to *Conn*.
3. Tested with Go weekly.2011-10-06

I think it is more pleasant to write *mysql.Conn* insted of *mymysql.MySQL*.

#### v0.3.7

Works with Go release.r57.1

#### v0.3.6

The *EscapeString* method was added.

#### v0.3.5

1. *IsConnected()* method was added.
2. Package name was changed from *mymy* to *mymysql*. Now the
package name corresponds to the name of Github repository.

#### v0.3.4

float type disappeared because Go release.2011-01-20. If you use
older Go release use mymysql v0.3.3 

#### v0.3.3

1. *Datetime* and *Date* types added.
2. *Run*, *Exec* and *ExecAC* accept parameters, *Start*, *Query*,
*QueryAC* no longer accept prepared statement as first argument.

#### v0.3.2

1. *Register* method was added. It allows to register commands which will be
executed immediately after connect. It is mainly useful with
*Reconnect* method and autoreconn interface.
2. Multi statements / multi results were added.
3. Types *ENUM* and *SET* were added for prepared statements results.

#### v0.3.1

1. There is one change in v0.3, which doesn't preserve backwards compatibility
with v0.2: the name of *Execute* method was changed to *Run*. A new *Exec*
method was added. It is similar in result to *Query* method.
2. *Reconnect* method was added. After reconnect it re-prepare all prepared
statements, related to database handler that was reconnected.
3. Autoreconn interface was added. It allows not worry about making the
connection, and not wory about re-establish connection after network error or
MySQL server restart. It is certainly safe to use it with *select* queries and
to prepare statements. You must be careful with *insert* queries. I'm not sure
whether the server performs an insert: immediately after receive query or after successfull sending OK packet. Even if it is the second option, server may not
immediately notice the network failure, becouse of network buffers in kernel.
Therefore query repetitions may cause additional unnecessary inserts into
database. This interface does not appear to be useful with local transactions.


## Installing

To install all subpackages of *mymysql* you need to goinstal three of them:

     $ go get github.com/ziutek/mymysql/thrsafe
     $ go get github.com/ziutek/mymysql/autorc
     $ go get github.com/ziutek/mymysql/godrv

*go get* automagicly select proper version of *mymysql* for your Go release.
After this command *mymysql* is ready to use.

## Testing

For testing you need test database and test user:

    mysql> create database test;
    mysql> grant all privileges on test.* to testuser@localhost;
    mysql> set password for testuser@localhost = password("TestPasswd9")

Make sure that MySQL *max_allowed_packet* variable in *my.cnf* is equal or greater than 34M (needed to test long packets).

The default MySQL test server address is *127.0.0.1:3306*.

Next run tests:

    $ cd $GOPATH/src/github.com/ziutek/mymysql
    $ ./all.bash test

## Examples

### Example 1

    package main

    import (
        "os"
        "github.com/ziutek/mymysql/mysql"
        _ "github.com/ziutek/mymysql/native" // Native engine
        // _ "github.com/ziutek/mymysql/thrsafe" // Thread safe engine
    )

    func main() {
        db := mysql.New("tcp", "", "127.0.0.1:3306", user, pass, dbname)

        err := db.Connect()
        if err != nil {
            panic(err)
        }

        rows, res, err := db.Query("select * from X where id > %d", 20)
        if err != nil {
            panic(err)
        }

        for _, row := range rows {
            for _, col := range row {
                if col == nil {
                    // col has NULL value
                } else {
                    // Do something with text in col (type []byte)
                }
            }
            // You can get specific value from a row
            val1 := row[1].([]byte)

            // You can use it directly if conversion isn't needed
            os.Stdout.Write(val1)

            // You can get converted value
            number := row.Int(0)      // Zero value
            str    := row.Str(1)      // First value
            bignum := row.MustUint(2) // Second value

            // You may get value by column name
            val2 := row[res.Map("FirstColumn")].([]byte)
        }
    }

If you do not want to load the entire result into memory you may use
*Start* and *GetRow* methods:

    res, err := db.Start("select * from X")
    checkError(err)

    // Print fields names
    for _, field := range res.Fields() {
        fmt.Print(field.Name, " ")
    }
    fmt.Println()

    // Print all rows
    for {
        row, err := res.GetRow()
        checkError(err)

        if row == nil {
            // No more rows
            break
        }

        // Print all cols
        for _, col := range row {
            if col == nil {
                fmt.Print("<NULL>")
            } else {
                os.Stdout.Write(col.([]byte))
            }
            fmt.Print(" ")
        }
        fmt.Println()
    }

### Example 2 - prepared statements

You can use *Run* or *Exec* method for prepared statements:

    stmt, err := db.Prepare("insert into X values (?, ?)")
    checkError(err)

    type Data struct {
        Id  int
        Tax *float32 // nil means NULL
    }

    data = new(Data)

    for {
        err := getData(data)
        if err == endOfData {
            break       
        }
        checkError(err)

        _, err = stmt.Run(data.Id, data.Tax)
        checkError(err)
    }

*getData* is your function which retrieves data from somewhere and set *Id* and
*Tax* fields of the Data struct. In the case of *Tax* field *getData* may
assign pointer to retieved variable or nil if NULL should be stored in
database.

If you pass parameters to *Run* or *Exec* method, data are rebinded on every
method call. It isn't efficient if statement is executed more than once. You
can bind parameters and use *Run* or *Exec* method without parameters, to avoid
these unnecessary rebinds. Warning! If you use *Bind* in multithreaded
application, you should be sure that no other thread will use *Bind* for the
same statement, until you no longer need binded parameters.

The simplest way to bind parameters is:

    stmt.Bind(data.Id, data.Tax)

but you can't use it in our example, becouse parameters binded this way can't
be changed by *getData* function. You may modify bind like this:

    stmt.Bind(&data.Id, &data.Tax)

and now it should work properly. But in our example there is better solution:

    stmt.Bind(data)

If *Bind* method has one parameter, and this parameter is a struct or
a pointer to the struct, it treats all fields of this struct as parameters and
bind them,

This is improved part of previous example:

    data = new(Data)
    stmt.Bind(data)

    for {
        err := getData(data)
        if err == endOfData {
            break       
        }
        checkError(err)

        _, err = stmt.Run()
        checkError(err)
    }

### Example 3 - using SendLongData in conjunction with http.Get

    _, err = db.Start("CREATE TABLE web (url VARCHAR(80), content LONGBLOB)")
    checkError(err)

    ins, err := db.Prepare("INSERT INTO web VALUES (?, ?)")
    checkError(err)

    var url string

    ins.Bind(&url, []byte(nil)) // []byte(nil) for properly type binding

    for  {
        // Read URL from stdin
        url = ""
        fmt.Scanln(&url)
        if len(url) == 0 {
            // Stop reading if URL is blank line
            break
        }

        // Make connection
        resp, err := http.Get(url)
        checkError(err)

        // We can retrieve response directly into database because 
        // the resp.Body implements io.Reader. Use 8 kB buffer.
        err = ins.SendLongData(1, resp.Body, 8192)
        checkError(err)

        // Execute insert statement
        _, err = ins.Run()
        checkError(err)
    }

### Example 4 - multi statement / multi result

    res, err := db.Start("select id from M; select name from M")
    checkError(err)

    // Get result from first select
    for {
        row, err := res.GetRow()
        checkError(err)
        if row == nil {
            // End of first result
            break
        }

        // Do something with with the data
        functionThatUseId(row.Int(0))
    }

    // Get result from second select
    res, err = res.NextResult()
    checkError(err)
    if res == nil {
        panic("Hmm, there is no result. Why?!")
    }
    for {
        row, err := res.GetRow()
        checkError(err)
        if row == nil {
            // End of second result
            break
        }

        // Do something with with the data
        functionThatUseName(row.Str(0))
    }

### Example 5 - transactions

    import (
        "github.com/ziutek/mymysql/mysql"
        _ "github.com/ziutek/mymysql/thrsafe" // for thread safe transactions

    // [...]

    // Statements prepared before transaction begin
    ins, err := db.Prepare("insert A values (?, ?)")
    checkError(err)

    // Begin a new transaction
    tr, err := db.Begin()
    checkError(err)

    // Now db is locked, so any method that uses db and sends commands to
    // MySQL server will be blocked until Commit or Rollback will be called.
    
    // Commands in transaction are thread safe to
    go func() {
        _, err = tr.Start("insert A values (1, 'jeden')")
        checkError(err)
    } ()
    _, err = tr.Start("insert A values (2, 'dwa')")
    checkError(err)

    // You can't use statements prepared before transaction in usual way,
    //  because connection is locked by Begin method. You must bind statement
    // to transaction before use it.
    _, err = tr.Do(ins).Run(3, "trzy")
    checkError(err)
    
    // For a greater number of calls
    ti := tr.Do(ins)
    _, err = ti.Run(4, "cztery")
    checkError(err)
    _, err = ti.Run(5, "pięć")
    checkError(err)
    
    // At end you can Commit or Rollback. tr is invalidated and any use of it
    // after Commit/Rollback causes panic.
    tr.Commit()

### Example 6 - autoreconn interface

    import (
        "github.com/ziutek/mymysql/autorc"
        _ "github.com/ziutek/mymysql/thrsafe" // You may use native engine to
    )

    // [...]

    db := autorc.New("tcp", "", "127.0.0.1:3306", user, pass, dbname)

    // Initilisation commands. They will be executed after each connect.
    db.Raw.Register("set names utf8")

    // There is no need to explicity connect to the MySQL server
    rows, res, err := db.Query("SELECT * FROM R")
    checkError(err)

    // Now we are connected.

    // It does not matter if connection will be interrupted during sleep, eg
    // due to server reboot or network down.
    time.Sleep(9e9)

    // If we can reconnect in no more than db.MaxRetries attempts this
    // statement will be prepared.
    sel, err := db.Prepare("SELECT name FROM R where id > ?")
    checkError(err)

    // We can destroy our connection on server side
    _, _, err = db.Query("kill %d", db.Raw.ThreadId())
    checkError(err)

    // But it doesn't matter
    sel.Raw.Bind(2)
    rows, res, err = sel.Exec()
	checkError(err)

### Example 7 - use database/sql with mymysql driver

	// Open new connection. The uri need to have the following syntax:
	//
    //   [PROTOCOL_SPECFIIC*]DBNAME/USER/PASSWD
	//
	// where protocol spercific part may be empty (this means connection to
	// local server using default protocol). Currently possible forms:
	//   DBNAME/USER/PASSWD
	//   unix:SOCKPATH*DBNAME/USER/PASSWD
	//   tcp:ADDR*DBNAME/USER/PASSWD
	
	db, err := sql.Open("mymysql", "test/testuser/TestPasswd9")
	checkErr(err)

	// For other information about database/sql see its own documentation.

	ins, err := db.Prepare("INSERT my_table SET txt=?")
	checkErr(err)

	res, err := ins.Exec("some text")
	checkErr(err)

	id, err := res.LastInsertId()
	checkErr(err)

	checkErr(ins.Close(ins))

	rows, err := db.Query("SELECT * FROM go")
	checkErr(err)

	for rows.Next() {
		var id int
		var txt string
		checkErr(rows.Scan(&id, &txt))
		// Do something with id and txt
	}

	checkErr(db.Close())

More examples are in *examples* directory.

## Type mapping

In the case of classic text queries, all variables that are sent to the MySQL
server are embded in text query. Thus you allways convert them to a string and
send embded in SQL query:

    rows, res, err := db.Query("select * from X where id > %d", id)

After text query you always receive a text result. Mysql text result
corresponds to *[]byte* type in mymysql. It isn't *string* type due to
avoidance of unnecessary type conversions. You can allways convert *[]byte* to
*string* yourself:

    fmt.Print(string(rows[0][1].([]byte)))

or usnig *Str* helper method:

    fmt.Print(rows[0].Str(1))

There are other helper methods, for data conversion like *Int* or *Uint*:

    fmt.Print(rows[0].Int(1))

All three above examples return value received in row 0 column 1. If you prefer
to use the column names, you can use *res.Map* which maps result field names to
corresponding indexes:

    name := res.Map("name")
    fmt.Print(rows[0].Str(name))

In case of prepared statements, the type mapping is slightly more complicated.
For parameters sended from the client to the server, Go/mymysql types are
mapped for MySQL protocol types as below:

              string  -->  MYSQL_TYPE_STRING
              []byte  -->  MYSQL_TYPE_VAR_STRING
         int8, uint8  -->  MYSQL_TYPE_TINY
       int16, uint16  -->  MYSQL_TYPE_SHORT
       int32, uint32  -->  MYSQL_TYPE_LONG
       int64, uint64  -->  MYSQL_TYPE_LONGLONG
           int, uint  -->  protocol integer type which match size of int
                bool  -->  MYSQL_TYPE_TINY
             float32  -->  MYSQL_TYPE_FLOAT
             float64  -->  MYSQL_TYPE_DOUBLE
           time.Time  -->  MYSQL_TYPE_DATETIME
     mysql.Timestamp  -->  MYSQL_TYPE_TIMESTAMP
          mysql.Date  -->  MYSQL_TYPE_DATE
       time.Duration  -->  MYSQL_TYPE_TIME
          mysql.Blob  -->  MYSQL_TYPE_BLOB
                 nil  -->  MYSQL_TYPE_NULL

The MySQL server maps/converts them to a particular MySQL storage type.

For received results MySQL storage types are mapped to Go/mymysql types as
below:

                                 TINYINT  -->  int8
                        UNSIGNED TINYINT  -->  uint8
                                SMALLINT  -->  int16
                       UNSIGNED SMALLINT  -->  uint16
                          MEDIUMINT, INT  -->  int32
        UNSIGNED MEDIUMINT, UNSIGNED INT  -->  uint32
                                  BIGINT  -->  int64
                         UNSIGNED BIGINT  -->  uint64
                                   FLOAT  -->  float32
                                  DOUBLE  -->  float64
                                 DECIMAL  -->  float64
                     TIMESTAMP, DATETIME  -->  time.Time
                                    DATE  -->  mysql.Date
                                    TIME  -->  time.Duration
                                    YEAR  -->  int16
        CHAR, VARCHAR, BINARY, VARBINARY  -->  []byte
     TEXT, TINYTEXT, MEDIUMTEXT, LONGTEX  -->  []byte
    BLOB, TINYBLOB, MEDIUMBLOB, LONGBLOB  -->  []byte
                                     BIT  -->  []byte
                               SET, ENUM  -->  []byte
                                    NULL  -->  nil

## Big packets

This package can send and receive MySQL data packets that are biger than 16 MB.
This means that you can receive response rows biger than 16 MB and can execute
prepared statements with parameter data biger than 16 MB without using
SendLongData method. If you want to use this feature you need to change default
mymysql settings using *Conn.SetMaxPktSize* method and change
*max_allowed_packet* value in MySQL server configuration.

## Thread safe engine

If you import "mymysql/thrsafe" engine instead of "mymysql/native" engine all methods are thread safe, unless the description of the method says something else.

If one thread is calling *Query* or *Exec* method, other threads will be
blocked if they call *Query*, *Start*, *Exec*, *Run* or other method which send
data to the server, until *Query*/*Exec* return in first thread.

If one thread is calling *Start* or *Run* method, other threads will be
blocked if they call *Query*, *Start*, *Exec*, *Run* or other method which send
data to the server,  until all results and all rows  will be readed from
the connection in first thread.

In all my web applications I use *autorecon* interface with *thrsafe* engine.
For any new connection, one gorutine is created. There is one persistant
connection to MySQL server shared by all gorutines. Applications are usually
running on dual-core machines with GOMAXPROCS=2. I use *siege* to test any
application befor put it into production. There is example output from siege:

    # siege my.httpserver.pl -c25 -d0 -t 30s
    ** SIEGE 2.69
    ** Preparing 25 concurrent users for battle.
    The server is now under siege...
    Lifting the server siege..      done.
    Transactions:                   3212 hits
    Availability:                 100.00 %
    Elapsed time:                  29.83 secs
    Data transferred:               3.88 MB
    Response time:                  0.22 secs
    Transaction rate:             107.68 trans/sec
    Throughput:	                    0.13 MB/sec
    Concurrency:                   23.43
    Successful transactions:        3218
    Failed transactions:               0
    Longest transaction:            9.28
    Shortest transaction:           0.01

## To do

1. Transactions in auto reconnect interface.
2. Complete documentation

## Known bugs

1. Old passwords don't work: you obtain *UNK_RESULT_PKT_ERROR* if you try connect
to MySQL server as user which have old password format in *user* table.
Workaround: change password using result from MYSQL >= 4.1 *PASSWORD* function
(you can generate the old password format back using *OLD_PASSWORD* function).

2. There is MySQL "bug" in the *SUM* function. If you use prepared statements
*SUM* returns *DECIMAL* value, even if you sum integer column. mymysql returns
decimals as *float64* so cast result from sum to integer (or use *Row.Int*)
causes panic.

# Documentation

[mysql](http://gopkgdoc.appspot.com/pkg/github.com/ziutek/mymysql/mysql)
[native](http://gopkgdoc.appspot.com/pkg/github.com/ziutek/mymysql/native)
[thrsafe](http://gopkgdoc.appspot.com/pkg/github.com/ziutek/mymysql/thrsafe)
[autorc](http://gopkgdoc.appspot.com/pkg/github.com/ziutek/mymysql/autorc)
[godrv](http://gopkgdoc.appspot.com/pkg/github.com/ziutek/mymysql/godrv)
