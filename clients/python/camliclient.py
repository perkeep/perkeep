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
"""Client library and command-line client for Camlistore."""

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
               auth=None):
    """Initializer.

    Args:
      server_address: hostname:port for the server.
      buffer_size: Byte size to use for in-memory buffering for various
        client-related operations.
      create_connection: Use for testing.
      auth: Optional. 'username:password' to use for HTTP basic auth.
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
    self.connection.request(
        'POST', '/camli/preupload', urllib.urlencode(preupload),
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
      self.connection.request('GET', '/camli/' + blobref)
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

################################################################################
# Begin command-line tool

def _upload_files(op, path_list):
  """Uploads a list of files.

  Args:
    op: The CamliOp to use.
    path_list: The list of file paths to upload.

  Returns:
    Exit code.
  """
  real_path_set = set([os.path.abspath(path) for path in path_list])
  all_blob_files = [open(path, 'rb') for path in real_path_set]
  logging.debug('Uploading blob paths: %r', real_path_set)
  op.put_blobs(all_blob_files)
  return 0


def _upload_dir(op, root_path, recursive=True, ignore_patterns=[r'^\..*']):
  """Uploads a directory of files recursively.

  Args:
    op: The CamliOp to use.
    root_path: The path of the directory to upload.
    recursively: If the whole directory and its children should be uploaded.
    ignore_patterns: Set of ignore regex expressions.

  Returns:
    Exit code.
  """
  # TODO: Make ignore patterns into a command-line flag.
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

  logging.debug('Uploading dir=%r', root_path)
  _upload_files(op, all_blob_paths)
  return 0


def _download_files(op, blobref_list, target_dir):
  """Downloads blobs to a target directory.

  Args:
    op: The CamliOp to use.
    blobref_list: The list of blobrefs to download.
    target_dir: The directory to save the downloaded blobrefs in.

  Returns:
    Exit code. 1 if there were any missing blobrefs.
  """
  all_blobs = set(blobref_list)
  found_blobs = set()

  def start_out(blobref):
    blob_path = os.path.join(target_dir, blobref)
    return open(blob_path, 'wb')

  def end_out(blobref, blob_file):
    found_blobs.add(blobref)
    blob_file.close()

  op.get_blobs(blobref_list, start_out=start_out, end_out=end_out)
  missing_blobs = all_blobs - found_blobs
  if missing_blobs:
    print >>sys.stderr, 'Missing blobrefs: %s' % ', '.join(missing_blobs)
    return 1
  else:
    return 0


def main(argv):
  usage = \
"""usage: %prog [options] [command]

Commands:
  put <filepath> ... [filepathN]
  \t\t\tupload a set of specific files
  putdir <directory>
  \t\t\tput all blobs present in a directory recursively
  get <blobref> ... [blobrefN] <directory>
  \t\t\tget and save blobs to a directory, named as their blobrefs;
  \t\t\t(!) files already present will be overwritten"""
  parser = optparse.OptionParser(usage=usage)
  parser.add_option('-a', '--auth', dest='auth',
                    default='',
                    help='username:pasword for HTTP basic authentication')
  parser.add_option('-s', '--server', dest='server',
                    default='localhost:8080',
                    help='hostname:port to connect to')
  parser.add_option('-d', '--debug', dest='debug',
                    action='store_true',
                    help='print debug logging')

  def _error_and_exit(message):
    print >>sys.stderr, message, '\n'
    parser.print_help()
    sys.exit(2)

  opts, args = parser.parse_args(argv[1:])
  if not args:
    parser.print_help()
    sys.exit(2)

  if opts.debug:
    logging.getLogger().setLevel(logging.DEBUG)

  op = CamliOp(opts.server, auth=opts.auth)
  command = args[0].lower()

  if command == 'putdir':
    if len(args) != 2:
      _error_and_exit('Must supply directory to put')
    return _upload_dir(op, args[1])
  elif command == 'put':
    if len(args) < 2:
      _error_and_exit('Must supply one or more file paths to upload')
    return _upload_files(op, args[1:])
  elif command == 'get':
    if len(args) < 3:
      _error_and_exit('Must supply one or more blobrefs to download '
                      'and a directory to save them to')
    return _download_files(op, args[1:-1], args[-1])
  else:
    _error_and_exit('Unknown command: %s' % command)


if __name__ == '__main__':
  sys.exit(main(sys.argv))
