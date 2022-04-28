/*
Copyright 2014 The Perkeep Authors

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

// IndexPage is the top level React component class that owns all the other
// components of the web UI.
// See the React documentation and in particular
// https://facebook.github.io/react/docs/component-specs.html to learn about
// components.
goog.provide('cam.IndexPage');

goog.require('goog.array');
goog.require('goog.dom');
goog.require('goog.dom.classlist');
goog.require('goog.events.EventHandler');
goog.require('goog.format');
goog.require('goog.functions');
goog.require('goog.Promise');
goog.require('goog.object');
goog.require('goog.string');
goog.require('goog.Uri');

goog.require('cam.BlobDetail');
goog.require('cam.ImageDetail');
goog.require('cam.AudioDetail');
goog.require('cam.BlobItemContainerReact');
goog.require('cam.DirContainer');
goog.require('cam.BlobItemDemoContent');
goog.require('cam.BlobItemFoursquareContent');
goog.require('cam.BlobItemGenericContent');
goog.require('cam.BlobItemImageContent');
goog.require('cam.BlobItemMastodonContent');
goog.require('cam.BlobItemInstapaperContent');
goog.require('cam.BlobItemTwitterContent');
goog.require('cam.BlobItemVideoContent');
goog.require('cam.BlobItemAudioContent');
goog.require('cam.blobref');
goog.require('cam.DetailView');
goog.require('cam.Dialog');
goog.require('cam.MapAspect');
goog.require('cam.Header');
goog.require('cam.Navigator');
goog.require('cam.BrowserViewDetail');
goog.require('cam.PermanodeDetail');
goog.require('cam.permanodeUtils');
goog.require('cam.reactUtil');
goog.require('cam.SearchSession');
goog.require('cam.ServerConnection');
goog.require('cam.Sidebar');
goog.require('cam.TagsControl');
goog.require('cam.MapUtils');

cam.IndexPage = React.createClass({
	displayName: 'IndexPage',

	SIDEBAR_OPEN_WIDTH_: 250,

	HEADER_HEIGHT_: 38,
	SEARCH_PREFIX_: {
		RAW: 'raw'
	},
	THUMBNAIL_SIZE_: 200,

	SEARCH_SESSION_CACHE_SIZE_: 3,

	// Note that these are ordered by priority.
	BLOB_ITEM_HANDLERS_: [
		cam.BlobItemDemoContent.getHandler,
		cam.BlobItemFoursquareContent.getHandler,
		cam.BlobItemTwitterContent.getHandler,
		cam.BlobItemMastodonContent.getHandler,
		cam.BlobItemInstapaperContent.getHandler,
		cam.BlobItemImageContent.getHandler,
		cam.BlobItemAudioContent.getHandler,
		cam.BlobItemVideoContent.getHandler,
		cam.BlobItemGenericContent.getHandler
	],

	BLOBREF_PATTERN_: new RegExp('^' + cam.blobref.PATTERN + '$'),

	propTypes: {
		availWidth: React.PropTypes.number.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		config: React.PropTypes.object.isRequired,
		eventTarget: React.PropTypes.shape({addEventListener:React.PropTypes.func.isRequired}).isRequired,
		history: React.PropTypes.shape({pushState:React.PropTypes.func.isRequired, replaceState:React.PropTypes.func.isRequired, go:React.PropTypes.func.isRequired, state:React.PropTypes.object}).isRequired,
		openWindow: React.PropTypes.func.isRequired,
		location: React.PropTypes.shape({href:React.PropTypes.string.isRequired, reload:React.PropTypes.func.isRequired}).isRequired,
		scrolling: cam.BlobItemContainerReact.propTypes.scrolling,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		timer: cam.Header.propTypes.timer,
	},

	// Invoked once right before initial rendering. This is essentially IndexPage's
	// constructor. We populate non-React helpers that live for the entire lifetime
	// of IndexPage here.
	componentWillMount: function() {
		this.baseURL_ = null;
		this.dragEndTimer_ = 0;
		this.navigator_ = null;
		this.searchSessionCache_ = [];
		this.targetSearchSession_ = null;
		this.childSearchSession_ = null;

		this.eh_ = new goog.events.EventHandler(this);

		var newURL = new goog.Uri(this.props.location.href);
		this.baseURL_ = newURL.resolve(new goog.Uri(this.props.config.uiRoot));

		this.navigator_ = new cam.Navigator(this.props.eventTarget, this.props.location, this.props.history);
		this.navigator_.onWillNavigate = this.handleWillNavigate_;
		this.navigator_.onDidNavigate = this.handleDidNavigate_;

		this.handleWillNavigate_(newURL);
		this.handleDidNavigate_();
	},

	// Invoked right after initial rendering.
	componentDidMount: function() {
		// TODO(aa): This supports some of the old iframed pages. We can remove it once they are dead.
		goog.global.getSearchSession = function() {
			return this.childSearchSession_;
		}.bind(this);
		this.eh_.listen(this.props.eventTarget, 'keypress', this.handleKeyPress_);
		this.eh_.listen(this.props.eventTarget, 'keyup', this.handleKeyUp_);
		this.eh_.listen(this.props.eventTarget, 'touchstart', this.handleTouchstart_);
		this.eh_.listen(this.props.eventTarget, 'touchend', this.handleTouchend_);
	},

	componentWillUnmount: function() {
		this.eh_.dispose();
		this.clearDragTimer_();
	},

	// Invoked once before everything else on initial rendering. Values are
	// subsequently in this.state. We use this to set the initial state and
	// also to document what state fields are possible
	getInitialState: function() {
		return {
			backwardPiggy: false,
			keyNavEnabled: true,
			currentURL: null,
			// currentSearch exists in Index just to wire together the searchbox of Header, and the Map
			// aspect, so the Map aspect can add the zoom level to the predicate in the search box.
			currentSearch: '',
			currentSet: '',
			dropActive: false,
			selection: {},
			importShareURL: null,
			serverStatus: null,
			// we keep track of where a touch started, so we can
			// tell when the touch ends if we consider it a swipe. We
			// reset it to -1 whenever a touch ends.
			touchStartPosition: -1,

			// TODO: This should be calculated by whether selection is empty, and not need separate state.
			sidebarVisible: false,

			progressDialogVisible: false,
			totalBytesToUpload: 0,
			totalBytesComplete: 0,
			totalNodesToAdd: 0,
			nodesAlreadyAdded: 0,

			// messageDialogContents is for displaying a message to
			// the user. It is the child of getMessageDialog_(). To
			// display a message, set messageDialogContents to whatever
			// you want (div, string, etc), and set
			// messageDialogVisible to true.
			messageDialogContents: null,
			messageDialogVisible: false,
			// dialogWidth and dialogHeight should be set to accommodate the size of
			// the text message we display in the dialog.
			dialogWidth: 0,
			dialogHeight: 0,
		};
	},

	// render() is called by React every time a component is determined to need
	// re-rendering. This is typically caused by a call to setState() or a parent
	// component re-rendering.
	render: function() {
		var aspects = this.getAspects_();
		var selectedAspect = goog.array.findIndex(aspects, function(v) {
			if (v.fragment == this.state.currentURL.getFragment()) {
				return true;
			}
			// we favor the map aspect if a "map:" query parameter is found.
			if (v.fragment == 'map' && cam.MapUtils.hasZoomParameter(this.state.currentURL.getDecodedQuery())) {
				return true;
			}
			return false;
		}, this);

		if (selectedAspect == -1) {
			selectedAspect = 0;
		}

		var contentSize = new goog.math.Size(this.props.availWidth, this.props.availHeight - this.HEADER_HEIGHT_);
		return React.DOM.div({onDragEnter:this.handleDragStart_, onDragOver:this.handleDragStart_, onDrop:this.handleDrop_},
			this.getHeader_(aspects, selectedAspect),
			React.DOM.div(
				{
					className: 'cam-content-wrap',
					style: {
						top: this.HEADER_HEIGHT_,
					},
				},
				aspects[selectedAspect] && aspects[selectedAspect].createContent(contentSize, this.state.backwardPiggy)
			),
			this.getSidebar_(aspects[selectedAspect]),
			this.getProgressDialog_(),
			this.getMessageDialog_()
		);
	},

	setSelection_: function(selection) {
		this.props.history.replaceState(cam.object.extend(this.props.history.state, {
			selection: selection,
		}), '', this.props.location.href);

		this.setState({selection: selection});
		this.setState({sidebarVisible: !goog.object.isEmpty(selection)});
	},

	getTargetBlobref_: function(opt_url) {
		var url = opt_url || this.state.currentURL;
		var suffix = url.getPath().substr(this.baseURL_.getPath().length);

		// TODO(aa): Need to implement something like ref.go that knows about the other hash types.
		var match = suffix.match(this.BLOBREF_PATTERN_);
		return match && match[0];
	},

	getAspects_: function() {
		var target = this.getTargetBlobref_();
		var getAspect = function(f) {
			return f(target, this.targetSearchSession_);
		}.bind(this);

		var specificAspects = [
			cam.ImageDetail.getAspect,
			cam.AudioDetail.getAspect,
			cam.BrowserViewDetail.getAspect,
			this.getDirAspect_.bind(null),
		];

		var generalAspects = [
			this.getSearchAspect_.bind(null, specificAspects),
			cam.PermanodeDetail.getAspect.bind(null, this.props.serverConnection, this.props.timer, this.toggleKeyNavigation_),
			cam.MapAspect.getAspect.bind(
				null,
				this.props.config,
				this.props.serverConnection,
				this.props.availWidth,
				this.props.availHeight - this.HEADER_HEIGHT_,
				this.updateSearchBarOnMap_,
				this.setPendingQuery_,
				this.childSearchSession_,
			),
			cam.BlobDetail.getAspect.bind(null, this.getDetailURL_, this.props.serverConnection),
		];

		return specificAspects
			.concat(generalAspects)
			.map(getAspect)
			.filter(goog.functions.identity);
	},

	getSearchAspect_: function(specificAspects, blobref, targetSearchSession) {
		if (blobref) {
			var m = targetSearchSession.getMeta(blobref);
			if (!m || !m.permanode) {
				// We have a target, but it's not a permanode. So don't show the contents view.
				// TODO(aa): Maybe we do want to for directories though?
				return null;
			}

			// If the permanode already has children, we always show the container view.
			// Otherwise, show the container view only if there is no more specific type.
			var showSearchAspect = false;
			if (cam.permanodeUtils.isContainer(m.permanode)) {
				showSearchAspect = true;
			} else if (!cam.permanodeUtils.getCamliNodeType(m.permanode) && specificAspects.length == 0) {
				showSearchAspect = true;
			}

			if (!showSearchAspect) {
				return null;
			}
		}

		// This can happen when a user types a raw (JSON) query that is invalid.
		if (!this.childSearchSession_) {
			return null;
		}

		return {
			title: blobref ? 'Contents' : 'Search',
			fragment: blobref ? 'contents': 'search',
			createContent: this.getBlobItemContainer_.bind(this),
		};
	},

	getDirAspect_: function(targetBlobRef, parentSearchSession) {
		if (!targetBlobRef) {
			return null;
		}
		var m = parentSearchSession.getMeta(targetBlobRef);
		if (!m) {
			return null;
		}

		if (!m.permanode) {
			if (m.camliType != 'directory') {
				// we are neither a permanode, nor a directory
				return null;
			}
			var dirbr = targetBlobRef;
		} else {
			var rm = parentSearchSession.getResolvedMeta(targetBlobRef);
			if (!rm || rm.camliType != 'directory') {
				// a permanode, but does not contain a directory
				return null;
			}
			var dirbr = rm.blobRef;
		}

		return {
			title: 'Directory',
			fragment: 'directory',
			createContent: function() {
				var scale = (this.props.availWidth - this.SIDEBAR_OPEN_WIDTH_) / this.props.availWidth;
				return React.createElement(cam.DirContainer, {
				key: 'dircontainer',
				ref: 'dirContainer',
				config: this.props.config,
				blobRef: dirbr,
				availHeight: this.props.availHeight,
				availWidth: this.props.availWidth,
				detailURL: this.getDetailURL_,
				handlers: this.BLOB_ITEM_HANDLERS_,
				history: this.props.history,
				onSelectionChange: this.handleSelectionChange_,
				scale: scale,
				scaleEnabled: this.state.sidebarVisible,
				scrolling: this.props.scrolling,
				selection: this.state.selection,
				style: this.getBlobItemContainerStyle_(),
				thumbnailSize: this.THUMBNAIL_SIZE_,
				serverConnection: this.props.serverConnection,
				});
			}.bind(this),
		};
	},

	// updateSearchBarOnMap_ is called within the map aspect to keep both the search
	// bar and the URL bar in sync with what the current search is. In particular,
	// whenever the zoom level changes, the "map:" predicate which represents the
	// current zoom-level is updated too.
	updateSearchBarOnMap_: function(currentSearch) {
		this.setState({
			currentSearch: currentSearch,
		});
		var newURI = this.state.currentURL.clone().setQuery("q="+currentSearch).toString();
		this.props.history.replaceState(cam.object.extend(this.props.history.state), '', newURI);
	},

	setPendingQuery_: function(pending) {
		this.setState({pendingQuery: pending});
	},

	handleDragStart_: function(e) {
		this.clearDragTimer_();
		e.preventDefault();
		this.dragEndTimer_ = window.setTimeout(this.handleDragStop_, 2000);
		this.setState({
			dropActive: true,
			progressDialogVisible: false,
		});
	},

	handleDragStop_: function() {
		this.clearDragTimer_();
		this.setState({dropActive: false});
	},

	clearDragTimer_: function() {
		if (this.dragEndTimer_) {
			window.clearTimeout(this.dragEndTimer_);
			this.dragEndTimer_ = 0;
		}
	},

	onAddToSetStart_: function() {
		var numNodes = goog.object.getCount(this.state.selection);
		this.setState({
			progressDialogVisible: true,
			totalNodesToAdd: numNodes,
			nodesAlreadyAdded: 0,
		});

		console.log('Adding %d item(s) to set...', numNodes);
	},

	onAddMemberToSet_: function(ref) {
		var nodesAdded = this.state.nodesAlreadyAdded + 1;
		this.setState({
			nodesAlreadyAdded: nodesAdded
		});

		console.log('Added item to set: %s', ref);
	},

	onAddToSetComplete_: function(permanode) {
		if (this.state.totalNodesToAdd != this.state.nodesAlreadyAdded) {
			return;
		}
		console.log('Set creation complete!');
		this.setState({
			progressDialogVisible: false,
			totalNodesToAdd: 0,
		});
		this.setSelection_({});
		this.refreshIfNecessary_();
		this.navigator_.navigate(this.getDetailURL_(permanode));
	},

	onUploadStart_: function(files) {
		var numFiles = files.length;
		var totalBytes = Array.prototype.reduce.call(files, function(sum, file) { return sum + file.size; }, 0);

		this.setState({
			dropActive: false,
			totalBytesToUpload: totalBytes,
			totalBytesComplete: 0,
		});

		console.log('Uploading %d files (%d bytes)...', numFiles, totalBytes);
	},

	onUploadProgressUpdate_: function(file) {
		var completedBytes = this.state.totalBytesComplete + file.size;

		this.setState({
			totalBytesComplete: completedBytes
		});

		console.log('Completed %d of %d bytes', completedBytes, this.state.totalBytesToUpload);
	},

	onUploadComplete_: function() {
		console.log('Upload complete!');

		this.setState({
			totalBytesToUpload: 0,
			totalBytesComplete: 0,
			progressDialogVisible: false
		});
	},

	handleInputFiles_: function(e) {
		console.log(e.nativeEvent.target.files);
		if (!e.nativeEvent.target.files) {
			return;
		}

		e.preventDefault();

		var files = e.nativeEvent.target.files;
		this.handleFilesUpload_(e, files);
	},

	handleDrop_: function(e, files) {
		if (!e.nativeEvent.dataTransfer.files) {
			return;
		}

		e.preventDefault();

		var files = e.nativeEvent.dataTransfer.files;
		this.handleFilesUpload_(e, files);
	},

	handleFilesUpload_: function(e, files) {
		var sc = this.props.serverConnection;
		var parent = this.getTargetBlobref_();

		this.onUploadStart_(files);

		goog.Promise.all(
			Array.prototype.map.call(files, function(file) {
				return uploadFile(file)
					.then(fetchPermanodeIfExists)
					.then(createPermanodeIfNotExists)
					.then(updatePermanodeRef)
					.then(checkExistingCamliMembership)
					.then(createPermanodeAssociations)
					.thenCatch(function(e) {
						console.error('File upload fall down go boom. file: %s, error: %s', file.name, e);
					})
					.then(this.onUploadProgressUpdate_.bind(this, file));
			}.bind(this))
		).thenCatch(function(e) {
			console.error('File upload failed with error: %s', e);
		}).then(this.onUploadComplete_);

		function uploadFile(file) {

			// capture status of upload promise chain
			var status = {
				fileRef: '',
				isCamliMemberOfParent: false,
				parentRef: parent,
				permanodeRef: '',
				permanodeCreated: false
			};

			var uploadFile = new goog.Promise(sc.uploadFile.bind(sc, file));

			return goog.Promise.all([new goog.Promise.resolve(status), uploadFile]);
		}

		function fetchPermanodeIfExists(results) {
			var status = results[0];
			status.fileRef = results[1];

			var getPermanode = new goog.Promise(sc.getPermanodeWithContent.bind(sc, status.fileRef));

			return goog.Promise.all([new goog.Promise.resolve(status), getPermanode]);
		}

		function createPermanodeIfNotExists(results) {
			var status = results[0];
			var permanodeRef = results[1];

			if (!permanodeRef) {
				status.permanodeCreated = true;

				var createPermanode = new goog.Promise(sc.createPermanode.bind(sc));
				return goog.Promise.all([new goog.Promise.resolve(status), createPermanode]);
			}

			return goog.Promise.all([new goog.Promise.resolve(status), new goog.Promise.resolve(permanodeRef)]);
		}

		function updatePermanodeRef(results) {
			var status = results[0];
			status.permanodeRef = results[1];

			return goog.Promise.all([new goog.Promise.resolve(status)]);
		}

		// TODO(mpl): this implementation means that when we're dropping on a set, we send
		// one additional query for each permanode that already exists. So in the worst case,
		// it amounts to one additional query per dropped item (with a small payload/response).
		// Alternatively, we could ask (either by tweaking the search session, or
		// "manually") the server for all the set members and cache the response, which means
		// only one additional query, and we can then do all the tests locally. However, the
		// response size scales with the number of members in the set, so I don't know if it's
		// better. A working example is at
		// https://camlistore-review.googlesource.com/#/c/5345/2 . We should benchmark and/or
		// ask Brad.

		// check, when appropriate, if the permanode is already part of the set we're dropping in.
		function checkExistingCamliMembership(results) {
			var status = results[0];

			// Permanode did not exist before, so it couldn't be a member of any set.
			if (!status.parentRef || status.permanodeCreated) {
				return goog.Promise.all([new goog.Promise.resolve(status), new goog.Promise.resolve(false)]);
			}

			console.log('checking membership');
			var hasMembership = new goog.Promise(sc.isCamliMember.bind(sc, status.permanodeRef, status.parentRef));
			return goog.Promise.all([new goog.Promise.resolve(status), hasMembership]);
		}

		function createPermanodeAssociations(results) {
			var status = results[0];
			status.isCamliMemberOfParent = results[1];

			var promises = [];

			// associate uploaded file to new permanode
			if (status.permanodeCreated) {
				var setCamliContent = new goog.Promise(sc.newSetAttributeClaim.bind(sc, status.permanodeRef, 'camliContent', status.fileRef));
				promises.push(setCamliContent);
			}

			// add CamliMember relationship if viewing a set
			if (status.parentRef && !status.isCamliMemberOfParent) {
				var setCamliMember = new goog.Promise(sc.newAddAttributeClaim.bind(sc, status.parentRef, 'camliMember', status.permanodeRef));
				promises.push(setCamliMember);
			}

			return goog.Promise.all(promises);
		}
	},

	handleWillNavigate_: function(newURL) {
		if (!goog.string.startsWith(newURL.toString(), this.baseURL_.toString())) {
			return false;
		}

		// reset the currentSearch on navigation, since the map aspect modifies it on
		// panning/zooming.
		var targetBlobref = this.getTargetBlobref_(newURL);
		var currentSearch = '';
		if (targetBlobref) {
			currentSearch = 'ref:' + targetBlobref;
		} else if (newURL) {
			currentSearch = newURL.getParameterValue('q') || '';
		}

		this.updateTargetSearchSession_(targetBlobref, newURL);
		this.updateChildSearchSession_(targetBlobref, newURL);
		this.pruneSearchSessionCache_();
		this.setState({
			backwardPiggy: false,
			currentURL: newURL,
			currentSearch: currentSearch,
		});
		return true;
	},

	handleDidNavigate_: function() {
		var s = this.props.history.state && this.props.history.state.selection;
		this.setSelection_(s || {});
	},

	updateTargetSearchSession_: function(targetBlobref, newURL) {
		this.targetSearchSession_ = null;
		if (targetBlobref) {
			var opt_sort = "blobref";
			var query = this.queryAsBlob_(targetBlobref);
			var parentPermanode = newURL.getParameterValue('p');
			if (parentPermanode) {
				query = this.queryFromParentPermanode_(parentPermanode);
				opt_sort = "-created";
			} else {
				var queryString = newURL.getParameterValue('q');
				if (queryString) {
					opt_sort = null;
					query = this.queryFromSearchParam_(queryString);
				}
			}
			this.targetSearchSession_ = this.getSearchSession_(targetBlobref, query, opt_sort);
		}
	},

	queryAsBlob_: function(blobRef) {
		return {
			blobRefPrefix: blobRef,
		}
	},

	updateChildSearchSession_: function(targetBlobref, newURL) {
		var query = '';
		if (targetBlobref) {
			query = this.queryFromParentPermanode_(targetBlobref);
		} else {
			var queryString = newURL.getParameterValue('q');
			if (queryString) {
				query = this.queryFromSearchParam_(queryString);
			}
		}
		this.childSearchSession_ = this.getSearchSession_(null, query);
	},

	queryFromSearchParam_: function(queryString) {
		// TODO(aa): Remove this when the server can do something like the 'raw' operator.
		if (goog.string.startsWith(queryString, this.SEARCH_PREFIX_.RAW + ':')) {
			try {
				return JSON.parse(queryString.substring(this.SEARCH_PREFIX_.RAW.length + 1));
			} catch (e) {
				console.error('Raw search is invalid JSON', e);
				return null;
			}
		} else {
			return queryString;
		}
	},

	queryFromParentPermanode_: function(blobRef) {
		return {
			permanode: {
				relation: {
					relation: 'parent',
					any: { blobRefPrefix: blobRef },
				},
			},
		};
	},

	// Finds an existing cached SearchSession that meets criteria, or creates a new one.
	//
	// If opt_query is present, the returned query must be exactly equivalent.
	// If opt_targetBlobref is present, the returned query must have current results that contain opt_targetBlobref. Otherwise, the returned query must contain the first result.
	//
	// If only opt_targetBlobref is set, then any query that happens to currently contain that blobref is acceptable to the caller.
	getSearchSession_: function(opt_targetBlobref, opt_query, opt_sort) {
		// This whole business of reusing search session relies on the assumption that we use the same describe rules for both detail queries and search queries.
		var queryString = JSON.stringify(opt_query);

		var cached = goog.array.findIndex(this.searchSessionCache_, function(ss) {
			if (opt_targetBlobref) {
				if (!ss.getMeta(opt_targetBlobref)) {
					return false;
				}
				if (!opt_query && !opt_sort) {
					return true;
				}
			}

			if (JSON.stringify(ss.getQuery()) != queryString) {
				return false;
			}

			if (!opt_sort) {
				opt_sort = "-created"
			}
			if (ss.getSort() != opt_sort) {
				return false;
			}

			if (!opt_targetBlobref) {
				return !ss.getAround();
			}

			// If there's a targetBlobref, we require that it is not at the very edge of the results so that we can implement lefr/right in detail views.
			var targetIndex = goog.array.findIndex(ss.getCurrentResults().blobs, function(b) {
				return b.blob == opt_targetBlobref;
			});
			return (targetIndex > 0) && (targetIndex < (ss.getCurrentResults().blobs.length - 1));
		});

		if (cached > -1) {
			this.searchSessionCache_.splice(0, 0, this.searchSessionCache_.splice(cached, 1)[0]);
			return this.searchSessionCache_[0];
		}

		console.log('Creating new search session for query %s', queryString);
		var ss = new cam.SearchSession(this.props.serverConnection, this.baseURL_.clone(), opt_query,
			this.handleSearchQueryError_.bind(this), opt_targetBlobref, opt_sort);
		this.eh_.listen(ss, cam.SearchSession.SEARCH_SESSION_CHANGED, function() {
			this.forceUpdate();
		});
		this.eh_.listen(ss, cam.SearchSession.SEARCH_SESSION_STATUS, function(e) {
			this.setState({
				serverStatus: e.status,
			});
		});
		this.eh_.listen(ss, cam.SearchSession.SEARCH_SESSION_ERROR, function() {
			this.forceUpdate();
		});
		ss.loadMoreResults();
		this.searchSessionCache_.splice(0, 0, ss);
		return ss;
	},

	// handleSearchQueryError_ removes the last search query from the search session
	// cache, and displays the errorMsg in a dialog.
	handleSearchQueryError_: function(errorMsg) {
		this.searchSessionCache_.splice(0, 1);
		var nbl = errorMsg.length / 40; // 40 chars per line.
		this.setState({
			messageDialogVisible: true,
			dialogWidth: 40*16, // 16px char width, 40 chars width
			dialogHeight: (nbl+1)*1.5*16, // 16px char height, and 1.5 to account for line spacing
			messageDialogContents: React.DOM.div({
				style: {
					textAlign: 'center',
					fontSize: 'medium',
				},},
				React.DOM.div({}, errorMsg)
			),
		});
	},

	pruneSearchSessionCache_: function() {
		for (var i = this.SEARCH_SESSION_CACHE_SIZE_; i < this.searchSessionCache_.length; i++) {
			this.searchSessionCache_[i].close();
		}

		this.searchSessionCache_.length = Math.min(this.searchSessionCache_.length, this.SEARCH_SESSION_CACHE_SIZE_);
	},

	getHeader_: function(aspects, selectedAspectIndex) {
		var chooser = this.getChooser_(aspects);
		return React.createElement(cam.Header,
			{
				getCurrentSearch: function() {
					return this.state.currentSearch;
				}.bind(this),
				setCurrentSearch: function(e) {
					this.setState({currentSearch: e.target.value});
				}.bind(this),
				errors: this.getErrors_(),
				pendingQuery: this.state.pendingQuery,
				height: 38,
				helpURL: this.baseURL_.resolve(new goog.Uri(this.props.config.helpRoot)),
				mobileSetupURL: this.baseURL_.resolve(new goog.Uri("/mobile-setup")),
				homeURL: this.baseURL_,
				importersURL: this.baseURL_.resolve(new goog.Uri(this.props.config.importerRoot)),
				mainControls: chooser.map(function(val, idx) {
					return React.DOM.a(
						{
							key: val.title,
							className: classNames({
								'cam-header-main-control-active': idx == selectedAspectIndex,
							}),
							href: this.state.currentURL.clone().setFragment(val.fragment).toString(),
						},
						val.title
					);
				}, this),
				onUpload: this.handleUpload_,
				onNewPermanode: this.handleCreateSetWithSelection_,
				onImportShare: this.getImportShareDialog_,
				onAbout: this.handleAbout_,
				onSearch: this.setSearch_,
				favoritesURL: this.getFavoritesURL_(),
				statusURL: this.baseURL_.resolve(new goog.Uri(this.props.config.statusRoot)),
				ref: 'header',
				timer: this.props.timer,
				width: this.props.availWidth,
				config: this.props.config,
			}
		)
	},

	getChooser_: function(aspects) {
		// We don't show the chooser if there's only one thing to choose from.
		if (aspects.length == 1) {
			return [];
		}
		return aspects;
	},

	handleAbout_: function() {
		this.props.serverConnection.serverStatus(
			function(serverStatus) {
				var dialogText = 'This is the web interface to a Perkeep server';
				if (serverStatus.version) {
					dialogText += `\n\nPerkeep ${serverStatus.version}`;
				}
				if (serverStatus.goInfo) {
					dialogText += `\n\n${serverStatus.goInfo}`;
				}
				alert(dialogText);
			}.bind(this),
		);
	},

	handleNewPermanode_: function() {
		this.props.serverConnection.createPermanode(this.getDetailURL_.bind(this));
	},

	getFavoritesURL_: function() {
		return this.baseURL_.clone().setParameterValue(
			'q',
			this.SEARCH_PREFIX_.RAW + ':' + JSON.stringify({
				permanode: {
					attr: 'camliRoot',
					numValue: {
						min: 1
					}
				}
			})
		);
	},

	handleSelectAsCurrentSet_: function() {
		this.setState({
			currentSet: goog.object.getAnyKey(this.state.selection),
		});
		this.setSelection_({});
		alert('Now, select the items to add to this set and click "Add to picked set" in the sidebar.\n\n' +
			  'Sorry this is lame, we\'re working on it.');
	},

	handleAddToSet_: function() {
		this.addMembersToSet_(this.state.currentSet, goog.object.getKeys(this.state.selection));
	},

	handleUpload_: function() {
		this.setState({
			progressDialogVisible: true,
		});
	},

	handleCreateSetWithSelection_: function() {
		var selection = goog.object.getKeys(this.state.selection);
		this.props.serverConnection.createPermanode(function(permanode) {
			this.props.serverConnection.newSetAttributeClaim(permanode, 'title', 'New set', function() {
				this.addMembersToSet_(permanode, selection);
			}.bind(this));
		}.bind(this));
	},

	getImportShareDialog_: function() {
		this.setState({
			messageDialogVisible: true,
			messageDialogContents: React.DOM.div({
				style: {
					textAlign: 'center',
					position: 'relative',
				},},
				React.DOM.div({}, 'Import from a share URL'),
				React.DOM.div({},
					React.DOM.form({onSubmit: function(e) {
							e.preventDefault();
							this.props.serverConnection.importShare(
								this.state.importShareURL,
								function(){
									this.updateImportShareStatusLoop_();
								}.bind(this),
								function(error){
									this.updateImportShareDialog_(error);
								}.bind(this),
							);
						}.bind(this)},
						React.DOM.input({
							type: 'text',
							onChange: function(e) {
								this.setState({importShareURL: e.target.value});
							}.bind(this),
							placeholder: 'https://yourfriendserver/share/sha224-shareclaim',
							size: 40,
							style: {textAlign: 'center',},
						}),
						React.DOM.button({type: 'submit'}, 'Import')
					)
				)
			),
		});
	},

	updateImportShareStatusLoop_: function() {
		const updateDialog = function(progress){
			const blobRef = progress.BlobRef || '<invalid-blob.Ref>';
			if (progress.Running) {
				if (progress.Assembled) {
					this.updateImportShareDialog_("Importing file in progress", "");
					return;
				}
				this.updateImportShareDialog_(`Working - ${progress.FilesCopied}/${progress.FilesSeen} files imported`, "");
				return;
			}

			if (progress.Assembled) {
				this.updateImportShareDialog_("File successfully imported as", blobRef)
				return;
			}
			this.updateImportShareDialog_(`Done - ${progress.FilesCopied}/${progress.FilesSeen} files imported under`, blobRef);
		}.bind(this);

		this.props.serverConnection.importShareStatus(
			function(progress){
				updateDialog(progress);
				if (progress.Running) {
					window.setTimeout(this.updateImportShareStatusLoop_, 2 * 1000);
				}
			}.bind(this),
		);
	},

	updateImportShareDialog_: function(resultMessage, br) {
		if (!this.state.messageDialogVisible) {
			return;
		}
		if (br != "") {
			var imported = React.DOM.a({href: br}, br);
		}
		this.setState({
			messageDialogVisible: true,
			messageDialogContents: React.DOM.div({
				style: {
					textAlign: 'center',
					position: 'relative',
				},},
				React.DOM.div({}, ''+resultMessage),
				React.DOM.div({
					style: {
						fontSize: 'smaller',
					},},
					imported),
			),
		});
	},

	addMembersToSet_: function(permanode, blobrefs) {
		var sc = this.props.serverConnection;
		function addMemberToSet(br, pm) {
			return new goog.Promise(sc.newAddAttributeClaim.bind(sc, pm, 'camliMember', br));
		}

		this.onAddToSetStart_();
		goog.Promise.all(
			Array.prototype.map.call(blobrefs, function(br) {
				return addMemberToSet(br, permanode)
					.thenCatch(function(e) {
						console.error('Unable to add member to set. item: %s, error: %s', br, e);
					})
					.then(this.onAddMemberToSet_.bind(null, br));
			}.bind(this))
		).thenCatch(function(e) {
			console.error('Add members to set failed with error: %s', e);
		}).then(this.onAddToSetComplete_.bind(null, permanode));
	},

	handleClearSelection_: function() {
		this.setSelection_({});
	},

	handleDeleteSelection_: function() {
		// TODO(aa): Use promises.
		var blobrefs = goog.object.getKeys(this.state.selection);
		var msg = 'Delete';
		if (blobrefs.length > 1) {
			msg += goog.string.subs(' %s items?', blobrefs.length);
		} else {
			msg += ' item?';
		}
		if (!confirm(msg)) {
			return null;
		}

		var numDeleted = 0;
		blobrefs.forEach(function(br) {
			this.props.serverConnection.newDeleteClaim(br, function() {
				if (++numDeleted == blobrefs.length) {
					this.setSelection_({});
					this.refreshIfNecessary_();
				}
			}.bind(this));
		}.bind(this));
	},

	handleRemoveSelectionFromSet_: function() {
		var target = this.getTargetBlobref_();
		var permanode = this.targetSearchSession_.getMeta(target).permanode;
		var sc = this.props.serverConnection;
		var changes = [];

		for (var k in permanode.attr) {
			var values = permanode.attr[k];
			for (var i = 0; i < values.length; i++) {
				if (this.state.selection[values[i]]) {
					if (k == 'camliMember' || goog.string.startsWith(k, 'camliPath:')) {
						changes.push(new goog.Promise(sc.newDelAttributeClaim.bind(sc, target, k, values[i])));
					} else {
						console.error('Unexpected attribute: ', k);
					}
				}
			}
		}

		goog.Promise.all(changes).then(this.refreshIfNecessary_);
	},

	handleDownload_: function() {
		var files = [];
		goog.object.getKeys(this.state.selection).forEach(function(br) {
			var meta = this.childSearchSession_.getResolvedMeta(br);
			if (!meta) {
				return;
			}
			if (!meta.file && !meta.dir) {
				// br does not have a file or a directory description, so it's probably neither.
				return;
			}
			if (meta.file && !meta.file.fileName) {
				// looks like a file, but no file name
				return;
			}
			if (meta.dir && !meta.dir.fileName) {
				// looks like a dir, but no file name
				return;
			}
			files.push(meta.blobRef);
		}.bind(this));

		var downloadPrefix = this.props.config.downloadHelper;

		if (files.length < 2) {
			window.open(`${downloadPrefix}/${files[0]}`);
			return;
		}

		var input = document.createElement("input");
		input.type = "text";
		input.name = "files";
		input.value = files.join(",");

		var form = document.createElement("form");
		form.action = downloadPrefix;
		form.method = "POST";
		form.appendChild(input);

		// As per
		// https://html.spec.whatwg.org/multipage/forms.html#form-submission-algorithm
		// step 2., a form must be connected to the DOM for submission.
		var body = document.querySelector("body");
		body.appendChild(form);
		form.submit();
		body.removeChild(form);
	},

	handleOpenWindow_: function(url) {
		this.props.openWindow(url);
	},

	handleTouchstart_: function(e) {
		if (!this.targetSearchSession_) {
			return;
		}
		var touches = e.getBrowserEvent().changedTouches;
		for (var i = 0; i < touches.length; i++) {
			// TODO(mpl): maybe disregard (as a swipe) a touch that
			// starts e.g. on the top bar? But then the proper solution
			// is probably to register the touch listener on the image
			// container, instead of on the index page?
			this.setState({touchStartPosition: touches[i].pageX});
			// assume we only care about one finger/event for now
			break
		}
	},

	handleTouchend_: function(e) {
		if (!this.targetSearchSession_) {
			return;
		}
		if (this.state.touchStartPosition < 0) {
			return;
		}
		var touches = e.getBrowserEvent().changedTouches;
		var halfScreen = this.props.availWidth / 2;
		for (var i = 0; i < touches.length; i++) {
			var swipeLength = touches[i].pageX - this.state.touchStartPosition;
			if (Math.abs(swipeLength) < halfScreen) {
				// do nothing if half-hearted swipe
				this.setState({touchStartPosition: -1});
				return;
			}

			// swipe left == nav right
			var isRight = (swipeLength < 0);
			this.navigateLeftRight_(isRight);
			this.setState({
				backwardPiggy: (swipeLength > 0),
				touchStartPosition: -1,
			});
			// assume we only care about one finger/event for now
			break;
		}
	},

	handleKeyPress_: function(e) {
		if (e.target.tagName == 'INPUT' || e.target.tagName == 'TEXTAREA') {
			return;
		}

		switch (String.fromCharCode(e.charCode)) {
			case '/': {
				this.refs['header'].focusSearch();
				e.preventDefault();
				break;
			}

			case '|': {
				window.__debugConsoleClient = {
					getSelectedItems: function() {
						return this.state.selection;
					}.bind(this),
					serverConnection: this.props.serverConnection,
				};
				window.open('debug_console.html', 'debugconsole', 'width=400,height=300');
				break;
			}
		}
	},

	toggleKeyNavigation_: function(enabled) {
		this.setState({
			keyNavEnabled: enabled,
		});
	},

	handleKeyUp_: function(e) {
		if (!this.state.keyNavEnabled) {
			return;
		}
		var isEsc = (e.keyCode == 27);
		var isRight = (e.keyCode == 39);
		var isLeft = (e.keyCode == 37);

		if (isEsc) {
			// TODO: This isn't right, it should go back to the context URL if there is one.
			this.navigator_.navigate(this.baseURL_);
			return;
		}

		if (!isRight && !isLeft) {
			return;
		}

		if (!this.targetSearchSession_) {
			return;
		}

		this.navigateLeftRight_(isRight);
		this.setState({
			backwardPiggy: isLeft,
		});
	},

	navigateLeftRight_: function(isRight) {
		var blobs = this.targetSearchSession_.getCurrentResults().blobs;
		var target = this.getTargetBlobref_();
		var idx = goog.array.findIndex(blobs, function(item) {
			return item.blob == target;
		});

		if (isRight) {
			if (idx >= (blobs.length - 1)) {
				return;
			}
			idx++;
		} else {
			if (idx <= 0) {
				return;
			}
			idx--;
		}

		var url = this.getDetailURL_(blobs[idx].blob, this.state.currentURL.getFragment());
		['q', 'p'].forEach(function(p) {
			var v = this.state.currentURL.getParameterValue(p);
			if (v) {
				url.setParameterValue(p, v);
			}
		}, this);
		this.navigator_.navigate(url);
	},

	handleDetailURL_: function(blobref) {
		return this.getChildDetailURL_(blobref);
	},

	getChildDetailURL_: function(blobref, opt_fragment) {
		var query = this.state.currentURL.getParameterValue('q');
		var targetBlobref = this.getTargetBlobref_();
		var url = this.getDetailURL_(blobref, opt_fragment);
		if (targetBlobref) {
			url.setParameterValue('p', targetBlobref);
		} else if (query && query != '') {
			url.setParameterValue('q', query);
		}
		return url;
	},

	getDetailURL_: function(blobref, opt_fragment) {
		var query = this.state.currentURL.getParameterValue('q');
		var targetBlobref = this.getTargetBlobref_();
		// TODO(mpl): now that "ref:refprefix" searches are fully supported, maybe we
		// could replace the mix of path+query URLs, with just query based URLs?
		return url = this.baseURL_.clone().setPath(this.baseURL_.getPath() + blobref).setFragment(opt_fragment || '');
	},

	setSearch_: function(query) {
		var searchURL;
		var match = query.match(/^ref:(.+)/);
		if (match && cam.blobref.isBlobRef(match[1])) {
			searchURL = this.getDetailURL_(match[1]);
		} else {
			searchURL = this.baseURL_.clone().setParameterValue('q', query);
		}
		this.navigator_.navigate(searchURL);
	},

	getSelectAsCurrentSetItem_: function() {
		if (goog.object.getCount(this.state.selection) != 1) {
			return null;
		}

		var blobref = goog.object.getAnyKey(this.state.selection);
		var m = this.childSearchSession_.getMeta(blobref);
		if (!m || m.camliType != 'permanode') {
			return null;
		}

		return React.DOM.button(
			{
				key:'selectascurrent',
				onClick:this.handleSelectAsCurrentSet_
			},
			'Add items to set'
		);
	},

	getAddToCurrentSetItem_: function() {
		if (!this.state.currentSet) {
			return null;
		}

		return React.DOM.button(
			{
				key:'addtoset',
				onClick:this.handleAddToSet_
			},
			'Add to picked set'
		);
	},

	getCreateSetWithSelectionItem_: function() {
		return React.DOM.button(
			{
				key:'createsetwithselection',
				onClick:this.handleCreateSetWithSelection_
			},
			'Create set with items'
		);
	},

	getClearSelectionItem_: function() {
		return React.DOM.button(
			{
				key:'clearselection',
				onClick:this.handleClearSelection_
			},
			'Clear selection'
		);
	},

	getDeleteSelectionItem_: function() {
		return React.DOM.button(
			{
				key:'deleteselection',
				onClick:this.handleDeleteSelection_
			},
			'Delete items'
		);
	},

	getRemoveSelectionFromSetItem_: function() {
		if (!goog.object.getAnyKey(this.state.selection)) {
			return null;
		}

		var target = this.getTargetBlobref_();
		if (!target) {
			return null;
		}

		var meta = this.targetSearchSession_.getMeta(target);
		if (!meta || !meta.permanode) {
			return null;
		}

		if (!cam.permanodeUtils.isContainer(meta.permanode)) {
			return null;
		}

		return React.DOM.button(
			{
				key: 'removeSelectionFromSet',
				onClick: this.handleRemoveSelectionFromSet_,
			},
			'Remove from set'
		);
	},

	getViewOriginalSelectionItem_: function() {
		if (goog.object.getCount(this.state.selection) != 1) {
			return null;
		}

		var blobref = goog.object.getAnyKey(this.state.selection);
		var rm = this.childSearchSession_.getResolvedMeta(blobref);
		if (!rm || !rm.file) {
			return null;
		}

		var fileName = '';
		if (rm.file.fileName) {
			fileName = goog.string.subs('/%s', rm.file.fileName);
		}

		var downloadUrl = goog.string.subs('%s%s%s?inline=1', this.props.config.downloadHelper, rm.blobRef, fileName);
		return React.DOM.button(
			{
				key:'viewSelection',
				onClick: this.handleOpenWindow_.bind(null, downloadUrl),
			},
			'View original'
		);
	},

	getDownloadSelectionItem_: function() {
		return React.DOM.button(
			{
				key: "download",
				onClick: this.handleDownload_,
			},
			"Download",
		)
	},

	getShareSelectionItem_: function() {
		const shareRoot = this.props.config.shareRoot;
		if (!shareRoot) {
			return;
		}
		return React.DOM.button(
			{
				key: "shareItemsButton",
				onClick: this.handleShareItems_,
			},
			"Share",
		);
	},

	handleShareItems_: function() {
		const getSelection = function() {
			var selection = goog.object.getKeys(this.state.selection);
			var files = [];
			selection.forEach(function(br) {
				var meta = this.childSearchSession_.getResolvedMeta(br);
				if (!meta) {
					return;
				}
				if (meta.dir) {
					files.push({'blobRef': meta.blobRef, 'isDir': true});
					return;
				}
				if (meta.file) {
					files.push({'blobRef': meta.blobRef, 'isDir': false});
					return;
				}
			}.bind(this))
			return files;
		}.bind(this);

		const showSharedURL = function(sharedURL) {
			const anchorText = sharedURL.substring(0,20) + "..." + sharedURL.slice(-20);
			this.setState({
				messageDialogVisible: true,
				messageDialogContents: React.DOM.div({
					style: {
						textAlign: 'center',
						position: 'relative',
					},},
					React.DOM.div({}, 'Share URL:'),
					React.DOM.div({}, React.DOM.a({href: sharedURL}, anchorText))
				),
			});
		}.bind(this);

		// newShareClaim creates, signs and uploads a transitive haveref share claim
		// for sharing the target item. It passes to cb the ref of the claim.
		const newShareClaim = function(fileRef, cb) {
			this.props.serverConnection.newShareClaim(
				"haveref",
				true,
				fileRef,
				function(claimRef){
					cb(claimRef);
				}.bind(this),
			);
		}.bind(this);

		// shareFile passes to cb the URL that can be used to share the target item.
		// If the item is a file, the URL can be used directly to fetch the file.
		// If the item is a directory, the URL should be used with pk-get -shared.
		const shareFile = function(fileRef, isDir, cb) {
			newShareClaim(
				fileRef,
				function(claimRef){
					const shareRoot = this.props.config.shareRoot;
					let shareURL = "";
					if (isDir) {
						shareURL = `${shareRoot}${claimRef}`;
					} else {
						shareURL = `${shareRoot}${fileRef}?via=${claimRef}&assemble=1`;
					}
					cb(shareURL);
				}.bind(this),
			);
		}.bind(this);

		// mkdir creates a new directory blob, with children composing its
		// static-set, and uploads it. It passes to cb the blobRef of the new
		// directory.
		const mkdir = function(children, cb) {
			this.props.serverConnection.newStaticSet(
				children,
				null,
				function(ssRef) {
					cb(ssRef);
				}.bind(this),
			)
		}.bind(this);

		const selection = getSelection();

		const fileRefs = [];
		let isDir = false;
		for (const item of selection) {
			fileRefs.push(item.blobRef);
			isDir = item.isDir;
		}

		if (fileRefs.length === 1) {
			shareFile(fileRefs[0], isDir, showSharedURL);
			return;
		}

		mkdir(
			fileRefs,
			function(dirRef){
				// TODO(mpl): should we bother deleting the dir and static set if
				// there's any failure from this point on? I think that as long as there's
				// no share claim referencing them, they're supposed to be GCed eventually,
				// aren't they? in which case, no need to worry about it.
				shareFile(dirRef, true, showSharedURL);
			}.bind(this),
		);
	},

	getSelectAllItem_: function() {
		// Don't display the button when we are on the "main" page
		// with no search.
		const selectionQuery = this.getSelectionQuery_();
		if (selectionQuery === '') {
			return;
		}
		return React.DOM.button(
			{
				key: "selectallBtnSidebar",
				onClick: this.handleSelectAll_,
			},
			"Select all",
		);
	},

	handleSelectAll_: function() {
		// Find all permanodes matching the current search session.
		const sc = this.props.serverConnection;
		const selectionQuery = this.getSelectionQuery_();

		const query = function() {
			if (!selectionQuery.startsWith("ref:")) {
				return selectionQuery;
			}
			// If we've got a 'ref:' predicate, assume the given blobRef is a container, and
			// find its children.
			const blobRef = selectionQuery.split(":")[1];
			return {
				"permanode": {
					"relation": {
						"Relation": "parent",
						"Any": {
							"blobRefPrefix": blobRef,
						},
					}
				},
			};
		}.bind(this)();

		sc.search(
			query,
			{
				limit: -1
			},
			function(response){
				const newSelection = {};
				for (const blob of response.blobs) {
					newSelection[blob.blob] = true;
				}
				this.setSelection_(newSelection);
			}.bind(this),
		);
	},

	getSelectionQuery_: function() {
		const target = this.getTargetBlobref_();
		let query = '';
		if (target) {
			query = 'ref:' + target;
		} else {
			query = this.state.currentURL.getParameterValue('q') || '';
		}
		return query;
	},

	getSidebar_: function(selectedAspect) {
		if (selectedAspect) {
			if (selectedAspect.fragment == 'search' || selectedAspect.fragment == 'contents') {
				var count = goog.object.getCount(this.state.selection);
				return React.createElement(cam.Sidebar, {
					isExpanded: this.state.sidebarVisible,
					header: React.DOM.span(
						{
							className: 'header',
						},
						goog.string.subs('%s selected item%s', count, count > 1 ? 's' : '')
					),
					mainControls: [
						{
							"displayTitle": "Update tags",
							"control": this.getTagsControl_()
						}
					].filter(goog.functions.identity),
					selectionControls: [
						this.getSelectAllItem_(),
						this.getClearSelectionItem_(),
						this.getCreateSetWithSelectionItem_(),
						this.getSelectAsCurrentSetItem_(),
						this.getAddToCurrentSetItem_(),
						this.getRemoveSelectionFromSetItem_(),
						this.getDeleteSelectionItem_(),
						this.getViewOriginalSelectionItem_(),
						this.getDownloadSelectionItem_(),
						this.getShareSelectionItem_(),
					].filter(goog.functions.identity),
					selectedItems: this.state.selection
				});
			}
		}

		return null;
	},

	getTagsControl_: function() {
		return React.createElement(cam.TagsControl, {
			selectedItems: this.state.selection,
			searchSession: this.childSearchSession_,
			serverConnection: this.props.serverConnection
		});
	},

	getMessageDialog_: function() {
		if (!this.state.messageDialogVisible) {
			return null;
		}

		var borderWidth = 18;
		var w = this.state.dialogWidth;
		var h = this.state.dialogHeight;
		if (w == 0 || h == 0) {
			// arbitrary defaults
			w = 50*16;
			h = 10*16;
		}

		return React.createElement(cam.Dialog, {
				availWidth: this.props.availWidth,
				availHeight: this.props.availHeight,
				width: w,
				height: h,
				borderWidth: borderWidth,
				onClose: function() {
					this.setState({
						messageDialogVisible: false,
						messageDialogContents: null,
						importShareURL: null,
						dialogWidth: 0,
						dialogHeight: 0,
					});
				}.bind(this),
			},
			this.state.messageDialogContents
		);
	},

	isUploading_: function() {
		return this.state.totalBytesToUpload > 0;
	},


	isAddingMembers_: function() {
		return this.state.totalNodesToAdd > 0;
	},

	getProgressDialog_: function() {
		if (!this.state.progressDialogVisible) {
			return false
		}

		var keepyWidth = 118;
		var keepyHeight = 108;
		var borderWidth = 18;
		var w = this.props.availWidth * 0.8;
		var h = this.props.availHeight * 0.8;
		var iconProps = {
			key: 'icon',
			sheetWidth: 6,
			spriteWidth: keepyWidth,
			spriteHeight: keepyHeight,
			style: {
				marginRight: 3,
				position: 'relative',
				display: 'inline-block',
			}
		};

		function getInputFiles() {
			if (this.isUploading_() || this.isAddingMembers_()) {
				return null;
			}
			return React.DOM.div(
				{},
				getInputFilesText.call(this),
				getInputFilesButton.call(this)
			)
		}

		function getInputFilesText() {
			// TODO: It does not have to be a div (it could be just
			// a string), but it's easier to make it a div than to
			// figure out the CSS to display it on its own line,
			// horizontally centered.
			return React.DOM.div(
				{},
				function() {
					return 'or select files: ';
				}.call(this)
			)
		}

		function getInputFilesButton() {
			return React.DOM.input(
			{
				type: "file",
				id: "fileupload",
				multiple: "true",
				name: "file",
				onChange: this.handleInputFiles_
			})
		}

		function getIcon() {
			if (this.isUploading_() || this.isAddingMembers_()) {
				return React.createElement(cam.SpritedAnimation, cam.object.extend(iconProps, {
					numFrames: 12,
					startFrame: 3,
					interval: 100,
					src: 'keepy/keepy-dancing.png',
				}));
			} else if (this.state.dropActive) {
				// TODO(mpl): keepy expressing interest.
				return React.createElement(cam.SpritedImage, cam.object.extend(iconProps, {
					index: 3,
					src: 'keepy/keepy-dancing.png',
				}));
			} else {
				return React.createElement(cam.SpritedImage, cam.object.extend(iconProps, {
					index: 3,
					src: 'keepy/keepy-dancing.png',
				}));
			}
		}

		function getText() {
			// TODO: It does not have to be a div (it could be just
			// a string), but it's easier to make it a div than to
			// figure out the CSS to display it on its own line,
			// horizontally centered.
			return React.DOM.div(
				{},
				function() {
					if (this.isAddingMembers_()) {
						return goog.string.subs('%s of %s items added',
							this.state.nodesAlreadyAdded,
							this.state.totalNodesToAdd);
					} else if (this.isUploading_()) {
						return goog.string.subs('Uploaded %s (%s%)',
							goog.format.numBytesToString(this.state.totalBytesComplete, 2),
							getUploadProgressPercent.call(this));
					} else {
						return 'Drop files here to upload,';
					}
				}.call(this)
			)
		}

		function getUploadProgressPercent() {
			if (!this.state.totalBytesToUpload) {
				return 0;
			}

			return Math.round(100 * (this.state.totalBytesComplete / this.state.totalBytesToUpload));
		}

		return React.createElement(cam.Dialog, {
				availWidth: this.props.availWidth,
				availHeight: this.props.availHeight,
				width: w,
				height: h,
				borderWidth: borderWidth,
				onClose: this.state.progressDialogVisible ? this.handleCloseProgressDialog_ : null,
			},
			React.DOM.div(
				{
					className: 'cam-index-upload-dialog',
					style: {
						textAlign: 'center',
						position: 'relative',
						left: -keepyWidth / 2,
						top: (h - keepyHeight - borderWidth * 2) / 2,
					},
				},
				getIcon.call(this),
				getText.call(this),
				getInputFiles.call(this)
			)
		);
	},

	handleCloseProgressDialog_: function() {
		this.setState({
			progressDialogVisible: false,
		});
	},

	handleSelectionChange_: function(newSelection) {
		this.setSelection_(newSelection);
	},

	getBlobItemContainer_: function() {
		var sidebarClosedWidth = this.props.availWidth;
		var sidebarOpenWidth = sidebarClosedWidth - this.SIDEBAR_OPEN_WIDTH_;
		var scale = sidebarOpenWidth / sidebarClosedWidth;

		return React.createElement(cam.BlobItemContainerReact, {
			key: 'blobitemcontainer',
			ref: 'blobItemContainer',
			availHeight: this.props.availHeight,
			availWidth: this.props.availWidth,
			detailURL: this.handleDetailURL_,
			handlers: this.BLOB_ITEM_HANDLERS_,
			history: this.props.history,
			onSelectionChange: this.handleSelectionChange_,
			scale: scale,
			scaleEnabled: this.state.sidebarVisible,
			scrolling: this.props.scrolling,
			searchSession: this.childSearchSession_,
			selection: this.state.selection,
			style: this.getBlobItemContainerStyle_(),
			thumbnailSize: this.THUMBNAIL_SIZE_,
		});
	},

	getBlobItemContainerStyle_: function() {
		return {
			left: 0,
			overflowY: this.state.dropActive ? 'hidden' : '',
			position: 'absolute',
			top: 0,
		};
	},

	getContentWidth_: function() {
		return this.props.availWidth;
	},

	refreshIfNecessary_: function() {
		if (this.targetSearchSession_) {
			this.targetSearchSession_.refreshIfNecessary();
		}
		if (this.childSearchSession_) {
			this.childSearchSession_.refreshIfNecessary();
		}
	},

	getErrors_: function() {
		var errors = (this.state.serverStatus && this.state.serverStatus.errors) || [];
		if ((this.targetSearchSession_ && this.targetSearchSession_.hasSocketError()) ||
			(this.childSearchSession_ && this.childSearchSession_.hasSocketError())) {
			errors.push({
				error: 'WebSocket error - click to reload',
				onClick: this.props.location.reload.bind(this.props.location, true),
			});
		}
		return errors;
	},
});
