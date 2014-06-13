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

goog.require('cam.reactUtil');

goog.require('cam.ServerConnection');

goog.require('goog.labs.Promise');

goog.require('goog.object');

cam.DebugConsole = React.createClass({
	HELP_TEXT: "-help",
	HANDLERS: {
		selected: {
			execute: function(client, input, callback) {
				var blobrefs = goog.object.getKeys(client.getSelectedItems());

				if (!blobrefs.length) {
					callback('Please select at least one item');
				} else {
					callback(goog.object.getKeys(client.getSelectedItems()).join(', '));
				}
			},
			help: function(callback) {
				callback('Usage: selected | Blobrefs of the selected items will be written to console output');
			},
		},
		tag: {
			execute: function(client, input, callback) {
				var blobrefs = goog.object.getKeys(client.getSelectedItems());
				var parts = cam.DebugConsole.parseCommandAndArgs(input);
				var mode = parts['command'];
				var tags = parts['args'].split(',').map(function(s) { return s.trim(); });
				var prettyTags = tags.join(', ');

				if (!blobrefs.length) {
					callback('Please select at least one item');
					return;
				} else if (!mode) {
					callback('Please provide a mode of operation for tag');
					return;
				} else if (mode != 'clear' && tags.some(function(t) { return !t })) {
					callback('At least one invalid tag value was supplied: ' + prettyTags);
					return;
				}

				var sc = client.serverConnection;
				var promises = [];

				// TODO(mr): do we need to restrict add/removal of tags based upon existing values? ex: Don't delete tag 'taco' if item is not tagged with 'taco'
				switch (mode) {
					case "add": {
						if (tags.length == 1 && tags[0] == '') {
							callback('Please provide at least one tag value to add');
							return;
						}

						blobrefs.forEach(function(permanode) {
							tags.forEach(function(tag) {
								console.log('add-tag-promise for: ' + permanode + ", tag: " + tag);
								promises.push(new goog.labs.Promise(sc.newAddAttributeClaim.bind(sc, permanode, 'tag', tag)));
							});
						});
						break;
					}
					case "del": {
						if (tags.length == 1 && tags[0] == '') {
							callback('Please provide at least one tag value to delete');
							return;
						}

						blobrefs.forEach(function(permanode) {
							tags.forEach(function(tag) {
								console.log('del-tag-promise for: ' + permanode + ", tag: " + tag);
								promises.push(new goog.labs.Promise(sc.newDelAttributeClaim.bind(sc, permanode, 'tag', tag)));
							});
						});
						break;
					}
					case "set": {
						if (tags.length == 1 && tags[0] == '') {
							callback('Please provide at least one tag value to set');
							return;
						}

						// 'set' tags using first value supplied then 'add' any additional
						var numTags = tags.length;
						blobrefs.forEach(function(permanode) {
							console.log('set-tag-promise for: ' + permanode + ", tag: " + tags[0]);
							promises.push(new goog.labs.Promise(sc.newSetAttributeClaim.bind(sc, permanode, 'tag', tags[0])));

							for (var i = 1; i < numTags; i++) {
								console.log('add-tag-promise for: ' + permanode + ", tag: " + tags[i]);
								promises.push(new goog.labs.Promise(sc.newAddAttributeClaim.bind(sc, permanode, 'tag', tags[i])));
							}
						});
						break;
					}
					case "clear": {
						blobrefs.forEach(function(permanode) {
							console.log('clear-tag-promise for: ' + permanode);
							promises.push(new goog.labs.Promise(sc.newDelAttributeClaim.bind(sc, permanode, 'tag', '')));
						});
						break;
					}
					default: {
						callback('tag command does not support <mode>: ' + mode);
						return;
					}
				}

				goog.labs.Promise.all(promises).thenCatch(function(e) {
					console.error('promise rejected: %s', e);
					callback('The system encountered an error executing tag ' + mode + ': ' + e);
				}).then(function(results) {
					if (results) {
						console.log('successfully completed %d of %d promises', results.length, promises.length);

						if (mode == 'add') {
							callback('Successfully added the tag(s) {' + prettyTags + '} to ' + blobrefs.length + ' items');
						} else if (mode == 'del') {
							callback('Successfully deleted the tag(s) {' + prettyTags + '} from ' + blobrefs.length + ' items');
						} else if (mode == 'set') {
							callback('Successfully reset ' + blobrefs.length + ' items to have the tag(s) {' + prettyTags + '}');
						} else if (mode == 'clear') {
							callback('Successfully deleted all tags from ' + blobrefs.length + ' items');
						}
					} else {
						// else: intentionally left blank. empty error object returned upon promise rejection
					}
				}).then(function() {
					console.log('tag operation complete');
				});
				callback('executing tag operation');
			},
			help: function(callback) {
				callback('Usage: tag <add | del | set | clear> [val1,val2,...] | Add, delete, set, or clear tag attributes on the selected permanodes | Examples: tag add val1,val2,val3 | tag del val1,val2 | tag set val1 | tag clear');
			},
		},
	},

	getPlaceholderText_: function() {
		return this.getAvailableCommands_() + " (" + this.HELP_TEXT + ")";
	},

	getStaticHelpText_: function() {
		return 'Further usage information is available by <command> ' + this.HELP_TEXT;
	},

	getAvailableCommands_: function() {
		return goog.object.getKeys(this.HANDLERS).join(', ');
	},

	handleInputChange_: function(e) {
		this.setState({commandInput:e.target.value});
	},

	handleSubmit_: function(e) {
		e.preventDefault();
		var parts = cam.DebugConsole.parseCommandAndArgs(this.state.commandInput);
		var h = this.HANDLERS[parts['command']];
		if (h) {
			if (parts['args'] == this.HELP_TEXT) {
				h.help(this.handleOutput_);
			} else {
				h.execute(this.props.client, parts['args'], this.handleOutput_);
			}
		} else {
			this.handleOutput_('Command not found. Available commands are: ' + this.getAvailableCommands_() + '. ' + this.getStaticHelpText_());
		}
	},

	handleOutput_: function(out) {
		this.setState({commandResult:out});
		this.setState({commandInput:''});
		this.refs.consoleInput.getDOMNode().focus();
	},

	/*
	 * ReactJS #ComponentSpec
	 */
	getInitialState: function() {
		return {
			commandInput: '',
			commandResult: 'Enter a command and hit Go or press the Enter key to execute. ' + this.getStaticHelpText_()
		};
	},

	propTypes: {
		client: React.PropTypes.shape({
			getSelectedItems: React.PropTypes.func.isRequired,
			// TODO(mr): JS warning in Chrome console, I assume here, though no exact line # provided. "invalid prop 'serverConnection' supplied to '<<anonymous>>', expected instance of '<<anonymous>>'"
			serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		}),
	},

	render: function() {
		// TODO(aa): Figure out flexbox to lay this out correctly.
		return React.DOM.div(null,
			React.DOM.div(null, "Input"),
			React.DOM.div(null,
				React.DOM.form({onSubmit:this.handleSubmit_},
					React.DOM.input({
						type: 'text',
						ref: 'consoleInput',
						placeholder: this.getPlaceholderText_(),
						style: {width:275},
						onChange: this.handleInputChange_,
						value: this.state.commandInput
					}),
					React.DOM.button(null, 'Go')
				)
			),
			React.DOM.div(null, "Output"),
			React.DOM.textarea({
				readOnly: true,
				style: {overflow:'auto', width:310, height:150},
				value: this.state.commandResult
			})
		);
	},

	statics: {
		/**
		* @return {'command' : 'x', 'args': 'y'} The first word (command) and remaining arguments of the input string
		*/
		parseCommandAndArgs : function(s) {
			var parts = s.split(' ');
			var firstCommand = parts.shift();
			var arguments = parts.join(' ').trim();

			return {'command':firstCommand, 'args':arguments};
		}
	},

	/*
	 * ReactJS #Lifecycle Methods
	 */
	componentDidMount: function() {
		// allow immediate entry of commands
		this.refs.consoleInput.getDOMNode().focus();
	},
});
