// THIS FILE IS AUTO-GENERATED FROM disco.html
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("disco.html", 1238, time.Unix(0, 1358726342000000000), fileembed.String("<html>\n"+
		"<head>\n"+
		"  <title>Camlistored UI</title>\n"+
		"  <script src=\"camli.js\"></script>\n"+
		"  <script src=\"sigdebug.js\"></script>\n"+
		"  <script src=\"./?camli.mode=config&cb=Camli.onConfiguration\"></script>\n"+
		"<script>\n"+
		"\n"+
		"// Or get configuration info like this:\n"+
		"function discover() {\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"        if (xhr.readyState != 4) { return; }\n"+
		"        if (xhr.status != 200) {\n"+
		"            console.log(\"no status 200; got \" + xhr.status);\n"+
		"            return;\n"+
		"        }\n"+
		"        disco = JSON.parse(xhr.responseText);\n"+
		"        document.getElementById(\"discores\").innerHTML = \"<pre>\" + JSON.stringify("+
		"disco, null, 2) + \"</pre>\";\n"+
		"    };\n"+
		"    xhr.open(\"GET\", \"./?camli.mode=config\", true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"\n"+
		"</script>\n"+
		"</head>\n"+
		"<body>\n"+
		"  <form>\n"+
		"    <h2>Root Discovery</h2>\n"+
		"    <p><input type=\"button\" id=\"discobtn\" onclick=\"discover()\" value=\"Do Discover"+
		"y\" /></p>\n"+
		"    <div id=\"discores\" style=\"border: 2px solid gray\">(discovery results)</div>\n"+
		"\n"+
		"\n"+
		"    <h2>Signing Discovery</h2>\n"+
		"    <p><input type=\"button\" id=\"sigdiscobtn\" onclick=\"discoverJsonSign()\" value=\""+
		"Do jsonSign discovery\" /></p>\n"+
		"    <div id=\"sigdiscores\" style=\"border: 2px solid gray\">(jsonsign discovery resu"+
		"lts)</div>\n"+
		"  </form>\n"+
		"</body>\n"+
		"</html>\n"+
		""))
}
