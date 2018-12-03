/*
Copyright 2018 The Perkeep Authors

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

goog.provide('cam.BlobItemInstapaperContent');

goog.require('goog.math.Size');
goog.require('cam.dateUtils');
goog.require('cam.permanodeUtils');

cam.BlobItemInstapaperContent = React.createClass({
  propTypes: {
    date: React.PropTypes.number.isRequired,
    href: React.PropTypes.string.isRequired,
    content: React.PropTypes.string.isRequired,
    size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
    title: React.PropTypes.string,
  },

  render: function () {
    return React.DOM.a({
        href: this.props.href,
        className: 'cam-blobitem-instapaper-highlight',
        style: {
          width: this.props.size.width,
          height: this.props.size.height,
        },
      },
      React.DOM.div({className: 'cam-blobitem-instapaper-date'}, cam.dateUtils.formatDateShort(this.props.date)),
      React.DOM.div({
          className: 'cam-blobitem-instapaper-content',
          dangerouslySetInnerHTML: {__html: this.props.content}
        }
      ),
      React.DOM.div({
          className: 'cam-blobitem-instapaper-title',
          dangerouslySetInnerHTML: {__html: this.props.title}
        }
      )
    );
  },
});

cam.BlobItemInstapaperContent.getHandler = function (blobref, searchSession, href) {
  var m = searchSession.getMeta(blobref);
  if (m.camliType !== 'permanode' || cam.permanodeUtils.getCamliNodeType(m.permanode) !== 'instapaper.com:highlight') {
    return null;
  }

  var date = cam.permanodeUtils.getSingleAttr(m.permanode, 'dateCreated');
  var content = cam.permanodeUtils.getSingleAttr(m.permanode, 'content');
  var title = searchSession.getTitle(blobref);
  return new cam.BlobItemInstapaperContent.Handler(title, content, Date.parse(date), href);
};

cam.BlobItemInstapaperContent.trimString = function (s, len) {
  var ts = s.substr(0, len);
  ts = ts.substr(0, Math.min(ts.length, ts.lastIndexOf(" ")));

  if (s.length > len) {
    ts = ts + '...';
  }

  return ts;
};

cam.BlobItemInstapaperContent.Handler = function (title, content, date, href) {
  this.title_ = title || "";
  this.content_ = content || "";
  this.date_ = date;
  this.href_ = href;
};

cam.BlobItemInstapaperContent.Handler.prototype.getAspectRatio = function () {
  return 1.0;
};

cam.BlobItemInstapaperContent.Handler.prototype.createContent = function (size) {
  return React.createElement(cam.BlobItemInstapaperContent, {
    title: cam.BlobItemInstapaperContent.trimString(this.title_, 50),
    content: cam.BlobItemInstapaperContent.trimString(this.content_, 150),
    date: this.date_,
    size: size,
    href: this.href_
  });
};
