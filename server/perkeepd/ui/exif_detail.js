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

goog.provide('cam.EXIFDetail');

goog.require('cam.ServerConnection');

// Full EXIF stays out of search descriptions. Fetch it only when this aspect
// mounts, and discard responses for files that are no longer being viewed.
cam.EXIFDetail = React.createClass({
	displayName: 'EXIFDetail',

	propTypes: {
		meta: React.PropTypes.object.isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		getCameraSearchURL: React.PropTypes.func.isRequired,
		getSearchURL: React.PropTypes.func.isRequired,
	},

	getInitialState: function() {
		return {data: null, error: false};
	},

	componentWillMount: function() {
		this.requestID_ = 0;
	},

	componentDidMount: function() {
		this.load_(this.props.meta.blobRef);
	},

	componentWillReceiveProps: function(nextProps) {
		if (nextProps.meta.blobRef != this.props.meta.blobRef) {
			this.load_(nextProps.meta.blobRef);
		}
	},

	componentWillUnmount: function() {
		++this.requestID_;
	},

	load_: function(fileRef) {
		var requestID = ++this.requestID_;
		this.setState({data: null, error: false});
		this.props.serverConnection.getEXIF(fileRef, function(data) {
			if (requestID == this.requestID_) {
				this.setState({data: data, error: false});
			}
		}.bind(this), function() {
			if (requestID == this.requestID_) {
				this.setState({data: null, error: true});
			}
		}.bind(this));
	},

	render: function() {
		var data = this.state.data;
		var tags = data ? data.tags : [];
		return React.DOM.div({className: 'cam-exif-detail'},
			React.DOM.header({className: 'cam-exif-header'},
				React.DOM.div(null,
					React.DOM.h1(null, 'EXIF metadata'),
					React.DOM.p({className: 'cam-exif-file'}, this.props.meta.file.fileName)
				),
				React.DOM.span({className: 'cam-exif-readonly'}, 'Read-only')
			),
			this.renderMediaTags_(),
			this.state.error ? this.renderError_() : !data ?
				React.DOM.p({className: 'cam-exif-status', role: 'status'}, 'Loading EXIF metadata…') :
				tags.length ? this.renderTags_(tags) : this.renderEmpty_()
		);
	},

	// Media tags are indexed from embedded metadata other than EXIF tags,
	// such as ID3 tags, Apple MakerNotes, and QuickTime metadata. They're
	// part of the file's description, so they don't need loading.
	renderMediaTags_: function() {
		var tags = this.props.meta.mediaTags || {};
		var names = Object.keys(tags).sort();
		var lp = this.props.meta.livePhoto;
		if (!names.length && !lp) {
			return null;
		}
		var labels = {
			burstuuid: 'Burst UUID',
			contentidentifier: 'Content identifier',
			creationdate: 'Creation date',
			durationms: 'Duration (ms)',
			livephotoauto: 'Live Photo auto',
			livephotovitalityscore: 'Live Photo vitality score',
			mediaref: 'Media ref',
			musicbrainzalbumid: 'MusicBrainz album ID',
			photoidentifier: 'Photo identifier',
		};
		return React.DOM.section({className: 'cam-exif-group'},
			React.DOM.h2(null, 'Media metadata', React.DOM.span(null, names.length + (names.length == 1 ? ' tag' : ' tags'))),
			lp ? React.DOM.p({className: 'cam-exif-note'},
				'This is the ' + (lp.video ? 'still image' : 'video') + ' of a Live Photo. ',
				React.DOM.a({href: this.props.getSearchURL('livephoto:' + lp.contentIdentifier)}, 'Show its still image and video')
			) : null,
			React.DOM.table(null,
				React.DOM.thead(null, React.DOM.tr(null,
					React.DOM.th({scope: 'col'}, 'Tag'),
					React.DOM.th({scope: 'col'}, 'Value')
				)),
				React.DOM.tbody(null, names.map(function(name) {
					var label = labels[name] || name.charAt(0).toUpperCase() + name.slice(1);
					return React.DOM.tr({key: name},
						React.DOM.th({scope: 'row'}, label, React.DOM.code(null, name)),
						React.DOM.td(null, React.DOM.span({className: 'cam-exif-value'}, tags[name]))
					);
				}))
			)
		);
	},

	renderError_: function() {
		return React.DOM.div({className: 'cam-exif-status', role: 'alert'},
			React.DOM.h2(null, 'Could not load EXIF metadata'),
			React.DOM.p(null, 'The indexed metadata could not be retrieved.'),
			React.DOM.button({type: 'button', onClick: function() {
				this.load_(this.props.meta.blobRef);
			}.bind(this)}, 'Try again')
		);
	},

	renderEmpty_: function() {
		return React.DOM.div({className: 'cam-exif-empty', role: 'status'},
			React.DOM.i({className: 'fa fa-camera', 'aria-hidden': true}),
			React.DOM.h2(null, 'No indexed EXIF metadata'),
			React.DOM.p(null, 'No EXIF tags are recorded in the index for this file.'),
			React.DOM.p({className: 'cam-exif-note'}, 'Not all images contain EXIF data. The original file is unchanged.')
		);
	},

	renderTags_: function(tags) {
		var groups = [];
		tags.forEach(function(tag) {
			var group = groups.filter(function(g) { return g.name == tag.directory; })[0];
			if (!group) {
				group = {name: tag.directory, tags: []};
				groups.push(group);
			}
			group.tags.push(tag);
		});
		return React.DOM.div(null,
			this.renderCamera_(tags),
			React.DOM.p({className: 'cam-exif-note'}, tags.length + (tags.length == 1 ? ' indexed tag' : ' indexed tags') + ' · Grouped by EXIF directory. Values come from the file, not permanode attributes.'),
			groups.map(this.renderGroup_)
		);
	},

	renderCamera_: function(tags) {
		var camera = tags.filter(function(tag) {
			return tag.directory == 'IFD0' && tag.type == 'string' && (tag.tag == '010f' || tag.tag == '0110') && this.getCameraValue_(tag);
		}, this);
		if (!camera.length) {
			return null;
		}
		return React.DOM.div({className: 'cam-exif-camera'},
			React.DOM.i({className: 'fa fa-camera', 'aria-hidden': true}),
			React.DOM.div(null,
				React.DOM.div({className: 'cam-exif-camera-label'}, 'Camera'),
				React.DOM.div({className: 'cam-exif-camera-values'}, camera.map(function(tag) {
					var value = this.getCameraValue_(tag);
					return React.DOM.a({key: tag.tag, href: this.props.getCameraSearchURL(value), title: 'Search for photos matching this camera ' + (tag.tag == '010f' ? 'make' : 'model')}, value);
				}, this)),
				React.DOM.p(null, 'Select a make or model to find matching photos.')
			)
		);
	},

	getCameraValue_: function(tag) {
		// Match the corpus's camera padding normalization for search links.
		return (tag.values[0] || '').replace(/^[\s\u0000]+|[\s\u0000]+$/g, '');
	},

	renderGroup_: function(group) {
		var titles = {IFD0: 'Image', IFD1: 'Thumbnail', Exif: 'Camera settings', GPS: 'Location'};
		return React.DOM.section({className: 'cam-exif-group', key: group.name},
			React.DOM.h2(null, titles[group.name] || 'EXIF directory', React.DOM.code(null, group.name), React.DOM.span(null, group.tags.length + (group.tags.length == 1 ? ' tag' : ' tags'))),
			React.DOM.table(null,
				React.DOM.thead(null, React.DOM.tr(null,
					React.DOM.th({scope: 'col'}, 'Tag'),
					React.DOM.th({scope: 'col'}, 'Value'),
					React.DOM.th({scope: 'col'}, 'Type')
				)),
				React.DOM.tbody(null, group.tags.map(function(tag) {
					var name = (tag.name || 'Unknown tag').replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2').replace(/([a-z0-9])([A-Z])/g, '$1 $2');
					return React.DOM.tr({key: tag.tag},
						React.DOM.th({scope: 'row'}, name, React.DOM.code(null, '0x' + tag.tag)),
						React.DOM.td(null, this.renderValue_(tag)),
						React.DOM.td({className: 'cam-exif-type'}, tag.type)
					);
				}, this))
			)
		);
	},

	renderValue_: function(tag) {
		var value = tag.values.join(', ');
		if (value.length > 200) {
			return React.DOM.details(null,
				React.DOM.summary(null, value.substring(0, 120) + '… ', React.DOM.span(null, 'Show full value')),
				React.DOM.pre(null, value)
			);
		}
		return React.DOM.span({className: 'cam-exif-value'}, value);
	},
});

// Quoting protects expression syntax, but camera: still interprets * and a
// leading ~ inside quotes. Use an exact structured constraint for such names
// (for example, PENTAX *ist D) instead of changing their meaning or failing.
cam.EXIFDetail.getCameraQuery = function(value) {
	if (value.indexOf('*') == -1 && value.charAt(0) != '~') {
		return 'camera:' + JSON.stringify(value);
	}
	return 'raw:' + JSON.stringify({
		permanode: {
			attr: 'camliContent',
			skipHidden: true,
			valueInSet: {file: {camera: {equals: value, caseInsensitive: true}}},
		},
	});
};

cam.EXIFDetail.getAspect = function(serverConnection, getCameraSearchURL, getSearchURL, blobref, searchSession) {
	if (!blobref) {
		return null;
	}
	var meta = searchSession.getResolvedMeta(blobref);
	if (!meta || meta.camliType != 'file' || !meta.file) {
		return null;
	}
	return {
		fragment: 'exif',
		title: 'EXIF',
		createContent: function() {
			return React.createElement(cam.EXIFDetail, {
				key: 'exif',
				meta: meta,
				serverConnection: serverConnection,
				getCameraSearchURL: getCameraSearchURL,
				getSearchURL: getSearchURL,
			});
		},
	};
};
