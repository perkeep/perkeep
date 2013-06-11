// THIS FILE IS AUTO-GENERATED FROM permanode.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("permanode.html", 2599, time.Unix(0, 1370942742232957700), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"<head>\n"+
		"	<title>Permanode</title>\n"+
		"	<script src=\"closure/goog/base.js\"></script>\n"+
		"	<script src=\"./deps.js\"></script>\n"+
		"    <script src=\"?camli.mode=config&var=CAMLISTORE_CONFIG\"></script>\n"+
		"	<!-- Begin non-Closure cheating; but depended on by server_connection.js -->\n"+
		"	<script type=\"text/javascript\" src=\"base64.js\"></script>\n"+
		"	<script type=\"text/javascript\" src=\"Crypto.js\"></script>\n"+
		"	<script type=\"text/javascript\" src=\"SHA1.js\"></script>\n"+
		"	<!-- End non-Closure cheating -->\n"+
		"	<script>\n"+
		"		goog.require('camlistore.PermanodePage');\n"+
		"	</script>\n"+
		"	<link rel=\"stylesheet\" href=\"permanode.css\">\n"+
		"	<link rel=\"stylesheet\" href=\"blob_item.css\">\n"+
		"	<link rel=\"stylesheet\" href=\"blob_item_container.css\">\n"+
		"</head>\n"+
		"<body class=\"cam-permanode-page\">\n"+
		"	<div class=\"cam-permanode-nav\"><a href=\"./\">Home</a></div>\n"+
		"	<h1>Permanode</h1>\n"+
		"\n"+
		"	<p>\n"+
		"	Permalink:\n"+
		"	<span id=\"permanode\"></span>\n"+
		"	<span id=\"permanodeBlob\" class=\"cam-permanode-nav\"></span>\n"+
		"	</p>\n"+
		"\n"+
		"	<form id=\"formTitle\">\n"+
		"		<p>\n"+
		"		Title:\n"+
		"		<input type=\"text\" id=\"inputTitle\" size=\"30\" value=\"(loading)\" disabled=\"disabl"+
		"ed\">\n"+
		"		<input type=\"submit\" id=\"btnSaveTitle\" value=\"Save\" disabled=\"disabled\">\n"+
		"		</p>\n"+
		"	</form>\n"+
		"\n"+
		"	<form id=\"formTags\">\n"+
		"		<p>\n"+
		"		Tags:\n"+
		"		<span id=\"spanTags\"></span>\n"+
		"		<input id=\"inputNewTag\" placeholder=\"tag1, tag2, tag3\">\n"+
		"		<input type=\"submit\" id=\"btnAddTag\" value=\"Add Tag(s)\">\n"+
		"	</form>\n"+
		"\n"+
		"	<form id=\"formAccess\">\n"+
		"		<p>\n"+
		"		Access:\n"+
		"		<select id=\"selectAccess\" disabled=\"disabled\">\n"+
		"			<option value=\"private\">Private</option>\n"+
		"			<option value=\"public\">Public</option>\n"+
		"		</select>\n"+
		"		<input type=\"submit\" id=\"btnSaveAccess\" value=\"Save\" disabled=\"disabled\">\n"+
		"\n"+
		"		... with URL:\n"+
		"		<select id=\"selectPublishRoot\">\n"+
		"			<option value=\"\"></option>\n"+
		"		</select>\n"+
		"		<input type=\"text\" id=\"publishSuffix\" size=\"40\">\n"+
		"		<input type=\"submit\" id=\"btnSavePublish\" value=\"Set URL\">\n"+
		"		</p>\n"+
		"	</form>\n"+
		"\n"+
		"	<div id=\"existingPaths\"></div>\n"+
		"\n"+
		"	<div id=\"cammountTip\"></div>\n"+
		"\n"+
		"	<div id=\"members\"></div>\n"+
		"	<p><button id=\"btnGallery\" value=\"list\">Thumbnails</button></p>\n"+
		"	<div id=\"membersList\"></div>\n"+
		"	<div id=\"membersThumbs\"></div>\n"+
		"\n"+
		"	<div id=\"content\"></div>\n"+
		"\n"+
		"	<div id=\"dnd\" class=\"cam-permanode-dnd\">\n"+
		"		<form id=\"fileForm\">\n"+
		"			<input type=\"file\" id=\"fileInput\" multiple=\"true\" onchange=\"\">\n"+
		"			<input type=\"submit\" id=\"fileUploadBtn\" value=\"Upload\">\n"+
		"		</form>\n"+
		"		<p id='filelist'>\n"+
		"		<em>or drag &amp; drop files here</em>\n"+
		"		</p>\n"+
		"		<pre id=\"info\"></pre>\n"+
		"	</div>\n"+
		"\n"+
		"	<h3>Current object attributes</h3>\n"+
		"	<pre id=\"debugattrs\" style=\"font-size: 8pt\"></pre>\n"+
		"\n"+
		"	<script>\n"+
		"		var page = new camlistore.PermanodePage(CAMLISTORE_CONFIG);\n"+
		"		page.decorate(document.body);\n"+
		"	</script>\n"+
		"</body>\n"+
		"</html>\n"+
		""))
}
