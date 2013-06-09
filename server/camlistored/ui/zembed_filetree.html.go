// THIS FILE IS AUTO-GENERATED FROM filetree.html
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("filetree.html", 557, time.Unix(0, 1358726342000000000), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"<head>\n"+
		"  <title>Gallery</title>\n"+
		"  <script src=\"base64.js\"></script>\n"+
		"  <script src=\"Crypto.js\"></script>\n"+
		"  <script src=\"SHA1.js\"></script>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"?camli.mode=config&cb=Camli.onConfiguration\"></script>\n"+
		"  <script src=\"filetree.js\"></script>\n"+
		"  <link rel=\"stylesheet\" href=\"camli.css\">\n"+
		"</head>\n"+
		"<body class=\"camli-ui-permanode\">\n"+
		"  <div class=\"camli-nav\"><a href=\"./\">Home</a></div>\n"+
		"  <h1>FileTree for <span id=\"curDir\" class=\"camli-nav\"></span> </h1>\n"+
		"\n"+
		"  <div id=\"children\"></div>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""))
}
