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

goog.provide('cam.BlobDetail');

goog.require('cam.CacheBusterIframe');

cam.BlobDetail.getAspect = function(baseURL, blobref, targetSearchSession) {
	if(!targetSearchSession) {
		return;
	}

	var m = targetSearchSession.getMeta(blobref);
	if (m) {
		return new cam.BlobDetail.Aspect(baseURL, blobref);
	} else {
		return null;
	}
};

cam.BlobDetail.Aspect = function(baseURL, blobref) {
	this.baseURL_ = baseURL;
	this.blobref_ = blobref;
};

cam.BlobDetail.Aspect.prototype.getFragment = function() {
	return 'blob';
};

cam.BlobDetail.Aspect.prototype.getTitle = function() {
	return 'Blob';
};

cam.BlobDetail.Aspect.prototype.createContent = function(size) {
	var url = this.baseURL_.clone();
	url.setParameterValue('b', this.blobref_);
	url.removeParameter('p');
	url.removeParameter('newui');
	return cam.CacheBusterIframe({
		height: size.height,
		key: 'blob',
		src: url,
		width: size.width,
	});
};
