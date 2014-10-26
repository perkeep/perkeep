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
	propTypes: {
		className: React.PropTypes.string,
		loopDelay: React.PropTypes.number,
		interval: React.PropTypes.number,
		numFrames: React.PropTypes.number.isRequired,
		sheetWidth: React.PropTypes.number.isRequired,
		spriteHeight: React.PropTypes.number.isRequired,
		spriteWidth: React.PropTypes.number.isRequired,
		src: React.PropTypes.string.isRequired,
		startFrame: React.PropTypes.number,
		style: React.PropTypes.object,
	},

	getInitialState: function() {
		return {
			index: this.props.startFrame || 0,
		}
	},

	componentDidMount: function(root) {
		this.scheduleFrame_();
	},

	scheduleFrame_: function() {
		var interval = function() {
			if (goog.isDef(this.props.loopDelay) && this.state.index == (this.props.numFrames - 1)) {
				return this.props.loopDelay;
			}
			if (goog.isDef(this.props.interval)) {
				return this.props.interval;
			}
			return 30;
		};
		this.timerId_ = window.setTimeout(function() {
			this.setState({
				index: ++this.state.index % this.props.numFrames
			}, this.scheduleFrame_);
		}.bind(this), interval.call(this));
	},

	componentWillUnmount: function() {
		window.clearInterval(this.timerId_);
	},

	render: function() {
		return cam.SpritedImage(cam.object.extend(this.props, {index: this.state.index}));
	}
});
