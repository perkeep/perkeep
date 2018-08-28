
Instapaper Importer
===============

This is a Perkeep importer for the Instapaper service. It can import bookmarks, each bookmark's full text, and highlights.

To use:

1) Email Instapaper support to receive a Client ID and Secret.

2a) Configure the Client ID and Secret using the importer's web UI at <server>/importer/instapaper.

OR

2b) Place the Client ID and the Client secret in your (high-level) server-config.json:

	"instapaper": "Client ID:Client secret",

    and start your Perkeep server.

OR

2c) Place the Client ID and the Client secret in your (low-level) server-config.json:

    "/importer-instapaper/": {
        "handler": "importer-instapaper",
        "handlerArgs": {
            "apiKey": "Client ID:Client secret"
        }
    },

    and start your Perkeep server.

3) Navigate to http://<server>/importer/instapaper and login with your Instapaper username/password.

4) Start the import process.


Usage
----

### Full Text Import and Highlights

For each bookmark, the importer will attempt to import a full text document as well as any highlights associated with
the bookmark. Imports for full text will only be attempted once when the importer first sees a bookmark. Subsequent
runs will not attempt to re-import full text.

* **Desktop mount access**: Full text for bookmarks will also be available as *.HTML files from the mounted `pk-mount`
volume in the `{username}'s Instapaper Data/bookmarks/` directory.

* **If text import fails**: Force a full text import to re-run for a specific bookmark by deleting the
`camliPath:{Bookmark Title}` attribute on the `{username}'s Bookmarks` permanode and then re-running the Instapaper import.
