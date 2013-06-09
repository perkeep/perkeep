// THIS FILE IS AUTO-GENERATED FROM filetree.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("filetree.html", 680, time.Unix(0, 1370450675000000000), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"<head>\n"+
		"		<script type=\"text/javascript\" src=\"all.js\"></script>\n"+
		"	<title>Filetree</title>\n"+
		"	\n"+
		"	\n"+
		"	<script src=\"?camli.mode=config&var=CAMLISTORE_CONFIG\"></script>\n"+
		"	<!-- Begin non-Closure cheating; but depended on by server_connection.js -->\n"+
		"	\n"+
		"	\n"+
		"	\n"+
		"	<!-- End non-Closure cheating -->\n"+
		"	\n"+
		"	<link rel=\"stylesheet\" href=\"filetree.css\">\n"+
		"</head>\n"+
		"<body class=\"cam-filetree-page\">\n"+
		"	<div class=\"cam-filetree-nav\"><a href=\"./\">Home</a></div>\n"+
		"	<h1>FileTree for <span id=\"curDir\" class=\"cam-filetree-nav\"></span> </h1>\n"+
		"\n"+
		"	<div id=\"children\"></div>\n"+
		"	<script>\n"+
		"		var page = new camlistore.FiletreePage(CAMLISTORE_CONFIG);\n"+
		"		page.decorate(document.body);\n"+
		"	</script>\n"+
		"</body>\n"+
		"</html>\n"+
		""))
}
