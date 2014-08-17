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

// TODO(aa): Rename file.
cam.DirectoryDetail.getAspect = function(baseURL, onChildFrameClick, blobref, targetSearchSession) {
	if (!targetSearchSession) {
		return;
	}

	var rm = targetSearchSession.getResolvedMeta(blobref);
	if (!rm || rm.camliType != 'directory') {
		return null;
	}

	return {
		fragment: 'directory',
		title: 'Directory',
		createContent: function(size) {
			var url = baseURL.clone();
			url.setParameterValue('d', rm.blobRef);
			return cam.CacheBusterIframe({
				baseURL: baseURL,
				height: size.height,
				onChildFrameClick: onChildFrameClick,
				src: url,
				width: size.width,
			});
		},
	};
};
