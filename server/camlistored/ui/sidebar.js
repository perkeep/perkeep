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

goog.provide('cam.Sidebar');

goog.require('goog.array');
goog.require('goog.object');
goog.require('goog.string');

goog.require('cam.ServerConnection');

cam.Sidebar = React.createClass({
	displayName: 'Sidebar',

	propTypes: {
		isExpanded: React.PropTypes.bool.isRequired,
		header: React.PropTypes.renderable,
		mainControls: React.PropTypes.arrayOf(
			React.PropTypes.shape(
				{
					displayTitle: React.PropTypes.string.isRequired,
					control: React.PropTypes.renderable.isRequired,
				}
			)
		),
		selectionControls: React.PropTypes.arrayOf(React.PropTypes.renderable).isRequired,
		selectedItems: React.PropTypes.object.isRequired,
	},

	getInitialState: function() {
		return {
			openControls: [],	// all controls that are currently 'open'
		};
	},

	render: function() {
		return React.DOM.div(
			{
				className: React.addons.classSet({
					'cam-sidebar': true,
					'cam-sidebar-hidden': !this.props.isExpanded,
				})
			},
			this.props.header,
			this.props.selectionControls,
			this.getMainControls_()
		);
	},

	getMainControls_: function() {
		return this.props.mainControls.map(
			function(c) {
				return cam.CollapsibleControl(
				{
					key: c.displayTitle,
					control: c.control,
					isOpen: this.isControlOpen_(c.displayTitle),
					onToggleOpen: this.handleToggleControlOpen_,
					title: c.displayTitle
				});
			}.bind(this)
		);
	},

	handleToggleControlOpen_: function(displayTitle) {
		var currentlyOpen = this.state.openControls;

		if(!this.isControlOpen_(displayTitle)) {
			currentlyOpen.push(displayTitle);
		} else {
			goog.array.remove(currentlyOpen, displayTitle);
		}

		this.setState({openControls : currentlyOpen});
	},

	isControlOpen_: function(displayTitle) {
		return goog.array.contains(this.state.openControls, displayTitle);
	}
});

cam.CollapsibleControl = React.createClass({
	displayName: 'CollapsibleControl',

	propTypes: {
		control: React.PropTypes.renderable.isRequired,
		isOpen: React.PropTypes.bool.isRequired,
		onToggleOpen: React.PropTypes.func,
		title: React.PropTypes.string.isRequired
	},

	getControl_: function() {
		if(!this.props.control || !this.props.isOpen) {
			return null;
		}

		return React.DOM.div(
			{
				className: 'cam-sidebar-section'
			},
			this.props.control
		);
	},

	render: function() {
		return React.DOM.div(
			{
				className: 'cam-sidebar-collapsible-section-header'
			},
			React.DOM.button(
				{
					onClick: this.handleToggleOpenClick_,
				},
				React.DOM.i(
					{
						className: React.addons.classSet({
							'fa': true,
							'fa-angle-down': this.props.isOpen,
							'fa-angle-right': !this.props.isOpen
						}),
						key: 'toggle-sidebar-section'
					}
				),
				this.props.title
			),
			this.getControl_()
		);
	},

	handleToggleOpenClick_: function(e) {
		e.preventDefault();
		this.props.onToggleOpen(this.props.title);
	}
});
