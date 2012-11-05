// THIS FILE IS AUTO-GENERATED FROM blobinfo.js
// DO NOT EDIT.
package ui

import "time"

func init() {
	Files.Add("blobinfo.js", "/*\n"+
		"Copyright 2011 Google Inc.\n"+
		"\n"+
		"Licensed under the Apache License, Version 2.0 (the \"License\");\n"+
		"you may not use this file except in compliance with the License.\n"+
		"You may obtain a copy of the License at\n"+
		"\n"+
		"     http://www.apache.org/licenses/LICENSE-2.0\n"+
		"\n"+
		"Unless required by applicable law or agreed to in writing, software\n"+
		"distributed under the License is distributed on an \"AS IS\" BASIS,\n"+
		"WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n"+
		"See the License for the specific language governing permissions and\n"+
		"limitations under the License.\n"+
		"*/\n"+
		"\n"+
		"// Gets the |p| query parameter, assuming that it looks like a blobref.\n"+
		"function getBlobParam() {\n"+
		"    var blobRef = getQueryParam('b');\n"+
		"    return (blobRef && isPlausibleBlobRef(blobRef)) ? blobRef : null;\n"+
		"}\n"+
		"\n"+
		"function blobInfoUpdate(bmap) {\n"+
		"    var blobmeta = document.getElementById('blobmeta');\n"+
		"    var bd = document.getElementById(\"blobdownload\")\n"+
		"    bd.innerHTML = \"\";\n"+
		"    var blobref = getBlobParam();\n"+
		"    if (!blobref) {\n"+
		"        alert(\"no blobref?\");\n"+
		"        return;\n"+
		"    }\n"+
		"    var binfo = bmap[blobref];\n"+
		"    if (!binfo) {\n"+
		"        blobmeta.innerHTML = \"(not found)\";\n"+
		"        return;\n"+
		"    }\n"+
		"    blobmeta.innerHTML = JSON.stringify(binfo, null, 2);\n"+
		"    if (binfo.camliType || (binfo.type && binfo.type.indexOf(\"text/\") == 0)) {\n"+
		"        camliGetBlobContents(\n"+
		"            blobref,\n"+
		"            {\n"+
		"                success: function(data) {\n"+
		"                    document.getElementById(\"blobdata\").innerHTML = linkifyBlobRe"+
		"fs(data);\n"+
		"                    var bb = document.getElementById('blobbrowse');\n"+
		"                    if (binfo.camliType != \"directory\") {\n"+
		"                        bb.style.visibility = 'hidden';\n"+
		"                    } else {\n"+
		"                        bb.innerHTML = \"<a href='?d=\" + blobref + \"'>browse</a>\";\n"+
		"                    }\n"+
		"                    if (binfo.camliType == \"file\") {\n"+
		"                        try {\n"+
		"                            finfo = JSON.parse(data);\n"+
		"                            bd.innerHTML = \"<a href=''></a>\";\n"+
		"                            var fileName = finfo.fileName || blobref;\n"+
		"                            bd.firstChild.href = \"./download/\" + blobref + \"/\" + "+
		"fileName;\n"+
		"                            if (binfo.file.mimeType.indexOf(\"image/\") == 0) {\n"+
		"                                document.getElementById(\"thumbnail\").innerHTML = "+
		"\"<img src='./thumbnail/\" + blobref + \"/\" + fileName + \"?mw=200&mh=200'>\";\n"+
		"                            } else {\n"+
		"                                document.getElementById(\"thumbnail\").innerHTML = "+
		"\"\";\n"+
		"                            }\n"+
		"                            setTextContent(bd.firstChild, fileName);\n"+
		"                            bd.innerHTML = \"download: \" + bd.innerHTML;\n"+
		"                        } catch (x) {\n"+
		"                        }\n"+
		"                    }\n"+
		"                },\n"+
		"                fail: alert\n"+
		"            });\n"+
		"    } else {\n"+
		"        document.getElementById(\"blobdata\").innerHTML = \"<em>Unknown/binary data<"+
		"/em>\";\n"+
		"    }\n"+
		"    bd.innerHTML = \"<a href='\" + camliBlobURL(blobref) + \"'>download</a>\";\n"+
		"\n"+
		"    if (binfo.camliType && binfo.camliType == \"permanode\") {\n"+
		"        document.getElementById(\"editspan\").style.display = \"inline\";\n"+
		"        document.getElementById(\"editlink\").href = \"./?p=\" + blobref;\n"+
		"\n"+
		"        var claims = document.getElementById(\"claimsdiv\");\n"+
		"        claims.style.visibility = \"\";\n"+
		"        camliGetPermanodeClaims(\n"+
		"            blobref,\n"+
		"            {\n"+
		"                success: function(data) {\n"+
		"                    document.getElementById(\"claims\").innerHTML = linkifyBlobRefs"+
		"(JSON.stringify(data, null, 2));\n"+
		"                },\n"+
		"                fail: function(msg) {\n"+
		"                    alert(msg);\n"+
		"                }\n"+
		"            });\n"+
		"    }\n"+
		"\n"+
		"}\n"+
		"\n"+
		"function blobInfoOnLoad() {\n"+
		"    var blobref = getBlobParam();\n"+
		"    if (!blobref) {\n"+
		"        return\n"+
		"    }\n"+
		"    var blobmeta = document.getElementById('blobmeta');\n"+
		"    blobmeta.innerText = \"(loading)\";\n"+
		"\n"+
		"    var blobdescribe = document.getElementById('blobdescribe');\n"+
		"    blobdescribe.innerHTML = \"<a href='\" + camliDescribeBlogURL(blobref) + \"'>des"+
		"cribe</a>\";\n"+
		"    camliDescribeBlob(\n"+
		"        blobref,\n"+
		"        {\n"+
		"            success: blobInfoUpdate,\n"+
		"            fail: function(msg) {\n"+
		"                alert(\"Error describing blob \" + blobref + \": \" + msg);\n"+
		"            }\n"+
		"        }\n"+
		"    );\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", blobInfoOnLoad);\n"+
		"", time.Unix(0, 1352107488430325498))
}
