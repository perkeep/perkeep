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

goog.provide('cam.IndexPageReact');

goog.require('goog.string');

goog.require('cam.NavReact');
goog.require('cam.reactUtil');

cam.IndexPageReact = React.createClass({
	propTypes: {
		baseURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		thumbnailVersion: React.PropTypes.number.isRequired,
		timer: cam.NavReact.originalSpec.propTypes.timer,

		// The event target to listen for key events at.
		keyTarget: cam.reactUtil.quacksLike({addEventListener:React.PropTypes.func.isRequired}).isRequired,
	},

	componentWillMount: function() {
		this.transformedBlobItemContainerHeight_ = 0;
	},

	getInitialState: function() {
		return {
			lorem: '',
			isNavOpen: false,
		};
	},

	componentDidMount: function() {
		var req = new XMLHttpRequest();
		req.open('GET', 'lorem.html', true);
		req.onload = function() {
			this.setState({lorem:req.responseText});
		}.bind(this);
		req.send(null);

		this.props.keyTarget.addEventListener('keypress', this.handleKeyPress_);
	},

	render: function() {
		return React.DOM.div({}, [
			cam.NavReact({key:'nav', ref:'nav', timer:this.props.timer, onOpen:this.handleNavOpen_, onClose:this.handleNavClose_}, [
					// TODO(aa): Flip these on and off dependent on selection in BlobItemContainer.
					cam.NavReact.SearchItem({key:'search', ref:'search', iconSrc:'magnifying_glass.svg', onSearch:this.handleSearch_}, 'Search'),
					cam.NavReact.Item({key:'newpermanode', iconSrc:'new_permanode.svg', onClick:this.handleNewPermanode_}, 'New permanode'),
					cam.NavReact.Item({key:'roots', iconSrc:'icon_27307.svg', onClick:this.handleShowSearchRoots_}, 'Search roots'),
					cam.NavReact.Item({key:'selectascurrent', iconSrc:'target.svg', onClick:this.handleSelectAsCurrentSet_}, 'Select as current set'),
					cam.NavReact.Item({key:'addtoset', iconSrc:'icon_16716.svg', onClick:this.handleAddToSet_}, 'Add to current set'),
					cam.NavReact.Item({key:'createsetwithselection', iconSrc:'circled_plus.svg', onClick:this.handleCreateSetWithSelection_}, 'Create set with 5 items'),
					cam.NavReact.Item({key:'clearselection', iconSrc:'clear.svg', onClick:this.handleClearSelection_}, 'Clear selection'),
					cam.NavReact.Item({key:'up', iconSrc:'up.svg', onClick:this.handleEmbiggen_}, 'Moar bigger'),
					cam.NavReact.Item({key:'down', iconSrc:'down.svg', onClick:this.handleEnsmallen_}, 'Less bigger'),
					cam.NavReact.LinkItem({key:'logo', iconSrc:'/favicon.ico', href:this.props.baseURL.toString(), extraClassName:'cam-logo'}, 'Camlistore'),
				]),
			React.DOM.div({key:'blobitemcontainer', ref:'blobItemContainer', className:'cam-blobitemcontainer', style:this.getBlobItemContainerStyle_()}, this.state.lorem),
		]);
	},

	handleNavOpen_: function() {
		this.setState({isNavOpen:true});
	},

	handleNavClose_: function() {
		this.refs.search.clear();
		this.refs.search.blur();
		this.setState({isNavOpen:false});
	},

	handleSearch_: function(query) {
		console.log('search', query);
	},

	handleShowSearchRoots_: function() {
		console.log('handle search roots');
	},

	handleSelectAsCurrentSet_: function() {
		console.log('select as current set');
	},

	handleAddToSet_: function() {
		console.log('add to current set');
	},

	handleCreateSetWithSelection_: function() {
		console.log('create set with selection');
	},

	handleClearSelection_: function() {
		console.log('clear selection');
	},

	handleEmbiggen_: function() {
		console.log('embiggen');
	},

	handleEnsmallen_: function() {
		console.log('ensmallen');
	},

	handleKeyPress_: function(e) {
		if (String.fromCharCode(e.charCode) == '/') {
			this.refs.nav.open();
			this.refs.search.focus();
			e.preventDefault();
		}
	},

	getBlobItemContainerStyle_: function() {
		var style = {
			// Temporary override to make our test data look better.
			whiteSpace:'normal',
			padding: '1em',
		};

		// Need to be mounted to getDOMNode() below.
		if (!this.isMounted()) {
			return style;
		}

		var closedWidth = this.getDOMNode().offsetWidth - 36;
		var openWidth = closedWidth - (275 - 36);  // TODO(aa): Get this from DOM somehow?
		var openScale = openWidth / closedWidth;

		var currentHeight = goog.dom.getDocumentHeight();
		var currentScroll = goog.dom.getDocumentScroll().y;
		var potentialScroll = currentHeight - goog.dom.getViewportSize().height;
		var scrolledRatio = currentScroll / potentialScroll;
		var originY = currentHeight * currentScroll / potentialScroll;

		style[cam.reactUtil.getVendorProp('transformOrigin')] = goog.string.subs('right %spx 0', originY);

		// The 3d transform is important. See: https://code.google.com/p/camlistore/issues/detail?id=284.
		var scale = this.state.isNavOpen ? openScale : 1;
		style[cam.reactUtil.getVendorProp('transform')] = goog.string.subs('scale3d(%s, %s, 1)', scale, scale);

		return style;
	},
});
