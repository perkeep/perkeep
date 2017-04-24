# Publishing

Camlistore delegates publishing to the publisher server application, which
uses Go html templates (http://golang.org/pkg/text/template/) to publish
pages.

Resources for publishing, such as go templates, javascript and css files
should be placed in the application source directory - app/publisher/ - so
they can be served directly when using the dev server or automatically
embedded in production.

You should then specify the Go template to be used through the configuration
file. The CSS files are automatically all available to the app. For example,
there already is a go template (gallery.html), and css file (pics.css) that work
together to provide publishing for image galleries. The dev server config
(config/dev-server-config.json) already uses them. Here is how one would
configure publishing for an image gallery in the server config
($HOME/.config/camlistore/server-config.json):

    "publish": {
      "/pics/": {
        "camliRoot": "mypics",
        "cacheRoot": "/home/joe/var/camlistore/blobs/cache",
        "goTemplate": "gallery.html"
      }
    }

For this to work you need a single permanode with an attribute "camliRoot"
set to "mypics" which will serve as the root node for publishing.

(See further settings for running behind a reverse proxy down below.)

Suppose you want to publish two permanodes as "foo" and "bar". The root node
needs the following atributes:

    camliRoot = mypics // must match server-config.json
    camilPath:foo = sha1-foo
    camliPath:bar = sha1-bar

where sha1-foo (and sha1-bar) is either a permanode with some camliContent,
or a permanode with some camliMembers.

This will serve content at the publisher root http(s)://«camlihost:port»/pics/
but note that publisher hides the contents of the root path.
Keeping with the example above, it would serve
http(s)://«camlihost:port»/pics/foo and http(s)://«camlihost:port»/pics/bar .

The parameters for setting up the app's process ("listen", "backendURL", and
"apiHost") are derived from the Camlistore server's "listen", and "baseURL", but
should the need arise (e.g. with a proxy setup) they can be specified as well.
See [serverconfig.Publish](https://camlistore.org/pkg/types/serverconfig/#Publish)
type for the details.

If you want to provide your own (Go) template, see
[camlistore.org/pkg/publish](/pkg/publish) for the data structures and
functions available to the template.

## Running Camlistore (and publisher) behind a reverse proxy

When Camlistore is serving in HTTP mode behind a HTTPS reverse proxy,
further settings are necessary to set up communication between publisher and
the parent camlistored process.

The settings are:

* "listen" is the address publisher should listen on
* "apiHost" URL prefix for publisher to connect to camlistored
* "backendURL" URL for camlistored to reach publisher

Assuming camlistored is serving HTTP on port 3179, and we want the to run publisher
on port 3155, the following settings can be used:

    "publish": {
      "/pics/": {
		"apiHost": "http://localhost:3179/",
		"backendURL": "http://localhost:3155/",
		"listen": ":3155",
		... other settings from above ...
      }
    }

