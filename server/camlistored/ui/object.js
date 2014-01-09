/**
 * Object related utilities beyond what exist in Closure.
 */
goog.provide('cam.object');

cam.object.extend = function(o, n) {
	var obj = {};
	goog.mixin(obj, o);
	goog.mixin(obj, n);
	return obj;
}
