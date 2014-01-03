goog.provide('SpritedImage');

goog.require('goog.object');
goog.require('goog.string');

goog.require('object');

var SpritedImage = React.createClass({
	render: function() {
		return (
			React.DOM.div({className: this.props.className, style: extend(this.props.style, {overflow: 'hidden'})},
				React.DOM.img({src: this.props.src, style: this.getImgStyle_()})));
	},

	getImgStyle_: function() {
		var x = this.props.index % this.props.sheetWidth;
		var y = Math.floor(this.props.index / this.props.sheetWidth);
		if (y >= this.props.sheetHeight) {
			throw new Error(goog.string.subs('Index %s out of range', this.props.index));
		}
		return {
			position: 'absolute',
			left: -x * this.props.spriteWidth,
			top: -y * this.props.spriteHeight
		};
	}
});
