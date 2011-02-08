#!/usr/bin/env python
#
# Camlistore blob server for App Engine.
#
# Derived from Brad's Brackup-gae utility:
#   http://github.com/bradfitz/brackup-gae-server
#
# Copyright 2010 Google Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

"""Upload server for camlistore.

To test:

# Stat -- 200 response
curl -v \
  -d camliversion=1 \
  http://localhost:8080/camli/stat

# Upload -- 200 response
curl -v -L \
  -F sha1-126249fd8c18cbb5312a5705746a2af87fba9538=@./test_data.txt \
  <the url returned by stat>

# Put with bad blob_ref parameter -- 400 response
curl -v -L \
  -F sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f=@./test_data.txt \
  <the url returned by stat>

# Get present -- the blob
curl -v http://localhost:8080/camli/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Get missing -- 404
curl -v http://localhost:8080/camli/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Check present -- 200 with only headers
curl -I http://localhost:8080/camli/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Check missing -- 404 with empty list response
curl -I http://localhost:8080/camli/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# List -- 200 with list of blobs (just one)
curl -v http://localhost:8080/camli/enumerate-blobs&limit=1

# List offset -- 200 with list of no blobs
curl -v http://localhost:8080/camli/enumerate-blobs?after=\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

"""

import cgi
import hashlib
import logging
import urllib
import wsgiref.handlers

from google.appengine.ext import blobstore
from google.appengine.ext import db
from google.appengine.ext import webapp
from google.appengine.ext.webapp import blobstore_handlers

import config


class Blob(db.Model):
  """Some content-addressable blob.

  The key is the algorithm, dash, and the lowercase hex digest:
    "sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15"
  """

  # The actual bytes.
  blob = blobstore.BlobReferenceProperty(indexed=False)

  # Size.  (already in the blobinfo, but denormalized for speed)
  size = db.IntegerProperty(indexed=False)


class HelloHandler(webapp.RequestHandler):
  """Present ourselves to the world."""

  def get(self):
    self.response.out.write('Hello!  This is an AppEngine Camlistore '
                            'blob server.<p>')
    self.response.out.write('<a href=js/index.html>js frontend</a>')


class ListHandler(webapp.RequestHandler):
  """Return chunks that the server has."""

  def get(self):
    after_blob_ref = self.request.get('after')
    limit = max(1, min(1000, int(self.request.get('limit') or 1000)))
    query = Blob.all().order('__key__')
    if after_blob_ref:
      query.filter('__key__ >', db.Key.from_path(Blob.kind(), after_blob_ref))
    blob_ref_list = query.fetch(limit)

    self.response.headers['Content-Type'] = 'text/javascript'
    out = [
      '{\n'
      '    "blobs": ['
    ]
    if blob_ref_list:
      out.extend([
        '\n        ',
        ',\n        '.join(
          '{"blobRef": "%s", "size": %d}' %
          (b.key().name(), b.size) for b in blob_ref_list),
        '\n    ',
      ])
    if blob_ref_list and len(blob_ref_list) == limit:
      out.append(
        '],'
        '\n  "continueAfter": "%s"\n'
        '}' % blob_ref_list[-1].key().name())
    else:
      out.append(
        ']\n'
        '}'
      )
    self.response.out.write(''.join(out))


class GetHandler(blobstore_handlers.BlobstoreDownloadHandler):
  """Gets a blob with the given ref."""

  def head(self, blob_ref):
    self.get(blob_ref)

  def get(self, blob_ref):
    blob = Blob.get_by_key_name(blob_ref)
    if not blob:
      self.error(404)
      return
    self.send_blob(blob.blob, 'application/octet-stream')


class StatHandler(webapp.RequestHandler):
  """Handler to return a URL for a script to get an upload URL."""

  def stat_key(self):
    return "stat"

  def get(self):
    self.handle()

  def post(self):
    self.handle()

  def handle(self):
    if self.request.get('camliversion') != '1':
      self.response.headers['Content-Type'] = 'text/plain'
      self.response.out.write('Bad parameter: "camliversion"')
      self.response.set_status(400)
      return

    blob_ref_list = []
    for key, value in self.request.params.items():
      if not key.startswith('blob'):
        continue
      try:
        int(key[len('blob'):])
      except ValueError:
        logging.exception('Bad parameter: %s', key)
        self.response.headers['Content-Type'] = 'text/plain'
        self.response.out.write('Bad parameter: "%s"' % key)
        self.response.set_status(400)
        return
      else:
        blob_ref_list.append(value)

    key_name = self.stat_key()

    self.response.headers['Content-Type'] = 'text/javascript'
    out = [
      '{\n'
      '  "maxUploadSize": %d,\n'
      '  "uploadUrl": "%s",\n'
      '  "uploadUrlExpirationSeconds": 600,\n'
      '  "%s": [\n'
      % (config.MAX_UPLOAD_SIZE,
         blobstore.create_upload_url('/upload_complete'),
         key_name)
    ]

    already_have = db.get([
        db.Key.from_path(Blob.kind(), b) for b in blob_ref_list])
    if already_have:
      out.extend([
        '\n        ',
        ',\n        '.join(
          '{"blobRef": "%s", "size": %d}' %
          (b.key().name(), b.size) for b in already_have if b is not None),
        '\n    ',
      ])
    out.append(
      ']\n'
      '}'
    )
    self.response.out.write(''.join(out))


class PostUploadHandler(StatHandler):

  def stat_key(self):
    return "received"


class UploadHandler(blobstore_handlers.BlobstoreUploadHandler):
  """Handle blobstore post, as forwarded by notification agent."""

  def compute_blob_ref(self, hash_func, blob_key):
    """Computes the blob ref for a blob stored using the given hash function.

    Args:
      hash_func: The name of the hash function (sha1, md5)
      blob_key: The BlobKey of the App Engine blob containing the blob's data.

    Returns:
      A newly computed blob_ref for the data.
    """
    hasher = hashlib.new(hash_func)
    last_index = 0
    while True:
      data = blobstore.fetch_data(
          blob_key, last_index, last_index + blobstore.MAX_BLOB_FETCH_SIZE - 1)
      if not data:
        break
      hasher.update(data)
      last_index += len(data)

    return '%s-%s' % (hash_func, hasher.hexdigest())

  def store_blob(self, blob_ref, blob_info, error_messages):
    """Store blob information.

    Writes a Blob to the datastore for the uploaded file.

    Args:
      blob_ref: The file that was uploaded.
      upload_file: List of BlobInfo records representing the uploads.
      error_messages: Empty list for storing error messages to report to user.
    """
    if not blob_ref.startswith('sha1-'):
      error_messages.append('Only sha1 supported for now.')
      return

    if len(blob_ref) != (len('sha1-') + 40):
      error_messages.append('Bogus blobRef.')
      return

    found_blob_ref = self.compute_blob_ref('sha1', blob_info.key())
    if blob_ref != found_blob_ref:
      error_messages.append('Found blob ref %s, expected %s' %
                            (found_blob_ref, blob_ref))
      return

    def txn():
      logging.info('Saving blob "%s" with size %d', blob_ref, blob_info.size)
      blob = Blob(key_name=blob_ref, blob=blob_info.key(), size=blob_info.size)
      blob.put()
    db.run_in_transaction(txn)

  def post(self):
    """Do upload post."""
    error_messages = []
    blob_info_dict = {}

    for key, value in self.request.params.items():
      if isinstance(value, cgi.FieldStorage):
        if 'blob-key' in value.type_options:
          blob_info = blobstore.parse_blob_info(value)
          blob_info_dict[value.name] = blob_info
          logging.info("got blob: %s" % value.name)
          self.store_blob(value.name, blob_info, error_messages)

    if error_messages:
      logging.error('Upload errors: %r', error_messages)
      blobstore.delete(blob_info_dict.values())
      self.response.set_status(303)
      # TODO: fix up this format
      self.response.headers.add_header("Location", '/error?%s' % '&'.join(
          'error_message=%s' % urllib.quote(m) for m in error_messages))
    else:
      query = ['/nonstandard/upload_complete?camliversion=1']
      query.extend('blob%d=%s' % (i + 1, k)
                   for i, k in enumerate(blob_info_dict.iterkeys()))
      self.response.set_status(303)
      self.response.headers.add_header("Location", str('&'.join(query)))


class ErrorHandler(webapp.RequestHandler):
  """The blob put failed."""

  def get(self):
    self.response.headers['Content-Type'] = 'text/plain'
    self.response.out.write('\n'.join(self.request.get_all('error_message')))
    self.response.set_status(400)


class DebugUploadForm(webapp.RequestHandler):
  def get(self):
        self.response.headers['Content-Type'] = 'text/html'
        uploadurl = blobstore.create_upload_url('/upload_complete')
        self.response.out.write('<body><form method="post" enctype="multipart/form-data" action="%s">' % uploadurl)
        self.response.out.write('<input type="file" name="sha1-f628050e63819347a095645ad9ae697415664f0">')
        self.response.out.write('<input type="submit"></form></body>')


APP = webapp.WSGIApplication(
  [
    ('/', HelloHandler),
    ('/debug/upform', DebugUploadForm),
    ('/camli/enumerate-blobs', ListHandler),
    ('/camli/stat', StatHandler),
    ('/camli/([^/]+)', GetHandler),
    ('/nonstandard/upload_complete', PostUploadHandler),
    ('/upload_complete', UploadHandler),  # Admin only.
    ('/error', ErrorHandler),
  ],
  debug=True)


def main():
  wsgiref.handlers.CGIHandler().run(APP)


if __name__ == '__main__':
  main()
