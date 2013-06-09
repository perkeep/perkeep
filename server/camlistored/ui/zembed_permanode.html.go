// THIS FILE IS AUTO-GENERATED FROM permanode.html
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("permanode.html", 2673, time.Unix(0, 1358726342000000000), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"<head>\n"+
		"  <title>Permanode</title>\n"+
		"  <script src=\"base64.js\"></script>\n"+
		"  <script src=\"Crypto.js\"></script>\n"+
		"  <script src=\"SHA1.js\"></script>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"?camli.mode=config&cb=Camli.onConfiguration\"></script>\n"+
		"  <script src=\"permanode.js\"></script>\n"+
		"  <link rel=\"stylesheet\" href=\"camli.css\">\n"+
		"</head>\n"+
		"<body class=\"camli-ui-permanode\">\n"+
		"  <div class=\"camli-nav\"><a href=\"./\">Home</a></div>\n"+
		"  <h1>Permanode</h1>\n"+
		"\n"+
		"  <p>\n"+
		"    Permalink:\n"+
		"    <span id=\"permanode\"></span>\n"+
		"    <span id=\"permanodeBlob\" class=\"camli-nav\"></span>\n"+
		"  </p>\n"+
		"\n"+
		"  <form id=\"formTitle\">\n"+
		"    <p>\n"+
		"      Title: <input type=\"text\" id=\"inputTitle\" size=\"30\" value=\"(loading)\" disab"+
		"led=\"disabled\">\n"+
		"      <input type=\"submit\" id=\"btnSaveTitle\" value=\"Save\" disabled=\"disabled\">\n"+
		"    </p>\n"+
		"  </form>\n"+
		"\n"+
		"  <form id=\"formTags\">\n"+
		"    <p>\n"+
		"      <label for=\"inputNewTag\">Tags:</label>\n"+
		"      <span id=\"spanTags\"></span>\n"+
		"      <input id=\"inputNewTag\" placeholder=\"tag1, tag2, tag3\">\n"+
		"      <input type=\"submit\" id=\"btnAddTag\" value=\"Add Tag(s)\">\n"+
		"  </form>\n"+
		"\n"+
		"  <form id=\"formAccess\">\n"+
		"  <p>Access:\n"+
		"    <select id=\"selectAccess\" disabled=\"disabled\">\n"+
		"      <option value=\"private\">Private</option>\n"+
		"      <option value=\"public\">Public</option>\n"+
		"    </select>\n"+
		"    <input type=\"submit\" id=\"btnSaveAccess\" value=\"Save\" disabled=\"disabled\">\n"+
		"\n"+
		"    ... with URL: <select id=\"selectPublishRoot\">\n"+
		"      <option value=\"\"></option>\n"+
		"      </select>\n"+
		"      <input type=\"text\" id=\"publishSuffix\" size=\"40\">\n"+
		"      <input type=\"submit\" id=\"btnSavePublish\" value=\"Set URL\">\n"+
		"  </p>\n"+
		"  </form>\n"+
		"\n"+
		"  <div id=\"existingPaths\"></div>\n"+
		"\n"+
		"  <div id=\"members\"></div>\n"+
		"\n"+
		"  <form id=\"formType\">\n"+
		"  <p>Type:\n"+
		"    <select id='type'>\n"+
		"      <option value=''>(None / auto)</option>\n"+
		"      <option value='_other'>(Other)</option>\n"+
		"      <option value=\"root\">Root (of a hierarchy)</option>\n"+
		"      <option value=\"collection\">Collection (e.g. directory, gallery)</option>\n"+
		"      <option value=\"file\">File</option>\n"+
		"      <option value=\"collection\">File Collection / Gallery</option>\n"+
		"      <option value=\"microblog\">Microblog Post</option>\n"+
		"      <option value=\"blog\">Blog Post</option>\n"+
		"    </select>\n"+
		"  </p>\n"+
		"  </form>\n"+
		"\n"+
		"  <p>\n"+
		"  <button id=\"btnGallery\"> Show gallery </button> \n"+
		"  </p>\n"+
		"\n"+
		"  <div id=\"content\"></div>\n"+
		"\n"+
		"  <div id=\"dnd\" class=\"camli-dnd\">\n"+
		"    <form id=\"fileForm\">\n"+
		"      <input type=\"file\" id=\"fileInput\" multiple=\"true\" onchange=\"\">\n"+
		"      <input type=\"submit\" id=\"fileUploadBtn\" value=\"Upload\">\n"+
		"    </form>\n"+
		"    <p id='filelist'>\n"+
		"      <em>or drag &amp; drop files here</em>\n"+
		"    </p>\n"+
		"    <pre id=\"info\"></pre>\n"+
		"  </div>\n"+
		"\n"+
		"  <h3>Current object attributes</h3>\n"+
		"  <pre id=\"debugattrs\" style=\"font-size: 8pt\"></pre>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""))
}
