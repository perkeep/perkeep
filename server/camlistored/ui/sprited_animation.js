/*
Copyright 2013 The Camlistore Authors

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

goog.provide('cam.SpritedAnimation');

goog.require('cam.SpritedImage');
goog.require('cam.object');

cam.SpritedAnimation = React.createClass({
	getInitialState: function() {
		return {
			index: 0
		}
	},

	componentDidMount: function(root) {
		this.timerId_ = window.setInterval(function() {
			this.setState({
				index: ++this.state.index % (this.props.sheetWidth * this.props.sheetHeight)
			})
		}.bind(this), this.props.interval);
	},

	componentWillUnmount: function() {
		window.clearInterval(this.timerId_);
	},

	render: function() {
		return cam.SpritedImage(cam.object.extend(this.props, {index: this.state.index}));
	}
});
