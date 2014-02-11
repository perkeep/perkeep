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
goog.provide('cam.MobileSetupView');

goog.require('goog.Uri');

cam.MobileSetupView = React.createClass({
	displayName: 'MobileSetupView',

	propTypes: {
		baseURL: React.PropTypes.object.isRequired,
		defaultUsername: React.PropTypes.string.isRequired,
	},

	getInitialState: function() {
		var serverURL = this.props.baseURL.clone().setPath('').setQuery('');
		return {
			autoUpload: false,
			// TODO(wathiede): autopopulate this, not sure how.
			certFingerprint: '',
			maxCacheSize: 256,
			server: serverURL.toString()
		};
	},

	getQRURL_: function() {
		// TODO(wathiede): I'm not sure what the Android and iPhone requirements are for registering a URL handler are.  If they can't be the same for both platforms, then we'll need this to be conditional based on a checkbox in the form.
		var settingsURL = goog.Uri.parse('camli://settings/');
		if (this.state.username != '') {
			settingsURL.setParameterValue('username', this.state.username);
		}
		if (this.state.server != '') {
			settingsURL.setParameterValue('server', this.state.server);
		}
		if (this.state.autoUpload) {
			settingsURL.setParameterValue('autoUpload', 1);
		}
		settingsURL.setParameterValue('maxCacheSize', this.state.maxCacheSize);
		if (this.state.certFingerprint != '') {
			settingsURL.setParameterValue('certFingerprint', this.state.certFingerprint);
		}

		var qrURL = this.props.baseURL.clone();
		qrURL.setPath(qrURL.getPath() + '/qr/').setParameterValue('url', settingsURL.toString());
		return qrURL.toString();
	},

	handleServerChange_: function(e) {
		this.setState({server: e.target.value});
	},

	handleUsernameChange_: function(e) {
		this.setState({username: e.target.value});
	},

	handleAutoUploadChange_: function(e) {
		this.setState({autoUpload: e.target.checked});
	},

	handleMaxCacheSizeChange_: function(e) {
		this.setState({maxCacheSize: e.target.value});
	},

	handleCertFingerprintChange_: function(e) {
		this.setState({certFingerprint: e.target.value});
	},

	render: function() {
		return (
			React.DOM.div({},
				React.DOM.img({src:this.getQRURL_()}),
				React.DOM.form({ref:'form', onSubmit:this.handleChange_},
					React.DOM.label({}, 'Camlistore Server:',
						React.DOM.input({
							defaultValue: this.state.server,
							onChange: this.handleServerChange_,
							placeholder: 'e.g. https://foo.example.com or example.com:3179',
							type: 'text'
						})),
					React.DOM.label({}, 'Username:',
						React.DOM.input({
							defaultValue: this.props.defaultUsername,
							onChange: this.handleUsernameChange_,
							placeholder: '<unset>',
							type: 'text'
						})),
					React.DOM.label({className: 'mobile-setup-auto-upload'},
						React.DOM.input({
							onChange: this.handleAutoUploadChange_,
							type: 'checkbox'
						}),
						'Auto-Upload',
						React.DOM.span({className: 'mobile-setup-helptext'}, 'Upload SD card files as created')),
					// TODO(wathiede): add suboptions to auto-upload?
					React.DOM.label({className: 'mobile-setup-max-cache-size'},
						'Maximum cache size',
						React.DOM.input({
							defaultValue: this.state.maxCacheSize,
							onChange: this.handleMaxCacheSizeChange_,
							type: 'text'
						}),
						'MB'),
					React.DOM.label({}, 'Self-signed cert fingerprint:',
						React.DOM.input({
							onChange: this.handleCertFingerprintChange_,
							placeholder: '<unset; optional 20 hex SHA-256 prefix>',
							type: 'text'
						}))
			)));
	},

	handleChange_: function() {
		var u = this.getQRURL_();
		console.log(u);
	},
});
