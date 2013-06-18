// Copyright 2005 The Closure Library Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS-IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/**
 * @fileoverview Implementation of EventTarget as defined by W3C DOM 2/3.
 *
 * @author arv@google.com (Erik Arvidsson) [Original implementation]
 * @author pupius@google.com (Daniel Pupius) [Port to use goog.events]
 * @see ../demos/eventtarget.html
 */

goog.provide('goog.events.EventTarget');

goog.require('goog.Disposable');
goog.require('goog.events');
goog.require('goog.events.Event');
goog.require('goog.events.Listenable');
goog.require('goog.events.Listener');



/**
 * Inherit from this class to give your object the ability to dispatch events.
 * Note that this class provides event <em>sending</em> behaviour, not event
 * receiving behaviour: your object will be able to broadcast events, and other
 * objects will be able to listen for those events using goog.events.listen().
 *
 * <p>The name "EventTarget" reflects the fact that this class implements the
 * <a href="http://www.w3.org/TR/DOM-Level-2-Events/events.html">
 * EventTarget interface</a> as defined by W3C DOM 2/3, with a few differences:
 * <ul>
 * <li>Event objects do not have to implement the Event interface. An object
 *     is treated as an event object if it has a 'type' property.
 * <li>You can use a plain string instead of an event object; an event-like
 *     object will be created with the 'type' set to the string value.
 * </ul>
 *
 * <p>Unless propagation is stopped, an event dispatched by an EventTarget
 * will bubble to the parent returned by <code>getParentEventTarget</code>.
 * To set the parent, call <code>setParentEventTarget</code> or override
 * <code>getParentEventTarget</code> in a subclass.  Subclasses that don't
 * support changing the parent should override the setter to throw an error.
 *
 * <p>Example usage:
 * <pre>
 *   var source = new goog.events.EventTarget();
 *   function handleEvent(event) {
 *     alert('Type: ' + e.type + '\nTarget: ' + e.target);
 *   }
 *   goog.events.listen(source, 'foo', handleEvent);
 *   ...
 *   source.dispatchEvent({type: 'foo'}); // will call handleEvent
 *   // or source.dispatchEvent('foo');
 *   ...
 *   goog.events.unlisten(source, 'foo', handleEvent);
 *
 *   // You can also use the Listener interface:
 *   var listener = {
 *     handleEvent: function(event) {
 *       ...
 *     }
 *   };
 *   goog.events.listen(source, 'bar', listener);
 * </pre>
 *
 * @constructor
 * @extends {goog.Disposable}
 * @implements {goog.events.Listenable}
 */
goog.events.EventTarget = function() {
  goog.Disposable.call(this);

  if (goog.events.Listenable.USE_LISTENABLE_INTERFACE) {
    /**
     * Maps of event type to an array of listeners.
     *
     * @type {Object.<string, !Array.<!goog.events.Listener>>}
     * @private
     */
    this.eventTargetListeners_ = {};
  }
};
goog.inherits(goog.events.EventTarget, goog.Disposable);


if (goog.events.Listenable.USE_LISTENABLE_INTERFACE) {
  goog.events.Listenable.addImplementation(goog.events.EventTarget);
}


/**
 * Used to tell if an event is a real event in goog.events.listen() so we don't
 * get listen() calling addEventListener() and vice-versa.
 * @type {boolean}
 * @private
 */
goog.events.EventTarget.prototype.customEvent_ = true;


/**
 * Parent event target, used during event bubbling.
 * @type {goog.events.EventTarget?}
 * @private
 */
goog.events.EventTarget.prototype.parentEventTarget_ = null;


/**
 * Returns the parent of this event target to use for bubbling.
 *
 * @return {goog.events.EventTarget} The parent EventTarget or null if there
 * is no parent.
 */
goog.events.EventTarget.prototype.getParentEventTarget = function() {
  return this.parentEventTarget_;
};


/**
 * Sets the parent of this event target to use for bubbling.
 *
 * @param {goog.events.EventTarget?} parent Parent EventTarget (null if none).
 */
goog.events.EventTarget.prototype.setParentEventTarget = function(parent) {
  this.parentEventTarget_ = parent;
};


/**
 * Adds an event listener to the event target. The same handler can only be
 * added once per the type. Even if you add the same handler multiple times
 * using the same type then it will only be called once when the event is
 * dispatched.
 *
 * Supported for legacy but use goog.events.listen(src, type, handler) instead.
 *
 * @param {string} type The type of the event to listen for.
 * @param {Function|Object} handler The function to handle the event. The
 *     handler can also be an object that implements the handleEvent method
 *     which takes the event object as argument.
 * @param {boolean=} opt_capture In DOM-compliant browsers, this determines
 *     whether the listener is fired during the capture or bubble phase
 *     of the event.
 * @param {Object=} opt_handlerScope Object in whose scope to call
 *     the listener.
 */
goog.events.EventTarget.prototype.addEventListener = function(
    type, handler, opt_capture, opt_handlerScope) {
  goog.events.listen(this, type, handler, opt_capture, opt_handlerScope);
};


/**
 * Removes an event listener from the event target. The handler must be the
 * same object as the one added. If the handler has not been added then
 * nothing is done.
 * @param {string} type The type of the event to listen for.
 * @param {Function|Object} handler The function to handle the event. The
 *     handler can also be an object that implements the handleEvent method
 *     which takes the event object as argument.
 * @param {boolean=} opt_capture In DOM-compliant browsers, this determines
 *     whether the listener is fired during the capture or bubble phase
 *     of the event.
 * @param {Object=} opt_handlerScope Object in whose scope to call
 *     the listener.
 */
goog.events.EventTarget.prototype.removeEventListener = function(
    type, handler, opt_capture, opt_handlerScope) {
  goog.events.unlisten(this, type, handler, opt_capture, opt_handlerScope);
};


/** @override */
goog.events.EventTarget.prototype.dispatchEvent = function(e) {
  if (goog.events.Listenable.USE_LISTENABLE_INTERFACE) {
    if (this.isDisposed()) {
      return true;
    }

    var ancestorsTree, ancestor = this.getParentEventTarget();
    if (ancestor) {
      ancestorsTree = [];
      for (; ancestor; ancestor = ancestor.getParentEventTarget()) {
        ancestorsTree.push(ancestor);
      }
    }

    return goog.events.EventTarget.dispatchEventInternal_(
        this, e, ancestorsTree);
  } else {
    return goog.events.dispatchEvent(this, e);
  }
};


/**
 * Unattach listeners from this object.  Classes that extend EventTarget may
 * need to override this method in order to remove references to DOM Elements
 * and additional listeners, it should be something like this:
 * <pre>
 * MyClass.prototype.disposeInternal = function() {
 *   MyClass.superClass_.disposeInternal.call(this);
 *   // Dispose logic for MyClass
 * };
 * </pre>
 * @override
 * @protected
 */
goog.events.EventTarget.prototype.disposeInternal = function() {
  goog.events.EventTarget.superClass_.disposeInternal.call(this);

  if (goog.events.Listenable.USE_LISTENABLE_INTERFACE) {
    this.removeAllListeners();
  } else {
    goog.events.removeAll(this);
  }

  this.parentEventTarget_ = null;
};


if (goog.events.Listenable.USE_LISTENABLE_INTERFACE) {

/** @override */
goog.events.EventTarget.prototype.listen = function(
    type, listener, opt_useCapture, opt_listenerScope) {
  return this.listenInternal_(
      type, listener, false /* callOnce */, opt_useCapture, opt_listenerScope);
};


/** @override */
goog.events.EventTarget.prototype.listenOnce = function(
    type, listener, opt_useCapture, opt_listenerScope) {
  return this.listenInternal_(
      type, listener, true /* callOnce */, opt_useCapture, opt_listenerScope);
};


/**
 * Adds an event listener. A listener can only be added once to an
 * object and if it is added again the key for the listener is
 * returned.
 *
 * Note that a one-off listener will not change an existing listener,
 * if any. On the other hand a normal listener will change existing
 * one-off listener to become a normal listener.
 *
 * @param {string} type Event type to listen to.
 * @param {!Function} listener Callback method.
 * @param {boolean} callOnce Whether the listener is a one-off
 *     listener or otherwise.
 * @param {boolean=} opt_useCapture Whether to fire in capture phase
 *     (defaults to false).
 * @param {Object=} opt_listenerScope Object in whose scope to call the
 *     listener.
 * @return {goog.events.ListenableKey} Unique key for the listener.
 * @private
 */
goog.events.EventTarget.prototype.listenInternal_ = function(
    type, listener, callOnce, opt_useCapture, opt_listenerScope) {
  goog.asserts.assert(!this.isDisposed());

  var listenerArray = this.eventTargetListeners_[type] ||
      (this.eventTargetListeners_[type] = []);

  var listenerObj;
  var index = goog.events.EventTarget.findListenerIndex_(
      listenerArray, listener, opt_useCapture, opt_listenerScope);
  if (index > -1) {
    listenerObj = listenerArray[index];
    if (!callOnce) {
      // Ensure that, if there is an existing callOnce listener, it is no
      // longer a callOnce listener.
      listenerObj.callOnce = false;
    }
    return listenerObj;
  }

  listenerObj = new goog.events.Listener();
  listenerObj.init(
      listener, null, this, type, !!opt_useCapture, opt_listenerScope);
  listenerObj.callOnce = callOnce;
  listenerArray.push(listenerObj);

  return listenerObj;
};


/** @override */
goog.events.EventTarget.prototype.unlisten = function(
    type, listener, opt_useCapture, opt_listenerScope) {
  goog.asserts.assert(!this.isDisposed());

  if (!(type in this.eventTargetListeners_)) {
    return false;
  }

  var listenerArray = this.eventTargetListeners_[type];
  var index = goog.events.EventTarget.findListenerIndex_(
      listenerArray, listener, opt_useCapture, opt_listenerScope);
  if (index > -1) {
    var listenerObj = listenerArray[index];
    goog.events.cleanUp(listenerObj);
    listenerObj.removed = true;
    return goog.array.removeAt(listenerArray, index);
  }
  return false;
};


/** @override */
goog.events.EventTarget.prototype.unlistenByKey = function(key) {
  goog.asserts.assert(!this.isDisposed());

  var type = key.type;
  if (!(type in this.eventTargetListeners_)) {
    return false;
  }

  var removed = goog.array.remove(this.eventTargetListeners_[type], key);
  if (removed) {
    goog.events.cleanUp(key);
    key.removed = true;
  }
  return removed;
};


/** @override */
goog.events.EventTarget.prototype.removeAllListeners = function(
    opt_type, opt_capture) {
  var count = 0;
  for (var type in this.eventTargetListeners_) {
    if (!opt_type || type == opt_type) {
      var listenerArray = this.eventTargetListeners_[type];
      for (var i = 0; i < listenerArray.length; i++) {
        ++count;
        goog.events.cleanUp(listenerArray[i]);
        listenerArray[i].removed = true;
      }
      listenerArray.length = 0;
    }
  }
  return count;
};


/** @override */
goog.events.EventTarget.prototype.fireListeners = function(
    type, capture, eventObject) {
  goog.asserts.assert(!this.isDisposed());

  if (!(type in this.eventTargetListeners_)) {
    return true;
  }

  var rv = true;
  var listenerArray = goog.array.clone(this.eventTargetListeners_[type]);
  for (var i = 0; i < listenerArray.length; ++i) {
    var listener = listenerArray[i];
    // We might not have a listener if the listener was removed.
    if (listener && !listener.removed && listener.capture == capture) {
      // TODO(user): This logic probably should be in the Listener
      // object instead.
      if (listener.callOnce) {
        this.unlistenByKey(listener);
      }
      rv = listener.handleEvent(eventObject) !== false && rv;
    }
  }

  return rv && eventObject.returnValue_ != false;
};


/** @override */
goog.events.EventTarget.prototype.getListeners = function(type, capture) {
  var listenerArray = this.eventTargetListeners_[type];
  var rv = [];
  if (listenerArray) {
    for (var i = 0; i < listenerArray.length; ++i) {
      var listenerObj = listenerArray[i];
      if (listenerObj.capture == capture) {
        rv.push(listenerObj);
      }
    }
  }
  return rv;
};


/**
 * Dispatches the given event on the ancestorsTree.
 *
 * TODO(user): Look for a way to reuse this logic in
 * goog.events, if possible.
 *
 * @param {!goog.events.EventTarget} target The target to dispatch on.
 * @param {goog.events.Event|Object|string} e The event object.
 * @param {Array.<goog.events.Listenable>=} opt_ancestorsTree The ancestors
 *     tree of the target, in reverse order from the closest ancestor
 *     to the root event target. May be null if the target has no ancestor.
 * @return {boolean} If anyone called preventDefault on the event object (or
 *     if any of the listeners returns false this will also return false.
 * @private
 */
goog.events.EventTarget.dispatchEventInternal_ = function(
    target, e, opt_ancestorsTree) {
  var type = e.type || /** @type {string} */ (e);

  // If accepting a string or object, create a custom event object so that
  // preventDefault and stopPropagation work with the event.
  if (goog.isString(e)) {
    e = new goog.events.Event(e, target);
  } else if (!(e instanceof goog.events.Event)) {
    var oldEvent = e;
    e = new goog.events.Event(type, target);
    goog.object.extend(e, oldEvent);
  } else {
    e.target = e.target || target;
  }

  var rv = true, currentTarget;

  // Executes all capture listeners on the ancestors, if any.
  if (opt_ancestorsTree) {
    for (var i = opt_ancestorsTree.length - 1; !e.propagationStopped_ && i >= 0;
         i--) {
      currentTarget = e.currentTarget = opt_ancestorsTree[i];
      rv = currentTarget.fireListeners(type, true, e) && rv;
    }
  }

  // Executes capture and bubble listeners on the target.
  if (!e.propagationStopped_) {
    currentTarget = e.currentTarget = target;
    rv = currentTarget.fireListeners(type, true, e) && rv;
    if (!e.propagationStopped_) {
      rv = currentTarget.fireListeners(type, false, e) && rv;
    }
  }

  // Executes all bubble listeners on the ancestors, if any.
  if (opt_ancestorsTree) {
    for (i = 0; !e.propagationStopped_ && i < opt_ancestorsTree.length; i++) {
      currentTarget = e.currentTarget = opt_ancestorsTree[i];
      rv = currentTarget.fireListeners(type, false, e) && rv;
    }
  }

  return rv;
};


/**
 * Finds the index of a matching goog.events.Listener in the given
 * listenerArray.
 * @param {!Array.<!goog.events.Listener>} listenerArray Array of listener.
 * @param {!Function} listener The listener function.
 * @param {boolean=} opt_useCapture The capture flag for the listener.
 * @param {Object=} opt_listenerScope The listener scope.
 * @return {number} The index of the matching listener within the
 *     listenerArray.
 * @private
 */
goog.events.EventTarget.findListenerIndex_ = function(
    listenerArray, listener, opt_useCapture, opt_listenerScope) {
  for (var i = 0; i < listenerArray.length; ++i) {
    var listenerObj = listenerArray[i];
    if (listenerObj.listener == listener &&
        listenerObj.capture == !!opt_useCapture &&
        listenerObj.handler == opt_listenerScope) {
      return i;
    }
  }
  return -1;
};

}  // if (goog.events.Listenable.USE_LISTENABLE_INTERFACE)
