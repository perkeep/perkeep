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

goog.provide('cam.AnimationLoop');

goog.require('goog.events.EventTarget');

// Provides an easier-to-use interface around window.requestAnimationFrame(), and abstracts away browser differences.
// @param {Window} win
cam.AnimationLoop = function(win) {
	goog.base(this);

	this.win_ = win;

	this.requestAnimationFrame_ = win.requestAnimationFrame || win.mozRequestAnimationFrame || win.webkitRequestAnimationFrame || win.msRequestAnimationFrame;

	this.handleFrame_ = this.handleFrame_.bind(this);

	this.lastTimestamp_ = 0;

	if (this.requestAnimationFrame_) {
		this.requestAnimationFrame_ = this.requestAnimationFrame_.bind(win);
	} else {
		this.requestAnimationFrame_ = this.simulateAnimationFrame_.bind(this);
	}
};

goog.inherits(cam.AnimationLoop, goog.events.EventTarget);

cam.AnimationLoop.FRAME_EVENT_TYPE = 'frame';

cam.AnimationLoop.prototype.isRunning = function() {
	return Boolean(this.lastTimestamp_);
};

cam.AnimationLoop.prototype.start = function() {
	if (this.isRunning()) {
		return;
	}

	this.lastTimestamp_ = -1;
	this.schedule_();
};

cam.AnimationLoop.prototype.stop = function() {
	this.lastTimestamp_ = 0;
};

cam.AnimationLoop.prototype.schedule_ = function() {
	this.requestAnimationFrame_(this.handleFrame_);
};

cam.AnimationLoop.prototype.handleFrame_ = function(opt_timestamp) {
	if (this.lastTimestamp_ == 0) {
		return;
	}

	var timestamp = opt_timestamp || new Date().getTime();
	if (this.lastTimestamp_ == -1) {
		this.lastTimestamp_ = timestamp;
	} else {
		this.dispatchEvent({
			type: this.constructor.FRAME_EVENT_TYPE,
			delay: timestamp - this.lastTimestamp_
		});
		this.lastTimestamp_ = timestamp;
	}

	this.schedule_();
};

cam.AnimationLoop.prototype.simulateAnimationFrame_ = function(fn) {
	this.win_.setTimeout(function() {
		fn(new Date().getTime());
	}, 0);
};
