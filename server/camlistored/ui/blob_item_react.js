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

goog.provide('cam.BlobItemReact');
goog.provide('cam.BlobItemReactData');

goog.require('goog.array');
goog.require('goog.object');
goog.require('goog.string');
goog.require('goog.math.Coordinate');
goog.require('goog.math.Size');

goog.require('cam.imageUtil');
goog.require('cam.math');
goog.require('cam.PyramidThrobber');

// Extracts the bits of metadata that BlobItemReact needs.
cam.BlobItemReactData = function(blobref, metabag) {
	this.blobref = blobref;
	this.metabag = metabag;
	this.m = metabag[blobref];
	this.rm = this.constructor.getResolvedMeta_(this.m, metabag);
	this.im = this.constructor.getImageMeta_(this.rm);
	this.isStaticCollection = this.constructor.isStaticCollection_(this.rm);
	this.isDynamicCollection = this.constructor.isDynamicCollection_(this.m);
	this.thumbType = this.constructor.getThumbType_(this);
	this.aspect = this.constructor.getAspect_(this.im, this.thumbType);
	this.title = this.constructor.getTitle_(this.m, this.rm);
};

cam.BlobItemReactData.getTitle_ = function(m, rm) {
	if (m) {
		if (m.camliType == 'permanode' && m.permanode && m.permanode.attr && m.permanode.attr.title) {
			return goog.isArray(m.permanode.attr.title) ? m.permanode.attr.title[0] : m.permanode.attr.title;
		}
	}
	if (rm) {
		if (rm.camliType == 'file' && rm.file) {
			return rm.file.fileName;
		}
		if (rm.camliType == 'directory' && rm.dir) {
			return rm.dir.fileName;
		}
		if (rm.camliType == 'permanode' && rm.permanode && rm.permanode.attr && rm.permanode.attr.title) {
			return goog.isArray(m.permanode.attr.title) ? m.permanode.attr.title[0] : m.permanode.attr.title;
		}
	}
	return 'Unknown title';
};

cam.BlobItemReactData.getAspect_ = function(im, tt) {
	if (tt == 'image') {
		return im.width / im.height;
	}

	// These are the dimensions of the static icons we use for each of these cases.
	if (tt == 'file') {
		return 260 / 300;
	} else if (tt == 'folder') {
		return 300 / 300;
	} else if (tt == 'node') {
		return 100 / 100;
	}

	throw new Error('Unexpected thumb type: ' + tt);
};

cam.BlobItemReactData.isStaticCollection_ = function(rm) {
	return rm.camliType == 'directory' || rm.camliType == 'static-set';
};

cam.BlobItemReactData.isDynamicCollection_ = function(m) {
	if (m.camliType == 'permanode') {
		if (goog.object.findKey(m.permanode.attr, function(v, k) { return k == 'camliMember' || goog.string.startsWith(k, 'camliPath:') })) {
			return true;
		}
	}
	return false;
};

cam.BlobItemReactData.getThumbType_ = function(data) {
	if (data.im) {
		return 'image';
	}

	if (data.rm.camliType == 'file') {
		return 'file';
	}

	if (data.isStaticCollection || data.isDynamicCollection) {
		return 'folder';
	}

	return 'file';
};

cam.BlobItemReactData.getImageMeta_ = function(rm) {
	if (rm && rm.image) {
		return rm.image;
	} else {
		return null;
	}
};

cam.BlobItemReactData.getResolvedMeta_ = function(m, metabag) {
	if (m.camliType == 'permanode' && m.permanode && m.permanode.attr && m.permanode.attr.camliContent && m.permanode.attr.camliContent.length == 1) {
		return metabag[m.permanode.attr.camliContent[0]];
	} else {
		return m;
	}
};


cam.BlobItemReact = React.createClass({
	displayName: 'BlobItemReact',

	TITLE_HEIGHT: 22,

	propTypes: {
		blobref: React.PropTypes.string.isRequired,
		checked: React.PropTypes.bool.isRequired,
		href: React.PropTypes.string.isRequired,
		data: React.PropTypes.instanceOf(cam.BlobItemReactData).isRequired,
		onCheckClick: React.PropTypes.func.isRequired,  // (string,event)->void
		position: React.PropTypes.instanceOf(goog.math.Coordinate).isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		thumbnailVersion: React.PropTypes.number.isRequired,
	},

	getInitialState: function() {
		return {
			loaded: false,
			hovered: false,
		};
	},

	componentWillMount: function() {
		this.currentIntrinsicThumbHeight_ = 0;
	},

	componentDidMount: function() {
		this.refs.thumb.getDOMNode().addEventListener('load', this.onThumbLoad_);
	},

	componentDidUpdate: function(prevProps, prevState) {
		if (prevProps.blobref != this.props.blobref) {
			this.currentIntrinsicThumbHeight_ = 0;
			this.setState({loaded: false});
		}
	},

	render: function() {
		var thumbClipSize = this.getThumbClipSize_();

		return React.DOM.div({
				className: this.getRootClassName_(),
				style: this.getRootStyle_(),
				onMouseEnter: this.handleMouseEnter_,
				onMouseLeave: this.handleMouseLeave_
			},
			React.DOM.div({className:'checkmark', onClick:this.handleCheckClick_}),
			React.DOM.a({href:this.props.href},
				React.DOM.div({className:this.getThumbClipClassName_(), style:thumbClipSize},
					this.getThrobber_(thumbClipSize),
					this.getThumb_(thumbClipSize)
				),
				this.getLabel_()
			)
		);
	},

	componentWillUnmount: function() {
		this.refs.thumb.getDOMNode().removeEventListener('load', this.onThumbLoad_);
	},

	onThumbLoad_: function() {
		this.setState({loaded:true});
	},

	getRootClassName_: function() {
		return React.addons.classSet({
			'cam-blobitem': true,
			'cam-blobitem-image': Boolean(this.props.data.im),
			'goog-control-hover': this.state.hovered,
			'goog-control-checked': this.props.checked,
		});
	},

	getRootStyle_: function() {
		return {
			left: this.props.position.x,
			top: this.props.position.y,
		};
	},

	handleMouseEnter_: function() {
		this.setState({hovered:true});
	},

	handleMouseLeave_: function() {
		this.setState({hovered:false});
	},

	handleCheckClick_: function(e) {
		this.props.onCheckClick(this.props.blobref, e);
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
			ref: 'thumb',
			src: this.getThumbSrc_(thumbSize),
			style: {left:pos.x, top:pos.y, visibility:(this.state.loaded ? 'visible' : 'hidden')},
			title: this.props.data.title,
			width: thumbSize.width,
			height: thumbSize.height,
		})
	},

	getThumbSrc_: function(thumbSize) {
		var baseName = '';
		if (this.props.data.thumbType == 'image') {
			var m = this.props.data.m;
			var rm = this.props.data.rm;
			baseName = goog.string.subs('thumbnail/%s/%s.jpg', m.permanode.attr.camliContent, rm.file && rm.file.fileName ? rm.file.fileName : m.permanode.attr.camliContent);
		} else {
			baseName = this.props.data.thumbType + '.png';
		}

		this.currentIntrinsicThumbHeight_ = cam.imageUtil.getSizeToRequest(thumbSize.height, this.currentIntrinsicThumbHeight_);
		return goog.string.subs('%s?mh=%s&tv=%s', baseName, this.currentIntrinsicThumbHeight_, this.props.thumbnailVersion);
	},

	getLabel_: function() {
		// We don't show the label at all for images.
		if (this.props.data.im) {
			return null;
		}
		return React.DOM.span({className:'cam-blobitem-thumbtitle', style:{width:this.props.size.width}},
			this.props.data.title);
	},

	getThumbSize_: function(thumbClipSize) {
		return cam.math.scaleToFit(new goog.math.Size(this.props.data.aspect, 1), thumbClipSize, Boolean(this.props.data.im));
	},

	getThumbClipSize_: function() {
		var h = this.props.size.height;
		if (!this.props.data.im) {
			h -= this.TITLE_HEIGHT;
		}
		return new goog.math.Size(this.props.size.width, h);
	},
});
