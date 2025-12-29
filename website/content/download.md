# Download Perkeep

The latest release is [0.12 ("Toronto")](https://github.com/perkeep/perkeep/releases/tag/v0.12), released 2025-11-11.

Or, using the latest code from git:

    $ git clone https://github.com/perkeep/perkeep.git perkeep.org

## Build

If you have downloaded one of the binary archives (for Darwin, Linux, or
Windows), skip this section.

[Download and install Go](http://golang.org/doc/install) if you don't
have that installed already. Perkeep requires [Go 1.25 or newer](https://golang.org/dl/).

    $ cd perkeep.org
    $ go run make.go

## Download

Binaries are available at https://github.com/perkeep/perkeep/releases/tag/v0.12 for Linux and Windows. (macOS binaries are omitted from this release due to signing + notarization requirements by macOS which we haven't set up automation for yet)

Containers are available at https://github.com/perkeep/perkeep/pkgs/container/perkeep

## Getting started

Once you've successfully built the Perkeep components, you can run
the server with:

    $ ./bin/perkeepd

This will create [configuration](/doc/server-config) and public/private
key information in `$HOME/.config/perkeep/` (or where
`pk env configdir` points). You can start and stop perkeepd as
you see fit.

You're done setting up! Running perkeepd should open a new browser
window pointed at your keep where you can start uploading and
interacting with data.

Developers typically use the `./bin/devcam` wrapper to isolate their
test environment from their production instance and to simplify common
development tasks. If you have questions, you can ask the [mailing
list](https://groups.google.com/group/camlistore).

## Release Notes

Previous release notes:

-   [0.12 ("Toronto")](/doc/release/0.12), 2025-11-11
-   [0.11 ("Seattle")](/doc/release/0.11), 2020-11-11
-   [0.10 ("Bellingham")](/doc/release/0.10), 2018-05-02
-   [2017-05-05](docs/release/monthly/2017-05-05.html)
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
