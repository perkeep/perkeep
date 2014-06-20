/*
Copyright 2014 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

goog.provide('cam.blob');

goog.require('goog.crypt');
goog.require('goog.crypt.Sha1');

// Returns the Camlistore blobref for hash object. The only supported hash function is currently sha1, but more might be added later.
// @param {!goog.crypt.Hash} hash
// @returns {!string}
cam.blob.refFromHash = function(hash) {
	if (hash instanceof goog.crypt.Sha1) {
		return 'sha1-' + goog.crypt.byteArrayToHex(hash.digest());
	}
	throw new Error('Unsupported hash function type');
};

// Returns the Camlistore blobref for a string using the currently recommended hash function.
// @param {!string} str
// @returns {!string}
cam.blob.refFromString = function(str) {
	var hash = cam.blob.createHash();
	// update only supports 8 bit chars: http://docs.closure-library.googlecode.com/git/class_goog_crypt_Sha1.html
	hash.update(goog.crypt.stringToUtf8ByteArray(str));
	return cam.blob.refFromHash(hash);
};

// Returns the Camlistore blobref for a DOM blob (different from Camlistore blob) using the currently recommended hash function. This function currently only works within workers.
// @param {Blob} blob
// @returns {!string}
cam.blob.refFromDOMBlob = function(blob) {
	if (!goog.global.FileReaderSync) {
		// TODO(aa): If necessary, we can also implement this using FileReader for use on the main thread. But beware that should not be done for very large objects without checking the effect on framerate carefully.
		throw new Error('FileReaderSync not available. Perhaps we are on the main thread?');
	}

	var fr = new FileReaderSync();
	var hash = cam.blob.createHash();
	var chunkSize = 1024 * 1024;
	for (var start = 0; start < blob.size; start += chunkSize) {
		var end = Math.min(start + chunkSize, blob.size);
		var slice = blob.slice(start, end);
		hash.update(new Uint8Array(fr.readAsArrayBuffer(slice)));
	}

	return cam.blob.refFromHash(hash);
};

// Creates an instance of the currently recommened hash function.
// @return {!goog.crypt.Hash'}
cam.blob.createHash = function() {
	return new goog.crypt.Sha1();
};
