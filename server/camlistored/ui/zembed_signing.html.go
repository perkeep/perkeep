// THIS FILE IS AUTO-GENERATED FROM signing.html
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("signing.html", 1236, fileembed.String("<html>\n"+
		"<head>\n"+
		"  <title>Camlistored UI</title>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"sigdebug.js\"></script>\n"+
		"  <script src=\"./?camli.mode=config&cb=onConfiguration\"></script>\n"+
		"</head>\n"+
		"<body>\n"+
		"  <h1>Signing Debug</h1>\n"+
		"  <form>\n"+
		"    <p><input type=\"button\" id=\"sigdiscobtn\" onclick=\"discoverJsonSign()\" value=\""+
		"Do jsonSign discovery\" /></p>\n"+
		"    <div id=\"sigdiscores\" style=\"border: 2px solid gray\">(jsonsign discovery resu"+
		"lts)</div>\n"+
		"\n"+
		"    <table>\n"+
		"      <tr align='left'>\n"+
		"        <th>JSON blob to sign: <input type='button' id='addkeyref' onclick='addKe"+
		"yRef()' value=\"Add keyref\"/></th>\n"+
		"        <th></th>\n"+
		"        <th>Signed blob:</th>\n"+
		"        <th></th>\n"+
		"        <th>Verification details:</th>\n"+
		"      </tr>\n"+
		"      <tr>\n"+
		"        <td><textarea id='clearjson' rows=10 cols=40>{\"camliVersion\": 1,\n"+
		" \"camliType\": \"whatever\",\n"+
		" \"foo\": \"bar\"\n"+
		"}</textarea></td>\n"+
		"        <td valign='middle'><input type='button' id='sign' onclick='doSign()' val"+
		"ue=\"Sign &gt;&gt;\" /></td>\n"+
		"        <td><textarea id=\"signedjson\" rows=10 cols=40></textarea></td>\n"+
		"        <td valign='middle'><input type='button' id='sign' onclick='doVerify()' v"+
		"alue=\"Verify &gt;&gt;\" /></td>\n"+
		"        <td><div id='verifyinfo'></div></td>\n"+
		"      </tr>\n"+
		"    </table>\n"+
		"  </form>\n"+
		"\n"+
		"</body>\n"+
		"</html>\n"+
		""), time.Unix(0, 1349725494303779986))
}
