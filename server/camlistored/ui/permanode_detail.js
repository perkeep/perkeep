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

goog.provide('cam.PermanodeDetail');

goog.require('goog.array');
goog.require('goog.labs.Promise');
goog.require('goog.object');

goog.require('cam.ServerConnection');

cam.PermanodeDetail = React.createClass({
	displayName: 'PermanodeDetail',

	propTypes: {
		meta: React.PropTypes.object.isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		timer: React.PropTypes.shape({
			setTimeout: React.PropTypes.func.isRequired,
		}).isRequired,
	},

	getInitialState: function() {
		return {
			newRow: {},
			rows: this.getInitialRows_(),
			sortBy: 'name',
			sortAsc: true,
			status: '',
		};
	},

	render: function() {
		return React.DOM.div({className: 'cam-permanode-detail'},
			React.DOM.h1(null, 'Current attributes'),
			this.getAttributesTable_(),
			this.getStatus_()
		);
	},

	getStatus_: function() {
		if (this.state.status) {
			return React.DOM.div(
				{className: 'cam-permanode-detail-status'},
				this.state.status
			);
		} else {
			return null;
		}
	},

	getInitialRows_: function() {
		var rows = [];
		for (var name in this.props.meta.permanode.attr) {
			var values = this.props.meta.permanode.attr[name];
			for (var i = 0; i < values.length; i++) {
				rows.push({
					'name': name,
					'value': values[i],
				});
			}
		}
		return rows;
	},

	getAttributesTable_: function() {
		var headerText = function(name, column) {
			var children = [name];
			if (this.state.sortBy == column) {
				children.push(
					React.DOM.i({
						key: goog.string.subs('%s-sort-icon', name),
						className: React.addons.classSet({
							'fa': true,
							'fa-caret-up': this.state.sortAsc,
							'fa-caret-down': !this.state.sortAsc,
						}),
					})
				);
			}
			return React.DOM.span(null, children);
		}.bind(this);

		var header = function(content, onclick) {
			return React.DOM.th(
				{
					className: 'cam-unselectable',
					onClick: onclick,
				},
				content
			);
		};

		return React.DOM.table(null,
			React.DOM.tbody(null,
				React.DOM.tr(
					{key: 'header'},
					header(headerText('Name', 'name'), this.handleSort_.bind(null, 'name')),
					header(headerText('Value', 'value'), this.handleSort_.bind(null, 'value')),
					header('')
				),
				cam.PermanodeDetail.AttributeRow({
					className: 'cam-permanode-detail-new-row',
					key: 'new',
					onBlur: this.handleBlur_,
					onChange: this.handleChange_,
					row: this.state.newRow,
				}),
				this.state.rows.map(function(r, i) {
					return cam.PermanodeDetail.AttributeRow({
						key: i,
						onBlur: this.handleBlur_,
						onChange: this.handleChange_,
						onDelete: this.handleDelete_.bind(null, r),
						row: r,
					});
				}, this)
			)
		);
	},

	handleChange_: function(row, column, e) {
		row[column] = e.target.value;
		this.forceUpdate();
	},

	handleDelete_: function(row) {
		this.setState({
			rows: this.state.rows.filter(function(r) { return r != row; }),
		}, function() {
			this.commitChanges_();
		}.bind(this));
	},

	handleBlur_: function(row) {
		if (row == this.state.newRow) {
			if (row.name && row.value) {
				this.state.rows.splice(0, 0, row);
				this.state.newRow = {};
				this.forceUpdate();
				this.commitChanges_();
			}
		} else {
			this.commitChanges_();
		}
	},

	handleSort_: function(sortBy) {
		var sortAsc = true;
		if (this.state.sortBy == sortBy) {
			sortAsc = !this.state.sortAsc;
		}
		this.setState({
			rows: this.getSortedRows_(sortBy, sortAsc),
			sortAsc: sortAsc,
			sortBy: sortBy,
		});
	},

	getSortedRows_: function(sortBy, sortAsc) {
		var numericSort = function(a, b) {
			return parseFloat(a) - parseFloat(b);
		}
		var stringSort = function(a, b) {
			return a.localeCompare(b);
		}

		var rows = goog.array.clone(this.state.rows);
		var sort = rows.some(function(r) {
			return isNaN(parseFloat(r[sortBy]));
		}) ? stringSort : numericSort;

		rows.sort(function(a, b) {
			if (!sortAsc) {
				var tmp = a;
				a = b;
				b = tmp;
			}
			return sort(a[sortBy], b[sortBy]);
		});

		return rows;
	},

	getChanges_: function() {
		var key = function(r) {
			return r.name + ':' + r.value;
		};
		var before = goog.array.toObject(this.getInitialRows_(), key);
		var after = goog.array.toObject(this.state.rows, key);

		var adds = goog.object.filter(after, function(v, k) { return !(k in before); });
		var deletes = goog.object.filter(before, function(v, k) { return !(k in after); });

		return {
			adds: goog.object.getValues(adds),
			deletes: goog.object.getValues(deletes),
		};
	},

	commitChanges_: function() {
		var changes = this.getChanges_();
		if (changes.adds.length == 0 && changes.deletes.length == 0) {
			return;
		}
		this.setState({
			status: 'Saving...',
		});
		var promises = changes.adds.map(function(add) {
			return new goog.labs.Promise(this.props.serverConnection.newAddAttributeClaim.bind(this.props.serverConnection, this.props.meta.blobRef, add.name, add.value));
		}, this).concat(changes.deletes.map(function(del) {
			return new goog.labs.Promise(this.props.serverConnection.newDelAttributeClaim.bind(this.props.serverConnection, this.props.meta.blobRef, del.name, del.value));
		}, this));
		goog.labs.Promise.all(promises).then(function() {
			this.props.timer.setTimeout(function() {
				this.setState({
					status: '',
				});
			}.bind(this), 500);
		}.bind(this));
	}
});

cam.PermanodeDetail.AttributeRow = React.createClass({
	displayName: 'AttributeRow',

	propTypes: {
		className: React.PropTypes.string,
		onBlur: React.PropTypes.func,
		onDelete: React.PropTypes.func,
		onChange: React.PropTypes.func.isRequired,
		row: React.PropTypes.object,
	},

	render: function() {
		var deleteButton = function(onDelete) {
			if (onDelete) {
				return React.DOM.i({
					className: 'fa fa-times-circle-o cam-permanode-detail-delete-attribute',
					onClick: onDelete,
				});
			} else {
				return null;
			}
		};

		return React.DOM.tr(
			{
				className: this.props.className,
				onBlur: this.props.onBlur && this.props.onBlur.bind(null, this.props.row),
			},
			React.DOM.td(null,
				React.DOM.input({
					onChange: this.props.onChange.bind(null, this.props.row, 'name'),
					placeholder: this.props.row.name ? '': 'New attribute name',
					type: 'text',
					value: this.props.row.name || '',
				})
			),
			React.DOM.td(null,
				React.DOM.input({
					onChange: this.props.onChange.bind(null, this.props.row, 'value'),
					placeholder: this.props.row.value ? '' : 'New attribute value',
					type: 'text',
					value: this.props.row.value || '',
				})
			),
			React.DOM.td(null, deleteButton(this.props.onDelete))
		);
	},
});

cam.PermanodeDetail.getAspect = function(serverConnection, timer, blobref, targetSearchSession) {
	if (!targetSearchSession) {
		return null;
	}

	var pm = targetSearchSession.getMeta(blobref);
	if (!pm || pm.camliType != 'permanode') {
		return null;
	}

	return {
		fragment: 'permanode',
		title: 'Permanode',
		createContent: function(size) {
			return cam.PermanodeDetail({
				meta: pm,
				serverConnection: serverConnection,
				timer: timer,
			});
		},
	};
};
