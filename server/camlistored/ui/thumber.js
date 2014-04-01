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

goog.provide('cam.Thumber');

goog.require('goog.string');

// Utility to efficiently choose thumbnail URLs for use by the UI.
//
// Sizes are rounded to the next highest power of two for cache friendliness. Also, the last requested size is remembered, and if the requested size is smaller than the last size, then we continue using the old URL.
cam.Thumber = function(pathname) {
	this.pathname_ = pathname;
	this.lastSize_ = 0;
};

cam.Thumber.MAX_SIZE = 2000;

cam.Thumber.fromImageMeta = function(imageMeta) {
	return new cam.Thumber(goog.string.subs('thumbnail/%s/%s', imageMeta.blobRef, (imageMeta.file && imageMeta.file.fileName) || imageMeta.blobRef + '.jpg'));
};

cam.Thumber.prototype.getSrc = function(displayHeight) {
	this.lastSize_ = this.getSizeToRequest_(displayHeight);
	return this.pathname_ + '?mh=' + this.lastSize_ + '&tv=' + (goog.global.CAMLISTORE_CONFIG ? goog.global.CAMLISTORE_CONFIG.thumbVersion : 1);
};

cam.Thumber.prototype.getSizeToRequest_ = function(displayHeight) {
	if (this.lastSize_ >= displayHeight) {
		return this.lastSize_;
	}

	for (var size = 64; (size <= displayHeight && size < cam.Thumber.MAX_SIZE); size <<= 1) {
	}
	return Math.min(size, cam.Thumber.MAX_SIZE);
};
