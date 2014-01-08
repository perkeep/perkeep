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
