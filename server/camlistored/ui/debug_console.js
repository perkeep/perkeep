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

goog.provide('cam.DebugConsole');

goog.require('goog.object');

goog.require('cam.reactUtil');

cam.DebugConsole = React.createClass({
	HANDLERS: {
		echo: function(client, input, callback) {
			callback('reticulating splines...');
			window.setTimeout(callback.bind(null, input), 2000)
		},
		selected: function(client, input, callback) {
			callback(goog.object.getKeys(client.getSelectedItems()).join(','));
		},
	},

	propTypes: {
		client: React.PropTypes.shape({
			getSelectedItems: React.PropTypes.func.isRequired,
		}),
	},

	getInitialState: function() {
		return {
			input: '',
			output: '',
		};
	},

	render: function() {
		// TODO(aa): Figure out flexbox to lay this out correctly.
		return React.DOM.div(null,
			React.DOM.div(null,
				React.DOM.input({
					type: 'text',
					placeholder: this.getPlaceholderText_(),
					style: {width: 275},
					onChange: this.handleInputChange_
				}),
				React.DOM.button({onClick: this.handleInputButton_}, 'Go')
			),
			React.DOM.div({
				style: {
					overflow: 'auto',
					height: 234,
				},
			}, this.state.output)
		);
	},

	getPlaceholderText_: function() {
		return goog.object.getKeys(this.HANDLERS).join(', ');
	},

	handleInputChange_: function(e) {
		this.setState({input:e.target.value});
	},

	handleInputButton_: function(e) {
		var parts = this.state.input.split(/\s+/);
		var command = parts.splice(0, 1);
		var h = this.HANDLERS[command];
		if (h) {
			h(this.props.client, parts.join(' '), this.handleOutput_);
		} else {
			this.handleOutput_('Error: Could not find handler for command');
		}
	},

	handleOutput_: function(out) {
		this.setState({
			output: out,
		});
	},
});
