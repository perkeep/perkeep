# Copyright (c) 2010 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.

"""A database of OWNERS files."""

import collections
import re


# If this is present by itself on a line, this means that everyone can review.
EVERYONE = '*'


# Recognizes 'X@Y' email addresses. Very simplistic.
BASIC_EMAIL_REGEXP = r'^[\w\-\+\%\.]+\@[\w\-\+\%\.]+$'


def _assert_is_collection(obj):
  assert not isinstance(obj, basestring)
  # Module 'collections' has no 'Iterable' member
  # pylint: disable=E1101
  if hasattr(collections, 'Iterable') and hasattr(collections, 'Sized'):
    assert (isinstance(obj, collections.Iterable) and
            isinstance(obj, collections.Sized))


class SyntaxErrorInOwnersFile(Exception):
  def __init__(self, path, lineno, msg):
    super(SyntaxErrorInOwnersFile, self).__init__((path, lineno, msg))
    self.path = path
    self.lineno = lineno
    self.msg = msg

  def __str__(self):
    return "%s:%d syntax error: %s" % (self.path, self.lineno, self.msg)


class Database(object):
  """A database of OWNERS files for a repository.

  This class allows you to find a suggested set of reviewers for a list
  of changed files, and see if a list of changed files is covered by a
  list of reviewers."""

  def __init__(self, root, fopen, os_path):
    """Args:
      root: the path to the root of the Repository
      open: function callback to open a text file for reading
      os_path: module/object callback with fields for 'abspath', 'dirname',
          'exists', and 'join'
    """
    self.root = root
    self.fopen = fopen
    self.os_path = os_path

    # Pick a default email regexp to use; callers can override as desired.
    self.email_regexp = re.compile(BASIC_EMAIL_REGEXP)

    # Mapping of owners to the paths they own.
    self.owned_by = {EVERYONE: set()}

    # Mapping of paths to authorized owners.
    self.owners_for = {}

    # Set of paths that stop us from looking above them for owners.
    # (This is implicitly true for the root directory).
    self.stop_looking = set([''])

  def reviewers_for(self, files):
    """Returns a suggested set of reviewers that will cover the files.

    files is a sequence of paths relative to (and under) self.root."""
    self._check_paths(files)
    self._load_data_needed_for(files)
    return self._covering_set_of_owners_for(files)

  def files_are_covered_by(self, files, reviewers):
    """Returns whether every file is owned by at least one reviewer."""
    return not self.files_not_covered_by(files, reviewers)

  def files_not_covered_by(self, files, reviewers):
    """Returns the set of files that are not owned by at least one reviewer.

    Args:
        files is a sequence of paths relative to (and under) self.root.
        reviewers is a sequence of strings matching self.email_regexp."""
    self._check_paths(files)
    self._check_reviewers(reviewers)
    if not reviewers:
      return files

    self._load_data_needed_for(files)
    files_by_dir = self._files_by_dir(files)
    covered_dirs = self._dirs_covered_by(reviewers)
    uncovered_files = []
    for d, files_in_d in files_by_dir.iteritems():
      if not self._is_dir_covered_by(d, covered_dirs):
        uncovered_files.extend(files_in_d)
    return set(uncovered_files)

  def _check_paths(self, files):
    def _is_under(f, pfx):
      return self.os_path.abspath(self.os_path.join(pfx, f)).startswith(pfx)
    _assert_is_collection(files)
    assert all(_is_under(f, self.os_path.abspath(self.root)) for f in files)

  def _check_reviewers(self, reviewers):
    _assert_is_collection(reviewers)
    assert all(self.email_regexp.match(r) for r in reviewers)

  def _files_by_dir(self, files):
    dirs = {}
    for f in files:
      dirs.setdefault(self.os_path.dirname(f), []).append(f)
    return dirs

  def _dirs_covered_by(self, reviewers):
    dirs = self.owned_by[EVERYONE]
    for r in reviewers:
      dirs = dirs | self.owned_by.get(r, set())
    return dirs

  def _stop_looking(self, dirname):
    return dirname in self.stop_looking

  def _is_dir_covered_by(self, dirname, covered_dirs):
    while not dirname in covered_dirs and not self._stop_looking(dirname):
      dirname = self.os_path.dirname(dirname)
    return dirname in covered_dirs

  def _load_data_needed_for(self, files):
    for f in files:
      dirpath = self.os_path.dirname(f)
      while not dirpath in self.owners_for:
        self._read_owners_in_dir(dirpath)
        if self._stop_looking(dirpath):
          break
        dirpath = self.os_path.dirname(dirpath)

  def _read_owners_in_dir(self, dirpath):
    owners_path = self.os_path.join(self.root, dirpath, 'OWNERS')
    if not self.os_path.exists(owners_path):
      return

    lineno = 0
    for line in self.fopen(owners_path):
      lineno += 1
      line = line.strip()
      if line.startswith('#'):
        continue
      if line == 'set noparent':
        self.stop_looking.add(dirpath)
        continue
      if line.startswith('set '):
        raise SyntaxErrorInOwnersFile(owners_path, lineno,
            'unknown option: "%s"' % line[4:].strip())
      if self.email_regexp.match(line) or line == EVERYONE:
        self.owned_by.setdefault(line, set()).add(dirpath)
        self.owners_for.setdefault(dirpath, set()).add(line)
        continue
      raise SyntaxErrorInOwnersFile(owners_path, lineno,
            ('line is not a comment, a "set" directive, '
             'or an email address: "%s"' % line))

  def _covering_set_of_owners_for(self, files):
    # TODO(dpranke): implement the greedy algorithm for covering sets, and
    # consider returning multiple options in case there are several equally
    # short combinations of owners.
    every_owner = set()
    for f in files:
      dirname = self.os_path.dirname(f)
      while dirname in self.owners_for:
        every_owner |= self.owners_for[dirname]
        if self._stop_looking(dirname):
          break
        dirname = self.os_path.dirname(dirname)
    return every_owner
