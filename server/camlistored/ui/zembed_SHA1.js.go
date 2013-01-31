// THIS FILE IS AUTO-GENERATED FROM SHA1.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("SHA1.js", 3604, fileembed.String("// From http://code.google.com/p/crypto-js/\x0d\n"+
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
		"if (typeof goog != 'undefined' && typeof goog.provide != 'undefined') {\n"+
		"    goog.provide('camlistore.SHA1');\n"+
		"\n"+
		"    goog.require('camlistore.Crypto');\n"+
		"}\n"+
		"\n"+
		"(function(){\x0d\n"+
		"\x0d\n"+
		"// Shortcuts\x0d\n"+
		"var C = Crypto,\x0d\n"+
		"    util = C.util,\x0d\n"+
		"    charenc = C.charenc,\x0d\n"+
		"    UTF8 = charenc.UTF8,\x0d\n"+
		"    Binary = charenc.Binary;\x0d\n"+
		"\x0d\n"+
		"// Public API\x0d\n"+
		"var SHA1 = C.SHA1 = function (message, options) {\x0d\n"+
		"	var digestbytes = util.wordsToBytes(SHA1._sha1(message));\x0d\n"+
		"	return options && options.asBytes ? digestbytes :\x0d\n"+
		"	       options && options.asString ? Binary.bytesToString(digestbytes) :\x0d\n"+
		"	       util.bytesToHex(digestbytes);\x0d\n"+
		"};\x0d\n"+
		"\x0d\n"+
		"// The core\x0d\n"+
		"SHA1._sha1 = function (message) {\x0d\n"+
		"\x0d\n"+
		"	// Convert to byte array\x0d\n"+
		"	if (message.constructor == String) message = UTF8.stringToBytes(message);\x0d\n"+
		"	/* else, assume byte array already */\x0d\n"+
		"\x0d\n"+
		"	var m  = util.bytesToWords(message),\x0d\n"+
		"	    l  = message.length * 8,\x0d\n"+
		"	    w  =  [],\x0d\n"+
		"	    H0 =  1732584193,\x0d\n"+
		"	    H1 = -271733879,\x0d\n"+
		"	    H2 = -1732584194,\x0d\n"+
		"	    H3 =  271733878,\x0d\n"+
		"	    H4 = -1009589776;\x0d\n"+
		"\x0d\n"+
		"	// Padding\x0d\n"+
		"	m[l >> 5] |= 0x80 << (24 - l % 32);\x0d\n"+
		"	m[((l + 64 >>> 9) << 4) + 15] = l;\x0d\n"+
		"\x0d\n"+
		"	for (var i = 0; i < m.length; i += 16) {\x0d\n"+
		"\x0d\n"+
		"		var a = H0,\x0d\n"+
		"		    b = H1,\x0d\n"+
		"		    c = H2,\x0d\n"+
		"		    d = H3,\x0d\n"+
		"		    e = H4;\x0d\n"+
		"\x0d\n"+
		"		for (var j = 0; j < 80; j++) {\x0d\n"+
		"\x0d\n"+
		"			if (j < 16) w[j] = m[i + j];\x0d\n"+
		"			else {\x0d\n"+
		"				var n = w[j-3] ^ w[j-8] ^ w[j-14] ^ w[j-16];\x0d\n"+
		"				w[j] = (n << 1) | (n >>> 31);\x0d\n"+
		"			}\x0d\n"+
		"\x0d\n"+
		"			var t = ((H0 << 5) | (H0 >>> 27)) + H4 + (w[j] >>> 0) + (\x0d\n"+
		"			         j < 20 ? (H1 & H2 | ~H1 & H3) + 1518500249 :\x0d\n"+
		"			         j < 40 ? (H1 ^ H2 ^ H3) + 1859775393 :\x0d\n"+
		"			         j < 60 ? (H1 & H2 | H1 & H3 | H2 & H3) - 1894007588 :\x0d\n"+
		"			                  (H1 ^ H2 ^ H3) - 899497514);\x0d\n"+
		"\x0d\n"+
		"			H4 =  H3;\x0d\n"+
		"			H3 =  H2;\x0d\n"+
		"			H2 = (H1 << 30) | (H1 >>> 2);\x0d\n"+
		"			H1 =  H0;\x0d\n"+
		"			H0 =  t;\x0d\n"+
		"\x0d\n"+
		"		}\x0d\n"+
		"\x0d\n"+
		"		H0 += a;\x0d\n"+
		"		H1 += b;\x0d\n"+
		"		H2 += c;\x0d\n"+
		"		H3 += d;\x0d\n"+
		"		H4 += e;\x0d\n"+
		"\x0d\n"+
		"	}\x0d\n"+
		"\x0d\n"+
		"	return [H0, H1, H2, H3, H4];\x0d\n"+
		"\x0d\n"+
		"};\x0d\n"+
		"\x0d\n"+
		"// Package private blocksize\x0d\n"+
		"SHA1._blocksize = 16;\x0d\n"+
		"\x0d\n"+
		"})();\x0d\n"+
		""), time.Unix(0, 1359675908724750359))
}
