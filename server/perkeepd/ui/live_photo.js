/*
Copyright 2026 The Perkeep Authors

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

goog.provide('cam.LivePhoto');

goog.require('goog.string');

// Apple Live Photos are a still image and a short video. See
// cam.SearchSession.prototype.getLivePhotoVideoMeta.

// Returns the URL of the video file described by videoMeta.
cam.LivePhoto.getVideoSrc = function(videoMeta) {
	return goog.string.subs('%s%s/%s', goog.global.CAMLISTORE_CONFIG.downloadHelper, videoMeta.blobRef, encodeURIComponent(videoMeta.file.fileName));
};

// Overlays a Live Photo badge on its still image, filling the nearest
// positioned ancestor. Hovering over the badge plays the video over the
// image.
cam.LivePhoto.Overlay = React.createClass({
	displayName: 'LivePhotoOverlay',

	propTypes: {
		// How the video fits the image area, as the CSS object-fit
		// property: 'cover' for cropped thumbnails, 'contain' otherwise.
		fit: React.PropTypes.oneOf(['cover', 'contain']),
		videoSrc: React.PropTypes.string.isRequired,
	},

	getInitialState: function() {
		return {
			hover: false,
			playing: false,
		};
	},

	render: function() {
		return React.DOM.div({className: 'cam-livephoto'},
			this.getVideo_(),
			React.DOM.span({
					className: classNames({
						'cam-livephoto-badge': true,
						'cam-livephoto-badge-active': this.state.playing,
					}),
					onMouseEnter: this.handleMouseEnter_,
					onMouseLeave: this.handleMouseLeave_,
					title: 'Live Photo',
				},
				React.DOM.svg({viewBox: '0 0 24 24', 'aria-hidden': 'true', focusable: 'false'},
					React.DOM.circle({cx: 12, cy: 12, r: 3, fill: 'currentColor'}),
					React.DOM.circle({cx: 12, cy: 12, r: 6.5, fill: 'none', stroke: 'currentColor', strokeWidth: 1.6}),
					React.DOM.circle({cx: 12, cy: 12, r: 10, fill: 'none', stroke: 'currentColor', strokeWidth: 1.6, strokeDasharray: '1.3 1.9'})
				)
			)
		);
	},

	getVideo_: function() {
		if (!this.state.hover) {
			return null;
		}
		return React.DOM.video({
			className: classNames({
				'cam-livephoto-video': true,
				'cam-livephoto-video-playing': this.state.playing,
			}),
			autoPlay: true,
			loop: true,
			muted: true,
			playsInline: true,
			onPlaying: this.handlePlaying_,
			ref: this.setVideoNode_,
			src: this.props.videoSrc,
			style: {objectFit: this.props.fit || 'cover'},
		});
	},

	setVideoNode_: function(node) {
		if (!node) {
			return;
		}
		// Browsers only autoplay muted videos, and React doesn't
		// reliably set the muted attribute.
		node.muted = true;
		var p = node.play();
		if (p && p.catch) {
			p.catch(function() {}); // e.g. no HEVC support
		}
	},

	handleMouseEnter_: function() {
		this.setState({hover: true});
	},

	handleMouseLeave_: function() {
		this.setState({hover: false, playing: false});
	},

	handlePlaying_: function() {
		this.setState({playing: true});
	},
});
