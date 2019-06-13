/*
Copyright 2019 The Perkeep Authors

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

goog.provide('cam.DropArea');

/**
 * Generic component that abstracts away drag-and-drop implementation.
 */
cam.DropArea = React.createClass({
	displayName: 'DropArea',

	propTypes: {
		/**
		 * Callback is called with `Promise<File[]>`
		 */
		onDrop: React.PropTypes.func.isRequired,
		/**
		 * Children are the default drop target
		 */
		children: React.PropTypes.Node,
		/**
		 * Custom drop target overrides the default drop target
		 * 
		 * Set it to `document.body` if you want the user to be able to drop anywhere
		 */
		target: React.PropTypes.instanceOf(Element),
		/**
		 * Callback is called with `true` if the drag overlay is shown, `false` otherwise
		 */
		onDragOverlayShown: React.PropTypes.func.isRequired,
		/**
		 * Rendered when user is dragging over drop target
		 */
		dragOverlay: React.PropTypes.Node,
	},

	getInitialState: function() {
		this.setDropTarget_(this.props.target);
		return {
			isDragOverlayShown: false,
		};
	},

	componentDidMount: function() {
		this.setIsDragOverlayShownThrottled = goog.functions.throttle(this.setIsDragOverlayShown, 33, this);
		this.dropTarget.addEventListener('dragenter', this.handleDragEnter_)
		this.dropTarget.addEventListener('dragleave', this.handleDragLeave_)
		this.dropTarget.addEventListener('dragover', this.handleDragOver_)
		this.dropTarget.addEventListener('drop', this.handleDrop_)
	},

	componentDidUpdate(prevProps) {
		if (prevProps.target !== this.props.target && this.props.target) {
			this.setDropTarget_(this.props.target);
		}
	},

	componentWillUnmount: function() {
		this.dropTarget.removeEventListener('dragenter', this.handleDragEnter_)
		this.dropTarget.removeEventListener('dragleave', this.handleDragLeave_)
		this.dropTarget.removeEventListener('dragover', this.handleDragOver_)
		this.dropTarget.removeEventListener('drop', this.handleDrop_)
	},

	render: function() {
		return this.props.children != null || this.props.dragOverlay != null
			? React.DOM.div({
					ref: this.props.target == null
						? this.setDropTarget_
						: null,
				},
				this.props.children,
				this.state.isDragOverlayShown
					? this.props.dragOverlay
					: null,
			)
			: null;
	},

	setDropTarget_: function (element) {
		// Always prefer target prop over ref
		this.dropTarget = this.props.target || element;
	},

	setIsDragOverlayShown: function(isDragOverlayShown) {
		this.setState({
			isDragOverlayShown: isDragOverlayShown,
		});
		this.props.onDragOverlayShown(isDragOverlayShown);
	},

	handleDragEnter_: function(event) {
		event.preventDefault();
		this.setIsDragOverlayShownThrottled(true);
	},

	handleDragLeave_: function(event) {
		event.preventDefault();
		var related = event.relatedTarget;
		var inside = false;
	
		if (related !== this.dropTarget) {
			if (related) {
				inside = goog.dom.contains(this.dropTarget, related);
			}
	
			if (!inside) {
				this.setIsDragOverlayShownThrottled(false);
			}
		}
	},

	handleDragOver_: function(event) {
		event.preventDefault();
		this.setIsDragOverlayShownThrottled(true);
	},

	handleDrop_: function(event) {
		event.preventDefault();
		this.setIsDragOverlayShownThrottled(true);

		var files = null;

		if (DataTransferItem != null && DataTransferItem.prototype.webkitGetAsEntry) {
			files = this.handleDragAndDropEntries_(
				goog.array.map(
					event.dataTransfer.items,
					function(item) {
						return item.webkitGetAsEntry();
					}
				)
			);
		} else {
			files = goog.Promise.resolve(Array.from(event.dataTransfer.files));
		}

		this.props.onDrop(files);
	},

	/**
	 * Tries to use a non-standard feature that Chrome supports to drag-and-drop directories.
	 * @param {DataTransferItem[]} items 
	 * @see {@link https://wiki.whatwg.org/wiki/DragAndDropEntries}
	 * @returns {goog.Promise<File[]>}
	 */
	handleDragAndDropEntries_: function findAllFiles(entries) {
		if (entries.length === 0) {
			return Promise.resolve([]);
		}

		var filePromises = [];
		var dirPromises = [];
		while (entries.length > 0) {
			var entry = entries.shift();
			if (entry != null) {
				if (entry.isFile) {
					filePromises.push(new goog.Promise(function(resolve) {
						entry.file(resolve);
					}));
				} else if (entry.isDirectory) {
					var dirReader = entry.createReader();
					dirPromises.push(new goog.Promise(function(resolve) {
						dirReader.readEntries(resolve);
					}));
				}
			}
		}

		return goog.Promise.all(dirPromises)
			.then(function(dirEntries) {
				return goog.array.flatten(dirEntries);
			})
			.then(function(dirEntries) {
				return findAllFiles(dirEntries);
			})
			.then(function(dirFiles) {
				return goog.Promise.all(filePromises)
					.then(function(files) {
						return goog.array.concat(files, dirFiles);
					});
			});
	},
});
