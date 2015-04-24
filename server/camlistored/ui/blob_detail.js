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

goog.provide('cam.BlobDetail');

goog.require('cam.blobref');
goog.require('cam.ServerConnection');

goog.require('goog.labs.Promise');

cam.BlobDetail = React.createClass({
	displayName: 'BlobDetail',

	BLOBREF_PATTERN_: new RegExp(cam.blobref.PATTERN, 'g'),
	propTypes: {
		getDetailURL: React.PropTypes.func.isRequired,
		meta: React.PropTypes.object.isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
	},

	getInitialState: function() {
		return {
			content: null,
			metadata: null,
			claims: null,
			refs: null,
		};
	},

	componentWillMount: function() {
		var sc = this.props.serverConnection;

		sc.getBlobContents(this.props.meta.blobRef, this.handleBlobContents_);
		sc.permanodeClaims(this.props.meta.blobRef, this.handleClaims_);

		goog.labs.Promise.all([
			new goog.labs.Promise(sc.pathsOfSignerTarget.bind(sc, this.props.meta.blobRef)),
			new goog.labs.Promise(sc.search.bind(sc, {
				permanode: {
					attr: 'camliMember',
					value: this.props.meta.blobRef,
				},
			}, null, null, null))
		]).then(this.handleRefs_);
	},

	render: function() {
		return React.DOM.div(
			{
				style: {
					fontFamily: 'Open Sans',
					margin: '1.5em 2em',
				}
			},
			this.getSection_("Blob content", this.state.content),
			this.getSection_("Indexer metadata", this.props.meta),
			this.getSection_("Mutation claims", this.state.claims),
			this.getReferencesSection_(this.state.refs)
		);
	},

	getReferencesSection_: function(refs) {
		if (!refs) {
			return this.getReferencesBlock_("Loading...");
		}

		if (refs.length <= 0) {
			return this.getReferencesBlock_("No references");
		}

		return this.getReferencesBlock_(
			React.DOM.ul(
				null,
				refs.map(function(blobref) {
					return React.DOM.li(
						{},
						React.DOM.a(
							{
								href: this.props.getDetailURL(blobref),
							},
							blobref
						)
					);
				}, this)
			)
		);
	},

	getReferencesBlock_: function(content) {
		return React.DOM.div(
			{
				key: 'References',
			},
			this.getHeader_("Referenced by"),
			content
		);
	},

	getSection_: function(title, content) {
		return React.DOM.div(
			{
				key: title
			},
			this.getHeader_(title),
			this.getCodeBlock_(content)
		);
	},

	getHeader_: function(title) {
		return React.DOM.h1(
			{
				key: 'header',
				style: {
					fontSize: '1.5em',
				}
			},
			title
		);
	},

	getCodeBlock_: function(stuff) {
		return React.DOM.pre(
			{
				key: 'code-block',
				style: {
					overflowX: 'auto',
				},
			},
			stuff ? this.linkify_(JSON.stringify(stuff, null, 2)) : "No data"
		);
	},

	linkify_: function(code) {
		var result = [];
		var match;
		var index = 0;
		while ((match = this.BLOBREF_PATTERN_.exec(code)) !== null) {
			result.push(code.substring(index, match.index));
			result.push(React.DOM.a({key: match.index, href: this.props.getDetailURL(match[0]).toString()}, match[0]));
			index = match.index + match[0].length;
		}
		result.push(code.substring(index));
		return result;
	},

	handleBlobContents_: function(data) {
		this.setState({content: JSON.parse(data)});
	},

	handleClaims_: function(data) {
		this.setState({claims: data});
	},

	handleRefs_: function(results) {
		var refs = [];
		if (results[0].paths) {
			refs = refs.concat(results[0].paths.map(function(path) {
				return path.baseRef;
			}));
		}
		if (results[1].blobs) {
			refs = refs.concat(results[1].blobs.map(function(blob) {
				return blob.blob;
			}));
		}
		this.setState({refs: refs});
	},
});

cam.BlobDetail.getAspect = function(getDetailURL, serverConnection, blobref, targetSearchSession) {
	if(!targetSearchSession) {
		return;
	}

	var m = targetSearchSession.getMeta(blobref);
	if (!m) {
		return null;
	}

	return {
		fragment: 'blob',
		title: 'Blob',
		createContent: function(size) {
			return cam.BlobDetail({
				getDetailURL: getDetailURL,
				meta: m,
				serverConnection: serverConnection,
			});
		},
	};
};
