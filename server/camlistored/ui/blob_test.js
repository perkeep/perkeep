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

goog.require('goog.crypt.Hash');
goog.require('goog.crypt.Sha1');
goog.require('goog.string');

var assert = require('assert');

goog.require('cam.blob');


var MockDOMBlob = function(buffer, start, end) {
    this.buffer_ = buffer;
    this.start_ = start;
    this.size = end - start;
};

MockDOMBlob.fromSize = function(size, chr) {
    var arr = new Uint8Array(size);
    for (var i = 0; i < arr.length; i++) {
        arr[i] = chr.charCodeAt(0);
    }
    return new MockDOMBlob(arr.buffer, 0, arr.length);
};

MockDOMBlob.prototype.slice = function(start, end) {
    if (start < 0 || start >= this.size) {
        throw new Error(goog.strings.subs("start '%s' out of range [0,%s)", start, this.size));
    }
    if (end < this.start_ || end > this.size) {
        throw new Error(goog.string.subs("end '%s' out of range [0,%s)", end, this.size));
    }
    if (end < start) {
        throw new Error(goog.string.subs("end '%s' is less than start '%s'", start, end));
    }

    return new MockDOMBlob(this.buffer_, this.start_ + start, this.start_ + end);
};

MockDOMBlob.prototype.getArrayBuffer = function() {
    return new Uint8Array(this.buffer_, this.start_, this.size);
};


var MockFileReaderSync = function() {
};

MockFileReaderSync.prototype.readAsArrayBuffer = function(blob) {
    return blob.getArrayBuffer();
};


describe('cam.blob', function() {
  describe('#refFromHash', function() {
    it('should calculate the right hash', function() {
        var hash = new goog.crypt.Sha1();
        assert.equal(cam.blob.refFromHash(hash), 'sha1-da39a3ee5e6b4b0d3255bfef95601890afd80709');

        hash.reset();
        hash.update('The quick brown fox jumps over the lazy dog');
        assert.equal(cam.blob.refFromHash(hash), 'sha1-2fd4e1c67a2d28fced849ee1bb76e7391b93eb12');
    });

    it('should complain about wrong hash function', function() {
        function FooHash() {};
        goog.inherits(FooHash, goog.crypt.Hash);
        assert.throws(cam.blob.refFromHash.bind(null, new FooHash()), /Unsupported hash function type/);
    });
  });

  describe('#refFromString', function() {
    it('should calculate the right hash', function() {
        assert.equal(cam.blob.refFromString(''), 'sha1-da39a3ee5e6b4b0d3255bfef95601890afd80709');
        assert.equal(cam.blob.refFromString('The quick brown fox jumps over the lazy dog'), 'sha1-2fd4e1c67a2d28fced849ee1bb76e7391b93eb12');
        assert.equal(cam.blob.refFromString('Les caractères accentués, quelle plaie.'), 'sha1-2ad8f499b8721a7fe35504bce86df451db37dd66');
    });
  });

  describe('#refFromDOMBlob', function() {
    it('should calculate the right hash', function() {
        blob = MockDOMBlob.fromSize(1000001, 'a');
        goog.global.FileReaderSync = MockFileReaderSync;
        try {
            // Verified with openssl.
            assert.equal(cam.blob.refFromDOMBlob(blob), 'sha1-432e7e01de7086c5246b6ac57f5f435b58f13752');
        } finally {
            delete goog.global.FileReaderSync;
        }
    });
  });
});
