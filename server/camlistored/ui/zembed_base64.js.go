// THIS FILE IS AUTO-GENERATED FROM base64.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("base64.js", 5764, fileembed.String("/*\n"+
		"Copyright (c) 2008 Fred Palmer fred.palmer_at_gmail.com\n"+
		"\n"+
		"Permission is hereby granted, free of charge, to any person\n"+
		"obtaining a copy of this software and associated documentation\n"+
		"files (the \"Software\"), to deal in the Software without\n"+
		"restriction, including without limitation the rights to use,\n"+
		"copy, modify, merge, publish, distribute, sublicense, and/or sell\n"+
		"copies of the Software, and to permit persons to whom the\n"+
		"Software is furnished to do so, subject to the following\n"+
		"conditions:\n"+
		"\n"+
		"The above copyright notice and this permission notice shall be\n"+
		"included in all copies or substantial portions of the Software.\n"+
		"\n"+
		"THE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND,\n"+
		"EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES\n"+
		"OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND\n"+
		"NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT\n"+
		"HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,\n"+
		"WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING\n"+
		"FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR\n"+
		"OTHER DEALINGS IN THE SOFTWARE.\n"+
		"*/\n"+
		"function StringBuffer()\n"+
		"{ \n"+
		"    this.buffer = []; \n"+
		"} \n"+
		"\n"+
		"StringBuffer.prototype.append = function append(string)\n"+
		"{ \n"+
		"    this.buffer.push(string); \n"+
		"    return this; \n"+
		"}; \n"+
		"\n"+
		"StringBuffer.prototype.toString = function toString()\n"+
		"{ \n"+
		"    return this.buffer.join(\"\"); \n"+
		"}; \n"+
		"\n"+
		"var Base64 =\n"+
		"{\n"+
		"    codex : \"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=\",\n"+
		"\n"+
		"    encode : function (input)\n"+
		"    {\n"+
		"        var output = new StringBuffer();\n"+
		"\n"+
		"        var enumerator = new Utf8EncodeEnumerator(input);\n"+
		"        while (enumerator.moveNext())\n"+
		"        {\n"+
		"            var chr1 = enumerator.current;\n"+
		"\n"+
		"            enumerator.moveNext();\n"+
		"            var chr2 = enumerator.current;\n"+
		"\n"+
		"            enumerator.moveNext();\n"+
		"            var chr3 = enumerator.current;\n"+
		"\n"+
		"            var enc1 = chr1 >> 2;\n"+
		"            var enc2 = ((chr1 & 3) << 4) | (chr2 >> 4);\n"+
		"            var enc3 = ((chr2 & 15) << 2) | (chr3 >> 6);\n"+
		"            var enc4 = chr3 & 63;\n"+
		"\n"+
		"            if (isNaN(chr2))\n"+
		"            {\n"+
		"                enc3 = enc4 = 64;\n"+
		"            }\n"+
		"            else if (isNaN(chr3))\n"+
		"            {\n"+
		"                enc4 = 64;\n"+
		"            }\n"+
		"\n"+
		"            output.append(this.codex.charAt(enc1) + this.codex.charAt(enc2) + thi"+
		"s.codex.charAt(enc3) + this.codex.charAt(enc4));\n"+
		"        }\n"+
		"\n"+
		"        return output.toString();\n"+
		"    },\n"+
		"\n"+
		"    decode : function (input)\n"+
		"    {\n"+
		"        // TypedArray usage added by brett@haxor.com 11/27/2010\n"+
		"        var size = 0;\n"+
		"        var buffer = new ArrayBuffer(input.length);\n"+
		"        var output = new Uint8Array(buffer, 0);\n"+
		"\n"+
		"        var enumerator = new Base64DecodeEnumerator(input);\n"+
		"        while (enumerator.moveNext()) {\n"+
		"            output[size++] = enumerator.current;\n"+
		"        }\n"+
		"\n"+
		"        // There is nothing in the TypedArray spec to copy/subset a buffer,\n"+
		"        // so we have to do a copy to ensure that typedarray.buffer is the\n"+
		"        // correct length when passed to XmlHttpRequest methods, etc.\n"+
		"        var outputBuffer = new ArrayBuffer(size);\n"+
		"        var outputArray = new Uint8Array(outputBuffer, 0);\n"+
		"        for (var i = 0; i < size; i++) {\n"+
		"          outputArray[i] = output[i];\n"+
		"        }\n"+
		"        return outputArray;\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"\n"+
		"function Utf8EncodeEnumerator(input)\n"+
		"{\n"+
		"    this._input = input;\n"+
		"    this._index = -1;\n"+
		"    this._buffer = [];\n"+
		"}\n"+
		"\n"+
		"Utf8EncodeEnumerator.prototype =\n"+
		"{\n"+
		"    current: Number.NaN,\n"+
		"\n"+
		"    moveNext: function()\n"+
		"    {\n"+
		"        if (this._buffer.length > 0)\n"+
		"        {\n"+
		"            this.current = this._buffer.shift();\n"+
		"            return true;\n"+
		"        }\n"+
		"        else if (this._index >= (this._input.length - 1))\n"+
		"        {\n"+
		"            this.current = Number.NaN;\n"+
		"            return false;\n"+
		"        }\n"+
		"        else\n"+
		"        {\n"+
		"            var charCode = this._input.charCodeAt(++this._index);\n"+
		"\n"+
		"            // \"\\r\\n\" -> \"\\n\"\n"+
		"            //\n"+
		"            if ((charCode == 13) && (this._input.charCodeAt(this._index + 1) == 1"+
		"0))\n"+
		"            {\n"+
		"                charCode = 10;\n"+
		"                this._index += 2;\n"+
		"            }\n"+
		"\n"+
		"            if (charCode < 128)\n"+
		"            {\n"+
		"                this.current = charCode;\n"+
		"            }\n"+
		"            else if ((charCode > 127) && (charCode < 2048))\n"+
		"            {\n"+
		"                this.current = (charCode >> 6) | 192;\n"+
		"                this._buffer.push((charCode & 63) | 128);\n"+
		"            }\n"+
		"            else\n"+
		"            {\n"+
		"                this.current = (charCode >> 12) | 224;\n"+
		"                this._buffer.push(((charCode >> 6) & 63) | 128);\n"+
		"                this._buffer.push((charCode & 63) | 128);\n"+
		"            }\n"+
		"\n"+
		"            return true;\n"+
		"        }\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"function Base64DecodeEnumerator(input)\n"+
		"{\n"+
		"    this._input = input;\n"+
		"    this._index = -1;\n"+
		"    this._buffer = [];\n"+
		"}\n"+
		"\n"+
		"Base64DecodeEnumerator.prototype =\n"+
		"{\n"+
		"    current: 64,\n"+
		"\n"+
		"    moveNext: function()\n"+
		"    {\n"+
		"        if (this._buffer.length > 0)\n"+
		"        {\n"+
		"            this.current = this._buffer.shift();\n"+
		"            return true;\n"+
		"        }\n"+
		"        else if (this._index >= (this._input.length - 1))\n"+
		"        {\n"+
		"            this.current = 64;\n"+
		"            return false;\n"+
		"        }\n"+
		"        else\n"+
		"        {\n"+
		"            var enc1 = Base64.codex.indexOf(this._input.charAt(++this._index));\n"+
		"            var enc2 = Base64.codex.indexOf(this._input.charAt(++this._index));\n"+
		"            var enc3 = Base64.codex.indexOf(this._input.charAt(++this._index));\n"+
		"            var enc4 = Base64.codex.indexOf(this._input.charAt(++this._index));\n"+
		"\n"+
		"            var chr1 = (enc1 << 2) | (enc2 >> 4);\n"+
		"            var chr2 = ((enc2 & 15) << 4) | (enc3 >> 2);\n"+
		"            var chr3 = ((enc3 & 3) << 6) | enc4;\n"+
		"\n"+
		"            this.current = chr1;\n"+
		"\n"+
		"            if (enc3 != 64)\n"+
		"                this._buffer.push(chr2);\n"+
		"\n"+
		"            if (enc4 != 64)\n"+
		"                this._buffer.push(chr3);\n"+
		"\n"+
		"            return true;\n"+
		"        }\n"+
		"    }\n"+
		"};\n"+
		""), time.Unix(0, 1349725494143774804))
}
