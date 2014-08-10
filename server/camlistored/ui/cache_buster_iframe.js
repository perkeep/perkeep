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

	componentDidMount: function() {
		this.getDOMNode().contentWindow.addEventListener('DOMContentLoaded', this.updateSize_);
	},

	getInitialState: function() {
		return {
			height: this.props.height,
			r: Date.now(),
		}
	},

	render: function() {
		var uri = this.props.src.clone();
		uri.setParameterValue('r', this.state.r);
		return React.DOM.iframe({
			height: this.state.height,
			src: uri.toString(),
			style: {
				border: 'none',
			},
			width: this.props.width,
		});
	},

	updateSize_: function() {
		if (!this.isMounted()) {
			return;
		}

		this.getDOMNode().contentDocument.body.style.overflowY = 'hidden';
		this.setState({height: this.getDOMNode().contentDocument.documentElement.offsetHeight });
		window.setTimeout(this.updateSize_, 200);
	},
});
