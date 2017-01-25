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

goog.provide('cam.DirectoryDetail');

goog.require('cam.ServerConnection');

goog.require('goog.labs.Promise');


cam.DirectoryDetail = React.createClass({
	displayName: 'DirectoryDetail',

	INDENT_STEP_: 20,

	propTypes: {
		isRoot: React.PropTypes.bool.isRequired,
		depth: React.PropTypes.number.isRequired,
		meta: React.PropTypes.object.isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
	},

	getDefaultProps: function() {
		return {
			isRoot: true,
			depth: 1,
		};
	},

	getInitialState: function() {
		return {
			isFolded: this.props.isRoot ? false : true,
			fileTree: null,
		};
	},

	componentWillMount: function() {
		this.props.serverConnection.getFileTree(this.props.meta.blobRef, this.handleFileTree_);
	},

	render: function() {
		return React.DOM.div(
			{},
			this.getDirLine_(this.props.meta),
			this.getChildrenArray_(this.state.fileTree)
		);
	},

	handleFileTree_: function(data) {
		this.setState({fileTree: data});
	},

	getDirLine_: function(node) {
		if (this.props.isRoot) {
			return null;
		}
		return React.DOM.div(
			{
				key: node.blobRef,
			},
			this.getFolderSign_(this.props.depth*this.INDENT_STEP_, node),
			this.getLink_(node),
			this.getNewPerm_(node)
		);
	},

	getChildrenArray_: function(fileTree) {
		if (this.state.isFolded) {
			return [];
		}
		if (!fileTree || !fileTree.children) {
			return [];
		}
		var children = fileTree.children;
		var childArr = [];
		for (var i = 0; i < children.length; i++) {
			childArr.push(this.getChild_(children[i]));
		}
		return childArr;
	},

	getChild_: function(meta) {
		var depth = this.props.depth+1;
		if (meta.type == 'directory') {
			return React.createElement(cam.DirectoryDetail, {
				isRoot: false,
				depth: depth,
				meta: meta,
				serverConnection: this.props.serverConnection,
			})
		}
		return React.DOM.div(
			{
				key: meta.blobRef,
			},
			this.getFolderSign_(depth*this.INDENT_STEP_, meta),
			this.getLink_(meta),
			this.getNewPerm_(meta)
		);
	},

	getFolderSign_: function(indent, node) {
		return React.DOM.span(
			{
				style: {
					cursor: 'pointer',
					color: 'darkgreen',
					'margin-left': '.4em',
					'font-size': '80%',
					paddingLeft: indent + "px",
				},
				key: 'folder-sign',
				onClick: this.toggleFolding_
			},
			node.type == 'directory' ? "+ " : "  "
		);
	},

	toggleFolding_: function(e) {
		this.setState({isFolded: !this.state.isFolded})
	},

	linkTextContent_: function(node) {
		switch (node.type) {
		case 'directory':
		case 'symlink':
		case 'file':
			return node.name;
		default:
			return node.blobRef;
		}
	},

	getLink_: function(childNode) {
		return React.DOM.a(
			{
				key: 'Content link',
				href: "./" + childNode.blobRef,
				id: childNode.blobRef
			},
			this.linkTextContent_(childNode)
		);
	},

	getNewPerm_: function(childNode) {
		return React.DOM.span(
			{
				style: {
					'text-decoration': 'underline',
					cursor: 'pointer',
					color: 'darkgreen',
					'margin-left': '.4em',
					'font-size': '80%',
				},
				key: 'permanode-creation',
				onClick: goog.bind(this.newPermanodeWithContent_, this, childNode.blobRef)
			},
			"P"
		);
	},

	newPermanodeWithContent_: function(contentRef, e) {
		var sc = this.props.serverConnection;
		var setContent = function(results) {
			var permanodeRef = results[0];
			return goog.labs.Promise.all([new goog.labs.Promise(sc.newAddAttributeClaim.bind(sc, permanodeRef, 'camliContent', contentRef))])
		};
		goog.labs.Promise.all([
			new goog.labs.Promise(sc.createPermanode.bind(sc))
		])
		.then(setContent)
		.thenCatch(function(err) {
			console.error('error creating permanode for content %s: %s', contentRef, err);
		})
		.then(function() {
			alert("permanode created for " + contentRef);
		});
	}

});

cam.DirectoryDetail.getAspect = function(baseURL, serverConnection, blobref, targetSearchSession) {
	if (!targetSearchSession) {
		return;
	}

	var rm = targetSearchSession.getResolvedMeta(blobref);
	if (!rm || rm.camliType != 'directory') {
		return null;
	}

	return {
		fragment: 'directory',
		title: 'Directory',
		createContent: function(size) {
			return React.createElement(cam.DirectoryDetail, {
				isRoot: true,
				depth: 1,
				meta: rm,
				serverConnection: serverConnection,
			});
		},
	};
};
