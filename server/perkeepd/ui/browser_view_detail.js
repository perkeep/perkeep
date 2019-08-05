/*
Copyright 2019 The Perkeep Authors

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

goog.provide('cam.BrowserViewDetail');

// Renders content that browsers understand natively. Works by embedding it in
// an iframe.
//
// Note that not all content that browsers understand natively is covered here.
// For example, images have their own custom support (ImageDetail) which provides
// a nicer UI.
cam.BrowserViewDetail = React.createClass({
	displayName: 'BrowserViewDetail',

	propTypes: {
		height: React.PropTypes.number.isRequired,
		resolvedMeta: React.PropTypes.object.isRequired,
		width: React.PropTypes.number.isRequired,
	},

	render: function() {
		var rm = this.props.resolvedMeta;
		var downloadUrl = goog.string.subs(
			'%s%s/%s?inline=1',
			goog.global.CAMLISTORE_CONFIG.downloadHelper,
			rm.blobRef,
			rm.file.fileName
		);
		return React.DOM.iframe({
			src: downloadUrl,
			style: {
				width: this.props.width,
				height: this.props.height,
				border: 'none',
			},
		});
	},
});

cam.BrowserViewDetail.getAspect = function(blobref, searchSession) {
	const supportedMimeTypes = [
		"application/pdf",
		"text/plain",
	]

	if(!blobref) {
		return null;
	}

	var rm = searchSession.getResolvedMeta(blobref);
	var pm = searchSession.getMeta(blobref);

	if (!pm) {
		return null;
	}

	if (pm.camliType != 'permanode') {
		pm = null;
	}


	if(rm.camliType !== 'file' || !supportedMimeTypes.includes(rm.file.mimeType)) {
		return null;
	}

	return {
		fragment: 'document',
		title: 'Document',
		createContent: function(size, backwardPiggy) {
			return React.createElement(cam.BrowserViewDetail, {
				resolvedMeta: rm,
				height: size.height,
				width: size.width,
			})
		},
	}
}
