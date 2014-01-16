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
goog.require('goog.Uri');

goog.require('cam.BlobItemContainerReact');
goog.require('cam.DetailView');
goog.require('cam.Navigator');
goog.require('cam.NavReact');
goog.require('cam.reactUtil');
goog.require('cam.SearchSession');
goog.require('cam.ServerConnection');

cam.IndexPageReact = React.createClass({
	displayName: 'IndexPageReact',

	THUMBNAIL_SIZES_: [75, 100, 150, 200, 250],

	propTypes: {
		availWidth: React.PropTypes.number.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		config: React.PropTypes.object.isRequired,
		eventTarget: cam.reactUtil.quacksLike({addEventListener:React.PropTypes.func.isRequired}).isRequired,
		history: cam.reactUtil.quacksLike({pushState:React.PropTypes.func.isRequired}).isRequired,
		location: cam.reactUtil.quacksLike({href:React.PropTypes.string.isRequired, reload:React.PropTypes.func.isRequired}).isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		timer: cam.NavReact.originalSpec.propTypes.timer,
	},

	componentWillMount: function() {
		var currentURL = new goog.Uri(this.props.location.href);
		var baseURL = currentURL.resolve(new goog.Uri(CAMLISTORE_CONFIG.uiRoot));
		baseURL.setParameterValue('react', '1');

		var navigator = new cam.Navigator(this.props.eventTarget, this.props.location, this.props.history, true);
		navigator.onNavigate = this.handleNavigate_;

		this.setState({
			baseURL: baseURL,
			navigator: navigator,
			searchSession: new cam.SearchSession(this.props.serverConnection, currentURL.clone(), ' '),
		});
		this.handleNavigate_(currentURL);
	},

	getInitialState: function() {
		return {
			isNavOpen: false,
			selection: {},
			thumbnailSizeIndex: 3,
		};
	},

	componentDidMount: function() {
		this.props.eventTarget.addEventListener('keypress', this.handleKeyPress_);
	},

	render: function() {
		return React.DOM.div({}, [
			this.getNav_(),
			this.getBlobItemContainer_(),
			this.getDetailView_(),
		]);
	},

	handleNavigate_: function(newURL) {
		if (this.state.currentURL) {
			if (this.state.currentURL.getPath() != newURL.getPath()) {
				return false;
			}
		}

		// This is super finicky. We should improve the URL scheme and give things that are different different paths.
		var query = newURL.getQueryData();
		var inSearchMode = (query.getCount() == 1 && query.containsKey('react')) || (query.getCount() == 2 && query.containsKey('react') && query.containsKey('q'));
		var inDetailMode = query.containsKey('p') && query.get('newui') == '1';

		if (!inSearchMode && !inDetailMode) {
			return false;
		}

		this.setState({
			currentURL: newURL,
			searchMode: inSearchMode,
			detailMode: inDetailMode,
		});
		return true;
	},

	getNav_: function() {
		if (!this.state.searchMode) {
			return null;
		}
		return cam.NavReact({key:'nav', ref:'nav', timer:this.props.timer, onOpen:this.handleNavOpen_, onClose:this.handleNavClose_}, [
			// TODO(aa): Flip these on and off dependent on selection in BlobItemContainer.
			cam.NavReact.SearchItem({key:'search', ref:'search', iconSrc:'magnifying_glass.svg', onSearch:this.handleSearch_}, 'Search'),
			cam.NavReact.Item({key:'newpermanode', iconSrc:'new_permanode.svg', onClick:this.handleNewPermanode_}, 'New permanode'),
			cam.NavReact.Item({key:'roots', iconSrc:'icon_27307.svg', onClick:this.handleShowSearchRoots_}, 'Search roots'),
			this.getSelectAsCurrentSetItem_(),
			this.getAddToCurrentSetItem_(),
			this.getCreateSetWithSelectionItem_(),
			this.getClearSelectionItem_(),
			cam.NavReact.Item({key:'up', iconSrc:'up.svg', onClick:this.handleEmbiggen_}, 'Moar bigger'),
			cam.NavReact.Item({key:'down', iconSrc:'down.svg', onClick:this.handleEnsmallen_}, 'Less bigger'),
			cam.NavReact.LinkItem({key:'logo', iconSrc:'/favicon.ico', href:this.state.baseURL.toString(), extraClassName:'cam-logo'}, 'Camlistore'),
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
		// TODO(aa)
	},

	handleShowSearchRoots_: function() {
		// TODO(aa)
	},

	handleSelectAsCurrentSet_: function() {
		// TODO(aa)
	},

	handleAddToSet_: function() {
		// TODO(aa)
	},

	handleCreateSetWithSelection_: function() {
		// TODO(aa)
	},

	handleClearSelection_: function() {
		this.setState({selection:{}});
	},

	handleEmbiggen_: function() {
		var newSizeIndex = this.state.thumbnailSizeIndex + 1;
		if (newSizeIndex < this.THUMBNAIL_SIZES_.length) {
			this.setState({thumbnailSizeIndex:newSizeIndex});
		}
	},

	handleEnsmallen_: function() {
		var newSizeIndex = this.state.thumbnailSizeIndex - 1;
		if (newSizeIndex >= 0) {
			this.setState({thumbnailSizeIndex:newSizeIndex});
		}
	},

	handleKeyPress_: function(e) {
		if (String.fromCharCode(e.charCode) == '/') {
			this.refs.nav.open();
			this.refs.search.focus();
			e.preventDefault();
		}
	},

	handleDetailURL_: function(item) {
		var detailURL = this.state.currentURL.clone();
		detailURL.setParameterValue('p', item.blobref);
		if (item.m.camliType == 'permanode' && item.im) {
			detailURL.setParameterValue('newui', '1');
		}
		return detailURL.toString();
	},

	getSelectAsCurrentSetItem_: function() {
		if (goog.object.getCount(this.state.selection) != 1) {
			return null;
		}

		var blobref = goog.object.getAnyKey(this.state.selection);
		var data = new cam.BlobItemReactData(blobref, this.state.searchSession.getCurrentResults().description.meta);
		if (!data.isDynamicCollection) {
			return null;
		}

		return cam.NavReact.Item({key:'selectascurrent', iconSrc:'target.svg', onClick:this.handleSelectAsCurrentSet_}, 'Select as current set');
	},

	getAddToCurrentSetItem_: function() {
		if (!goog.object.getAnyKey(this.state.selection)) {
			return null;
		}
		return cam.NavReact.Item({key:'addtoset', iconSrc:'icon_16716.svg', onClick:this.handleAddToSet_}, 'Add to current set');
	},

	getCreateSetWithSelectionItem_: function() {
		var numItems = goog.object.getCount(this.state.selection);
		if (numItems == 0) {
			return null;
		}
		var label = numItems == 1 ? 'Create set with item' : goog.string.subs('Create set with %s items', numItems);
		return cam.NavReact.Item({key:'createsetwithselection', iconSrc:'circled_plus.svg', onClick:this.handleCreateSetWithSelection_}, label);
	},

	getClearSelectionItem_: function() {
		if (!goog.object.getAnyKey(this.state.selection)) {
			return null;
		}
		return cam.NavReact.Item({key:'clearselection', iconSrc:'clear.svg', onClick:this.handleClearSelection_}, 'Clear selection');
	},

	handleSelectionChange_: function(newSelection) {
		this.setState({selection:newSelection});
	},

	getBlobItemContainer_: function() {
		if (!this.state.searchMode) {
			return null;
		}
		return cam.BlobItemContainerReact({
			key: 'blobitemcontainer',
			ref: 'blobItemContainer',
			availWidth: this.props.availWidth,
			availHeight: this.props.availHeight,
			detailURL: this.handleDetailURL_,
			onSelectionChange: this.handleSelectionChange_,
			scrollEventTarget: this.props.eventTarget,
			searchSession: this.state.searchSession,
			selection: this.state.selection,
			style: this.getBlobItemContainerStyle_(),
			thumbnailSize: this.THUMBNAIL_SIZES_[this.state.thumbnailSizeIndex],
			thumbnailVersion: Number(this.props.config.thumbVersion),
		});
	},

	getBlobItemContainerStyle_: function() {
		var style = {};

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

	getDetailView_: function() {
		if (!this.state.detailMode) {
			return null;
		}

		var searchURL = this.state.baseURL.clone();
		if (this.state.currentURL.getQueryData().containsKey('q')) {
			searchURL.setParameterValue('q', this.state.currentURL.getParameterValue('q'));
		}

		var oldURL = this.state.baseURL.clone();
		oldURL.setParameterValue('p', this.state.currentURL.getParameterValue('p'));

		var getDetailURL = function(blobRef) {
			var result = this.state.currentURL.clone();
			result.setParameterValue('p', blobRef);
			return result;
		}.bind(this);

		return cam.DetailView({
			key: 'detailview',
			blobref: this.state.currentURL.getParameterValue('p'),
			searchSession: this.state.searchSession,
			searchURL: searchURL,
			oldURL: oldURL,
			getDetailURL: getDetailURL,
			navigator: this.state.navigator,
			keyEventTarget: this.props.eventTarget,
			width: this.props.availWidth,
			height: this.props.availHeight,
		});
	},
});
