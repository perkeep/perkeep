// THIS FILE IS AUTO-GENERATED FROM search.html
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("search.html", 1516, fileembed.String("<html>\n"+
		"<head>\n"+
		"  <title>Camlistored UI</title>\n"+
		"  <script type=\"text/javascript\" src=\"Crypto.js\"></script>\n"+
		"  <script type=\"text/javascript\" src=\"SHA1.js\"></script>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"?camli.mode=config&cb=Camli.onConfiguration\"></script>\n"+
		"  <script src=\"search.js\"></script>\n"+
		"  <link rel=\"stylesheet\" href=\"camli.css\">\n"+
		"</head>\n"+
		"<body>\n"+
		"    <div class=\"camli-nav\"><a href=\"./\">Home</a></div>\n"+
		"\n"+
		"    <h1>Search</h1>\n"+
		"\n"+
		"    <h2>Find all roots</h2>\n"+
		"    <form id=\"formRoots\">\n"+
		"        <input type=\"submit\" id=\"btnRoots\" value=\"Search\">\n"+
		"    </form>\n"+
		"\n"+
		"    <h2>In all attributes</h2>\n"+
		"    <form id=\"formAnyAttr\">\n"+
		"        <input id=\"inputAnyAttr\" placeholder=\"attrValue1\">\n"+
		"        <input type=\"submit\" id=\"btnAnyAttr\" value=\"Search\">\n"+
		"    </form>\n"+
		"\n"+
		"    <h2>By Tag</h2>\n"+
		"    <form id=\"formTags\">\n"+
		"        <input id=\"inputTag\" placeholder=\"tag1\">\n"+
		"        <input type=\"submit\" id=\"btnTagged\" value=\"Search\"></br>\n"+
		"        <input id=\"maxTagged\" placeholder=\"nb of results: 50\">\n"+
		"    </form>\n"+
		"\n"+
		"    <h2>By Title</h2>\n"+
		"    <form id=\"formTitles\">\n"+
		"        <input id=\"inputTitle\" placeholder=\"title1\">\n"+
		"        <input type=\"submit\" id=\"btnTitle\" value=\"Search\">\n"+
		"    </form>\n"+
		"\n"+
		"    <h3 id=\"titleRes\">Search</h3>\n"+
		"    <div id=\"divRes\">\n"+
		"</div>\n"+
		"    <p>\n"+
		"    <form id=\"formAddToCollec\">\n"+
		"        <input id=\"inputCollec\" placeholder=\"collection's permanode\">\n"+
		"        <input type=\"submit\" id=\"btnAddToCollec\" value=\"Add to collection\"> or\n"+
		"    </form>\n"+
		"    <button id=\"btnNewCollec\">Create new collection</button>\n"+
		"    </p>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""), time.Unix(0, 1364837799719094732))
}
