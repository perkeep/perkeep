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

goog.provide('cam.BlobItemGenericContent');

goog.require('goog.math.Size');

goog.require('cam.math');
goog.require('cam.object');
goog.require('cam.permanodeUtils');

// Renders the content of blob items that are not known to be some more specific type. A generic file or folder icon is shown, along with a title if one can be determined.
cam.BlobItemGenericContent = React.createClass({
	displayName: 'BlobItemGenericContent',

	TITLE_HEIGHT: 22,

	propTypes: {
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		thumbSrc: React.PropTypes.string.isRequired,
		thumbAspect: React.PropTypes.number.isRequired,
		title: React.PropTypes.string.isRequired,
	},

	render: function() {
		var thumbClipSize = this.getThumbClipSize_();
		// TODO(aa): I think we don't need/want the thumb clip div anymore. We can just make the anchor position:relative position the thumb inside it.
		return React.DOM.a({href:this.props.href},
			React.DOM.div({className:this.getThumbClipClassName_(), style:thumbClipSize},
				this.getThumb_(thumbClipSize)
			),
			this.getLabel_()
		);
	},

	getThumbClipClassName_: function() {
		return React.addons.classSet({
			'cam-blobitem-thumbclip': true,
			'cam-blobitem-loading': false,
		});
	},

	getThumb_: function(thumbClipSize) {
		var thumbSize = this.getThumbSize_(thumbClipSize);
		var pos = cam.math.center(thumbSize, thumbClipSize);
		return React.DOM.img({
			className: 'cam-blobitem-thumb',
			ref: 'thumb',
			src: this.props.thumbSrc,
			style: {left:pos.x, top:pos.y},
			width: thumbSize.width,
			height: thumbSize.height,
		})
	},

	getLabel_: function() {
		return React.DOM.span({className:'cam-blobitem-thumbtitle', style:{width:this.props.size.width}}, this.props.title);
	},

	getThumbSize_: function(available) {
		var bleed = false;
		return cam.math.scaleToFit(new goog.math.Size(this.props.thumbAspect, 1), available, bleed);
	},

	getThumbClipSize_: function() {
		return new goog.math.Size(this.props.size.width, this.props.size.height - this.TITLE_HEIGHT);
	},
});

cam.BlobItemGenericContent.getHandler = function(blobref, searchSession, href) {
	return new cam.BlobItemGenericContent.Handler(blobref, searchSession, href);
};


cam.BlobItemGenericContent.Handler = function(blobref, searchSession, href) {
	this.blobref_ = blobref;
	this.searchSession_ = searchSession;
	this.href_ = href;
	this.thumbType_ = this.getThumbType_();
};

cam.BlobItemGenericContent.Handler.ICON_ASPECT = {
	FILE: 260 / 300,
	FOLDER: 300 / 300,
};

cam.BlobItemGenericContent.Handler.prototype.getAspectRatio = function() {
	return this.thumbType_ == 'folder' ? this.constructor.ICON_ASPECT.FOLDER : this.constructor.ICON_ASPECT.FILE;
};

cam.BlobItemGenericContent.Handler.prototype.createContent = function(size) {
	// TODO(aa): In the case of a permanode that is a container (cam.permanodeUtils.isContainer()) and has a camliContentImage, it would be nice to show that image somehow along with the folder icon.
	return cam.BlobItemGenericContent({
		href: this.href_,
		size: size,
		thumbSrc: this.thumbType_ + '.png',
		thumbAspect: this.getAspectRatio(),
		title: this.searchSession_.getTitle(this.blobref_),
	});
};

cam.BlobItemGenericContent.Handler.prototype.getThumbType_ = function() {
	var m = this.searchSession_.getMeta(this.blobref_);
	var rm = this.searchSession_.getResolvedMeta(this.blobref_);

	if (rm) {
		if (rm.camliType == 'file') {
			return 'file';
		}

		if (rm.camliType == 'directory' || rm.camliType == 'static-set') {
			return 'folder';
		}
	}

	// Using the directory icon for any random permanode is a bit weird. Ideally we'd use file for that. The problem is that we can't tell the difference between a permanode that is representing an empty dynamic set and a permanode that is representing something else entirely.
	// And unfortunately, the UI has a big prominent button that says 'new set', and it looks funny if the new set is shown as a file icon :(
	if (m.camliType == 'permanode') {
		return 'folder';
	}

	return 'file';
};
