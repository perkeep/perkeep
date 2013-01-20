// THIS FILE IS AUTO-GENERATED FROM gallery.html
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("gallery.html", 622, fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"<head>\n"+
		"  <title>Gallery</title>\n"+
		"  <script src=\"base64.js\"></script>\n"+
		"  <script src=\"Crypto.js\"></script>\n"+
		"  <script src=\"SHA1.js\"></script>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"?camli.mode=config&cb=Camli.onConfiguration\"></script>\n"+
		"  <script src=\"gallery.js\"></script>\n"+
		"  <link rel=\"stylesheet\" href=\"camli.css\">\n"+
		"</head>\n"+
		"<body class=\"camli-ui-permanode\">\n"+
		"  <div class=\"camli-nav\"><a href=\"./\">Home</a></div>\n"+
		"  <h1>Gallery</h1>\n"+
		"\n"+
		"  <p>\n"+
		"    Permalink:\n"+
		"    <span id=\"permanode\"></span>\n"+
		"    <span id=\"permanodeBlob\" class=\"camli-nav\"></span>\n"+
		"  </p>\n"+
		"\n"+
		"  <div id=\"members\"></div>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""), time.Unix(0, 1358714667000000000))
}
