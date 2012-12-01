// THIS FILE IS AUTO-GENERATED FROM Crypto.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("Crypto.js", 5731, fileembed.String("// From http://code.google.com/p/crypto-js/\x0d\n"+
		"// License: http://www.opensource.org/licenses/bsd-license.php\x0d\n"+
		"//\x0d\n"+
		"// Copyright (c) 2009, Jeff Mott. All rights reserved.\x0d\n"+
		"// \x0d\n"+
		"// Redistribution and use in source and binary forms, with or without\x0d\n"+
		"// modification, are permitted provided that the following conditions are met:\x0d\n"+
		"// \x0d\n"+
		"// Redistributions of source code must retain the above copyright notice, this\x0d\n"+
		"// list of conditions and the following disclaimer. Redistributions in binary\x0d\n"+
		"// form must reproduce the above copyright notice, this list of conditions and\x0d\n"+
		"// the following disclaimer in the documentation and/or other materials provided\x0d\n"+
		"// with the distribution. Neither the name Crypto-JS nor the names of its\x0d\n"+
		"// contributors may be used to endorse or promote products derived from this\x0d\n"+
		"// software without specific prior written permission. THIS SOFTWARE IS PROVIDED\x0d\n"+
		"// BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS \"AS IS\" AND ANY EXPRESS OR IMPLIED\x0d\n"+
		"// WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF\x0d\n"+
		"// MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO\x0d\n"+
		"// EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT,\x0d\n"+
		"// INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES\x0d\n"+
		"// (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;\x0d\n"+
		"// LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND\x0d\n"+
		"// ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT\x0d\n"+
		"// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS\x0d\n"+
		"// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.\x0d\n"+
		"\x0d\n"+
		"if (typeof Crypto == \"undefined\" || ! Crypto.util)\x0d\n"+
		"{\x0d\n"+
		"(function(){\x0d\n"+
		"\x0d\n"+
		"var base64map = \"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"+
		"\";\x0d\n"+
		"\x0d\n"+
		"// Global Crypto object\x0d\n"+
		"var Crypto = window.Crypto = {};\x0d\n"+
		"\x0d\n"+
		"// Crypto utilities\x0d\n"+
		"var util = Crypto.util = {\x0d\n"+
		"\x0d\n"+
		"	// Bit-wise rotate left\x0d\n"+
		"	rotl: function (n, b) {\x0d\n"+
		"		return (n << b) | (n >>> (32 - b));\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Bit-wise rotate right\x0d\n"+
		"	rotr: function (n, b) {\x0d\n"+
		"		return (n << (32 - b)) | (n >>> b);\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Swap big-endian to little-endian and vice versa\x0d\n"+
		"	endian: function (n) {\x0d\n"+
		"\x0d\n"+
		"		// If number given, swap endian\x0d\n"+
		"		if (n.constructor == Number) {\x0d\n"+
		"			return util.rotl(n,  8) & 0x00FF00FF |\x0d\n"+
		"			       util.rotl(n, 24) & 0xFF00FF00;\x0d\n"+
		"		}\x0d\n"+
		"\x0d\n"+
		"		// Else, assume array and swap all items\x0d\n"+
		"		for (var i = 0; i < n.length; i++)\x0d\n"+
		"			n[i] = util.endian(n[i]);\x0d\n"+
		"		return n;\x0d\n"+
		"\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Generate an array of any length of random bytes\x0d\n"+
		"	randomBytes: function (n) {\x0d\n"+
		"		for (var bytes = []; n > 0; n--)\x0d\n"+
		"			bytes.push(Math.floor(Math.random() * 256));\x0d\n"+
		"		return bytes;\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert a byte array to big-endian 32-bit words\x0d\n"+
		"	bytesToWords: function (bytes) {\x0d\n"+
		"		for (var words = [], i = 0, b = 0; i < bytes.length; i++, b += 8)\x0d\n"+
		"			words[b >>> 5] |= bytes[i] << (24 - b % 32);\x0d\n"+
		"		return words;\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert big-endian 32-bit words to a byte array\x0d\n"+
		"	wordsToBytes: function (words) {\x0d\n"+
		"		for (var bytes = [], b = 0; b < words.length * 32; b += 8)\x0d\n"+
		"			bytes.push((words[b >>> 5] >>> (24 - b % 32)) & 0xFF);\x0d\n"+
		"		return bytes;\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert a byte array to a hex string\x0d\n"+
		"	bytesToHex: function (bytes) {\x0d\n"+
		"		for (var hex = [], i = 0; i < bytes.length; i++) {\x0d\n"+
		"			hex.push((bytes[i] >>> 4).toString(16));\x0d\n"+
		"			hex.push((bytes[i] & 0xF).toString(16));\x0d\n"+
		"		}\x0d\n"+
		"		return hex.join(\"\");\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert a hex string to a byte array\x0d\n"+
		"	hexToBytes: function (hex) {\x0d\n"+
		"		for (var bytes = [], c = 0; c < hex.length; c += 2)\x0d\n"+
		"			bytes.push(parseInt(hex.substr(c, 2), 16));\x0d\n"+
		"		return bytes;\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert a byte array to a base-64 string\x0d\n"+
		"	bytesToBase64: function (bytes) {\x0d\n"+
		"\x0d\n"+
		"		// Use browser-native function if it exists\x0d\n"+
		"		if (typeof btoa == \"function\") return btoa(Binary.bytesToString(bytes));\x0d\n"+
		"\x0d\n"+
		"		for(var base64 = [], i = 0; i < bytes.length; i += 3) {\x0d\n"+
		"			var triplet = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2];\x0d\n"+
		"			for (var j = 0; j < 4; j++) {\x0d\n"+
		"				if (i * 8 + j * 6 <= bytes.length * 8)\x0d\n"+
		"					base64.push(base64map.charAt((triplet >>> 6 * (3 - j)) & 0x3F));\x0d\n"+
		"				else base64.push(\"=\");\x0d\n"+
		"			}\x0d\n"+
		"		}\x0d\n"+
		"\x0d\n"+
		"		return base64.join(\"\");\x0d\n"+
		"\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert a base-64 string to a byte array\x0d\n"+
		"	base64ToBytes: function (base64) {\x0d\n"+
		"\x0d\n"+
		"		// Use browser-native function if it exists\x0d\n"+
		"		if (typeof atob == \"function\") return Binary.stringToBytes(atob(base64));\x0d\n"+
		"\x0d\n"+
		"		// Remove non-base-64 characters\x0d\n"+
		"		base64 = base64.replace(/[^A-Z0-9+\\/]/ig, \"\");\x0d\n"+
		"\x0d\n"+
		"		for (var bytes = [], i = 0, imod4 = 0; i < base64.length; imod4 = ++i % 4) {\x0d\n"+
		"			if (imod4 == 0) continue;\x0d\n"+
		"			bytes.push(((base64map.indexOf(base64.charAt(i - 1)) & (Math.pow(2, -2 * imod4"+
		" + 8) - 1)) << (imod4 * 2)) |\x0d\n"+
		"			           (base64map.indexOf(base64.charAt(i)) >>> (6 - imod4 * 2)));\x0d\n"+
		"		}\x0d\n"+
		"\x0d\n"+
		"		return bytes;\x0d\n"+
		"\x0d\n"+
		"	}\x0d\n"+
		"\x0d\n"+
		"};\x0d\n"+
		"\x0d\n"+
		"// Crypto mode namespace\x0d\n"+
		"Crypto.mode = {};\x0d\n"+
		"\x0d\n"+
		"// Crypto character encodings\x0d\n"+
		"var charenc = Crypto.charenc = {};\x0d\n"+
		"\x0d\n"+
		"// UTF-8 encoding\x0d\n"+
		"var UTF8 = charenc.UTF8 = {\x0d\n"+
		"\x0d\n"+
		"	// Convert a string to a byte array\x0d\n"+
		"	stringToBytes: function (str) {\x0d\n"+
		"		return Binary.stringToBytes(unescape(encodeURIComponent(str)));\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert a byte array to a string\x0d\n"+
		"	bytesToString: function (bytes) {\x0d\n"+
		"		return decodeURIComponent(escape(Binary.bytesToString(bytes)));\x0d\n"+
		"	}\x0d\n"+
		"\x0d\n"+
		"};\x0d\n"+
		"\x0d\n"+
		"// Binary encoding\x0d\n"+
		"var Binary = charenc.Binary = {\x0d\n"+
		"\x0d\n"+
		"	// Convert a string to a byte array\x0d\n"+
		"	stringToBytes: function (str) {\x0d\n"+
		"		for (var bytes = [], i = 0; i < str.length; i++)\x0d\n"+
		"			bytes.push(str.charCodeAt(i));\x0d\n"+
		"		return bytes;\x0d\n"+
		"	},\x0d\n"+
		"\x0d\n"+
		"	// Convert a byte array to a string\x0d\n"+
		"	bytesToString: function (bytes) {\x0d\n"+
		"		for (var str = [], i = 0; i < bytes.length; i++)\x0d\n"+
		"			str.push(String.fromCharCode(bytes[i]));\x0d\n"+
		"		return str.join(\"\");\x0d\n"+
		"	}\x0d\n"+
		"\x0d\n"+
		"};\x0d\n"+
		"\x0d\n"+
		"})();\x0d\n"+
		"}\x0d\n"+
		""), time.Unix(0, 1349725494123778548))
}
