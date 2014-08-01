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

goog.provide('cam.BlobItemImageContent');

goog.require('goog.math.Size');

goog.require('cam.math');
goog.require('cam.permanodeUtils');
goog.require('cam.PyramidThrobber');
goog.require('cam.Thumber');

// Renders image blob items. Handles the following cases:
// a) camliType == 'file', and also has an 'image' property.
// b) permanode with camliContent pointing to (a)
// c) permanode with 'camliImageContent' attribute pointing to (a)
cam.BlobItemImageContent = React.createClass({
	displayName: 'BlobItemImageContent',

	propTypes: {
		aspect: React.PropTypes.number.isRequired,
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		src: React.PropTypes.string.isRequired,
		title: React.PropTypes.string,
	},

	getInitialState: function() {
		return {
			loaded: false,
		};
	},

	componentWillMount: function() {
		this.currentIntrinsicThumbHeight_ = 0;
	},

	componentDidUpdate: function(prevProps, prevState) {
		// TODO(aa): It seems like we would not need this if we always use this component with the 'key' prop.
		if (prevProps.blobref != this.props.blobref) {
			this.currentIntrinsicThumbHeight_ = 0;
			this.setState({loaded: false});
		}
	},

	render: function() {
		var thumbClipSize = new goog.math.Size(this.props.size.width, this.props.size.height);
		return React.DOM.a({href:this.props.href},
			React.DOM.div({className:this.getThumbClipClassName_(), style:thumbClipSize},
				this.getThrobber_(thumbClipSize),
				this.getThumb_(thumbClipSize)
			)
		);
	},

	onThumbLoad_: function() {
		this.setState({loaded:true});
	},

	getThumbClipClassName_: function() {
		return React.addons.classSet({
			'cam-blobitem-thumbclip': true,
			'cam-blobitem-loading': !this.state.loaded,
		});
	},

	getThrobber_: function(thumbClipSize) {
		if (this.state.loaded) {
			return null;
		}
		return cam.PyramidThrobber({pos:cam.math.center(cam.PyramidThrobber.SIZE, thumbClipSize)});
	},

	getThumb_: function(thumbClipSize) {
		var thumbSize = this.getThumbSize_(thumbClipSize);
		var pos = cam.math.center(thumbSize, thumbClipSize);
		return React.DOM.img({
			className: 'cam-blobitem-thumb',
			onLoad: this.onThumbLoad_,
			src: this.props.src,
			style: {left:pos.x, top:pos.y, visibility:(this.state.loaded ? 'visible' : 'hidden')},
			title: this.props.title,
			width: thumbSize.width,
			height: thumbSize.height,
		})
	},

	getThumbSize_: function(thumbClipSize) {
		var bleed = true;
		return cam.math.scaleToFit(new goog.math.Size(this.props.aspect, 1), thumbClipSize, bleed);
	},
});

cam.BlobItemImageContent.getHandler = function(blobref, searchSession, href) {
	var rm = searchSession.getResolvedMeta(blobref);
	if (rm && rm.image) {
		return new cam.BlobItemImageContent.Handler(rm, href, searchSession.getTitle(blobref));
	}

	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode') {
		return null;
	}

	// Sets can have the camliContentImage attr to indicate a user-chosen "cover image" for the entire set. Until we have some rendering for those, the folder in the generic handler is a better fit than the single image.
	if (cam.permanodeUtils.isContainer(m.permanode)) {
		return null;
	}

	var cci = cam.permanodeUtils.getSingleAttr(m.permanode, 'camliContentImage');
	if (cci) {
		var ccim = searchSession.getResolvedMeta(cci);
		if (ccim) {
			return new cam.BlobItemImageContent.Handler(ccim, href, searchSession.getTitle(blobref));
		}
	}

	return null;
};

cam.BlobItemImageContent.Handler = function(imageMeta, href, title) {
	this.imageMeta_ = imageMeta;
	this.href_ = href;
	this.title_ = title;
	this.thumber_ = cam.Thumber.fromImageMeta(imageMeta);
};

cam.BlobItemImageContent.Handler.prototype.getAspectRatio = function() {
	return this.imageMeta_.image.width / this.imageMeta_.image.height;
};

cam.BlobItemImageContent.Handler.prototype.createContent = function(size) {
	return cam.BlobItemImageContent({
		aspect: this.getAspectRatio(),
		href: this.href_,
		size: size,
		src: this.thumber_.getSrc(size.height),
		title: this.title_,
	});
};
