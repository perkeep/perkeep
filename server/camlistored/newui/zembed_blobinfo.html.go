// THIS FILE IS AUTO-GENERATED FROM blobinfo.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blobinfo.html", 1105, fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"<head>\n"+
		"		<script type=\"text/javascript\" src=\"all.js\"></script>\n"+
		"	<title>Blob info</title>\n"+
		"	\n"+
		"	\n"+
		"	<script src=\"?camli.mode=config&var=CAMLISTORE_CONFIG\"></script>\n"+
		"	<!-- Begin non-Closure cheating; but depended on by server_connection.js -->\n"+
		"	\n"+
		"	\n"+
		"	\n"+
		"	<!-- End non-Closure cheating -->\n"+
		"	<link rel=\"stylesheet\" href=\"blobinfo.css\">\n"+
		"	\n"+
		"</head>\n"+
		"<body class=\"cam-blobinfo-page\">\n"+
		"	<div class=\"cam-blobinfo-nav\"><a href=\"./\">Home</a></div>\n"+
		"	<h1>Blob Contents</h1>\n"+
		"\n"+
		"	<div id=\"thumbnail\"></div>\n"+
		"	<span id=\"editspan\" class=\"cam-blobinfo-nav\" style=\"display: none;\"><a href=\"#\" "+
		"id=\"editlink\">edit</a></span>\n"+
		"	<span id=\"blobdownload\" class=\"cam-blobinfo-nav\"></span>\n"+
		"	<span id=\"blobdescribe\" class=\"cam-blobinfo-nav\"></span>\n"+
		"	<span id=\"blobbrowse\" class=\"cam-blobinfo-nav\"></span>\n"+
		"\n"+
		"	<pre id=\"blobdata\"></pre>\n"+
		"\n"+
		"	<h1>Indexer Metadata</h1>\n"+
		"	<pre id=\"blobmeta\"></pre>\n"+
		"\n"+
		"	<div id=\"claimsdiv\" style=\"visibility: hidden\">\n"+
		"		<h1>Mutation Claims</h1>\n"+
		"		<pre id=\"claims\"></pre>\n"+
		"	</div>\n"+
		"\n"+
		"	<script>\n"+
		"		var page = new camlistore.BlobPage(CAMLISTORE_CONFIG);\n"+
		"		page.decorate(document.body);\n"+
		"	</script>\n"+
		"</body>\n"+
		"</html>\n"+
		""), time.Unix(0, 1370352368981715089))
}
