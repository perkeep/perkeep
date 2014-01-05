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

goog.provide('camlistore.AnimationLoop');

goog.require('goog.events.EventTarget');

/**
 * Provides an easier-to-use interface around
 * window.requestAnimationFrame(), and abstracts away browser differences.
 * @param {Window} win
 */
camlistore.AnimationLoop = function(win) {
  goog.base(this);

  /**
   * @type {Window}
   * @private
   */
  this.win_ = win;

  /**
   * @type {Function}
   * @private
   */
  this.requestAnimationFrame_ = win.requestAnimationFrame ||
    win.mozRequestAnimationFrame || win.webkitRequestAnimationFrame ||
    win.msRequestAnimationFrame;

  /**
   * @type {Function}
   * @private
   */
  this.handleFrame_ = this.handleFrame_.bind(this);

  /**
   * @type {number}
   * @private
   */
  this.lastTimestamp_ = 0;

  if (this.requestAnimationFrame_) {
    this.requestAnimationFrame_ = this.requestAnimationFrame_.bind(win);
  } else {
    this.requestAnimationFrame_ = this.simulateAnimationFrame_.bind(this);
  }
};

goog.inherits(camlistore.AnimationLoop, goog.events.EventTarget);

/**
 * @type {string}
 */
camlistore.AnimationLoop.FRAME_EVENT_TYPE = 'frame';

/**
 * @returns {boolean}
 */
camlistore.AnimationLoop.prototype.isRunning = function() {
  return Boolean(this.lastTimestamp_);
};

camlistore.AnimationLoop.prototype.start = function() {
  if (this.isRunning()) {
    return;
  }

  this.lastTimestamp_ = -1;
  this.schedule_();
};

camlistore.AnimationLoop.prototype.stop = function() {
  this.lastTimestamp_ = 0;
};

/**
 * @private
 */
camlistore.AnimationLoop.prototype.schedule_ = function() {
  this.requestAnimationFrame_(this.handleFrame_);
};

/**
 * @param {number=} opt_timestamp A timestamp in milliseconds that is used to
 * measure progress through the animation.
 * @private
 */
camlistore.AnimationLoop.prototype.handleFrame_ = function(opt_timestamp) {
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

/**
 * Simulates requestAnimationFrame as best as possible for browsers that don't
 * have it.
 * @param {Function} fn
 * @private
 */
camlistore.AnimationLoop.prototype.simulateAnimationFrame_ = function(fn) {
  this.win_.setTimeout(function() {
    fn(new Date().getTime());
  }, 0);
};
