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

goog.provide('cam.PermanodeDetail');

goog.require('cam.CacheBusterIframe');

cam.PermanodeDetail.getAspect = function(baseURL, blobref, targetSearchSession) {
	if (!targetSearchSession) {
		return null;
	}

	var pm = targetSearchSession.getMeta(blobref);
	if (pm && pm.camliType == 'permanode') {
		return new cam.PermanodeDetail.Aspect(baseURL, blobref);
	} else {
		return null;
	}
};

cam.PermanodeDetail.Aspect = function(baseURL, blobref) {
	this.baseURL_ = baseURL;
	this.blobref_ = blobref;
};

cam.PermanodeDetail.Aspect.prototype.getFragment = function() {
	return 'permanode';
};

cam.PermanodeDetail.Aspect.prototype.getTitle = function() {
	return 'Permanode';
};

cam.PermanodeDetail.Aspect.prototype.createContent = function(size) {
	var url = this.baseURL_.clone();
	url.setParameterValue('p', this.blobref_);
	url.removeParameter('newui');
	return cam.CacheBusterIframe({
		height: size.height,
		key: 'permanode',
		src: url,
		width: size.width,
	});
};
