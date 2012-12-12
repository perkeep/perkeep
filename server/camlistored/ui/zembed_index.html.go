// THIS FILE IS AUTO-GENERATED FROM index.html
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("index.html", 1620, fileembed.String("<html>\n"+
		"<head>\n"+
		"  <title>Recent Permanodes</title>\n"+
		"  <script type=\"text/javascript\" src=\"base64.js\"></script>\n"+
		"  <script type=\"text/javascript\" src=\"Crypto.js\"></script>\n"+
		"  <script type=\"text/javascript\" src=\"SHA1.js\"></script>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"index.js\"></script>\n"+
		"  <script src=\"?camli.mode=config&cb=onConfiguration\"></script>\n"+
		"  <link rel=\"stylesheet\" href=\"camli.css\">\n"+
		"  <link rel=\"stylesheet\" href=\"index.css\">\n"+
		"</head>\n"+
		"<body class=\"camli-ui-index\">\n"+
		"  <h1 id=\"topTitle\">Recent Permanodes</h1>\n"+
		"  <div id='toolbar'>\n"+
		"    <input type='button' id='btnList' value=\"list\"><input type='button' id='btnTh"+
		"umbs' value=\"thm\">\n"+
		"\n"+
		"    <input type='button' id='btnSmaller' value=\"-\"><input type='button' id='btnBi"+
		"gger' value=\"+\">\n"+
		"\n"+
		"    <form style=\"display: inline\" id=\"formView\">\n"+
		"      View:\n"+
		"      <select id=\"selectView\">\n"+
		"        <option value=\"recent\">Recent</a>\n"+
		"        <option value=\"date\">From &lt;date&gt;...</a>\n"+
		"        <option id=\"optFromSel\" value=\"fromsel\" disabled='true'>From selected ite"+
		"m</a>\n"+
		"        <optiongroup title=\"Debug\">\n"+
		"          <option value=\"search\">Old Search</a>\n"+
		"          <option value=\"debug:disco\">Debug: Discovery</a>\n"+
		"          <option value=\"debug:signing\">Debug: Signing</a>\n"+
		"          <option value=\"debug:misc\">Debug: Misc</a>\n"+
		"        </optiongroup>\n"+
		"      </select>\n"+
		"    </form>\n"+
		"\n"+
		"    <form style=\"float: right\" id=\"formSearch\">\n"+
		"      <input type=\"text\" id=\"textSearch\" size=15 title=\"Search\"><input type=\"subm"+
		"it\" id=\"btnSearch\" value=\"Srch\">\n"+
		"    </form>\n"+
		"  </div>\n"+
		"  <ul id=\"recent\"></ul>\n"+
		"\n"+
		"  <div style=\"display: block; clear: both\" id=\"debugstatus\"></div>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""), time.Unix(0, 1355281087518096739))
}
