#!/usr/bin/env python
#
# Camlistore blob server for App Engine.
#
# Derived from Brad's Brackup-gae utility:
#   http://github.com/bradfitz/brackup-gae-server
#
# Copyright 2009 Brad Fitzpatrick <brad@danga.com>
# Copyright 2010 Brett Slatkin <bslatkin@gmail.com>
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

# Put -- 200 response
curl -v -L \
  -F file=@./test_data.txt \
  http://localhost:8080/put/sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Put with bad blob_ref parameter -- 400 response
curl -v -L \
  -F file=@./test_data.txt \
  http://localhost:8080/put/sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Get present -- the blob
curl -v http://localhost:8080/get/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Get missing -- 404
curl -v http://localhost:8080/get/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Check present -- 200 with blob ref list response
curl -v http://localhost:8080/check/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Check missing -- 404 with empty list response
curl -v http://localhost:8080/check/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# List -- 200 with list of blobs (just one)
curl -v http://localhost:8080/list

# List offset -- 200 with list of no blobs
curl -v http://localhost:8080/list/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

"""

import hashlib
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

  # Size.  (already in the blobinfo, but denormalized for speed,
  # avoiding extra lookups)
  size = db.IntegerProperty(indexed=False)


def render_blob_refs(blob_ref_list):
  """Renders a bunch of blob_refs as JSON.

  Args:
    blob_ref_list: List of Blob objects.

  Returns:
    A string containing the JSON payload.
  """
  out = [
    '{\n'
    '    "blob_refs": ['
  ]

  if blob_ref_list:
    out.extend([
      '\n        ',
      ',\n        '.join(
        '{"blob_ref": "%s", "size": %d}' %
        (b.key().name(), b.size) for b in blob_ref_list),
      '\n    ',
    ])

  out.append(
    ']\n'
    '}'
  )
  return ''.join(out)


class ListHandler(webapp.RequestHandler):
  """Return chunks that the server has."""

  def get(self, after_blob_ref):
    count = max(1, min(1000, int(self.request.get('count') or 1000)))
    query = Blob.all().order('__key__')
    if after_blob_ref:
      query.filter('__key__ >', db.Key.from_path(Blob.kind(), after_blob_ref))
    blob_refs = query.fetch(count)
    self.response.headers['Content-Type'] = 'text/plain'
    self.response.out.write(render_blob_refs(blob_refs))


class GetHandler(blobstore_handlers.BlobstoreDownloadHandler):
  """Gets a blob with the given ref."""

  def get(self, blob_ref):
    blob = Blob.get_by_key_name(blob_ref)
    if not blob:
      self.error(404)
      return
    self.send_blob(blob.blob, 'application/octet-stream')


class CheckHandler(webapp.RequestHandler):
  """Checks if a Blob is present on this server."""

  def get(self, blob_ref):
    blob = Blob.get_by_key_name(blob_ref)
    if not blob:
      blob_refs = []
      self.response.set_status(404)
    else:
      blob_refs = [blob]
      self.response.set_status(200)

    self.response.headers['Content-Type'] = 'text/plain'
    self.response.out.write(render_blob_refs(blob_refs))


class GetUploadUrlHandler(webapp.RequestHandler):
  """Handler to return a URL for a script to get an upload URL."""

  def post(self, blob_ref):
    self.response.headers['Location'] = blobstore.create_upload_url(
        '/upload_complete/%s' % blob_ref)
    self.response.set_status(307)


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

  def store_blob(self, blob_ref, upload_files, error_messages):
    """Store blob information.

    Writes a Blob to the datastore for the uploaded file.

    Args:
      upload_files: List of BlobInfo records representing the uploads.
      error_messages: Empty list for storing error messages to report to user.
    """
    if not upload_files:
      error_messages.append('Missing upload file field')

    if len(upload_files) != 1:
      error_messages.append('More than one file.')

    if not blob_ref.startswith('sha1-'):
      error_messages.append('Only sha1 supported for now.')
      return

    if len(blob_ref) != (len('sha1-') + 40):
      error_messages.append('Bogus length of blob_ref.')
      return

    blob_info, = upload_files

    found_blob_ref = self.compute_blob_ref('sha1', blob_info.key())
    if blob_ref != found_blob_ref:
      error_messages.append('Found blob ref %s, expected %s' %
                            (found_blob_ref, blob_ref))
      return

    def txn():
      blob = Blob(key_name=blob_ref,
                  blob=blob_info.key(),
                  size=blob_info.size)
      blob.put()
    db.run_in_transaction(txn)

  def post(self, blob_ref):
    """Do upload post."""
    error_messages = []

    upload_files = self.get_uploads('file')

    self.store_blob(blob_ref, upload_files, error_messages)

    if error_messages:
      blobstore.delete(upload_files)
      self.redirect('/error?%s' % '&'.join(
          'error_message=%s' % urllib.quote(m) for m in error_messages))
    else:
      self.redirect('/success')


class SuccessHandler(webapp.RequestHandler):
  """The blob put was successful."""

  def get(self):
    self.response.headers['Content-Type'] = 'text/plain'
    self.response.out.write('{}')
    self.response.set_status(200)


class ErrorHandler(webapp.RequestHandler):
  """The blob put failed."""

  def get(self):
    self.response.headers['Content-Type'] = 'text/plain'
    self.response.out.write('\n'.join(self.request.get_all('error_message')))
    self.response.set_status(400)


APP = webapp.WSGIApplication(
  [
    ('/get/([^/]+)', GetHandler),
    ('/check/([^/]+)', CheckHandler),
    ('/list/([^/]+)', ListHandler),
    ('/put/([^/]+)', GetUploadUrlHandler),
    ('/upload_complete/([^/]+)', UploadHandler),  # Admin only.
    ('/success', SuccessHandler),
    ('/error', ErrorHandler),
  ],
  debug=True)


def main():
  wsgiref.handlers.CGIHandler().run(APP)


if __name__ == '__main__':
  main()
