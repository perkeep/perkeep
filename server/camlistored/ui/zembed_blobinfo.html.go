// THIS FILE IS AUTO-GENERATED FROM blobinfo.html
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blobinfo.html", 855, fileembed.String("<html>\n"+
		"<head>\n"+
		"  <title>Blob info</title>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"?camli.mode=config&cb=Camli.onConfiguration\"></script>\n"+
		"  <script src=\"blobinfo.js\"></script>\n"+
		"  <link rel=\"stylesheet\" href=\"camli.css\">\n"+
		"</head>\n"+
		"<body class=\"camli-ui-blobinfo\">\n"+
		"  <div class=\"camli-nav\"><a href=\"./\">Home</a></div>\n"+
		"  <h1>Blob Contents</h1>\n"+
		"\n"+
		"  <div id=\"thumbnail\"></div>\n"+
		"  <span id=\"editspan\" class=\"camli-nav\" style=\"display: none;\"><a href=\"#\" id=\"ed"+
		"itlink\">edit</a></span>\n"+
		"  <span id=\"blobdownload\" class=\"camli-nav\"></span>\n"+
		"  <span id=\"blobdescribe\" class=\"camli-nav\"></span>\n"+
		"  <span id=\"blobbrowse\" class=\"camli-nav\"></span>\n"+
		"\n"+
		"  <pre id=\"blobdata\"></pre>\n"+
		"\n"+
		"  <h1>Indexer Metadata</h1>\n"+
		"  <pre id=\"blobmeta\"></pre>\n"+
		"\n"+
		"  <div id=\"claimsdiv\" style=\"visibility: hidden\">\n"+
		"    <h1>Mutation Claims</h1>\n"+
		"    <pre id=\"claims\"></pre>\n"+
		"  </div>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""), time.Unix(0, 1358714598000000000))
}
