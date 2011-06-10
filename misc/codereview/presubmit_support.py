#!/usr/bin/env python
# Copyright (c) 2011 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.

"""Enables directory-specific presubmit checks to run at upload and/or commit.
"""

__version__ = '1.6.1'

# TODO(joi) Add caching where appropriate/needed. The API is designed to allow
# caching (between all different invocations of presubmit scripts for a given
# change). We should add it as our presubmit scripts start feeling slow.

import cPickle  # Exposed through the API.
import cStringIO  # Exposed through the API.
import fnmatch
import glob
import logging
import marshal  # Exposed through the API.
import optparse
import os  # Somewhat exposed through the API.
import pickle  # Exposed through the API.
import random
import re  # Exposed through the API.
import sys  # Parts exposed through API.
import tempfile  # Exposed through the API.
import time
import traceback  # Exposed through the API.
import types
import unittest  # Exposed through the API.
import urllib2  # Exposed through the API.
from warnings import warn

try:
  import simplejson as json  # pylint: disable=F0401
except ImportError:
  try:
    import json  # pylint: disable=F0401
  except ImportError:
    # Import the one included in depot_tools.
    sys.path.append(os.path.join(os.path.dirname(__file__), 'third_party'))
    import simplejson as json  # pylint: disable=F0401

# Local imports.
import fix_encoding
import gclient_utils
import owners
import presubmit_canned_checks
import scm
import subprocess2 as subprocess  # Exposed through the API.


# Ask for feedback only once in program lifetime.
_ASKED_FOR_FEEDBACK = False


class PresubmitFailure(Exception):
  pass


def normpath(path):
  '''Version of os.path.normpath that also changes backward slashes to
  forward slashes when not running on Windows.
  '''
  # This is safe to always do because the Windows version of os.path.normpath
  # will replace forward slashes with backward slashes.
  path = path.replace(os.sep, '/')
  return os.path.normpath(path)


def _RightHandSideLinesImpl(affected_files):
  """Implements RightHandSideLines for InputApi and GclChange."""
  for af in affected_files:
    lines = af.ChangedContents()
    for line in lines:
      yield (af, line[0], line[1])


class PresubmitOutput(object):
  def __init__(self, input_stream=None, output_stream=None):
    self.input_stream = input_stream
    self.output_stream = output_stream
    self.reviewers = []
    self.written_output = []
    self.error_count = 0

  def prompt_yes_no(self, prompt_string):
    self.write(prompt_string)
    if self.input_stream:
      response = self.input_stream.readline().strip().lower()
      if response not in ('y', 'yes'):
        self.fail()
    else:
      self.fail()

  def fail(self):
    self.error_count += 1

  def should_continue(self):
    return not self.error_count

  def write(self, s):
    self.written_output.append(s)
    if self.output_stream:
      self.output_stream.write(s)

  def getvalue(self):
    return ''.join(self.written_output)


class OutputApi(object):
  """This class (more like a module) gets passed to presubmit scripts so that
  they can specify various types of results.
  """
  class PresubmitResult(object):
    """Base class for result objects."""
    fatal = False
    should_prompt = False

    def __init__(self, message, items=None, long_text=''):
      """
      message: A short one-line message to indicate errors.
      items: A list of short strings to indicate where errors occurred.
      long_text: multi-line text output, e.g. from another tool
      """
      self._message = message
      self._items = []
      if items:
        self._items = items
      self._long_text = long_text.rstrip()

    def handle(self, output):
      output.write(self._message)
      output.write('\n')
      for index, item in enumerate(self._items):
        output.write('  ')
        # Write separately in case it's unicode.
        output.write(str(item))
        if index < len(self._items) - 1:
          output.write(' \\')
        output.write('\n')
      if self._long_text:
        output.write('\n***************\n')
        # Write separately in case it's unicode.
        output.write(self._long_text)
        output.write('\n***************\n')
      if self.fatal:
        output.fail()

  class PresubmitAddReviewers(PresubmitResult):
    """Add some suggested reviewers to the change."""
    def __init__(self, reviewers):
      super(OutputApi.PresubmitAddReviewers, self).__init__('')
      self.reviewers = reviewers

    def handle(self, output):
      output.reviewers.extend(self.reviewers)

  class PresubmitError(PresubmitResult):
    """A hard presubmit error."""
    fatal = True

  class PresubmitPromptWarning(PresubmitResult):
    """An warning that prompts the user if they want to continue."""
    should_prompt = True

  class PresubmitNotifyResult(PresubmitResult):
    """Just print something to the screen -- but it's not even a warning."""
    pass

  class MailTextResult(PresubmitResult):
    """A warning that should be included in the review request email."""
    def __init__(self, *args, **kwargs):
      super(OutputApi.MailTextResult, self).__init__()
      raise NotImplementedError()


class InputApi(object):
  """An instance of this object is passed to presubmit scripts so they can
  know stuff about the change they're looking at.
  """
  # Method could be a function
  # pylint: disable=R0201

  # File extensions that are considered source files from a style guide
  # perspective. Don't modify this list from a presubmit script!
  DEFAULT_WHITE_LIST = (
      # C++ and friends
      r".*\.c$", r".*\.cc$", r".*\.cpp$", r".*\.h$", r".*\.m$", r".*\.mm$",
      r".*\.inl$", r".*\.asm$", r".*\.hxx$", r".*\.hpp$", r".*\.s$", r".*\.S$",
      # Scripts
      r".*\.js$", r".*\.py$", r".*\.sh$", r".*\.rb$", r".*\.pl$", r".*\.pm$",
      # No extension at all, note that ALL CAPS files are black listed in
      # DEFAULT_BLACK_LIST below.
      r"(^|.*?[\\\/])[^.]+$",
      # Other
      r".*\.java$", r".*\.mk$", r".*\.am$",
  )

  # Path regexp that should be excluded from being considered containing source
  # files. Don't modify this list from a presubmit script!
  DEFAULT_BLACK_LIST = (
      r".*\bexperimental[\\\/].*",
      r".*\bthird_party[\\\/].*",
      # Output directories (just in case)
      r".*\bDebug[\\\/].*",
      r".*\bRelease[\\\/].*",
      r".*\bxcodebuild[\\\/].*",
      r".*\bsconsbuild[\\\/].*",
      # All caps files like README and LICENCE.
      r".*\b[A-Z0-9_]{2,}$",
      # SCM (can happen in dual SCM configuration). (Slightly over aggressive)
      r"(|.*[\\\/])\.git[\\\/].*",
      r"(|.*[\\\/])\.svn[\\\/].*",
  )

  def __init__(self, change, presubmit_path, is_committing, tbr,
      rietveld, verbose):
    """Builds an InputApi object.

    Args:
      change: A presubmit.Change object.
      presubmit_path: The path to the presubmit script being processed.
      is_committing: True if the change is about to be committed.
      tbr: True if '--tbr' was passed to skip any reviewer/owner checks
      rietveld: rietveld client object
    """
    # Version number of the presubmit_support script.
    self.version = [int(x) for x in __version__.split('.')]
    self.change = change
    self.is_committing = is_committing
    self.tbr = tbr
    self.rietveld = rietveld
    # TBD
    self.host_url = 'http://codereview.appspot.com'
    if self.rietveld:
      self.host_url = rietveld.url

    # We expose various modules and functions as attributes of the input_api
    # so that presubmit scripts don't have to import them.
    self.basename = os.path.basename
    self.cPickle = cPickle
    self.cStringIO = cStringIO
    self.json = json
    self.os_listdir = os.listdir
    self.os_walk = os.walk
    self.os_path = os.path
    self.pickle = pickle
    self.marshal = marshal
    self.re = re
    self.subprocess = subprocess
    self.tempfile = tempfile
    self.time = time
    self.traceback = traceback
    self.unittest = unittest
    self.urllib2 = urllib2

    # To easily fork python.
    self.python_executable = sys.executable
    self.environ = os.environ

    # InputApi.platform is the platform you're currently running on.
    self.platform = sys.platform

    # The local path of the currently-being-processed presubmit script.
    self._current_presubmit_path = os.path.dirname(presubmit_path)

    # We carry the canned checks so presubmit scripts can easily use them.
    self.canned_checks = presubmit_canned_checks

    # TODO(dpranke): figure out a list of all approved owners for a repo
    # in order to be able to handle wildcard OWNERS files?
    self.owners_db = owners.Database(change.RepositoryRoot(),
        fopen=file, os_path=self.os_path)
    self.verbose = verbose

  def PresubmitLocalPath(self):
    """Returns the local path of the presubmit script currently being run.

    This is useful if you don't want to hard-code absolute paths in the
    presubmit script.  For example, It can be used to find another file
    relative to the PRESUBMIT.py script, so the whole tree can be branched and
    the presubmit script still works, without editing its content.
    """
    return self._current_presubmit_path

  def DepotToLocalPath(self, depot_path):
    """Translate a depot path to a local path (relative to client root).

    Args:
      Depot path as a string.

    Returns:
      The local path of the depot path under the user's current client, or None
      if the file is not mapped.

      Remember to check for the None case and show an appropriate error!
    """
    local_path = scm.SVN.CaptureInfo(depot_path).get('Path')
    if local_path:
      return local_path

  def LocalToDepotPath(self, local_path):
    """Translate a local path to a depot path.

    Args:
      Local path (relative to current directory, or absolute) as a string.

    Returns:
      The depot path (SVN URL) of the file if mapped, otherwise None.
    """
    depot_path = scm.SVN.CaptureInfo(local_path).get('URL')
    if depot_path:
      return depot_path

  def AffectedFiles(self, include_dirs=False, include_deletes=True):
    """Same as input_api.change.AffectedFiles() except only lists files
    (and optionally directories) in the same directory as the current presubmit
    script, or subdirectories thereof.
    """
    dir_with_slash = normpath("%s/" % self.PresubmitLocalPath())
    if len(dir_with_slash) == 1:
      dir_with_slash = ''
    return filter(
        lambda x: normpath(x.AbsoluteLocalPath()).startswith(dir_with_slash),
        self.change.AffectedFiles(include_dirs, include_deletes))

  def LocalPaths(self, include_dirs=False):
    """Returns local paths of input_api.AffectedFiles()."""
    return [af.LocalPath() for af in self.AffectedFiles(include_dirs)]

  def AbsoluteLocalPaths(self, include_dirs=False):
    """Returns absolute local paths of input_api.AffectedFiles()."""
    return [af.AbsoluteLocalPath() for af in self.AffectedFiles(include_dirs)]

  def ServerPaths(self, include_dirs=False):
    """Returns server paths of input_api.AffectedFiles()."""
    return [af.ServerPath() for af in self.AffectedFiles(include_dirs)]

  def AffectedTextFiles(self, include_deletes=None):
    """Same as input_api.change.AffectedTextFiles() except only lists files
    in the same directory as the current presubmit script, or subdirectories
    thereof.
    """
    if include_deletes is not None:
      warn("AffectedTextFiles(include_deletes=%s)"
               " is deprecated and ignored" % str(include_deletes),
           category=DeprecationWarning,
           stacklevel=2)
    return filter(lambda x: x.IsTextFile(),
                  self.AffectedFiles(include_dirs=False, include_deletes=False))

  def FilterSourceFile(self, affected_file, white_list=None, black_list=None):
    """Filters out files that aren't considered "source file".

    If white_list or black_list is None, InputApi.DEFAULT_WHITE_LIST
    and InputApi.DEFAULT_BLACK_LIST is used respectively.

    The lists will be compiled as regular expression and
    AffectedFile.LocalPath() needs to pass both list.

    Note: Copy-paste this function to suit your needs or use a lambda function.
    """
    def Find(affected_file, items):
      local_path = affected_file.LocalPath()
      for item in items:
        if self.re.match(item, local_path):
          logging.debug("%s matched %s" % (item, local_path))
          return True
      return False
    return (Find(affected_file, white_list or self.DEFAULT_WHITE_LIST) and
            not Find(affected_file, black_list or self.DEFAULT_BLACK_LIST))

  def AffectedSourceFiles(self, source_file):
    """Filter the list of AffectedTextFiles by the function source_file.

    If source_file is None, InputApi.FilterSourceFile() is used.
    """
    if not source_file:
      source_file = self.FilterSourceFile
    return filter(source_file, self.AffectedTextFiles())

  def RightHandSideLines(self, source_file_filter=None):
    """An iterator over all text lines in "new" version of changed files.

    Only lists lines from new or modified text files in the change that are
    contained by the directory of the currently executing presubmit script.

    This is useful for doing line-by-line regex checks, like checking for
    trailing whitespace.

    Yields:
      a 3 tuple:
        the AffectedFile instance of the current file;
        integer line number (1-based); and
        the contents of the line as a string.

    Note: The cariage return (LF or CR) is stripped off.
    """
    files = self.AffectedSourceFiles(source_file_filter)
    return _RightHandSideLinesImpl(files)

  def ReadFile(self, file_item, mode='r'):
    """Reads an arbitrary file.

    Deny reading anything outside the repository.
    """
    if isinstance(file_item, AffectedFile):
      file_item = file_item.AbsoluteLocalPath()
    if not file_item.startswith(self.change.RepositoryRoot()):
      raise IOError('Access outside the repository root is denied.')
    return gclient_utils.FileRead(file_item, mode)


class AffectedFile(object):
  """Representation of a file in a change."""
  # Method could be a function
  # pylint: disable=R0201
  def __init__(self, path, action, repository_root=''):
    self._path = path
    self._action = action
    self._local_root = repository_root
    self._is_directory = None
    self._properties = {}
    logging.debug('%s(%s)' % (self.__class__.__name__, self._path))

  def ServerPath(self):
    """Returns a path string that identifies the file in the SCM system.

    Returns the empty string if the file does not exist in SCM.
    """
    return ""

  def LocalPath(self):
    """Returns the path of this file on the local disk relative to client root.
    """
    return normpath(self._path)

  def AbsoluteLocalPath(self):
    """Returns the absolute path of this file on the local disk.
    """
    return os.path.abspath(os.path.join(self._local_root, self.LocalPath()))

  def IsDirectory(self):
    """Returns true if this object is a directory."""
    if self._is_directory is None:
      path = self.AbsoluteLocalPath()
      self._is_directory = (os.path.exists(path) and
                            os.path.isdir(path))
    return self._is_directory

  def Action(self):
    """Returns the action on this opened file, e.g. A, M, D, etc."""
    # TODO(maruel): Somewhat crappy, Could be "A" or "A  +" for svn but
    # different for other SCM.
    return self._action

  def Property(self, property_name):
    """Returns the specified SCM property of this file, or None if no such
    property.
    """
    return self._properties.get(property_name, None)

  def IsTextFile(self):
    """Returns True if the file is a text file and not a binary file.

    Deleted files are not text file."""
    raise NotImplementedError()  # Implement when needed

  def NewContents(self):
    """Returns an iterator over the lines in the new version of file.

    The new version is the file in the user's workspace, i.e. the "right hand
    side".

    Contents will be empty if the file is a directory or does not exist.
    Note: The cariage returns (LF or CR) are stripped off.
    """
    if self.IsDirectory():
      return []
    else:
      return gclient_utils.FileRead(self.AbsoluteLocalPath(),
                                    'rU').splitlines()

  def OldContents(self):
    """Returns an iterator over the lines in the old version of file.

    The old version is the file in depot, i.e. the "left hand side".
    """
    raise NotImplementedError()  # Implement when needed

  def OldFileTempPath(self):
    """Returns the path on local disk where the old contents resides.

    The old version is the file in depot, i.e. the "left hand side".
    This is a read-only cached copy of the old contents. *DO NOT* try to
    modify this file.
    """
    raise NotImplementedError()  # Implement if/when needed.

  def ChangedContents(self):
    """Returns a list of tuples (line number, line text) of all new lines.

     This relies on the scm diff output describing each changed code section
     with a line of the form

     ^@@ <old line num>,<old size> <new line num>,<new size> @@$
    """
    new_lines = []
    line_num = 0

    if self.IsDirectory():
      return []

    for line in self.GenerateScmDiff().splitlines():
      m = re.match(r'^@@ [0-9\,\+\-]+ \+([0-9]+)\,[0-9]+ @@', line)
      if m:
        line_num = int(m.groups(1)[0])
        continue
      if line.startswith('+') and not line.startswith('++'):
        new_lines.append((line_num, line[1:]))
      if not line.startswith('-'):
        line_num += 1
    return new_lines

  def __str__(self):
    return self.LocalPath()

  def GenerateScmDiff(self):
    raise NotImplementedError()  # Implemented in derived classes.

class SvnAffectedFile(AffectedFile):
  """Representation of a file in a change out of a Subversion checkout."""
  # Method 'NNN' is abstract in class 'NNN' but is not overridden
  # pylint: disable=W0223

  def __init__(self, *args, **kwargs):
    AffectedFile.__init__(self, *args, **kwargs)
    self._server_path = None
    self._is_text_file = None

  def ServerPath(self):
    if self._server_path is None:
      self._server_path = scm.SVN.CaptureInfo(
          self.AbsoluteLocalPath()).get('URL', '')
    return self._server_path

  def IsDirectory(self):
    if self._is_directory is None:
      path = self.AbsoluteLocalPath()
      if os.path.exists(path):
        # Retrieve directly from the file system; it is much faster than
        # querying subversion, especially on Windows.
        self._is_directory = os.path.isdir(path)
      else:
        self._is_directory = scm.SVN.CaptureInfo(
            path).get('Node Kind') in ('dir', 'directory')
    return self._is_directory

  def Property(self, property_name):
    if not property_name in self._properties:
      self._properties[property_name] = scm.SVN.GetFileProperty(
          self.AbsoluteLocalPath(), property_name).rstrip()
    return self._properties[property_name]

  def IsTextFile(self):
    if self._is_text_file is None:
      if self.Action() == 'D':
        # A deleted file is not a text file.
        self._is_text_file = False
      elif self.IsDirectory():
        self._is_text_file = False
      else:
        mime_type = scm.SVN.GetFileProperty(self.AbsoluteLocalPath(),
                                            'svn:mime-type')
        self._is_text_file = (not mime_type or mime_type.startswith('text/'))
    return self._is_text_file

  def GenerateScmDiff(self):
    return scm.SVN.GenerateDiff([self.AbsoluteLocalPath()])


class GitAffectedFile(AffectedFile):
  """Representation of a file in a change out of a git checkout."""
  # Method 'NNN' is abstract in class 'NNN' but is not overridden
  # pylint: disable=W0223

  def __init__(self, *args, **kwargs):
    AffectedFile.__init__(self, *args, **kwargs)
    self._server_path = None
    self._is_text_file = None

  def ServerPath(self):
    if self._server_path is None:
      raise NotImplementedError('TODO(maruel) Implement.')
    return self._server_path

  def IsDirectory(self):
    if self._is_directory is None:
      path = self.AbsoluteLocalPath()
      if os.path.exists(path):
        # Retrieve directly from the file system; it is much faster than
        # querying subversion, especially on Windows.
        self._is_directory = os.path.isdir(path)
      else:
        self._is_directory = False
    return self._is_directory

  def Property(self, property_name):
    if not property_name in self._properties:
      raise NotImplementedError('TODO(maruel) Implement.')
    return self._properties[property_name]

  def IsTextFile(self):
    if self._is_text_file is None:
      if self.Action() == 'D':
        # A deleted file is not a text file.
        self._is_text_file = False
      elif self.IsDirectory():
        self._is_text_file = False
      else:
        self._is_text_file = os.path.isfile(self.AbsoluteLocalPath())
    return self._is_text_file

  def GenerateScmDiff(self):
    return scm.GIT.GenerateDiff(self._local_root, files=[self.LocalPath(),])

class Change(object):
  """Describe a change.

  Used directly by the presubmit scripts to query the current change being
  tested.

  Instance members:
    tags: Dictionnary of KEY=VALUE pairs found in the change description.
    self.KEY: equivalent to tags['KEY']
  """

  _AFFECTED_FILES = AffectedFile

  # Matches key/value (or "tag") lines in changelist descriptions.
  _TAG_LINE_RE = re.compile(
      '^\s*(?P<key>[A-Z][A-Z_0-9]*)\s*=\s*(?P<value>.*?)\s*$')

  def __init__(self, name, description, local_root, files, issue, patchset):
    if files is None:
      files = []
    self._name = name
    self._full_description = description
    # Convert root into an absolute path.
    self._local_root = os.path.abspath(local_root)
    self.issue = issue
    self.patchset = patchset
    self.scm = ''

    # From the description text, build up a dictionary of key/value pairs
    # plus the description minus all key/value or "tag" lines.
    description_without_tags = []
    self.tags = {}
    for line in self._full_description.splitlines():
      m = self._TAG_LINE_RE.match(line)
      if m:
        self.tags[m.group('key')] = m.group('value')
      else:
        description_without_tags.append(line)

    # Change back to text and remove whitespace at end.
    self._description_without_tags = (
        '\n'.join(description_without_tags).rstrip())

    self._affected_files = [
        self._AFFECTED_FILES(info[1], info[0].strip(), self._local_root)
        for info in files
    ]

  def Name(self):
    """Returns the change name."""
    return self._name

  def DescriptionText(self):
    """Returns the user-entered changelist description, minus tags.

    Any line in the user-provided description starting with e.g. "FOO="
    (whitespace permitted before and around) is considered a tag line.  Such
    lines are stripped out of the description this function returns.
    """
    return self._description_without_tags

  def FullDescriptionText(self):
    """Returns the complete changelist description including tags."""
    return self._full_description

  def RepositoryRoot(self):
    """Returns the repository (checkout) root directory for this change,
    as an absolute path.
    """
    return self._local_root

  def __getattr__(self, attr):
    """Return tags directly as attributes on the object."""
    if not re.match(r"^[A-Z_]*$", attr):
      raise AttributeError(self, attr)
    return self.tags.get(attr)

  def AffectedFiles(self, include_dirs=False, include_deletes=True):
    """Returns a list of AffectedFile instances for all files in the change.

    Args:
      include_deletes: If false, deleted files will be filtered out.
      include_dirs: True to include directories in the list

    Returns:
      [AffectedFile(path, action), AffectedFile(path, action)]
    """
    if include_dirs:
      affected = self._affected_files
    else:
      affected = filter(lambda x: not x.IsDirectory(), self._affected_files)

    if include_deletes:
      return affected
    else:
      return filter(lambda x: x.Action() != 'D', affected)

  def AffectedTextFiles(self, include_deletes=None):
    """Return a list of the existing text files in a change."""
    if include_deletes is not None:
      warn("AffectedTextFiles(include_deletes=%s)"
               " is deprecated and ignored" % str(include_deletes),
           category=DeprecationWarning,
           stacklevel=2)
    return filter(lambda x: x.IsTextFile(),
                  self.AffectedFiles(include_dirs=False, include_deletes=False))

  def LocalPaths(self, include_dirs=False):
    """Convenience function."""
    return [af.LocalPath() for af in self.AffectedFiles(include_dirs)]

  def AbsoluteLocalPaths(self, include_dirs=False):
    """Convenience function."""
    return [af.AbsoluteLocalPath() for af in self.AffectedFiles(include_dirs)]

  def ServerPaths(self, include_dirs=False):
    """Convenience function."""
    return [af.ServerPath() for af in self.AffectedFiles(include_dirs)]

  def RightHandSideLines(self):
    """An iterator over all text lines in "new" version of changed files.

    Lists lines from new or modified text files in the change.

    This is useful for doing line-by-line regex checks, like checking for
    trailing whitespace.

    Yields:
      a 3 tuple:
        the AffectedFile instance of the current file;
        integer line number (1-based); and
        the contents of the line as a string.
    """
    return _RightHandSideLinesImpl(
        x for x in self.AffectedFiles(include_deletes=False)
        if x.IsTextFile())


class SvnChange(Change):
  _AFFECTED_FILES = SvnAffectedFile

  def __init__(self, *args, **kwargs):
    Change.__init__(self, *args, **kwargs)
    self.scm = 'svn'
    self._changelists = None

  def _GetChangeLists(self):
    """Get all change lists."""
    if self._changelists == None:
      previous_cwd = os.getcwd()
      os.chdir(self.RepositoryRoot())
      # Need to import here to avoid circular dependency.
      import gcl
      self._changelists = gcl.GetModifiedFiles()
      os.chdir(previous_cwd)
    return self._changelists

  def GetAllModifiedFiles(self):
    """Get all modified files."""
    changelists = self._GetChangeLists()
    all_modified_files = []
    for cl in changelists.values():
      all_modified_files.extend(
          [os.path.join(self.RepositoryRoot(), f[1]) for f in cl])
    return all_modified_files

  def GetModifiedFiles(self):
    """Get modified files in the current CL."""
    changelists = self._GetChangeLists()
    return [os.path.join(self.RepositoryRoot(), f[1])
            for f in changelists[self.Name()]]


class GitChange(Change):
  _AFFECTED_FILES = GitAffectedFile

  def __init__(self, *args, **kwargs):
    Change.__init__(self, *args, **kwargs)
    self.scm = 'git'


def ListRelevantPresubmitFiles(files, root):
  """Finds all presubmit files that apply to a given set of source files.

  If inherit-review-settings-ok is present right under root, looks for
  PRESUBMIT.py in directories enclosing root.

  Args:
    files: An iterable container containing file paths.
    root: Path where to stop searching.

  Return:
    List of absolute paths of the existing PRESUBMIT.py scripts.
  """
  files = [normpath(os.path.join(root, f)) for f in files]

  # List all the individual directories containing files.
  directories = set([os.path.dirname(f) for f in files])

  # Ignore root if inherit-review-settings-ok is present.
  if os.path.isfile(os.path.join(root, 'inherit-review-settings-ok')):
    root = None

  # Collect all unique directories that may contain PRESUBMIT.py.
  candidates = set()
  for directory in directories:
    while True:
      if directory in candidates:
        break
      candidates.add(directory)
      if directory == root:
        break
      parent_dir = os.path.dirname(directory)
      if parent_dir == directory:
        # We hit the system root directory.
        break
      directory = parent_dir

  # Look for PRESUBMIT.py in all candidate directories.
  results = []
  for directory in sorted(list(candidates)):
    p = os.path.join(directory, 'PRESUBMIT.py')
    if os.path.isfile(p):
      results.append(p)

  logging.debug('Presubmit files: %s' % ','.join(results))
  return results


class GetTrySlavesExecuter(object):
  @staticmethod
  def ExecPresubmitScript(script_text, presubmit_path):
    """Executes GetPreferredTrySlaves() from a single presubmit script.

    Args:
      script_text: The text of the presubmit script.

    Return:
      A list of try slaves.
    """
    context = {}
    try:
      exec script_text in context
    except Exception, e:
      raise PresubmitFailure('"%s" had an exception.\n%s' % (presubmit_path, e))

    function_name = 'GetPreferredTrySlaves'
    if function_name in context:
      result = eval(function_name + '()', context)
      if not isinstance(result, types.ListType):
        raise PresubmitFailure(
            'Presubmit functions must return a list, got a %s instead: %s' %
            (type(result), str(result)))
      for item in result:
        if not isinstance(item, basestring):
          raise PresubmitFailure('All try slaves names must be strings.')
        if item != item.strip():
          raise PresubmitFailure(
              'Try slave names cannot start/end with whitespace')
    else:
      result = []
    return result


def DoGetTrySlaves(changed_files,
                   repository_root,
                   default_presubmit,
                   verbose,
                   output_stream):
  """Get the list of try servers from the presubmit scripts.

  Args:
    changed_files: List of modified files.
    repository_root: The repository root.
    default_presubmit: A default presubmit script to execute in any case.
    verbose: Prints debug info.
    output_stream: A stream to write debug output to.

  Return:
    List of try slaves
  """
  presubmit_files = ListRelevantPresubmitFiles(changed_files, repository_root)
  if not presubmit_files and verbose:
    output_stream.write("Warning, no presubmit.py found.\n")
  results = []
  executer = GetTrySlavesExecuter()
  if default_presubmit:
    if verbose:
      output_stream.write("Running default presubmit script.\n")
    fake_path = os.path.join(repository_root, 'PRESUBMIT.py')
    results += executer.ExecPresubmitScript(default_presubmit, fake_path)
  for filename in presubmit_files:
    filename = os.path.abspath(filename)
    if verbose:
      output_stream.write("Running %s\n" % filename)
    # Accept CRLF presubmit script.
    presubmit_script = gclient_utils.FileRead(filename, 'rU')
    results += executer.ExecPresubmitScript(presubmit_script, filename)

  slaves = list(set(results))
  if slaves and verbose:
    output_stream.write(', '.join(slaves))
    output_stream.write('\n')
  return slaves


class PresubmitExecuter(object):
  def __init__(self, change, committing, tbr, rietveld, verbose):
    """
    Args:
      change: The Change object.
      committing: True if 'gcl commit' is running, False if 'gcl upload' is.
      tbr: True if '--tbr' was passed to skip any reviewer/owner checks
      rietveld: rietveld client object.
    """
    self.change = change
    self.committing = committing
    self.tbr = tbr
    self.rietveld = rietveld
    self.verbose = verbose

  def ExecPresubmitScript(self, script_text, presubmit_path):
    """Executes a single presubmit script.

    Args:
      script_text: The text of the presubmit script.
      presubmit_path: The path to the presubmit file (this will be reported via
        input_api.PresubmitLocalPath()).

    Return:
      A list of result objects, empty if no problems.
    """

    # Change to the presubmit file's directory to support local imports.
    main_path = os.getcwd()
    os.chdir(os.path.dirname(presubmit_path))

    # Load the presubmit script into context.
    input_api = InputApi(self.change, presubmit_path, self.committing,
                         self.tbr, self.rietveld, self.verbose)
    context = {}
    try:
      exec script_text in context
    except Exception, e:
      raise PresubmitFailure('"%s" had an exception.\n%s' % (presubmit_path, e))

    # These function names must change if we make substantial changes to
    # the presubmit API that are not backwards compatible.
    if self.committing:
      function_name = 'CheckChangeOnCommit'
    else:
      function_name = 'CheckChangeOnUpload'
    if function_name in context:
      context['__args'] = (input_api, OutputApi())
      logging.debug('Running %s in %s' % (function_name, presubmit_path))
      result = eval(function_name + '(*__args)', context)
      logging.debug('Running %s done.' % function_name)
      if not (isinstance(result, types.TupleType) or
              isinstance(result, types.ListType)):
        raise PresubmitFailure(
          'Presubmit functions must return a tuple or list')
      for item in result:
        if not isinstance(item, OutputApi.PresubmitResult):
          raise PresubmitFailure(
            'All presubmit results must be of types derived from '
            'output_api.PresubmitResult')
    else:
      result = ()  # no error since the script doesn't care about current event.

    # Return the process to the original working directory.
    os.chdir(main_path)
    return result


def DoPresubmitChecks(change,
                      committing,
                      verbose,
                      output_stream,
                      input_stream,
                      default_presubmit,
                      may_prompt,
                      tbr,
                      rietveld):
  """Runs all presubmit checks that apply to the files in the change.

  This finds all PRESUBMIT.py files in directories enclosing the files in the
  change (up to the repository root) and calls the relevant entrypoint function
  depending on whether the change is being committed or uploaded.

  Prints errors, warnings and notifications.  Prompts the user for warnings
  when needed.

  Args:
    change: The Change object.
    committing: True if 'gcl commit' is running, False if 'gcl upload' is.
    verbose: Prints debug info.
    output_stream: A stream to write output from presubmit tests to.
    input_stream: A stream to read input from the user.
    default_presubmit: A default presubmit script to execute in any case.
    may_prompt: Enable (y/n) questions on warning or error.
    tbr: was --tbr specified to skip any reviewer/owner checks?
    rietveld: rietveld object.

  Warning:
    If may_prompt is true, output_stream SHOULD be sys.stdout and input_stream
    SHOULD be sys.stdin.

  Return:
    A PresubmitOutput object. Use output.should_continue() to figure out
    if there were errors or warnings and the caller should abort.
  """
  output = PresubmitOutput(input_stream, output_stream)
  if committing:
    output.write("Running presubmit commit checks ...\n")
  else:
    output.write("Running presubmit upload checks ...\n")
  start_time = time.time()
  presubmit_files = ListRelevantPresubmitFiles(change.AbsoluteLocalPaths(True),
                                               change.RepositoryRoot())
  if not presubmit_files and verbose:
    output.write("Warning, no presubmit.py found.\n")
  results = []
  executer = PresubmitExecuter(change, committing, tbr, rietveld, verbose)
  if default_presubmit:
    if verbose:
      output.write("Running default presubmit script.\n")
    fake_path = os.path.join(change.RepositoryRoot(), 'PRESUBMIT.py')
    results += executer.ExecPresubmitScript(default_presubmit, fake_path)
  for filename in presubmit_files:
    filename = os.path.abspath(filename)
    if verbose:
      output.write("Running %s\n" % filename)
    # Accept CRLF presubmit script.
    presubmit_script = gclient_utils.FileRead(filename, 'rU')
    results += executer.ExecPresubmitScript(presubmit_script, filename)

  errors = []
  notifications = []
  warnings = []
  for result in results:
    if result.fatal:
      errors.append(result)
    elif result.should_prompt:
      warnings.append(result)
    else:
      notifications.append(result)

  output.write('\n')
  for name, items in (('Messages', notifications),
                      ('Warnings', warnings),
                      ('ERRORS', errors)):
    if items:
      output.write('** Presubmit %s **\n' % name)
      for item in items:
        item.handle(output)
        output.write('\n')

  total_time = time.time() - start_time
  if total_time > 1.0:
    output.write("Presubmit checks took %.1fs to calculate.\n\n" % total_time)

  if not errors:
    if not warnings:
      output.write('Presubmit checks passed.\n')
    elif may_prompt:
      output.prompt_yes_no('There were presubmit warnings. '
                          'Are you sure you wish to continue? (y/N): ')
    else:
      output.fail()

  global _ASKED_FOR_FEEDBACK
  # Ask for feedback one time out of 5.
  if (len(results) and random.randint(0, 4) == 0 and not _ASKED_FOR_FEEDBACK):
    output.write("Was the presubmit check useful? Please send feedback "
                 "& hate mail to maruel@chromium.org!\n")
    _ASKED_FOR_FEEDBACK = True
  return output


def ScanSubDirs(mask, recursive):
  if not recursive:
    return [x for x in glob.glob(mask) if '.svn' not in x and '.git' not in x]
  else:
    results = []
    for root, dirs, files in os.walk('.'):
      if '.svn' in dirs:
        dirs.remove('.svn')
      if '.git' in dirs:
        dirs.remove('.git')
      for name in files:
        if fnmatch.fnmatch(name, mask):
          results.append(os.path.join(root, name))
    return results


def ParseFiles(args, recursive):
  logging.debug('Searching for %s' % args)
  files = []
  for arg in args:
    files.extend([('M', f) for f in ScanSubDirs(arg, recursive)])
  return files


def load_files(options, args):
  """Tries to determine the SCM."""
  change_scm = scm.determine_scm(options.root)
  files = []
  if change_scm == 'svn':
    change_class = SvnChange
    status_fn = scm.SVN.CaptureStatus
  elif change_scm == 'git':
    change_class = GitChange
    status_fn = scm.GIT.CaptureStatus
  else:
    logging.info('Doesn\'t seem under source control. Got %d files' % len(args))
    if not args:
      return None, None
    change_class = Change
  if args:
    files = ParseFiles(args, options.recursive)
  else:
    # Grab modified files.
    files = status_fn([options.root])
  return change_class, files


def Main(argv):
  parser = optparse.OptionParser(usage="%prog [options] <files...>",
                                 version="%prog " + str(__version__))
  parser.add_option("-c", "--commit", action="store_true", default=False,
                   help="Use commit instead of upload checks")
  parser.add_option("-u", "--upload", action="store_false", dest='commit',
                   help="Use upload instead of commit checks")
  parser.add_option("-r", "--recursive", action="store_true",
                   help="Act recursively")
  parser.add_option("-v", "--verbose", action="count", default=0,
                   help="Use 2 times for more debug info")
  parser.add_option("--name", default='no name')
  parser.add_option("--description", default='')
  parser.add_option("--issue", type='int', default=0)
  parser.add_option("--patchset", type='int', default=0)
  parser.add_option("--root", default=os.getcwd(),
                    help="Search for PRESUBMIT.py up to this directory. "
                    "If inherit-review-settings-ok is present in this "
                    "directory, parent directories up to the root file "
                    "system directories will also be searched.")
  parser.add_option("--default_presubmit")
  parser.add_option("--may_prompt", action='store_true', default=False)
  options, args = parser.parse_args(argv)
  if options.verbose >= 2:
    logging.basicConfig(level=logging.DEBUG)
  elif options.verbose:
    logging.basicConfig(level=logging.INFO)
  else:
    logging.basicConfig(level=logging.ERROR)
  change_class, files = load_files(options, args)
  if not change_class:
    parser.error('For unversioned directory, <files> is not optional.')
  logging.info('Found %d file(s).' % len(files))
  try:
    results = DoPresubmitChecks(
        change_class(options.name,
                    options.description,
                    options.root,
                    files,
                    options.issue,
                    options.patchset),
        options.commit,
        options.verbose,
        sys.stdout,
        sys.stdin,
        options.default_presubmit,
        options.may_prompt,
        False,
        None)
    return not results.should_continue()
  except PresubmitFailure, e:
    print >> sys.stderr, e
    print >> sys.stderr, 'Maybe your depot_tools is out of date?'
    print >> sys.stderr, 'If all fails, contact maruel@'
    return 2


if __name__ == '__main__':
  fix_encoding.fix_encoding()
  sys.exit(Main(None))
