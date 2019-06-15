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

goog.provide('cam.BlobItemAudioContent');

goog.require('goog.math.Size');

// Renders audio blob items.
cam.BlobItemAudioContent = React.createClass({
	displayName: 'BlobItemAudioContent',

	audioRef_: null,

	propTypes: {
		blobref: React.PropTypes.string.isRequired,
		filename: React.PropTypes.string.isRequired,
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
	},

	getInitialState: function() {
		return {
			mouseover: false,
			playing: false,
		};
	},

	render: function() {
		return React.DOM.div({
				className: 'cam-blobitem-audio',
				onMouseEnter: this.handleMouseOver_,
				onMouseLeave: this.handleMouseOut_,
			},
			React.DOM.a({
					className: 'cam-unstyled-button',
					href: this.props.href
				},
				this.getAudio_(),
				this.getPoster_()
			),
			this.getPlayPauseButton_(),
		);
	},

	getPoster_: function() {
		// TODO(oac): When server indexes audio and provides a poster image, render it here.
		return React.DOM.i({
			className: 'fa fa-volume-up',
			style: {
				fontSize: this.props.size.height / 1.5 + 'px',
				lineHeight: this.props.size.height + 'px',
				width: this.props.size.width,
			},
		});
	},

	getAudio_: function() {
		return React.DOM.audio({
			src: goog.string.subs('%s%s/%s', goog.global.CAMLISTORE_CONFIG.downloadHelper, this.props.blobref, this.props.filename),
			controls: true,
			ref: this.setAudioRef_,
			style: {
				display: 'none',
			},
		});
	},

	getPlayPauseButton_: function() {
		if (!this.state.mouseover) {
			return null;
		}
		return (
			React.DOM.button({
				className: classNames({
					'cam-unstyled-button': true,
					'cam-blobitem-audio-play': !this.state.playing,
					'cam-blobitem-audio-pause': this.state.playing,
				}),
				onClick: this.handlePlayPauseClick_,
			},
				React.DOM.i({
					className: classNames({
						'fa': true,
						'fa-play': !this.state.playing,
						'fa-pause': this.state.playing,
					}),
					style: {
						fontSize: this.props.size.height / 5 + 'px',
					},
				}),
			)
		);
	},

	setAudioRef_: function(audio) {
		this.audioRef_ = audio;
	},

	handlePlayPauseClick_: function(e) {
		if (this.state.playing) {
			this.audioRef_.pause();
			this.setState({
				playing: false,
			});
		} else {
			this.audioRef_.play();
			this.setState({
				playing: true,
			});
		}
	},

	handleMouseOver_: function() {
		this.setState({
			mouseover: true,
		});
	},

	handleMouseOut_: function() {
		this.setState({
			mouseover: false,
		});
	},
});

cam.BlobItemAudioContent.isAudio = function(rm) {
	// From https://developer.mozilla.org/en-US/docs/Web/HTML/Supported_media_formats
	var extensions = [
		'webm',
		'ogg',
		'mp3',
		'wav',
		'flac',
	];
	return rm && rm.file && goog.array.some(extensions, goog.string.endsWith.bind(null, rm.file.fileName.toLowerCase()));
};

cam.BlobItemAudioContent.getHandler = function(blobref, searchSession, href) {
	var rm = searchSession.getResolvedMeta(blobref);

	if (cam.BlobItemAudioContent.isAudio(rm)) {
		return new cam.BlobItemAudioContent.Handler(rm, href)
	}

	return null;
};

cam.BlobItemAudioContent.Handler = function(rm, href) {
	this.rm_ = rm;
	this.href_ = href;
};

cam.BlobItemAudioContent.Handler.prototype.getAspectRatio = function() {
	// TODO(oac): Provide the right value here once server indexes audio.
	return 1;
};

cam.BlobItemAudioContent.Handler.prototype.createContent = function(size) {
	return React.createElement(cam.BlobItemAudioContent, {
		blobref: this.rm_.blobRef,
		filename: this.rm_.file.fileName,
		href: this.href_,
		size: size,
	});
};
