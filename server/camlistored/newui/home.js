goog.provide('home');
goog.require('goog.dom');

home.hello = function() {
	var h1 = goog.dom.createDom('h1', {'style': 'background-color:#EEE'},
		'Welcome to the new camlistore ui');
	goog.dom.appendChild(document.body, h1);
}
