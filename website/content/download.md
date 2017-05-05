# Download Camlistore

The latest release is [2017-05-05](docs/release/monthly/2017-05-05).

Or, the canonical git repo is:

    $ git clone https://camlistore.googlesource.com/camlistore

## Build

If you have downloaded one of the binary archives (for Darwin, Linux, or
Windows), skip this section.

[Download and install Go](http://golang.org/doc/install) if you don't
have that installed already. As of revision
[c35cd68b5c](https://github.com/camlistore/camlistore/commit/c35cd68b5c9e914ef78811e88338ffd02f378a1c),
Camlistore requires [Go 1.8 or newer](https://golang.org/dl/).

Build Camlistore by running this command in the folder you downloaded or
checked out:

    $ go run make.go

## Getting started

Once you've successfully built the Camlistore components, you can run
the server with:

    $ ./bin/camlistored

This will create [configuration](/doc/server-config) and public/private
key information in `$HOME/.config/camlistore/` (or where
`camtool env configdir` points). You can start and stop camlistored as
you see fit.

You're done setting up! Running camlistored should open a new browser
window pointed at your camlistore where you can start uploading and
interacting with data.

Developers typically use the `./bin/devcam` wrapper to isolate their
test environment from their production instance and to simplify common
development tasks. If you have questions, you can ask the [mailing
list](https://groups.google.com/group/camlistore).

## Release Notes

Previous release notes:

-   [2017-03-01](docs/release/monthly/2017-03-01.html)
-   [0.9 ("Astrakhan")](/doc/release/0.9), 2015-12-30
-   [0.8 ("Tokyo")](/doc/release/0.8), 2014-08-03
-   [0.7 ("Brussels")](/doc/release/0.7), 2014-02-27
-   [0.6 ("Cannon Beach")](/doc/release/0.6), 2013-12-25
-   [0.5 ("Castletownbere")](/doc/release/0.5), 2013-09-21
-   [0.4 ("Oyens")](/doc/release/0.4), 2013-08-26
-   [0.3 ("Glebe")](/doc/release/0.3), 2013-07-28
-   [0.2 ("Portland")](/doc/release/0.2), 2013-06-22
-   [0.1 ("Grenoble")](/doc/release/0.1), 2013-06-11
