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

The parameters for setting up the app's process ("listen", "backendURL", and
"apiHost") are derived from the Camlistore server's "listen", and "baseURL", but
should the need arise (e.g. with a proxy setup) they can be specified as well.
See [serverconfig.Publish](https://camlistore.org/pkg/types/serverconfig/#Publish)
type for the details.

If you want to provide your own (Go) template, see
[camlistore.org/pkg/publish](/pkg/publish) for the data structures and
functions available to the template.

