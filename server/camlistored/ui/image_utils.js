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

// Image utils.
goog.provide('image_utils');

// Returns the size of image to request for a given dimension. We round up to the nearest power of two for cache friendliness.
image_utils.getSizeToRequest = function(sizeToDisplay, opt_existingSize) {
	if (opt_existingSize && sizeToDisplay <= opt_existingSize) {
		return opt_existingSize;
	}

	var maxImageSize = 2000;  // max size server will accept
	for (var size = 64; (size <= sizeToDisplay && size < maxImageSize); size <<= 1) {
	}
	return Math.min(size, maxImageSize);
};
