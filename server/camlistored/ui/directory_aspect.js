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

goog.provide('cam.DirectoryDetail');

goog.require('cam.CacheBusterIframe');

cam.DirectoryDetail.getAspect = function(baseURL, blobref, searchSession) {
	var rm = searchSession.getResolvedMeta(blobref);
	if (rm.camliType == 'directory') {
		return new cam.DirectoryDetail.Aspect(baseURL, rm.blobRef);
	} else {
		return null;
	}
};

cam.DirectoryDetail.Aspect = function(baseURL, blobref) {
	this.baseURL_ = baseURL;
	this.blobref_ = blobref;
};

cam.DirectoryDetail.Aspect.prototype.getTitle = function() {
	return 'Directory';
};

cam.DirectoryDetail.Aspect.prototype.createContent = function(size) {
	var url = this.baseURL_.clone();
	url.setParameterValue('d', this.blobref_);
	return cam.CacheBusterIframe({
		height: size.height,
		src: url,
		width: size.width,
	});
};
