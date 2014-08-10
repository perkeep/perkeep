/**
 * Object related utilities beyond what exist in Closure.
 */
goog.provide('cam.object');

cam.object.extend = function(o, n) {
	var obj = {};
	if (o) {
		goog.mixin(obj, o);
	}
	if (n) {
		goog.mixin(obj, n);
	}
	return obj;
}
