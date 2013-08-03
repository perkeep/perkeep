#!/usr/bin/env python
#
# Camlistore uploader client for Python.
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
"""Client library for Camlistore."""

__author__ = 'Brett Slatkin (bslatkin@gmail.com)'

import base64
import cStringIO
import hashlib
import httplib
import logging
import mimetools
import urllib
import urlparse

import simplejson

__all__ = ['Error', 'ServerError', 'PayloadError', 'BUFFER_SIZE', 'CamliOp']


BUFFER_SIZE = 512 * 1024


class Error(Exception):
  """Base class for exceptions in this module."""


class ServerError(Error):
  """An unexpected error was returned by the server."""


class PayloadError(ServerError):
  """Something about a data payload was bad."""


def buffered_sha1(data, buffer_size=BUFFER_SIZE):
  """Calculates the sha1 hash of some data.

  Args:
    data: A string of data to write or an open file-like object. File-like
      objects will be seeked back to their original position before this
      function returns.
    buffer_size: How much data to munge at a time.

  Returns:
    Hex sha1 string.
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
  """Camlistore client class that is single threaded, using one socket."""

  def __init__(self,
               server_address,
               buffer_size=BUFFER_SIZE,
               create_connection=httplib.HTTPConnection,
               auth=None,
               basepath=""):
    """Initializer.

    Args:
      server_address: hostname:port for the server.
      buffer_size: Byte size to use for in-memory buffering for various
        client-related operations.
      create_connection: Use for testing.
      auth: Optional. 'username:password' to use for HTTP basic auth.
      basepath: Optional path suffix. e.g. if the server is at
            "localhost:3179/bs", the basepath should be "/bs".
    """
    self.server_address = server_address
    self.buffer_size = buffer_size
    self._create_connection = create_connection
    self._connection = None
    self._authorization = ''
    self.basepath = ""
    if auth:
      if len(auth.split(':')) != 2:
          # Default to dummy username; current server doesn't care
          # TODO(jrabbit): care when necessary
          auth = "username:" + auth #If username not given use the implicit default, 'username'
      self._authorization = ('Basic ' + base64.encodestring(auth).strip())
    if basepath:
      if '/' not in basepath:
        raise NameError("basepath must be in form '/bs'")
      if basepath[-1] == '/':
        basepath = basepath[:-1]
      self.basepath = basepath

  def _setup_connection(self):
    """Sets up the HTTP connection."""
    self.connection = self._create_connection(self.server_address)

  def put_blobs(self, blobs):
    """Puts a set of blobs.

    Args:
      blobs: List of (data, blobref) tuples; list of open files; or list of
        blob data strings.

    Returns:
      The set of blobs that were actually uploaded. If all blobs are already
      present this set will be empty.

    Raises:
      ServerError if the server response is bad.
      PayloadError if the server response is not in the right format.
      OSError or IOError if reading any blobs breaks.
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
    if self.basepath:
      fullpath = self.basepath + '/camli/stat'
    else:
      fullpath = '/camli/stat'
    self.connection.request(
        'POST', fullpath, urllib.urlencode(preupload),
        {'Content-Type': 'application/x-www-form-urlencoded',
         'Authorization': self._authorization})
    response = self.connection.getresponse()
    logging.debug('Preupload HTTP response: %d %s',
                  response.status, response.reason)
    if response.status != 200:
      raise ServerError('Bad preupload response status: %d %s' %
                        (response.status, response.reason))

    data = response.read()
    try:
      response_dict = simplejson.loads(data)
    except simplejson.decoder.JSONDecodeError:
      raise PayloadError('Server returned bad preupload response: %r' % data)

    logging.debug('Parsed preupload response: %r', response_dict)
    if 'stat' not in response_dict:
      raise PayloadError(
          'Could not find "stat" in preupload response: %r' %
          response_dict)
    if 'uploadUrl' not in response_dict:
      raise PayloadError(
          'Could not find "uploadUrl" in preupload response: %r' %
          response_dict)

    already_have_blobrefs = set()
    for blobref_json in response_dict['stat']:
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
      self.connection.request('GET', relative_url, headers={
          'Authorization': self._authorization})
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
    return received_blobrefs

  def get_blobs(self,
                blobref_list,
                start_out=None,
                end_out=None,
                check_sha1=True):
    """Gets a set of blobs.

    Args:
      blobref_list: A single blobref as a string or an iterable of strings that
        are blobrefs.
      start_out: Optional. A function taking the blobref's key, returns a
        file-like object to which the blob should be written. Called before
        the blob has started any writing.
      end_out: Optional along with start_out. A function that takes the
        blobref and open file-like object that does proper cleanup and closing
        of the file. Called when all of the file's contents have been written.
      check_sha1: Double-check that the file's contents match the blobref.

    Returns:
      If start_out is not supplied, then all blobs will be kept in memory. If
      blobref_list is a single blobref, then the return value will be a string
      with the blob data or None if the blob was not present. If blobref_list
      was iterable, the return value will be a dictionary mapping blobref to
      blob data for each blob that was found.

      If start_out is supplied, the return value will be None. Callers can
      check for missing blobs by comparing their own input of the blobref_list
      argument to the blobrefs that are passed to start_out.

    Raises:
      ServerError if the server response is invalid for whatever reason.
      OSError or IOError if writing to any files breaks.
    """
    multiple = not isinstance(blobref_list, basestring)
    result = {}
    if start_out is None:
      def start_out(blobref):
        buffer = cStringIO.StringIO()
        return buffer

      def end_out(blobref, file_like):
        result[blobref] = file_like.getvalue()
    else:
      result = None  # Rely on user-supplied start_out for reporting blobrefs.
      if end_out is None:
        def end_out(blobref, file_like):
          file_like.close()

    self._setup_connection()

    # Note, we could use a 'preupload' here as a quick, bulk existence check,
    # but that may not always work depending on the access the user has.
    # It's possible the user has read-only access, and thus can only do
    # GET or HEAD on objects.

    for blobref in blobref_list:
      logging.debug('Getting blobref=%s', blobref)
      if self.basepath:
          fullpath = self.basepath + '/camli/'
      else:
          fullpath = '/camli/'
      self.connection.request('GET', fullpath + blobref,
                              headers={'Authorization': self._authorization})
      response = self.connection.getresponse()
      if response.status == 404:
        logging.debug('Server does not have blobref=%s', blobref)
        continue
      elif response.status != 200:
        raise ServerError('Bad response status: %d %s' %
                          (response.status, response.reason))

      if check_sha1:
        compute_hash = hashlib.sha1()

      out_file = start_out(blobref)
      while True:
        buf = response.read(self.buffer_size)
        if buf == '':
          end_out(blobref, out_file)
          break

        if check_sha1:
          compute_hash.update(buf)

        out_file.write(buf)

      if check_sha1:
        found = 'sha1-' + compute_hash.hexdigest()
        if found != blobref:
          raise ValueError('sha1 hash of blobref does not match; '
                           'found %s, expected %s' % (found, blobref))

    if result and not multiple:
      return result.values()[0]
    return result
