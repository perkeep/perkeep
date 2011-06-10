#!/usr/bin/env python
# Copyright (c) 2011 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.

"""Watchlists

Watchlists is a mechanism that allow a developer (a "watcher") to watch over
portions of code that he is interested in. A "watcher" will be cc-ed to
changes that modify that portion of code, thereby giving him an opportunity
to make comments on codereview.chromium.org even before the change is
committed.
Refer: http://dev.chromium.org/developers/contributing-code/watchlists

When invoked directly from the base of a repository, this script lists out
the watchers for files given on the command line. This is useful to verify
changes to WATCHLISTS files.
"""

import logging
import os
import re
import sys


class Watchlists(object):
  """Manage Watchlists.

  This class provides mechanism to load watchlists for a repo and identify
  watchers.
  Usage:
    wl = Watchlists("/path/to/repo/root")
    watchers = wl.GetWatchersForPaths(["/path/to/file1",
                                       "/path/to/file2",])
  """

  _RULES = "WATCHLISTS"
  _RULES_FILENAME = _RULES
  _repo_root = None
  _defns = {}       # Definitions
  _watchlists = {}  # name to email mapping

  def __init__(self, repo_root):
    self._repo_root = repo_root
    self._LoadWatchlistRules()

  def _GetRulesFilePath(self):
    """Returns path to WATCHLISTS file."""
    return os.path.join(self._repo_root, self._RULES_FILENAME)

  def _HasWatchlistsFile(self):
    """Determine if watchlists are available for this repo."""
    return os.path.exists(self._GetRulesFilePath())

  def _ContentsOfWatchlistsFile(self):
    """Read the WATCHLISTS file and return its contents."""
    try:
      watchlists_file = open(self._GetRulesFilePath())
      contents = watchlists_file.read()
      watchlists_file.close()
      return contents
    except IOError, e:
      logging.error("Cannot read %s: %s" % (self._GetRulesFilePath(), e))
      return ''

  def _LoadWatchlistRules(self):
    """Load watchlists from WATCHLISTS file. Does nothing if not present."""
    if not self._HasWatchlistsFile():
      return

    contents = self._ContentsOfWatchlistsFile()
    watchlists_data = None
    try:
      watchlists_data = eval(contents, {'__builtins__': None}, None)
    except SyntaxError, e:
      logging.error("Cannot parse %s. %s" % (self._GetRulesFilePath(), e))
      return

    defns = watchlists_data.get("WATCHLIST_DEFINITIONS")
    if not defns:
      logging.error("WATCHLIST_DEFINITIONS not defined in %s" %
                    self._GetRulesFilePath())
      return
    watchlists = watchlists_data.get("WATCHLISTS")
    if not watchlists:
      logging.error("WATCHLISTS not defined in %s" % self._GetRulesFilePath())
      return
    self._defns = defns
    self._watchlists = watchlists

    # Verify that all watchlist names are defined
    for name in watchlists:
      if name not in defns:
        logging.error("%s not defined in %s" % (name, self._GetRulesFilePath()))

  def GetWatchersForPaths(self, paths):
    """Fetch the list of watchers for |paths|

    Args:
      paths: [path1, path2, ...]

    Returns:
      [u1@chromium.org, u2@gmail.com, ...]
    """
    watchers = set()  # A set, to avoid duplicates
    for path in paths:
      path = path.replace(os.sep, '/')
      for name, rule in self._defns.iteritems():
        if name not in self._watchlists:
          continue
        rex_str = rule.get('filepath')
        if not rex_str:
          continue
        if re.search(rex_str, path):
          map(watchers.add, self._watchlists[name])
    return list(watchers)


def main(argv):
  # Confirm that watchlists can be parsed and spew out the watchers
  if len(argv) < 2:
    print "Usage (from the base of repo):"
    print "  %s [file-1] [file-2] ...." % argv[0]
    return 1
  wl = Watchlists(os.getcwd())
  watchers = wl.GetWatchersForPaths(argv[1:])
  print watchers


if __name__ == '__main__':
  main(sys.argv)
