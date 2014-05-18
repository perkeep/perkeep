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

goog.provide('cam.BlobItemVideoContent');

goog.require('goog.math.Size');

// Renders video blob items. Currently recognizes movies by looking for a filename with a common movie extension.
cam.BlobItemVideoContent = React.createClass({
	displayName: 'BlobItemVideoContent',

	MIN_PREVIEW_SIZE: 128,

	propTypes: {
		blobref: React.PropTypes.string.isRequired,
		filename: React.PropTypes.string.isRequired,
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
	},

	getInitialState: function() {
		return {
			loaded: false,
			mouseover: false,
			playing: false,
		};
	},

	render: function() {
		return React.DOM.div({
				className: React.addons.classSet({
					'cam-blobitem-video': true,
					'cam-blobitem-video-loaded': this.state.loaded,
				}),
				onMouseEnter: this.handleMouseOver_,
				onMouseLeave: this.handleMouseOut_,
			},
			React.DOM.a({href: this.props.href},
				this.getVideo_(),
				this.getPoster_()
			),
			this.getPlayPauseButton_()
		);
	},

	getPoster_: function() {
		if (this.state.loaded) {
			return null;
		}
		// TODO(aa): When server indexes videos and provides a poster image, render it here.
		return React.DOM.i({
			className: 'fa fa-video-camera',
			style: {
				fontSize: this.props.size.height / 1.5 + 'px',
				lineHeight: this.props.size.height + 'px',
				width: this.props.size.width,
			}
		})
	},

	getVideo_: function() {
		if (!this.state.loaded) {
			return null;
		}
		return React.DOM.video({
			autoPlay: true,
			src: goog.string.subs('%s%s/%s', goog.global.CAMLISTORE_CONFIG.downloadHelper, this.props.blobref, this.props.filename),
			width: this.props.size.width,
			height: this.props.size.height,
		})
	},

	getPlayPauseButton_: function() {
		if (!this.state.mouseover || this.props.size.width < this.MIN_PREVIEW_SIZE || this.props.size.height < this.MIN_PREVIEW_SIZE) {
			return null;
		}
		return React.DOM.i({
			className: React.addons.classSet({
					'fa': true,
					'fa-play': !this.state.playing,
					'fa-pause': this.state.playing,
				}),
			onClick: this.handlePlayPauseClick_,
			style: {
				fontSize: this.props.size.height / 5 + 'px',
			}
		})
	},

	handlePlayPauseClick_: function(e) {
		this.setState({
			loaded: true,
			playing: !this.state.playing,
		});

		if (this.state.loaded) {
			var video = this.getDOMNode().querySelector('video');
			if (this.state.playing) {
				video.pause();
			} else {
				video.play();
			}
		}
	},

	handleMouseOver_: function() {
		this.setState({mouseover:true});
	},

	handleMouseOut_: function() {
		this.setState({mouseover:false});
	},
});

cam.BlobItemVideoContent.isVideo = function(rm) {
	// From http://en.wikipedia.org/wiki/List_of_file_formats
	// TODO(aa): Fix this quick hack once the server indexes movies and gives us more information.
	var extensions = [
		'3gp',
		'aav',
		'asf',
		'avi',
		'dat',
		'm1v',
		'm2v',
		'm4v',
		'mov',
		'mp4',
		'mpe',
		'mpeg',
		'mpg',
		'ogg',
		'wmv',
	];
	return rm && rm.file && goog.array.some(extensions, goog.string.endsWith.bind(null, rm.file.fileName.toLowerCase()));
};

cam.BlobItemVideoContent.getHandler = function(blobref, searchSession, href) {
	var rm = searchSession.getResolvedMeta(blobref);

	// From http://en.wikipedia.org/wiki/List_of_file_formats
	// TODO(aa): Fix this quick hack once the server indexes movies and gives us more information.
	var extensions = [
		'3gp',
		'aav',
		'asf',
		'avi',
		'dat',
		'm1v',
		'm2v',
		'm4v',
		'mov',
		'mp4',
		'mpe',
		'mpeg',
		'mpg',
		'ogg',
		'wmv',
	];
	if (cam.BlobItemVideoContent.isVideo(rm)) {
		return new cam.BlobItemVideoContent.Handler(rm, href)
	}

	return null;
};

cam.BlobItemVideoContent.Handler = function(rm, href) {
	this.rm_ = rm;
	this.href_ = href;
};

cam.BlobItemVideoContent.Handler.prototype.getAspectRatio = function() {
	// TODO(aa): Provide the right value here once server indexes movies.
	return 1;
};

cam.BlobItemVideoContent.Handler.prototype.createContent = function(size) {
	return cam.BlobItemVideoContent({
		blobref: this.rm_.blobRef,
		filename: this.rm_.file.fileName,
		href: this.href_,
		size: size,
	});
};
