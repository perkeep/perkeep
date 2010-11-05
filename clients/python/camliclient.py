#!/usr/bin/env python
#
# Camlistore uploader client for Python.
#
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
"""TODO
"""

__author__ = 'Brett Slatkin (bslatkin@gmail.com)'

import base64
import cStringIO
import hashlib
import httplib
import logging
import mimetools
import optparse
import os
import re
import string
import sys
import urllib
import urlparse

import simplejson

__all__ = ['Error', 'ServerError', 'PayloadError', 'BUFFER_SIZE', 'CamliOp']

################################################################################
# Library

BUFFER_SIZE = 512 * 1024


class Error(Exception):
  """Base class for exceptions in this module."""


class ServerError(Error):
  """An unexpected error was returned by the server."""


class PayloadError(ServerError):
  """Something about a data payload was bad."""


def buffered_sha1(data, buffer_size=BUFFER_SIZE):
  """TODO
  restores the file pointer to its original location
  """
  compute = hashlib.sha1()
  if isinstance(data, basestring):
    compute.update(data)
  else:
    start = data.tell()
    while True:
      line = data.read(buffer_size)
      if line == '':
        break
      compute.update(line)
    data.seek(start)
  return compute.hexdigest()


class CamliOp(object):
  """TODO
  one connection socket
  single threaded yadda yadda
  """

  def __init__(self,
               server_address,
               buffer_size=BUFFER_SIZE,
               create_connection=httplib.HTTPConnection,
               auth=''):
    """TODO
    """
    self.server_address = server_address
    self.buffer_size = buffer_size
    self._create_connection = create_connection
    self._connection = None
    self._authorization = ''
    if auth:
      if len(auth.split(':')) != 2:
        logging.fatal('Invalid auth string; should be username:password')
      self._authorization = ('Basic ' + string.strip(base64.encodestring(auth)))

  def _setup_connection(self):
    """Sets up the HTTP connection."""
    self.connection = self._create_connection(self.server_address)

##  def get(self, blobrefs, start_out=None, end_out=None):
##    """
##    blobrefs can be an iterable or just a single blobref
##
##    start_out is a function taking the blobref's key, returns a file-like
##    object to which the blob should be written. end_out should take the
##    blobref and the open file as args. if end_out is None then the file will
##    just be closed.
##
##    return value will be a dictionary of key/value pairs when start_out is
##    not supplied, a single value is blobrefs is a string, or None if
##    start_out was supplied and used to report each blob's start/end
##
##    Raises:
##      
##    """
##    multiple = not isinstance(blobrefs, basestring)
##    result = {}
##    if start_out is None:
##      def start_out(blobref):
##        buffer = cStringIO.StringIO()
##        result[blobref] = buffer
##        return buffer
##      def end_out(blobref, unused):
##        result[blobref] = result[blobref].getvalue()
##    else:
##      result = None  # Rely on start_out for reporting all outputs.
##      if end_out is None:
##        def end_out(blobref, file_like):
##          file_like.close()
##
##    self._setup_connection()
##    preupload = {'camliversion': '1'}
##    for index, blobref in enumerate(blobrefs):
##      preupload['blob%d' % index+1] = blobref
##
##    # TODO: What is the max number of blobs that can be specified in a
##    # preupload request? The server probably has some reasonable limit and
##    # after that we need to do batching.
##
##    self.connection.request(
##        'POST', '/camli/preupload', urllib.quote(preupload),
##        {'Content-Type': 'application/x-www-form-urlencoded'})
##    response = self.connection.getresponse()
##    if response.status != 200:
##      raise ServerError('Bad response status: %d %s' %
##                        (response.status, response.reason))
##    data = response.read()
##    try:
##      response_dict = simplejson.loads(data)
##    except simplejson.decoder.JSONDecodeError:
##      raise PayloadError('Server returned bad response: %r' % data)
##
##    if 'alreadyHave' not in response_dict:
##      raise PayloadError('Could not find "alreadyHave" in response: %r' %
##                         response_dict)
##    already_have = set()
##    for blobref_json in response_dict['already_have']:
##      already_have.add(blobref_json)
##    logging.debug('Already have %d blobs', len(already_have))
##
##    for blobref in blobrefs:
##      if blobref in already_have:
##        logging.debug('Already have blobref=%s' blobref)
##        continue
##
##      if check_sha1:
##        compute_hash = hashlib.sha1()
##      out = start_out(blobref)
##      while True:
##        buf = response.read(self.buffer_size)
##        if buf == '':
##          end_out(blobref, out)
##        if check_sha1:
##          compute_hash.update(buf)
##        out.write(buf)
##
##      if check_sha1:
##        found = 'sha1-' + compute_hash.hexdigest()
##        if found != blobref:
##          raise ValueError('sha1 hash of blobref does not match; '
##                           'found %s, expected %s' % (found, blobref))
##
##    if result and not multiple:
##      return result.values()[0]
##    return result

  def put_blobs(self, blobs):
    """TODO
    takes list of blobs or a dictionary mapping blobrefs to blobs
    """
    if isinstance(blobs, dict):
      raise TypeError('Must pass iterable of tuples, open files, or strings.')

    blobref_dict = {}
    for item in blobs:
      if isinstance(item, tuple):
        blob, blobref = item
      else:
        blob, blobref = item, None
      if blobref is None:
        blobref = 'sha1-' + buffered_sha1(blob, buffer_size=self.buffer_size)
      blobref_dict[blobref] = blob

    preupload = {'camliversion': '1'}
    for index, blobref in enumerate(blobref_dict.keys()):
      preupload['blob%d' % (index+1)] = blobref

    # TODO: What is the max number of blobs that can be specified in a
    # preupload request? The server probably has some reasonable limit and
    # after that we need to do batching in smaller groups.

    self._setup_connection()
    self.connection.request(
        'POST', '/camli/preupload', urllib.urlencode(preupload),
        {'Content-Type': 'application/x-www-form-urlencoded',
         'Authorization': self._authorization})
    response = self.connection.getresponse()
    logging.debug('Preupload response: %d %s', response.status, response.reason)
    if response.status != 200:
      raise ServerError('Bad preupload response status: %d %s' %
                        (response.status, response.reason))

    data = response.read()
    try:
      response_dict = simplejson.loads(data)
    except simplejson.decoder.JSONDecodeError:
      raise PayloadError('Server returned bad preupload response: %r' % data)

    logging.info('Response: %r', response_dict)

    if 'alreadyHave' not in response_dict:
      raise PayloadError(
          'Could not find "alreadyHave" in preupload response: %r' %
          response_dict)
    if 'uploadUrl' not in response_dict:
      raise PayloadError(
          'Could not find "uploadUrl" in preupload response: %r' %
          response_dict)

    already_have_blobrefs = set()
    for blobref_json in response_dict['alreadyHave']:
      if 'blobRef' not in blobref_json:
        raise PayloadError(
            'Cannot find "blobRef" in preupload response: %r',
            response_dict)
      already_have_blobrefs.add(blobref_json['blobRef'])
    logging.debug('Already have blobs: %r', already_have_blobrefs)

    missing_blobrefs = set(blobref_dict.iterkeys())
    missing_blobrefs.difference_update(already_have_blobrefs)
    if not missing_blobrefs:
      logging.debug('All blobs already present.')
      return

    # TODO(bslatkin): Figure out the 'Content-Length' header value by looking
    # at the size of the files by seeking; required for multipart POST.
    out = cStringIO.StringIO()
    boundary = mimetools.choose_boundary()
    boundary_start = '--' + boundary

    blob_number = 0
    for blobref in blobref_dict.iterkeys():
      if blobref in already_have_blobrefs:
        logging.debug('Already have blobref=%s', blobref)
        continue
      blob = blobref_dict[blobref]
      blob_number += 1

      out.write(boundary_start)
      out.write('\r\nContent-Type: application/octet-stream\r\n')
      out.write('Content-Disposition: form-data; name="%s"; '
                'filename="%d"\r\n\r\n' % (blobref, blob_number))
      if isinstance(blob, basestring):
        out.write(blob)
      else:
        while True:
          buf = blob.read(self.buffer_size)
          if buf == '':
            break
          out.write(buf)
      out.write('\r\n')
    out.write(boundary_start)
    out.write('--\r\n')
    request_body = out.getvalue()
    print request_body

    pieces = list(urlparse.urlparse(response_dict['uploadUrl']))
    # TODO: Support upload servers on another base URL.
    pieces[0], pieces[1] = '', ''
    relative_url = urlparse.urlunparse(pieces)
    self.connection.request(
        'POST', relative_url, request_body,
        {'Content-Type': 'multipart/form-data; boundary="%s"' % boundary,
         'Content-Length': str(len(request_body)),
         'Authorization': self._authorization})

    response = self.connection.getresponse()
    logging.debug('Upload response: %d %s', response.status, response.reason)
    if response.status not in (200, 301, 302, 303):
      raise ServerError('Bad upload response status: %d %s' %
                        (response.status, response.reason))

    while response.status in (301, 302, 303):
      # TODO(bslatkin): Support connections to servers on different addresses
      # after redirects. For now just send another request to the same server.
      location = response.getheader('Location')
      pieces = list(urlparse.urlparse(location))
      pieces[0], pieces[1] = '', ''
      new_relative_url = urlparse.urlunparse(pieces)
      logging.debug('Redirect %s -> %s', relative_url, new_relative_url)
      relative_url = new_relative_url
      self.connection.request('GET', relative_url)
      response = self.connection.getresponse()

    if response.status != 200:
      raise ServerError('Bad upload response status: %d %s' %
                        (response.status, response.reason))

    data = response.read()
    try:
      response_dict = simplejson.loads(data)
    except simplejson.decoder.JSONDecodeError:
      raise PayloadError('Server returned bad upload response: %r' % data)

    if 'received' not in response_dict:
      raise PayloadError('Could not find "received" in upload response: %r' %
                         response_dict)

    received_blobrefs = set()
    for blobref_json in response_dict['received']:
      if 'blobRef' not in blobref_json:
        raise PayloadError(
            'Cannot find "blobRef" in upload response: %r',
            response_dict)
      received_blobrefs.add(blobref_json['blobRef'])
    logging.debug('Received blobs: %r', received_blobrefs)

    missing_blobrefs.difference_update(received_blobrefs)
    if missing_blobrefs:
      # TODO: Try to upload the missing ones.
      raise ServerError('Some blobs not uploaded: %r', missing_blobrefs)

    logging.debug('Upload of %d blobs successful.', len(blobref_dict))

################################################################################
# Begin command-line tool

def upload_dir(op, root_path, recursive=True, ignore_patterns=[r'^\..*']):
  """TODO
  """
  def should_ignore(dirname):
    for pattern in ignore_patterns:
      if re.match(pattern, dirname):
        return True
    return False

  def error(e):
    raise e

  all_blob_paths = []
  for dirpath, dirnames, filenames in os.walk(root_path, onerror=error):
    allowed_dirnames = []
    for name in dirnames:
      if not should_ignore(name):
        allowed_dirnames.append(name)
    for i in xrange(len(dirnames)):
      dirnames.pop(0)
    if recursive:
      dirnames.extend(allowed_dirnames)

    all_blob_paths.extend(os.path.join(dirpath, name) for name in filenames)

  all_blob_files = [open(path, 'rb') for path in all_blob_paths]
  logging.debug('Uploading dir=%r with blob paths: %r',
                root_path, all_blob_paths)
  op.put_blobs(all_blob_files)



def main(argv):
  logging.getLogger().setLevel(logging.DEBUG)

  usage = 'usage: %prog [options] DIR'
  parser = optparse.OptionParser(usage=usage)
  parser.add_option("-a", "--auth", dest="auth",
                    default="",
                    help="username:pasword for authentication")
  parser.add_option("-s", "--server", dest="server",
                    default="localhost:8080",
                    help="hostname:port to connect to")
  (opts, args) = parser.parse_args(argv[1:])

  if not args:
    parser.print_usage()
    sys.exit(2)

  op = CamliOp(opts.server, auth=opts.auth)
  upload_dir(op, args[0])


if __name__ == '__main__':
  main(sys.argv)
