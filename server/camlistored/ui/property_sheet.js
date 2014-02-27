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

goog.provide('cam.PropertySheet');
goog.provide('cam.PropertySheetContainer');

goog.require('cam.style.ClassNameBuilder');

cam.PropertySheet = React.createClass({
	displayName: 'PropertySheet',

	propTypes: {
		className: React.PropTypes.string,
		title: React.PropTypes.string.isRequired,
	},

	render: function() {
		return (
			React.DOM.div({className: new cam.style.ClassNameBuilder().add('cam-property-sheet').add(this.props.className).build()}, [
				React.DOM.div({className: 'cam-property-sheet-title'}, this.props.title),
				React.DOM.div({className: 'cam-property-sheet-content'}, this.props.children),
			])
		);
	},
});

cam.PropertySheetContainer = React.createClass({
	displayName: 'PropertySheetContainer',

	propTypes: {
		className: React.PropTypes.string,
		style: React.PropTypes.object,
	},

	render: function() {
		return React.DOM.div({
				className: new cam.style.ClassNameBuilder().add('cam-property-sheet-container').add(this.props.className).build(),
				style: this.props.style,
			},
			this.props.children
		);
	},
});
