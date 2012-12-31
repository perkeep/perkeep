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
"""Command-line example client for Camlistore."""

__author__ = 'Brett Slatkin (bslatkin@gmail.com)'

import logging
import optparse
import os
import re
import sys

try:
  import camli.op
except ImportError:
  sys.path.insert(0, '../../lib/python')
  import camli.op


def upload_files(op, path_list):
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


def upload_dir(op, root_path, recursive=True, ignore_patterns=[r'^\..*']):
  """Uploads a directory of files recursively.

  Args:
    op: The CamliOp to use.
    root_path: The path of the directory to upload.
    recursively: If the whole directory and its children should be uploaded.
    ignore_patterns: Set of ignore regex expressions.

  Returns:
    Exit code.
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

  logging.debug('Uploading dir=%r', root_path)
  upload_files(op, all_blob_paths)
  return 0


def download_files(op, blobref_list, target_dir):
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
                    default='localhost:3179',
                    help='hostname:port to connect to')
  parser.add_option('-d', '--debug', dest='debug',
                    action='store_true',
                    help='print debug logging')
  parser.add_option('-i', '--ignore_patterns', dest="ignore_patterns",
                    default="",
                    help='regexp patterns to ignore')

  def error_and_exit(message):
    print >>sys.stderr, message, '\n'
    parser.print_help()
    sys.exit(2)

  opts, args = parser.parse_args(argv[1:])
  if not args:
    parser.print_help()
    sys.exit(2)

  if opts.debug:
    logging.getLogger().setLevel(logging.DEBUG)

  op = camli.op.CamliOp(opts.server, auth=opts.auth, basepath="/bs")
  command = args[0].lower()

  if command == 'putdir':
    if len(args) < 2:
      error_and_exit('Must supply at least a directory to put')
    return upload_dir(op, args[1], opts.ignore_patterns)
  elif command == 'put':
    if len(args) < 2:
      error_and_exit('Must supply one or more file paths to upload')
    return upload_files(op, args[1:])
  elif command == 'get':
    if len(args) < 3:
      error_and_exit('Must supply one or more blobrefs to download '
                      'and a directory to save them to')
    return download_files(op, args[1:-1], args[-1])
  else:
    error_and_exit('Unknown command: %s' % command)


if __name__ == '__main__':
  sys.exit(main(sys.argv))
