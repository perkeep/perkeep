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

goog.provide('cam.ContainerDetail');

goog.require('cam.BlobItemContainerReact');
goog.require('goog.object');
goog.require('goog.string');

cam.ContainerDetail.getAspect = function(detailURL, handlers, history, getSearchSession, thumbnailSize, blobref, searchSession) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode') {
		return null;
	}

	// TODO(aa): Also handle directories and static sets.
	if (!goog.object.some(m.permanode.attr, function(v, k) { return k == 'camliMember' || goog.string.startsWith(k, 'camliPath:'); })) {
		return null;
	}

	return new cam.ContainerDetail.Aspect(detailURL, handlers, history, getSearchSession, thumbnailSize, blobref);
};

cam.ContainerDetail.Aspect = function(detailURL, handlers, history, getSearchSession, thumbnailSize, blobref) {
	this.detailURL_ = detailURL;
	this.handlers_ = handlers;
	this.history_ = history;
	this.getSearchSession_ = getSearchSession;
	this.thumbnailSize_ = thumbnailSize;
	this.blobref_ = blobref;
};

cam.ContainerDetail.Aspect.prototype.getTitle = function() {
	return 'Container';
};

cam.ContainerDetail.Aspect.prototype.createContent = function(size) {
	if (!this.searchSession) {
		this.searchSession_ = this.getSearchSession_(this.blobref_);
	}
	return cam.BlobItemContainerReact({
		detailURL: this.detailURL_,
		handlers: this.handlers_,
		history: this.history_,
		onSelectionChange: function() {
			console.error('TODO');
		},
		searchSession: this.searchSession_,
		selection: {},
		style: {
			background: 'white',
			height: size.height,
			left: 0,
			overflowX: 'hidden',
			overflowY: 'scroll',
			position: 'absolute',
			top: 0,
			width: size.width,
		},
		thumbnailSize: this.thumbnailSize_,
	});
};
