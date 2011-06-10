#
# Copyright (C) 2009 The Android Open Source Project
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import sys
from time import time

class Progress(object):
  def __init__(self, title, total=0):
    self._title = title
    self._total = total
    self._done = 0
    self._lastp = -1
    self._start = time()
    self._show = False
    self._width = 0

  def update(self, inc=1, extra=''):
    self._done += inc

    if not self._show:
      if 0.5 <= time() - self._start:
        self._show = True
      else:
        return

    text = None

    if self._total <= 0:
      text = '%s: %3d' % (self._title, self._done)
    else:
      p = (100 * self._done) / self._total

      if self._lastp != p:
        self._lastp = p
        text = '%s: %3d%% (%2d/%2d)' % (self._title, p,
                                        self._done, self._total)

    if text:
      text += ' ' + extra
      spaces = max(self._width - len(text), 0)
      sys.stdout.write('%s%*s\r' % (text, spaces, ''))
      sys.stdout.flush()
      self._width = len(text)

  def end(self):
    if not self._show:
      return

    if self._total <= 0:
      sys.stdout.write('%s: %d, done.\n' % (
        self._title,
        self._done))
      sys.stdout.flush()
    else:
      p = (100 * self._done) / self._total
      sys.stdout.write('%s: %3d%% (%d/%d), done.\n' % (
        self._title,
        p,
        self._done,
        self._total))
      sys.stdout.flush()
