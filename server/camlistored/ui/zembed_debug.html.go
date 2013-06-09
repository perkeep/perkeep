// THIS FILE IS AUTO-GENERATED FROM debug.html
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("debug.html", 1258, time.Unix(0, 1358726342000000000), fileembed.String("<html>\n"+
		"<head>\n"+
		"  <title>Camlistored UI</title>\n"+
		"  <script type=\"text/javascript\" src=\"base64.js\"></script>\n"+
		"  <script type=\"text/javascript\" src=\"Crypto.js\"></script>\n"+
		"  <script type=\"text/javascript\" src=\"SHA1.js\"></script>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"debug.js\"></script>\n"+
		"  <script src=\"?camli.mode=config&cb=Camli.onConfiguration\"></script>\n"+
		"  <link rel=\"stylesheet\" href=\"camli.css\">\n"+
		"</head>\n"+
		"<body class=\"camli-ui-index\">\n"+
		"  <h1>Camlistored UI</h1>\n"+
		"  <p class=\"camli-nav\"><strong>Debug:</strong>\n"+
		"    <a href=\"disco.html\">discovery</a> |\n"+
		"    <a href=\"signing.html\">signing</a></p>\n"+
		"\n"+
		"  <button id=\"btnNew\">New</button> - create a new item or collection \n"+
		"\n"+
		"  <h2>Recent Objects</h2>\n"+
		"  <p class=\"camli-nav\">\n"+
		"    <strong>View:</strong>\n"+
		"    <a href=\"recent.html\">thumbnails</a></p>\n"+
		"  <ul id=\"recent\"></ul>\n"+
		"\n"+
		"  <h2>Search</h2>\n"+
		"  <form id=\"formSearch\">\n"+
		"    <p>\n"+
		"      <input id=\"inputSearch\" placeholder=\"tag1\">\n"+
		"      <input type=\"submit\" id=\"btnSearch\" value=\"Search\">\n"+
		"  </form>\n"+
		"\n"+
		"  <h2>Upload</h2>\n"+
		"  <form method=\"POST\" id=\"uploadform\" enctype=\"multipart/form-data\">\n"+
		"    <input type=\"file\" id=\"fileinput\" multiple=\"true\" name=\"file\" disabled=\"true\""+
		">\n"+
		"    <input type=\"submit\" id=\"filesubmit\" value=\"Upload\" disabled=\"true\">\n"+
		"  </form>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""))
}
