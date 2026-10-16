# Configuring the server

The server's config file at $HOME/.config/perkeep/server-config.json is
JSON. It can either be in [simple mode](#simplemode) (for basic configurations), or in
[low-level mode](#lowlevel) (for any sort of crazy configuration).

# Configuration Keys & Values {#simplemode}

**Note,** if you can't find what you're looking for here, check the API docs: [/pkg/types/serverconfig](https://perkeep.org/pkg/types/serverconfig/).

* `auth`: the authentication mechanism to use. Example values include:

  * `none`: No authentication.
  * `localhost`: Accept connections coming from localhost. On Linux, this means
    connections from localhost that are also from the same user as the user
    running the server.
  * `userpass:alice:secret`: HTTP basic authentication. Username "alice",
    password "secret". Only recommended if using HTTPS.
  * `userpass:alice:secret:+localhost`: Same as above, but also accept
    localhost auth
  * `userpass:alice:secret:vivify=othersecret`: Alice has password "secret",
    but her Android phone can use password "othersecret" to do a minimal set of
    operations (upload new things, but not access anything).
  * `tailscale:ARG`: permit access only over Tailscale, where ARG is one of:
    * `full-access-to-tailnet`: to grant full read/write access to Perkeep to any
      entity on the Tailscale tailnet which has network-level access to the Perkeep server;
    * `foo@bar`: permit read/write Perkeep accesss only to the provided email (or email-like, e.g. `foo@github`) address.

* `baseURL`: Optional. If non-empty, this is the root of your URL prefix for
  your Perkeep server. Useful for when running behind a reverse proxy.
  Should not end in a slash. e.g. `https://yourserver.example.com`

* `https`: if "true", HTTPS is used.

  * `httpsCert`: path to the HTTPS certificate file. This is the public file.
    It should include the concatenation of any required intermediate certs as
    well.
  * `httpsKey`: path to the HTTPS private key file.
  * If an explicit certificate and key are not provided, a certificate from
    [Let's Encrypt](https://letsencrypt.org) is requested automatically if the
    following conditions apply:
     * A fully qualified domain name is specified in either `baseURL` or `listen`.
     * Perkeep listens on port `443` in order to answer the TLS-SNI challenge
       from Let's Encrypt.
  * As a fallback, if no FQDN is found, a self-signed certificate is generated.

* `identity`: your GPG fingerprint. A keypair is created for new users on
  start, but this may be changed if you know what you're doing.

* `identitySecretRing`: your GnuPG secret keyring file. A new keyring is
  created on start for new users, but may be changed if you know what you're
  doing.

* `listen`: The port (like "80" or ":80") or IP & port (like "10.0.0.2:8080")
  to listen for HTTP(s) connections on. Alternatively, the value
  can be `tailscale` or `tailscale:ARG` to run only on a Tailscale
  network (tailnet). In that case, the optional `ARG` can be either
  a directory in which to store the state (if it contains a slash)
  or else just the name of an instance, in which case the state
  directory is placed in `~/.config/tsnet-NAME`. The default name
  is `perkeep`.

* `shareHandler`: if true, the server's sharing functionality is enabled,
  letting your friends have access to any content you've specifically shared.
  Its URL prefix path defaults to "`/share/`".

* `shareHandlerPath`: Optional. If non-empty, it specifies the URL prefix path
  to the share handler, and the `shareHandler` value is ignored (i.e the share
  handler is enabled). Example: "`/public/`".

* `runIndex`: defaults to true. If "false", no search, no UI, no indexing.
  (These can be controlled at a more granular level by writing a low-level
  config file)

* `copyIndexToMemory`: defaults to true. If "false", don't slurp the whole
  index into memory on start-up. Specifying false will result in certain
  queries being slow, unavailable, or unsorted (work in progress). This option
  may be unsupported in the future. Keeping this set to "true" is recommended.

* `sourceRoot`: Optional. If non-empty, it specifies the path to an alternative
  Perkeep source tree, in order to override the embedded UI and/or Closure
  resources. The UI files will be expected in `<sourceRoot>/server/perkeepd/ui`
  and the Closure library in `<sourceRoot>/third_party/closure/lib`.


## Storage options {#storage}

At least one of these must be set:

* `memoryStorage`: if true, blobs will be stored in memory only. This is
  generally only useful for debugging & development.
* `blobPath`: local disk path to store blobs. (valid for diskpacked too).
* `s3`: "`accessKey:secretKey:bucketName[/optional/dir][:hostname]`"
* `b2`: "`accessKey:secretKey:bucketName[/optional/dir]:hostname`"
* `googlecloudstorage`: "`clientId:clientSecret:refreshToken:bucketName[/optional/dir]`"

The `s3` storage option's `hostname` value may be set to use an S3-compatible
endpoint instead of AWS S3, such as `my-minio-server.example.com`. A specific
region may be specified by using [Low-level Configuration](#lowlevel), though
the bucket's region will generally be detected automatically.

Additionally, there are two mutually exclusive options:

* `packRelated`: if true, blobs are automatically repacked for fast read access.
* `packBlobs`: if true, diskpacked is used instead of the default
  filestorage. This gives better write throughput, at the cost of slower
  read access.

With `packRelated`, the `s3`, `b2`, and `googlecloudstorage` storage also
use blobpacked, storing loose blobs under `loose/` and packed ones under
`packed/` in their bucket, with the index of packed blobs on local disk.
`packRelatedExcludeTypes` lists the ones that shouldn't, such as
`["s3"]`, which instead store loose blobs at the root of their bucket (or
its optional directory). Before 2021, mirrors in the cloud always used that
layout, so a mirror written before then and since has blobs in both
layouts. When an `s3` or `b2` storage finds blobs in the layout it doesn't
use, the server goes read-only until the bucket is converted to one
layout. With the server stopped, `pk copy-storage` can copy the blobs
from one layout to the other, using the current config and one with the
other layout; then delete the old layout's objects from the bucket.

For now, if more than one storage option is set, one of them is the primary
storage and the other ones are set up as mirrors. The precedence order is the
same as the order they are listed above.

Others aren't yet supported by the simple config mode. Patches to
[pkg/serverinit](https://perkeep.org/pkg/serverinit/genconfig.go) welcome.

Examples for [configuring storage backends](/doc/storage-examples.md)

## Indexing options {#indexing}

Unless `runIndex` is set to `false`, exactly one of these must be set:

* `sqlite`: path to SQLite database file to use for indexing
* `levelDB`: path to levelDB (https://github.com/syndtr/goleveldb) database file to use for indexing
* `mysql`: user@host:password
* `postgres`: user@host:password
* `memoryIndex`: if true, a memory-only indexer is used.

SQLite is the default indexer and a good starting point for most installations.
It stores the index in a local file, requires no separate database server, and
uses the pure-Go `modernc.org/sqlite` driver, including on Windows. It also
supports batched reindexing and detects an interrupted reindex so that it can
be rebuilt before serving requests.

LevelDB is another local, file-backed option that does not require a
database server. MySQL (including MariaDB) and PostgreSQL require a
separate database server; choose one if you want to manage the index with an
existing DBMS. `memoryIndex` is for development and testing: its contents are
lost when the server stops.

These options select the index backend, not where blobs are stored. The index
can be rebuilt from the blobs with `perkeepd --reindex`.

## Database-related options {#database}

* `dbname`: optional name of the index database if MySQL or PostgreSQL
  is used. If empty, dbUnique is used as part of the database name.
* `dbUnique`: optionally provides a unique value to differentiate databases on a
  DBMS shared by multiple Perkeep instances. It should not contain spaces or
  punctuation. If empty, identity is used instead. If the latter is absent, the
  current username (provided by the operating system) is used instead. For the
  index database, dbname takes priority.

When using [MariaDB](https://downloads.mariadb.org/)
or [MySQL](https://dev.mysql.com/downloads/), the user will need to be able to
create a schema in addition to the default schema. You will need `grant create,
insert, update, delete, alter, show databases on *.*` permissions for your
database user.

You can use the [pk dbinit](/cmd/pk/) command to initialize your
database, and see [dbinit.go](/cmd/pk/dbinit.go) and
[dbschema.go](/pkg/sorted/mysql/dbschema.go) if you're curious about the
details.

## Publishing options {#publishing}

Perkeep uses Go html templates to publish pages, and publishing can be
configured through the `publish` key. There is already support for an image
gallery view, which can be enabled similarly to the example below (obviously,
the camliRoot will be different).

    "publish": {
      "/pics/": {
        "camliRoot": "mypics",
        "cacheRoot": "/home/joe/var/perkeep/blobs/cache",
        "goTemplate": "gallery.html"
      }
    }

See the
[serverconfig.Publish](https://perkeep.org/pkg/types/serverconfig/#Publish)
type for all the configuration parameters.

One can create any permanode with **pk-put** or the web UI, and set its camliRoot
attribute to the value set in the config, to use it as the root permanode for
publishing.

One common use-case is for Perkeep (and the publisher app) to run behind a
reverse-proxy (such as Nginx), which takes care of the TLS termination, and
where therefore it might be acceptable for both perkeepd and publisher to listen
for non-TLS HTTP connections. In that case, the app handler configuration
parameters should be specified, such as in the example below. In addition,
please note that the reverse-proxy should not modify the Host header of the
incoming requests.

    "publish": {
        "/pics/": {
            "camliRoot": "mypics",
            "cacheRoot": "/home/joe/var/perkeep/blobs/cache",
            "goTemplate": "gallery.html",
            "apiHost": "http://localhost:3179/",
            "listen": ":44352",
            "backendURL": "http://localhost:44352/"
        }
    },

Please see the [publishing README](/doc/publishing/README) for further details
on how to set up permanodes for publishing, or if you want to
make/contribute more publishing views.


## Importers

Perkeep has several built-in importers, including:

 * Feeds (RSS, Atom, and RDF)
 * Flickr
 * Foursquare
 * Picasa
 * Pinboard
 * Twitter
 * Instapaper

These can be setup by visiting the "`/importer/`" URL prefix path, e.g. `http://localhost:3179/importer/`

### Importer scratch caches {#importer-scratch-caches}

Importers have optional scratch key/value caches, separate from their imported
blobs and permanode claims. **A cache may be deleted, absent, or stale. It must
only accelerate an import; it must not be required for importer correctness.**
Even when persisted, it is not stored or replicated in the main blob storage.

With a local `blobPath`, scratch stores default to
`<blobPath>/importer-cache/`. Importer-wide stores use a directory such as
`strava/`; account stores use `strava-<account-permanode-blobref>/`, for example
`strava-sha224-.../`. Each directory contains the selected backend's files,
such as `cache.sqlite` or a `cache.leveldb/` directory. Stores are created only
when used. The backend follows the configured file-based index type; with a
database-server or memory-only index it defaults to LevelDB.

Without a local `blobPath`, caches are memory-only. Low-level configurations
can set the importer handler's `cacheRoot` and `cacheType` explicitly:

```json
{
    "handler": "importer",
    "handlerArgs": {
        "cacheRoot": "/srv/perkeep/blobs/importer-cache",
        "cacheType": "sqlite"
    }
}
```

Strava uses its shared importer cache for application-wide rate-limit
snapshots. Stop the server before deleting persistent scratch stores.
Restoring or reindexing the main blobs does not restore these caches.

## Health checks {#health}

The server's background checks, such as of its storage and its syncs,
are listed with their results on its status page (`/status/`), where
some can also be run, canceled, or scheduled to run on their own.
`/healthz` reports a one-word summary, without authentication, for
monitoring.

Each storage has a scrub (`storage.scrub[...]`), which reads every
blob and checks that it matches its digest, and fails, naming them, if
any don't. Scrubs only run when started from the status page, or on a
schedule set there, as reading all of a storage costs time, and, for
storage in the cloud, money, which the status page estimates.

The server saves the checks' schedules, if changed on the status page,
and when they last ran, by default in `health.json` in its var
directory. To keep them elsewhere, set `healthState` to the
configuration of any [sorted key-value store](https://perkeep.org/pkg/sorted),
such as:

```json
"healthState": {
    "type": "sqlite",
    "file": "/path/to/health.sqlite"
}
```

## Windows {#windows}

The default SQLite indexer works on Windows. Its pure-Go driver does not
require cgo or a C compiler, so there is no need to switch to MySQL just to run
on Windows. If you prefer a database server, see the [indexing](#indexing) and
[database-related](#database) options above.

# Low-level configuration {#lowlevel}

You can specify a low-level configuration file to perkeepd with the same
`-configfile` option that is used to specify the simple mode configuration file.
Perkeep tests for the presence of the `"handlerConfig": true` key/value
pair to determine whether the configuration should be considered low-level.

As the low-level configuration needs to be much more detailed and precise, it is
not advised to write one from scratch. Therefore, the easiest way to get started
is to first run Perkeep with a simple configuration (or none, as one will be
automatically generated), and to download the equivalent low-level configuration
that can be found at /debug/config on your Perkeep instance.

In the following are examples of features that can only be achieved through
low-level configuration, for now.

## Replication to another Perkeep instance {#replication}

If `"/bs"` is the storage for your primary instance, such as for example:

        "/bs/": {
            "handler": "storage-blobpacked",
            "handlerArgs": {
                "largeBlobs": "/bs-packed/",
                "metaIndex": {
                    "file": "/home/you/var/perkeep/blobs/packed/packindex.leveldb",
                    "type": "leveldb"
                },
                "smallBlobs": "/bs-loose/"
            }
        },

then instead of `"/bs"`, you can use everywhere else instead in the config the
prefix `"/bsrepl/"`, which can be defined as:

        "/bsrepl/": {
            "handler": "storage-replica",
            "handlerArgs": {
                "backends": [
                    "/bs/",
                    "/r1/"
                ]
            }
        },

where `"/r1/"` is the blobserver for your other Perkeep instance, such as:

		"/r1/": {
			"handler": "storage-remote",
			"handlerArgs": {
				"url": "https://example.com:3179",
				"auth": "userpass:foo:bar",
				"skipStartupCheck": false
			}
		},
