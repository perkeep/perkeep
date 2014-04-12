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
// Sizes are bucketized for cache friendliness. Also, the last requested size is remembered, and if the requested size is smaller than the last size, then we continue using the old URL.
cam.Thumber = function(pathname, opt_aspect) {
	this.pathname_ = pathname;
	this.lastHeight_ = 0;
	this.aspect_ = opt_aspect || 1;
};

// We originally just used powers of 2, but we need sizes between 200 and 400 all the time in the UI and it seemed wasteful to jump to 512. Having an explicit list will make it easier to tune the buckets more in the future if necessary.
cam.Thumber.SIZES = [64, 128, 256, 375, 500, 750, 1000, 1500, 2000];

cam.Thumber.fromImageMeta = function(imageMeta) {
	return new cam.Thumber(goog.string.subs('thumbnail/%s/%s', imageMeta.blobRef, (imageMeta.file && imageMeta.file.fileName) || imageMeta.blobRef + '.jpg'),
		imageMeta.image.width / imageMeta.image.height);
};

// @param {number|goog.math.Size} minSize The minimum size of the required thumbnail. If this is a number, it is the minimum height. If it is goog.math.Size, then it is the min size of both dimensions.
cam.Thumber.prototype.getSrc = function(minSize) {
	var minWidth, minHeight;
	if (typeof minSize == 'number') {
		minHeight = minSize;
		minWidth = 0;
	} else {
		minWidth = minSize.width;
		minHeight = minSize.height;
	}

	this.lastHeight_ = this.getSizeToRequest_(minWidth, minHeight);
	return goog.string.subs('%s?mh=%s&tv=%s', this.pathname_, this.lastHeight_, goog.global.CAMLISTORE_CONFIG ? goog.global.CAMLISTORE_CONFIG.thumbVersion : 1);
};

cam.Thumber.prototype.getSizeToRequest_ = function(minWidth, minHeight) {
	if (this.lastHeight_ >= minHeight && ((this.lastHeight_ * this.aspect_) >= minWidth)) {
		return this.lastHeight_;
	}
	var newHeight;
	for (var i = 0; i < cam.Thumber.SIZES.length; i++) {
		newHeight = cam.Thumber.SIZES[i];
		if (newHeight >= minHeight && ((newHeight * this.aspect_) >= minWidth)) {
			break;
		}
	}
	return newHeight;
};
