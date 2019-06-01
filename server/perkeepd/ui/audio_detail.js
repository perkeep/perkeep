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

goog.provide('cam.AudioDetail');

goog.require('cam.BlobItemAudioContent');

cam.AudioDetail = React.createClass({
	displayName: 'AudioDetail',

	propTypes: {
		permanodeMeta: React.PropTypes.object,
		resolvedMeta: React.PropTypes.object.isRequired,
	},

	render: function() {
		return React.DOM.div({
				className: 'cam-detail-audio',
			},
			this.getControls_(),
		);
	},

	getControls_: function() {
		var mediaTags = this.props.resolvedMeta.mediaTags;
		return React.DOM.div({
				className: 'cam-detail-audio-controls',
			},
			React.DOM.div({
					className: 'cam-detail-audio-controls-meta'
				},
				mediaTags.title != null && React.DOM.div({
					className: 'cam-detail-audio-controls-meta-title',
				}, mediaTags.title),

				mediaTags.artist != null && React.DOM.div({
					className: 'cam-detail-audio-controls-meta-artist',
				}, mediaTags.artist),
			),

			React.DOM.div({
					className: 'cam-detail-audio-controls-player',
				},
				React.DOM.audio({
					controls: true,
					key: this.props.resolvedMeta.blobRef,
					src: goog.string.subs('%s%s/%s', goog.global.CAMLISTORE_CONFIG.downloadHelper, this.props.resolvedMeta.blobRef, this.props.resolvedMeta.file.fileName),
				}),
			),
		);
	},
});

cam.AudioDetail.getAspect = function(blobref, searchSession) {
	if (!blobref) {
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

	if (rm && cam.BlobItemAudioContent.isAudio(rm)) {
		return {
			fragment: 'audio',
			title: 'Audio',
			createContent: function(size, backwardPiggy) {
				return React.createElement(cam.AudioDetail, {
					key: 'audio',
					permanodeMeta: pm,
					resolvedMeta: rm,
				});
			},
		};
	} else {
		return null;
	}
};
