# Environment Variables

The standard library's [strconv.ParseBool][] is used to parse boolean environment
variables.  It accepts 1, t, T, TRUE, true, True, 0, f, F, FALSE, false,
False. Any other value is an implicit false.

For integer values, [strconv.Atoi][] is used which means only base 10 numbers are
valid.

[strconv.ParseBool]: https://golang.org/pkg/strconv/#ParseBool
[strconv.Atoi]: https://golang.org/pkg/strconv/#Atoi

## General

`AWS_ACCESS_KEY_ID` (string)
and
`AWS_ACCESS_KEY_SECRET` (string)
: See http://docs.aws.amazon.com/fws/1.1/GettingStartedGuide/index.html?AWSCredentials.html.
  Used in s3 tests.  If not set some tests are skip.  If set, queries will be
  sent to Amazon's S3 service.

`CAMLI_APP_BINDIR` (string)
: Path to the directory where Perkeep first looks for the server applications
  executables, when starting them. It looks in PATH otherwise.

`CAMLI_AUTH` (string)
: See [server-config](server-config.md).
  Used as a fallback in pkg/client.Client (except on android) when
  configuration files lack and 'auth' entry.  If a client is using the -server
  commandline to specify the Perkeep instance to talk to, this env var
  takes precedence over that specified in the configuration files.

`CAMLI_BASEURL` (string)
: URL set in devcam to act as a baseURL in the devcam launched perkeepd.

`PERKEEP_CACHE_DIR` (string)
or
`CAMLI_CACHE_DIR` (string)
: Path used by [pkg/osutil](/pkg/osutil) to override operating system specific
  cache directory.

`CAMLI_CONFIG_DIR` (string)
: Path used by pkg/osutil to override operating system specific configuration
  directory.

`CAMLI_DBNAME` (string)
: Backend specific data source name (DSN).
  Set in devcam to pass database configuration for the indexer to the devcam
  launched perkeepd.

`CAMLI_DEFAULT_SERVER` (string)
: The server alias to use by default. The string is the server's alias key
  in the client-config.json "servers" object. If set, the `CAMLI_DEFAULT_SERVER`
  takes precedence over the "default" bool in client-config.json.

`CAMLI_DEV_CAMLI_ROOT` (string)
: If set, the base directory of Perkeep when in dev mode.
  Used by [pkg/server](/pkg/server) for finding static assests (js, css, html).
  Used as a signal by [pkg/index/\*](/pkg/index) and [pkg/server](/pkg/server)
  to output more helpful error message when run under devcam.

`CAMLI_DEV_CLOSURE_DIR` (string)
: Path override for [pkg/server](/pkg/server). If specified, this path will be
  used to serve the closure handler.

`CAMLI_DISABLE_IMPORTERS` (bool)
: If true, importers are disabled (at least automatic background
  importing, e.g. at start-up). Mostly for debugging.

`CAMLI_FORCE_OSARCH` (bool)
  Used by make.go to force building an unrecommended OS/ARCH pair.

`CAMLI_GCE_\*`
: Variables prefixed with `CAMLI_GCE_` concern the Google Compute Engine deploy
  handler in [pkg/deploy/gce](/pkg/deploy/gce), which is only used by camweb to
  launch Perkeep on Google Compute Engine. They do not affect Perkeep's
  behaviour.

`CAMLI_GCE_CLIENTID` (string)
: See `CAMLI_GCE_*` first. This string is used by gce.DeployHandler as the
  application's OAuth Client ID. If blank, camweb does not enable the Google
  Compute Engine launcher.

`CAMLI_GCE_CLIENTSECRET` (string)
: See `CAMLI_GCE_*` first. Used by gce.DeployHandler as the application's OAuth
  Client Secret. If blank, gce.NewDeployHandler returns an error, and camweb
  fails to start if the Google Compute Engine launcher was enabled.

`CAMLI_GCE_DATA` (string)
: See `CAMLI_GCE_*` first. Path to the directory where gce.DeployHandler stores
  the instances configuration and state. If blank, the "camli-gce-data" default
  is used instead.

`CAMLI_GCE_PROJECT` (string)
: See `CAMLI_GCE_*` first. ID of the Google Project that provides the above
  client ID and secret. It is used when we query for the list of all the
  existing zones, since such a query requires a project ID. If blank, a
  hard-coded list of zones is used instead.

`CAMLI_GCE_SERVICE_ACCOUNT` (string)
: See `CAMLI_GCE_*` first. Path to a Google service account JSON file. This
  account should have at least compute.readonly permissions on the Google
  Project wih ID CAMLI_GCE_PROJECT.  It is used to authenticate when querying
  for the list of all the existing zones. If blank, a hard-coded list of zones
  is used instead.

`CAMLI_GCE_XSRFKEY` (string)
: See `CAMLI_GCE_*` first. Used by gce.DeployHandler as the XSRF protection key.
  If blank, gce.NewDeployHandler generates a new random key instead.

`CAMLI_GOPHERJS_GOROOT` (string)
: As gopherjs does not build with go tip, when make.go is run with go devel,
  CAMLI_GOPHERJS_GOROOT should be set to a Go 1.10 root so that gopherjs can be
  built with Go 1.10. Otherwise it defaults to $HOME/go1.10.

`CAMLI_HELLO_ENABLED` (bool)
: Whether to start the hello world app as well. Variable used only by devcam server.

`CAMLI_HTTP_EXPVAR` (bool)
: Enable json export of expvars at /debug/vars

`CAMLI_HTTP_PPROF` (bool)
: Enable standard library's pprof handler at /debug/pprof/

`CAMLI_IGNORED_FILES` (string)
: Override client configuration option 'ignoredFiles'.  Comma-seperated list of
files to be ignored by [pkg/client](/pkg/client) when uploading.

`CAMLI_INCLUDE_PATH` (string)
: Path to search for files.
  Referenced in [pkg/osutil](/pkg/osutil) and used indirectly by
  [go4.org/jsonconfig.ConfigParser](http://go4.org/jsonconfig#ConfigParser) to search for
  files mentioned in configurations.  This is used as a last resort after first
  checking the current directory and the Perkeep config directory. It should
  be in the OS path form, i.e. unix-like systems would be
  /path/1:/path/two:/some/other/path, and Windows would be C:\path\one;D:\path\2

`CAMLI_KEYID` (string)
: Optional GPG identity to use, taking precedence over config files.
  Used by devcam commands, in config/dev-server-config.json, and
  config/dev-client-dir/client-config.json as the public ID of the GPG
  key to use for signing.

`CAMLI_KV_VERIFY` (bool)
: Enable all the VerifyDb\* options in cznic/kv, to e.g. track down
  corruptions.

`CAMLI_KVINDEX_ENABLED` (bool)
:  Use cznic/kv as the indexer. Variable used only by devcam server.

`CAMLI_LEVELDB_ENABLED` (bool)
: Use syndtr/goleveldb as the indexer. Variable used only by devcam server.

`CAMLI_MEMINDEX_ENABLED` (bool)
: Use a memory-only indexer. Supported only by devcam server.

`CAMLI_MONGO_WIPE` (bool)
: Wipe out mongo based index on startup.

`CAMLI_NO_FILE_DUP_SEARCH` (bool)
: This will cause the search-for-exists-before-upload step to be skipped when
  pk put is uploading files.

`CAMLI_PPROF_START` (string)
: Filename base to write a "<base>.cpu" and "<base>.mem" profile out
  to during server start-up.  Used to profile index corpus scanning,
  mostly.

`CAMLI_PUBLISH_ENABLED (bool)
: Whether to start the publisher app as well. Variable used only by devcam server.

`CAMLI_SCANCAB_ENABLED (bool)
: Whether to start the scanning cabinet app as well. Variable used only by devcam server.

`CAMLI_SECRET_RING` (string)
: Path to the GPG secret keyring, which is otherwise set by identitySecretRing
  in the server config, and secretRing in the client config.

`CAMLI_DISABLE_CLIENT_CONFIG_FILE` (bool)
: If set, the [pkg/client](/pkg/client) code will never use the on-disk config
  file.

`CAMLI_TRACK_FS_STATS` (bool)
: Enable operation counts for fuse filesystem.

`CAMLI_TRUSTED_CERT` (string)
: Override client configuration option 'trustedCerts'.
  Comma-seperated list of paths to trusted certificate fingerprints.

`CAMPUT_ANDROID_OUTPUT` (bool)
: Enable pkg/client status messages to print to stdout. Used in android client.

`CAMLI_DISABLE_DJPEG` (bool)
: Disable use of djpeg(1) to down-sample JPEG images by a factor of 2, 4 or 8.
  Only has an effect when djpeg is found in the PATH.

`CAMLI_DISABLE_THUMB_CACHE` (bool)
: If true, no thumbnail caching is done, and URLs even have cache
  buster components, to force browsers to reload a lot.

`CAMLI_REDO_INDEX_ON_RECEIVE` (bool)
: If true, the indexer will always index any blob it receives, regardless of
  whether it thinks it's done it in the past. This is generally only useful when
  working on the indexing code and retroactively indexing a subset of content
  without forcing a global reindexing.

`CAMLI_VAR_DIR` (string)
: Path used by [pkg/osutil](/pkg/osutil) to override operating system specific
  application storage directory. Generally unused.

`CAMLI_S3_FAIL_PERCENT` (int)
: Number from 0-100 of what percentage of the time to fail receiving blobs
  for the S3 handler.

## Development

`CAMLI_DEBUG` (bool)
: Used by perkeepd and pk put to enable additional commandline options.
  Used in pkg/schema to enable additional logging.

`CAMLI_HTTP_DEBUG` (bool)
: Enable per-request logging in [pkg/webserver](/pkg/webserver).

`CAMLI_DEBUG_CONFIG` (bool)
: Causes pkg/serverconfig to dump low-level configuration derived from
  high-level configuation on load.

`CAMLI_DEBUG_X` (string)
: String containing magic substring(s) to enable debuggging in code.

`CAMLI_DEBUG_UPLOADS` (bool)
: Used by [pkg/client](/pkg/client) to enable additional logging.

`CAMLI_DEBUG_IMAGES` (bool)
: Enable extra debugging in [pkg/images](/pkg/images) when decoding images.
  Used by indexers.

`CAMLI_SHA1_ENABLED` (bool)
: Whether to enable the use of legacy sha1 blobs. Only used for development, for
  creating new blobs with the legacy SHA-1 hash, instead of with the current one.
  It does not affect the ability of Perkeep to read SHA-1 blobs.

`CAMLI_FAST_DEV` (bool)
: Used by dev/demo.sh for giving presentations with devcam server/put/etc
  for faster pre-built builds, without calling make.go.

`DEV_THROTTLE_KBPS` (integer) and `DEV_THROTTLE_LATENCY_MS` (integer)
: Rate limit and/or inject latency in pkg/webserver responses. A value of 0
  disables traffic-shaping.

`RUN_BROKEN_TESTS` (bool)
: Run known-broken tests.

## Undocumented

These variables are yet to be documented:

`CAMLI_API_HOST` (todo)

`CAMLI_APP_LISTEN` (todo)

`CAMLI_DEBUG_QUERY_SPEED` (todo)

`CAMLI_DEV_MAP_CLUSTERING` (todo)

`CAMLI_FAKE_STATUS_ERROR` (todo)

`CAMLI_GPHOTOS_FULL_IMPORT` (todo)

`CAMLI_MORE_FLAGS` (todo)

`CAMLI_MORE_FLAGS` (todo)

`CAMLI_PICASA_FULL_IMPORT` (todo)

`CAMLI_QUIET` (todo)

`CAMLI_REINDEX_START` (todo)

`CAMLI_SERVER` (todo)

`CAMLI_SET_BASE_URL_AND_SEND_ADDR_TO` (todo)

`CAMLI_SYNC_VALIDATE` (todo)

`CAMLI_TWITTER_FULL_IMPORT` (todo)

`CAMLI_TWITTER_SKIP_API_IMPORT` (todo)

`DEV_THROTTLE_KBPS` (todo)

`DEV_THROTTLE_LATENCY_MS` (todo)

`PERKEEP_MASTODON_FULL_IMPORT` (todo)
