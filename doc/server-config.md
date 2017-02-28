# Configuring the server

The server's config file at $HOME/.config/camlistore/server-config.json is
JSON. It can either be in [simple mode](#simplemode) (for basic configurations), or in
[low-level mode](#lowlevel) (for any sort of crazy configuration).

# Configuration Keys & Values {#simplemode}

**Note,** if you can't find what you're looking for here, check the API docs: [/pkg/types/serverconfig](https://camlistore.org/pkg/types/serverconfig/).

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

* `baseURL`: Optional. If non-empty, this is the root of your URL prefix for
  your Camlistore server. Useful for when running behind a reverse proxy.
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
     * Camlistore listens on port `443` in order to answer the TLS-SNI challenge
       from Let's Encrypt.
  * As a fallback, if no FQDN is found, a self-signed certificate is generated.

* `camliNetIP`: the optional internet-facing IP address for this
  Camlistore instance. If set, a name in the camlistore.net domain for
  that IP address will be requested on startup. The obtained domain name
  will then be used as the host name in the base URL.
  For now, the protocol to get the name requires receiving a challenge
  on port 443. Also, this option implies `https`, and that the HTTPS
  certificate is obtained from [Let's Encrypt](https://letsencrypt.org).
  For these reasons, this option is mutually exclusive with `baseURL`, `listen`,
  `httpsCert`, and `httpsKey`.
  On cloud instances (Google Compute Engine only for now), this option is
  automatically used.

* `identity`: your GPG fingerprint. A keypair is created for new users on
  start, but this may be changed if you know what you're doing.

* `identitySecretRing`: your GnuPG secret keyring file. A new keyring is
  created on start for new users, but may be changed if you know what you're
  doing.

* `listen`: The port (like "80" or ":80") or IP & port (like "10.0.0.2:8080")
  to listen for HTTP(s) connections on.

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
  Camlistore source tree, in order to override the embedded UI and/or Closure
  resources. The UI files will be expected in `<sourceRoot>/server/camlistored/ui`
  and the Closure library in `<sourceRoot>/third_party/closure/lib`.


## Storage options {#storage}

At least one of these must be set:

* `memoryStorage`: if true, blobs will be stored in memory only. This is
  generally only useful for debugging & development.
* `blobPath`: local disk path to store blobs. (valid for diskpacked too).
* `s3`: "`key:secret:bucket[/optional/dir]`" or "`key:secret:bucket[/optional/dir]:hostname`" (with colons,
  but no quotes).
* `googlecloudstorage`: "`clientId:clientSecret:refreshToken:bucketName[/optional/dir]`"

Additionally, there are two mutually exclusive options which only apply if `blobPath` is set:

* `packRelated`: if true, blobs are automatically repacked for fast read access.
* `packBlobs`: if true, diskpacked is used instead of the default filestorage.

For now, if more than one storage option is set, one of them is the primary
storage and the other ones are set up as mirrors. The precedence order is the
same as the order they are listed above.

Others aren't yet supported by the simple config mode. Patches to
[pkg/serverinit](https://camlistore.org/pkg/serverinit/genconfig.go) welcome.

Examples for [configuring storage backends](/doc/storage-examples.md)

## Indexing options {#indexing}

Unless `runIndex` is set to `false`, exactly one of these must be set:

* `sqlite`: path to SQLite database file to use for indexing
* `kvIndexFile`: path to kv (https://github.com/cznic/kv) database file to use for indexing
* `levelDB`: path to levelDB (https://github.com/syndtr/goleveldb) database file to use for indexing
* `mongo`: user:password@host
* `mysql`: user@host:password
* `postgres`: user@host:password
* `memoryIndex`: if true, a memory-only indexer is used.

Additionally, mongo, mysql, and postgres require the `dbname` value set.
Initialize your database with [camtool dbinit](/cmd/camtool/).

There's also an in-memory index type, but only in the low-level config, as used
by `devcam server`.


## Publishing options {#publishing}

Camlistore uses Go html templates to publish pages, and publishing can be
configured through the `publish` key. There is already support for an image
gallery view, which can be enabled similarly to the example below (obviously,
the rootPermanode will be different).

    "publish": {
      "/pics/": {
        "camliRoot": "mypics",
        "backendURL": "http://localhost:3178/",
        "cacheRoot": "/home/joe/var/camlistore/blobs/cache",
        "goTemplate": "gallery.html"
      }
    }

See the
[serverconfig.Publish](https://camlistore.org/pkg/types/serverconfig/#Publish)
type for all the configuration parameters.

One can create any permanode with camput or the UI, and set its camliRoot
attribute to the value set in the config, to use it as the root permanode for
publishing.

Please see the [publishing README](/doc/publishing/README) for further details
on how to set up permanodes for publishing, or if you want to
make/contribute more publishing views.


## Importers

Camlistore has several built-in importers, including:

 * Feeds (RSS, Atom, and RDF)
 * Flickr
 * Foursquare
 * Picasa
 * Pinboard
 * Twitter

These can be setup by visiting the "`/importer/`" URL prefix path, e.g. `http://localhost:3179/importer/`

## Windows {#windows}

The default configuration comes with SQLite for the indexer. However, getting
[mattn go-sqlite3](https://github.com/mattn/go-sqlite3) to work on windows is
not straightforward, so we suggest using one of the other indexers, like MySQL.

The following steps should get you started with MySQL:

* Dowload and install [MariaDB](https://downloads.mariadb.org/mariadb/5.5.32/)
  or [MySQL](http://dev.mysql.com/downloads/windows/installer/) (the latter
  requires .NET).
* Edit your server configuration file (if it does not exit yet, running
  **camlistored** will automatically create it):
  * Remove the <b>sqlite</b> option.
  * Add a <b>dbname</b> option. (ex: "dbname": "camliprod")
  * Add a <b>mysql</b> option. (ex: "mysql": "foo@localhost:bar")
* Create a dedicated user/password for your mysql server.
* Initialize the database with **camtool**: `camtool dbinit --user=foo
  --password=bar --host=localhost --dbname=camliprod --wipe`

Setting up MongoDB is even simpler, but the MongoDB indexer is not as well
tested as the MySQL one.


## App Engine {#appengine}

Most configuration doesn't apply on App Engine as it's pre-configured
to use the App Engine Blobstore and Datastore, as well as App Engine's
user auth mechanisms. But as of 2013-06-12 we don't yet recommend running
on App Engine; there are still some sharp corners.

The UI requires some static resources that are not included by default in the
App Engine application directory (`server/appengine/`). You can define that
directory in the server configuration file (`server/appengine/config.json`),
with the `sourceRoot` parameter, like so:

      "/ui/": {
        "handler": "ui",
        "handlerArgs": {
          "sourceRoot": "dir_name",
          "jsonSignRoot": "/sighelper/"
        }
      },

You will then have to populate that directory with all the necessary resources
(UI static files and closure library files).

Alternatively, you can run `devcam appengine` once, which will create and
populate the default directory (`server/appengine/source_root`). Please see the
[CONTRIBUTING](https://camlistore.googlesource.com/camlistore/+/master/CONTRIBUTING.md)
doc to build devcam.

# Low-level configuration {#lowlevel}

You can specify a low-level configuration file to camlistored with the same
`-configfile` option that is used to specify the simple mode configuration file.
Camlistore tests for the presence of the `"handlerConfig": true` key/value
pair to determine whether the configuration should be considered low-level.

As the low-level configuration needs to be much more detailed and precise, it is
not advised to write one from scratch. Therefore, the easiest way to get started
is to first run Camlistore with a simple configuration (or none, as one will be
automatically generated), and to download the equivalent low-level configuration
that can be found at /debug/config on your Camlistore instance.

In the following are examples of features that can only be achieved through
low-level configuration, for now.

## Replication to another Camlistore instance {#replication}

If `"/bs"` is the storage for your primary instance, such as for example:

        "/bs/": {
            "handler": "storage-blobpacked",
            "handlerArgs": {
                "largeBlobs": "/bs-packed/",
                "metaIndex": {
                    "file": "/home/you/var/camlistore/blobs/packed/packindex.leveldb",
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

where `"/r1/"` is the blobserver for your other Camlistore instance, such as:

		"/r1/": {
			"handler": "storage-remote",
			"handlerArgs": {
				"url": "https://example.com:3179",
				"auth": "userpass:foo:bar",
				"skipStartupCheck": false
			}
		},
