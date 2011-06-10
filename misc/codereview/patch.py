# coding=utf8
# Copyright (c) 2011 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.
"""Utility functions to handle patches."""

import posixpath
import os
import re


class UnsupportedPatchFormat(Exception):
  def __init__(self, filename, status):
    super(UnsupportedPatchFormat, self).__init__(filename, status)
    self.filename = filename
    self.status = status

  def __str__(self):
    out = 'Can\'t process patch for file %s.' % self.filename
    if self.status:
      out += '\n%s' % self.status
    return out


class FilePatchBase(object):
  """Defines a single file being modified.

  '/' is always used instead of os.sep for consistency.
  """
  is_delete = False
  is_binary = False

  def __init__(self, filename):
    self.filename = None
    self._set_filename(filename)

  def _set_filename(self, filename):
    self.filename = filename.replace('\\', '/')
    # Blacklist a few characters for simplicity.
    for i in ('%', '$', '..', '\'', '"'):
      if i in self.filename:
        self._fail('Can\'t use \'%s\' in filename.' % i)
    for i in ('/', 'CON', 'COM'):
      if self.filename.startswith(i):
        self._fail('Filename can\'t start with \'%s\'.' % i)

  def get(self):
    raise NotImplementedError('Nothing to grab')

  def set_relpath(self, relpath):
    if not relpath:
      return
    relpath = relpath.replace('\\', '/')
    if relpath[0] == '/':
      self._fail('Relative path starts with %s' % relpath[0])
    self._set_filename(posixpath.join(relpath, self.filename))

  def _fail(self, msg):
    raise UnsupportedPatchFormat(self.filename, msg)


class FilePatchDelete(FilePatchBase):
  """Deletes a file."""
  is_delete = True

  def __init__(self, filename, is_binary):
    super(FilePatchDelete, self).__init__(filename)
    self.is_binary = is_binary

  def get(self):
    raise NotImplementedError('Nothing to grab')


class FilePatchBinary(FilePatchBase):
  """Content of a new binary file."""
  is_binary = True

  def __init__(self, filename, data, svn_properties):
    super(FilePatchBinary, self).__init__(filename)
    self.data = data
    self.svn_properties = svn_properties or []

  def get(self):
    return self.data


class FilePatchDiff(FilePatchBase):
  """Patch for a single file."""

  def __init__(self, filename, diff, svn_properties):
    super(FilePatchDiff, self).__init__(filename)
    self.diff_header, self.diff_hunks = self._split_header(diff)
    self.svn_properties = svn_properties or []
    self.is_git_diff = self._is_git_diff_header(self.diff_header)
    self.patchlevel = 0
    if self.is_git_diff:
      self._verify_git_header()
      assert not svn_properties
    else:
      self._verify_svn_header()

  def get(self):
    return self.diff_header + self.diff_hunks

  def set_relpath(self, relpath):
    old_filename = self.filename
    super(FilePatchDiff, self).set_relpath(relpath)
    # Update the header too.
    self.diff_header = self.diff_header.replace(old_filename, self.filename)

  def _split_header(self, diff):
    """Splits a diff in two: the header and the hunks."""
    header = []
    hunks = diff.splitlines(True)
    while hunks:
      header.append(hunks.pop(0))
      if header[-1].startswith('--- '):
        break
    else:
      # Some diff may not have a ---/+++ set like a git rename with no change or
      # a svn diff with only property change.
      pass

    if hunks:
      if not hunks[0].startswith('+++ '):
        self._fail('Inconsistent header')
      header.append(hunks.pop(0))
      if hunks:
        if not hunks[0].startswith('@@ '):
          self._fail('Inconsistent hunk header')

    # Mangle any \\ in the header to /.
    header_lines = ('Index:', 'diff', 'copy', 'rename', '+++', '---')
    basename = os.path.basename(self.filename)
    for i in xrange(len(header)):
      if (header[i].split(' ', 1)[0] in header_lines or
          header[i].endswith(basename)):
        header[i] = header[i].replace('\\', '/')
    return ''.join(header), ''.join(hunks)

  @staticmethod
  def _is_git_diff_header(diff_header):
    """Returns True if the diff for a single files was generated with git."""
    # Delete: http://codereview.chromium.org/download/issue6368055_22_29.diff
    # Rename partial change:
    # http://codereview.chromium.org/download/issue6250123_3013_6010.diff
    # Rename no change:
    # http://codereview.chromium.org/download/issue6287022_3001_4010.diff
    return any(l.startswith('diff --git') for l in diff_header.splitlines())

  def mangle(self, string):
    """Mangle a file path."""
    return '/'.join(string.replace('\\', '/').split('/')[self.patchlevel:])

  def _verify_git_header(self):
    """Sanity checks the header.

    Expects the following format:

    <garbagge>
    diff --git (|a/)<filename> (|b/)<filename>
    <similarity>
    <filemode changes>
    <index>
    <copy|rename from>
    <copy|rename to>
    --- <filename>
    +++ <filename>

    Everything is optional except the diff --git line.
    """
    lines = self.diff_header.splitlines()

    # Verify the diff --git line.
    old = None
    new = None
    while lines:
      match = re.match(r'^diff \-\-git (.*?) (.*)$', lines.pop(0))
      if not match:
        continue
      old = match.group(1).replace('\\', '/')
      new = match.group(2).replace('\\', '/')
      if old.startswith('a/') and new.startswith('b/'):
        self.patchlevel = 1
        old = old[2:]
        new = new[2:]
      # The rename is about the new file so the old file can be anything.
      if new not in (self.filename, 'dev/null'):
        self._fail('Unexpected git diff output name %s.' % new)
      if old == 'dev/null' and new == 'dev/null':
        self._fail('Unexpected /dev/null git diff.')
      break

    if not old or not new:
      self._fail('Unexpected git diff; couldn\'t find git header.')

    # Handle these:
    # rename from <>
    # rename to <>
    # copy from <>
    # copy to <>
    while lines:
      if lines[0].startswith('--- '):
        break
      match = re.match(r'^(rename|copy) from (.+)$', lines.pop(0))
      if not match:
        continue
      if old != match.group(2):
        self._fail('Unexpected git diff input name for %s.' % match.group(1))
      if not lines:
        self._fail('Missing git diff output name for %s.' % match.group(1))
      match = re.match(r'^(rename|copy) to (.+)$', lines.pop(0))
      if not match:
        self._fail('Missing git diff output name for %s.' % match.group(1))
      if new != match.group(2):
        self._fail('Unexpected git diff output name for %s.' % match.group(1))

    # Handle ---/+++
    while lines:
      match = re.match(r'^--- (.*)$', lines.pop(0))
      if not match:
        continue
      if old != self.mangle(match.group(1)) and match.group(1) != '/dev/null':
        self._fail('Unexpected git diff: %s != %s.' % (old, match.group(1)))
      if not lines:
        self._fail('Missing git diff output name.')
      match = re.match(r'^\+\+\+ (.*)$', lines.pop(0))
      if not match:
        self._fail('Unexpected git diff: --- not following +++.')
      if new != self.mangle(match.group(1)) and '/dev/null' != match.group(1):
        self._fail('Unexpected git diff: %s != %s.' % (new, match.group(1)))
      assert not lines, '_split_header() is broken'
      break

  def _verify_svn_header(self):
    """Sanity checks the header.

    A svn diff can contain only property changes, in that case there will be no
    proper header. To make things worse, this property change header is
    localized.
    """
    lines = self.diff_header.splitlines()
    while lines:
      match = re.match(r'^--- ([^\t]+).*$', lines.pop(0))
      if not match:
        continue
      if match.group(1) not in (self.filename, '/dev/null'):
        self._fail('Unexpected diff: %s.' % match.group(1))
      match = re.match(r'^\+\+\+ ([^\t]+).*$', lines.pop(0))
      if not match:
        self._fail('Unexpected diff: --- not following +++.')
      if match.group(1) not in (self.filename, '/dev/null'):
        self._fail('Unexpected diff: %s.' % match.group(1))
      assert not lines, '_split_header() is broken'
      break
    else:
      # Cheap check to make sure the file name is at least mentioned in the
      # 'diff' header. That the only remaining invariant.
      if not self.filename in self.diff_header:
        self._fail('Diff seems corrupted.')


class PatchSet(object):
  """A list of FilePatch* objects."""

  def __init__(self, patches):
    self.patches = patches

  def set_relpath(self, relpath):
    """Used to offset the patch into a subdirectory."""
    for patch in self.patches:
      patch.set_relpath(relpath)

  def __iter__(self):
    for patch in self.patches:
      yield patch

  @property
  def filenames(self):
    return [p.filename for p in self.patches]
