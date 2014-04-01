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

var assert = require('assert');

goog.require('cam.permanodeUtils');


describe('cam.permanodeUtils', function() {
  describe('#getSingleAttr', function() {
    it('should return null if attr unknown or empty string', function() {
        var pn = {
            attr: {
                foo: '',
            },
        };
        assert.strictEqual(null, cam.permanodeUtils.getSingleAttr(pn, 'foo'));
        assert.strictEqual(null, cam.permanodeUtils.getSingleAttr(pn, 'bar'));
    });

    it('should return first array val', function() {
        var pn = {
            attr: {
                foo: ['bar', 'baz'],
            },
        };
        assert.equal('bar', cam.permanodeUtils.getSingleAttr(pn, 'foo'));
    });

    it('should return string val', function() {
        var pn = {
            attr: {
                foo: 'bar',
            },
        };
        assert.equal('bar', cam.permanodeUtils.getSingleAttr(pn, 'foo'));
    });
  });
});
