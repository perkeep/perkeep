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

goog.provide('cam.CacheBusterIframe');

goog.require('goog.Uri');

// Reload/shift-reload doesn't actually reload iframes from server in Chrome.
// We should implement content stamping, but for now, this is a workaround.
cam.CacheBusterIframe = React.createClass({
	propTypes: {
		height: React.PropTypes.number.isRequired,
		src: React.PropTypes.instanceOf(goog.Uri).isRequired,
		width: React.PropTypes.number.isRequired,
	},

	getInitialState: function() {
		return {
			r: Date.now(),
		}
	},

	render: function() {
		var uri = this.props.src.clone();
		uri.setParameterValue('r', this.state.r);
		return React.DOM.iframe({
			height: this.props.height,
			src: uri.toString(),
			width: this.props.width,
		});
	},
});
