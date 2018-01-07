httpfs
======

[![Build Status](https://travis-ci.org/shurcooL/httpfs.svg?branch=master)](https://travis-ci.org/shurcooL/httpfs) [![GoDoc](https://godoc.org/github.com/shurcooL/httpfs?status.svg)](https://godoc.org/github.com/shurcooL/httpfs)

Collection of Go packages for working with the [`http.FileSystem`](https://godoc.org/net/http#FileSystem) interface.

Installation
------------

```bash
go get -u github.com/shurcooL/httpfs/...
```

Directories
-----------

| Path                                                                              | Synopsis                                                                                                   |
|-----------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------|
| [filter](https://godoc.org/github.com/shurcooL/httpfs/filter)                     | Package filter offers an http.FileSystem wrapper with the ability to keep or skip files.                   |
| [html/vfstemplate](https://godoc.org/github.com/shurcooL/httpfs/html/vfstemplate) | Package vfstemplate offers html/template helpers that use http.FileSystem.                                 |
| [httputil](https://godoc.org/github.com/shurcooL/httpfs/httputil)                 | Package httputil implements HTTP utility functions for http.FileSystem.                                    |
| [path/vfspath](https://godoc.org/github.com/shurcooL/httpfs/path/vfspath)         | Package vfspath implements utility routines for manipulating virtual file system paths.                    |
| [union](https://godoc.org/github.com/shurcooL/httpfs/union)                       | Package union offers a simple http.FileSystem that can unify multiple filesystems at various mount points. |
| [vfsutil](https://godoc.org/github.com/shurcooL/httpfs/vfsutil)                   | Package vfsutil implements some I/O utility functions for http.FileSystem.                                 |

License
-------

-	[MIT License](https://opensource.org/licenses/mit-license.php)
