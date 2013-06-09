// THIS FILE IS AUTO-GENERATED FROM debug.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("debug.html", 1505, time.Unix(0, 1370450675000000000), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"<head>\n"+
		"		<script type=\"text/javascript\" src=\"all.js\"></script>\n"+
		"	<title>Camlistored UI</title>\n"+
		"	\n"+
		"	\n"+
		"	<script src=\"?camli.mode=config&var=CAMLISTORE_CONFIG\"></script>\n"+
		"	<!-- Begin non-Closure cheating; but depended on by server_connection.js -->\n"+
		"	\n"+
		"	\n"+
		"	\n"+
		"	<!-- End non-Closure cheating -->\n"+
		"	\n"+
		"\n"+
		"</head>\n"+
		"<body>\n"+
		"	<form>\n"+
		"		<h2>Root Discovery</h2>\n"+
		"		<p><input type=\"button\" id=\"discobtn\" value=\"Do Discovery\" /></p>\n"+
		"		<div id=\"discores\" style=\"border: 2px solid gray\">(discovery results)</div>\n"+
		"\n"+
		"		<h2>Signing Discovery</h2>\n"+
		"		<p><input type=\"button\" id=\"sigdiscobtn\" value=\"Do jsonSign discovery\" /></p>\n"+
		"		<div id=\"sigdiscores\" style=\"border: 2px solid gray\">(jsonsign discovery result"+
		"s)</div>\n"+
		"\n"+
		"		<h2>Signing Debug</h2>\n"+
		"		<table>\n"+
		"		<tr align='left'>\n"+
		"			<th>JSON blob to sign: <input type='button' id='addkeyref' value=\"Add keyref\"/"+
		"></th>\n"+
		"			<th></th>\n"+
		"			<th>Signed blob:</th>\n"+
		"			<th></th>\n"+
		"			<th>Verification details:</th>\n"+
		"		</tr>\n"+
		"		<tr>\n"+
		"			<td><textarea id='clearjson' rows=10 cols=40>{\"camliVersion\": 1, \"camliType\": "+
		"\"whatever\", \"foo\": \"bar\"}</textarea></td>\n"+
		"			<td valign='middle'><input type='button' id='sign' value=\"Sign &gt;&gt;\" /></t"+
		"d>\n"+
		"			<td><textarea id=\"signedjson\" rows=10 cols=40></textarea></td>\n"+
		"			<td valign='middle'><input type='button' id='verify' value=\"Verify &gt;&gt;\" /"+
		"></td>\n"+
		"			<td><div id='verifyinfo'></div></td>\n"+
		"		</tr>\n"+
		"		</table>\n"+
		"	</form>\n"+
		"\n"+
		"	<script>\n"+
		"		var page = new camlistore.DebugPage(CAMLISTORE_CONFIG);\n"+
		"		page.decorate(document.body);\n"+
		"	</script>\n"+
		"</body>\n"+
		"</html>\n"+
		""))
}
