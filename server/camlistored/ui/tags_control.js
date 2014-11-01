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

// ENHANCEMENTS:
// should the control have a hot-key to launch?
// should the control be draggable within the window? Is there a better strategy for not hiding permanodes you want to select (@see: http://jsfiddle.net/Af9Jt/2/)
// discuss: create type-ahead list of existing tags for add tag input (this will require mods to the supporting service - likely creation of attribute index?)

goog.provide('cam.TagsControl');

goog.require('goog.array');
goog.require('goog.labs.Promise');
goog.require('goog.object');
goog.require('goog.Uri');

goog.require('cam.permanodeUtils');
goog.require('cam.reactUtil');
goog.require('cam.ServerConnection');

cam.TagsControl = React.createClass({
	displayName: 'TagsControl',

	propTypes: {
		selectedItems: React.PropTypes.object.isRequired,
		searchSession: React.PropTypes.shape({getMeta:React.PropTypes.func.isRequired}),
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
	},

	doesBlobHaveTag: function(blobref, tag) {
		var blobmeta = this.props.searchSession.getMeta(blobref);

		if (blobmeta && blobmeta.camliType == 'permanode') {
			var tags = blobmeta.permanode.attr.tag;

			if (tags) {
				return goog.array.contains(tags, tag);
			}
		}

		return false;
	},

	executePromises: function(componentId, promises, callbackSuccess) {
		goog.labs.Promise.all(promises).thenCatch(function(e) {
			console.error('%s: error executing promises: %s', componentId, e);
			alert('The system encountered an error updating tags: ' + e);
		}).then(function(results) {
			if (results) {
				console.log('%s: successfully completed %d of %d promises', componentId, results.length, promises.length);

				if (callbackSuccess) {
					callbackSuccess();
				}
			} else {
				// TODO: I'm not sure this is ever reached, but keep for now and monitor
				console.error('%s: results object is empty', componentId);
			}
		}).then(function() {
			console.log('%s: operation complete', componentId);
		});
	},

	render: function() {
		var props = this.props;
		var blobrefs = goog.object.getKeys(props.selectedItems);
		var blobs = blobrefs.map(function(blobref) {
		  return props.searchSession.getMeta(blobref);
		});

		return React.DOM.div(
			{
				className: 'cam-tagscontrol-main'
			},
			React.DOM.div(
				{
					className: 'cam-tagscontrol-header'
				}
			),
			cam.AddTagsInput(
				{
					blobrefs: blobrefs,
					serverConnection: this.props.serverConnection,
					doesBlobHaveTag: this.doesBlobHaveTag,
					executePromises: this.executePromises
				}
			),
			cam.EditTagsControl(
				{
					blobs: blobs,
					blobrefs: blobrefs,
					serverConnection: this.props.serverConnection,
					doesBlobHaveTag: this.doesBlobHaveTag,
					executePromises: this.executePromises
				}
			)
		);
	}
});

cam.AddTagsInput = React.createClass({
	displayName: 'AddTagsInput',

	PLACEHOLDER: 'Add tag(s) [val1,val2,...]',

	propTypes: {
		blobrefs: React.PropTypes.array.isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		doesBlobHaveTag: React.PropTypes.func.isRequired,
		executePromises: React.PropTypes.func.isRequired
	},

	getInitialState: function() {
		return {
			inputValue: null,
			statusMessage: null
		};
	},

	componentDidMount: function() {
		this.getInputNode().focus();
	},

	getInputNode: function() {
		return this.refs['inputField'].getDOMNode();
	},

	handleOnSubmit_: function(e) {
		e.preventDefault();

		var inputVal = this.getInputNode().value;
		if (goog.string.isEmpty(inputVal)) {
			this.setState({statusMessage: 'Please provide at least one tag value'});
		} else {
			var tags = inputVal.split(',').map(function(s) { return s.trim(); });
			if (tags.some(function(t) { return !t })) {
				this.setState({statusMessage: 'At least one invalid value was supplied'});
			} else {
				this.executeAddTags_(tags);
			}
		}
	},

	handleOnChange_: function(e) {
		this.setState({statusMessage: null});
		this.setState({inputValue: e.target.value});
	},

	handleOnFocus_: function(e) {
		this.setState({statusMessage: null});
	},

	handleAddSuccess_: function() {
		this.setState({inputValue: ''});
	},

	executeAddTags_: function(tags) {
		var blobrefs = this.props.blobrefs;
		var doesBlobHaveTag = this.props.doesBlobHaveTag;
		var sc = this.props.serverConnection;
		var promises = [];

		blobrefs.forEach(function(pm) {
			tags.forEach(function(tag) {
				if (!doesBlobHaveTag(pm, tag)) {
					promises.push(new goog.labs.Promise(sc.newAddAttributeClaim.bind(sc, pm, 'tag', tag)));
				}
			});
		});

		this.props.executePromises('AddTag', promises, this.handleAddSuccess_);
	},

	getStatusMessageItem_: function() {
		if (!this.state.statusMessage) {
			return null;
		}

		return React.DOM.div({}, this.state.statusMessage);
	},

	render: function() {
		return React.DOM.form(
			{
				className: 'cam-addtagsinput-form',
				onSubmit: this.handleOnSubmit_,
			},
			React.DOM.input(
				{
					onChange: this.handleOnChange_,
					onFocus: this.handleOnFocus_,
					placeholder: this.PLACEHOLDER,
					ref: 'inputField',
					value: this.state.inputValue,
				}
			),
			this.getStatusMessageItem_()
		);
	}
});

cam.EditTagsControl = React.createClass({
	displayName: 'EditTagsControl',

	propTypes: {
		blobs: React.PropTypes.array.isRequired,
		blobrefs: React.PropTypes.array.isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		doesBlobHaveTag: React.PropTypes.func.isRequired,
		executePromises: React.PropTypes.func.isRequired
	},

	handleApplyTag_: function(e) {
		e.preventDefault();
		var tag = e.target.value;
		this.executeApplyTag_(tag);
	},

	handleRemoveTag_: function(e) {
		e.preventDefault();
		var tag = e.target.value;
		this.executeRemoveTag_(tag);
	},

	executeApplyTag_: function(tag) {
		var blobrefs = this.props.blobrefs;
		var doesBlobHaveTag = this.props.doesBlobHaveTag;
		var sc = this.props.serverConnection;
		var promises = [];

		blobrefs.forEach(function(pm) {
			if (!doesBlobHaveTag(pm, tag)) {
				promises.push(new goog.labs.Promise(sc.newAddAttributeClaim.bind(sc, pm, 'tag', tag)));
			}
		});

		this.props.executePromises('ApplyTag', promises);
	},

	executeRemoveTag_: function(tag) {
		var blobrefs = this.props.blobrefs;
		var doesBlobHaveTag = this.props.doesBlobHaveTag;
		var sc = this.props.serverConnection;
		var promises = [];

		blobrefs.forEach(function(pm) {
			if (doesBlobHaveTag(pm, tag)) {
				promises.push(new goog.labs.Promise(sc.newDelAttributeClaim.bind(sc, pm, 'tag', tag)));
			}
		});

		this.props.executePromises('DeleteTag', promises);
	},

	getApplyTagButton_: function(numBlobs, tag, allTags) {
		var totalHits = allTags[tag];
		if (totalHits == numBlobs) {
			return React.DOM.button(
				{
					key: 'apply-tag-' + tag,
					className: 'cam-edittagscontrol-button-all-tagged',
					disabled: true
				},
				tag
			);
		}

		return React.DOM.button(
			{
				key: 'apply-tag-' + tag,
				className: 'cam-edittagscontrol-button-some-tagged',
				title: 'Apply tag to all selected items',
				onClick: this.handleApplyTag_,
				value: tag
			},
			tag
		);
	},

	render: function() {
		var tagControls = [];
		var allTags = {};

		var numBlobs = this.props.blobs.length;

		this.props.blobs.forEach(function(blobmeta) {
			if (blobmeta && blobmeta.camliType == 'permanode') {
				var tags = blobmeta.permanode.attr.tag;
				if (tags) {
					tags.forEach(function(tag) {
						if (!allTags.hasOwnProperty(tag)) {
							allTags[tag] = 0;
						}
						++allTags[tag];
					});
				}
			} else {
				console.log('EditTagsControl: blob not a permanode!');
			}
		});

		goog.object.getKeys(allTags).sort().forEach(function(tag) {
			tagControls.push(React.DOM.div(
				{
					className: 'cam-edittagscontrol-button-group'
				},
				this.getApplyTagButton_(numBlobs, tag, allTags),
				React.DOM.button(
					{
						key:'del-tag-' + tag,
						title: 'Remove tag from all selected items',
						className: 'cam-edittagscontrol-button-remove-tag',
						onClick: this.handleRemoveTag_,
						value: tag
					},
					'x'
				)
			));
		}.bind(this));

		return React.DOM.div(
			{
				className: 'cam-edittagscontrol-main',
			},
			tagControls
		);
	}
});
