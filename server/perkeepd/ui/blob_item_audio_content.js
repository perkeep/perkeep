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
goog.require('cam.BlobItemGenericContent');

// Renders audio blob items.
cam.BlobItemAudioContent = React.createClass({
	displayName: 'BlobItemAudioContent',

	audioRef_: null,

	propTypes: {
		blobref: React.PropTypes.string.isRequired,
		filename: React.PropTypes.string.isRequired,
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		mediaTags: React.PropTypes.object,
	},

	getInitialState: function() {
		return {
			playing: false,
		};
	},

	render: function() {
		var titleHeight = 22;
		// Leave room for a preview as well as both lines on small tiles.
		var trackInfo = this.props.size.height >= 3 * titleHeight ? this.getTrackInfo_() : null;
		var contentSize = new goog.math.Size(this.props.size.width, Math.max(titleHeight, this.props.size.height - (trackInfo ? titleHeight : 0)));
		return React.DOM.div({
				className: 'cam-blobitem-audio',
				style: {width: this.props.size.width, height: this.props.size.height},
			},
			this.getAudio_(),
			React.createElement(cam.BlobItemGenericContent, {
				href: this.props.href,
				size: contentSize,
				thumbFAIcon: 'volume-up',
				// TODO(oac): When server indexes audio and provides a poster image, add it here
				// thumbSrc: ,
				// thumbAspect: ,
				title: this.getTitle_(),
			}),
			trackInfo,
			this.getPlayPauseButton_(Math.max(0, contentSize.height - titleHeight)),
		);
	},

	getAudio_: function() {
		return React.DOM.audio({
			src: goog.string.subs('%s%s/%s', goog.global.CAMLISTORE_CONFIG.downloadHelper, this.props.blobref, this.props.filename),
			controls: true,
			ref: this.setAudioRef_,
			onPlay: this.handlePlay_,
			onPause: this.handlePause_,
			onEnded: this.handlePause_,
			onError: this.handlePause_,
			onEmptied: this.handlePause_,
			style: {
				display: 'none',
			},
		});
	},

	getTitle_: function() {
		var mediaTags = this.props.mediaTags;
		return (mediaTags && mediaTags.title) || this.props.filename || 'Audio File';
	},

	getTrackInfo_: function() {
		var mediaTags = this.props.mediaTags;
		if (mediaTags && (mediaTags.artist || mediaTags.album)) {
			return React.DOM.span({
				className:'cam-blobitem-thumbtitle cam-blobitem-audio-trackinfo',
				style: { width: this.props.size.width }
			}, [mediaTags.artist, mediaTags.album].filter(function(value) { return !!value; }).join(' / '));
		}
		return null;
	},

	getPlayPauseButton_: function(previewHeight) {
		return (
			React.DOM.button({
				className: classNames({
					'cam-unstyled-button': true,
					'cam-blobitem-audio-play': !this.state.playing,
					'cam-blobitem-audio-pause': this.state.playing,
				}),
				onClick: this.handlePlayPauseClick_,
				'aria-label': this.state.playing ? 'Pause audio' : 'Play audio',
				style: {top: previewHeight / 2},
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
		if (!this.audioRef_) {
			return;
		}
		if (!this.audioRef_.paused) {
			this.audioRef_.pause();
		} else {
			// The play event is authoritative: play() may reject, e.g. if
			// the browser cannot decode the file or blocks playback.
			var result = this.audioRef_.play();
			if (result && result.catch) {
				result.catch(function() {});
			}
		}
	},

	handlePlay_: function() {
		this.setState({playing: true});
	},

	handlePause_: function() {
		this.setState({playing: false});
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
		mediaTags: this.rm_.mediaTags
	});
};
