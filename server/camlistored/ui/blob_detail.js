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

cam.BlobDetail = React.createClass({
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
		};
	},

	componentWillMount: function() {
		this.props.serverConnection.getBlobContents(this.props.meta.blobRef, this.handleBlobContents_);
		this.props.serverConnection.permanodeClaims(this.props.meta.blobRef, this.handleClaims_);
	},

	render: function() {
		var children = [
			this.getHeader_("Blob content"),
			this.getCodeBlock_(this.state.content),
			this.getHeader_("Indexer metadata"),
			this.getCodeBlock_(this.props.meta),
		];

		// TODO(aa): This should really move to permanode detail.
		if (this.state.claims) {
			children.push(this.getHeader_("Mutation claims"));
			children.push(this.getCodeBlock_(this.state.claims));
		}

		return React.DOM.div(
			{
				style: {
					fontFamily: 'Open Sans',
					margin: '1.5em 2em',
				}
			},
			children);
	},

	getHeader_: function(title) {
		return React.DOM.h1(
			{
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
				style: {
					overflowX: 'auto',
				},
			},
			stuff ? this.linkify_(JSON.stringify(stuff, null, 2)) : null
		);
	},

	linkify_: function(code) {
		var result = [];
		var match;
		var index = 0;
		while ((match = this.BLOBREF_PATTERN_.exec(code)) !== null) {
			result.push(code.substring(index, match.index));
			result.push(React.DOM.a({href: this.props.getDetailURL(match[0]).toString()}, match[0]));
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
