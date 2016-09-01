# Application Environment

Camlistore applications run with the following environment variables set:

`CAMLI_API_HOST` (string)
: URL prefix of the Camlistore server which the app should use to make API calls.
  It always ends in a trailing slash. Examples:
   -   https://foo.org:3178/pub/
   -   https://foo.org/pub/
   -   http://192.168.0.1/
   -   http://192.168.0.1:1234/

`CAMLI_APP_LISTEN` (string)
: address (of the form host|ip:port) on which the app will listen.
  See https://golang.org/pkg/net/#Dial for the supported syntax.

`CAMLI_APP_CONFIG_URL` (string)
: URL containing JSON configuration for the app. The app should once, upon
  startup, fetch this URL (using CAMLI_AUTH) to retrieve its configuration data.
  The response JSON is the contents of the app's "appConfig" part of the config
  file.

`CAMLI_APP_MASTERQUERY_URL` (string)
: URL to Post (using CAMLI_AUTH) the search.SearchQuery, that the app
  handler should register as being the master query for the app handler search
  proxy. All subsequent searches will then only be allowed if their response is a
  subset of the master query response. If the URL parameter "refresh=1" is sent,
  the SearchQuery is ignored and the app handler will rerun the currently registered
  master query to refresh the corresponding cache.

`CAMLI_AUTH` (string)
: Username and password (username:password) that the app should use to
  authenticate over HTTP basic auth with the Camlistore server. Basic auth is
  unencrypted, hence it should only be used with HTTPS or in a secure (local
  loopback) environment.

See the
[app.HandlerConfig](https://camlistore.org/pkg/server/app/#HandlerConfig)
type for how the Camlistore's app handler sets the variables up.
