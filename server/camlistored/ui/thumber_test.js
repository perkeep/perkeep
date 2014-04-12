/*
Copyright 2014 The Camlistore Authors

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

var assert = require('assert');

goog.require('goog.math.Size');
goog.require('goog.Uri');

goog.require('cam.Thumber');


describe('cam.Thumber', function() {
  describe('#getSrc', function() {
    it('it should bucketize properly', function() {
        var thumber = new cam.Thumber('foo.png');
        assert.equal(128, goog.Uri.parse(thumber.getSrc(100)).getParameterValue('mh'));
        assert.equal(128, goog.Uri.parse(thumber.getSrc(128)).getParameterValue('mh'));
        assert.equal(256, goog.Uri.parse(thumber.getSrc(129)).getParameterValue('mh'));
        assert.equal(256, goog.Uri.parse(thumber.getSrc(256)).getParameterValue('mh'));
    });

    it('should max out at a sane size', function() {
        var thumber = new cam.Thumber('foo.png');
        var maxSize = cam.Thumber.SIZES[cam.Thumber.SIZES.length - 1];
        assert.equal(maxSize, goog.Uri.parse(thumber.getSrc(1999)).getParameterValue('mh'));
        assert.equal(maxSize, goog.Uri.parse(thumber.getSrc(2000)).getParameterValue('mh'));
        assert.equal(maxSize, goog.Uri.parse(thumber.getSrc(2001)).getParameterValue('mh'));
    });

    it('should only increase in size, never decrease', function() {
        var thumber = new cam.Thumber('foo.png');
        assert.equal(64, goog.Uri.parse(thumber.getSrc(50)).getParameterValue('mh'));
        assert.equal(64, goog.Uri.parse(thumber.getSrc(64)).getParameterValue('mh'));
        assert.equal(128, goog.Uri.parse(thumber.getSrc(65)).getParameterValue('mh'));
        assert.equal(128, goog.Uri.parse(thumber.getSrc(50)).getParameterValue('mh'));
        assert.equal(256, goog.Uri.parse(thumber.getSrc(129)).getParameterValue('mh'));
    });

    it('should handle Size objects properly', function() {
        var thumber = new cam.Thumber('foo.png', 2);
        assert.equal(128, goog.Uri.parse(thumber.getSrc(new goog.math.Size(100, 100))).getParameterValue('mh'));
        thumber = new cam.Thumber('foo.png', 0.5);
        assert.equal(256, goog.Uri.parse(thumber.getSrc(new goog.math.Size(100, 100))).getParameterValue('mh'));

        assert.equal(256, goog.Uri.parse(thumber.getSrc(new goog.math.Size(128, 100))).getParameterValue('mh'));
        assert.equal(375, goog.Uri.parse(thumber.getSrc(new goog.math.Size(129, 100))).getParameterValue('mh'));
    });
  });
});
