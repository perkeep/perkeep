// THIS FILE IS AUTO-GENERATED FROM sigdebug.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("sigdebug.js", 2322, fileembed.String("var sigdisco = null;\n"+
		"\n"+
		"function discoverJsonSign() {\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"        if (xhr.readyState != 4) { return; }\n"+
		"        if (xhr.status != 200) {\n"+
		"            console.log(\"no status 200; got \" + xhr.status);\n"+
		"            return;\n"+
		"        }\n"+
		"        sigdisco = JSON.parse(xhr.responseText);\n"+
		"        document.getElementById(\"sigdiscores\").innerHTML = \"<pre>\" + JSON.stringi"+
		"fy(sigdisco, null, 2) + \"</pre>\";\n"+
		"    };\n"+
		"    xhr.open(\"GET\", Camli.config.jsonSignRoot + \"/camli/sig/discovery\", true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"function addKeyRef() {\n"+
		"    if (!sigdisco) {\n"+
		"        alert(\"must do jsonsign discovery first\");        \n"+
		"        return;\n"+
		"    }\n"+
		"    clearta = document.getElementById(\"clearjson\");\n"+
		"    var j;\n"+
		"    try {\n"+
		"        j = JSON.parse(clearta.value);\n"+
		"    } catch (x) {\n"+
		"        alert(x);\n"+
		"        return\n"+
		"    }\n"+
		"    j.camliSigner = sigdisco.publicKeyBlobRef;\n"+
		"    clearta.value = JSON.stringify(j);\n"+
		"}\n"+
		"\n"+
		"function doSign() {\n"+
		"    if (!sigdisco) {\n"+
		"        alert(\"must do jsonsign discovery first\");\n"+
		"        return;\n"+
		"    }\n"+
		"    clearta = document.getElementById(\"clearjson\");\n"+
		"\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"        if (xhr.readyState != 4) { return; }\n"+
		"        if (xhr.status != 200) {\n"+
		"            alert(\"got status \" + xhr.status)\n"+
		"            return;\n"+
		"        }\n"+
		"        document.getElementById(\"signedjson\").value = xhr.responseText;\n"+
		"    };\n"+
		"    xhr.open(\"POST\", sigdisco.signHandler, true);\n"+
		"    xhr.setRequestHeader(\"Content-Type\", \"application/x-www-form-urlencoded\");\n"+
		"    xhr.send(\"json=\" + encodeURIComponent(clearta.value));\n"+
		"}\n"+
		"\n"+
		"function doVerify() {\n"+
		"    if (!sigdisco) {\n"+
		"        alert(\"must do jsonsign discovery first\");\n"+
		"        return;\n"+
		"    }\n"+
		"\n"+
		"    signedta = document.getElementById(\"signedjson\");\n"+
		"\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"        if (xhr.readyState != 4) { return; }\n"+
		"        if (xhr.status != 200) {\n"+
		"            alert(\"got status \" + xhr.status)\n"+
		"            return;\n"+
		"        }\n"+
		"        document.getElementById(\"verifyinfo\").innerHTML = \"<pre>\" + xhr.responseT"+
		"ext + \"</pre>\";\n"+
		"    };\n"+
		"    xhr.open(\"POST\", sigdisco.verifyHandler, true);\n"+
		"    xhr.setRequestHeader(\"Content-Type\", \"application/x-www-form-urlencoded\");\n"+
		"    xhr.send(\"sjson=\" + encodeURIComponent(signedta.value));\n"+
		"}\n"+
		""), time.Unix(0, 1359676098841984696))
}
