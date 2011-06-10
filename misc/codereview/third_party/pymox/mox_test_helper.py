#!/usr/bin/python2.4
#
# Copyright 2008 Google Inc.
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

"""A very basic test class derived from mox.MoxTestBase, used by mox_test.py.

The class defined in this module is used to test the features of
MoxTestBase and is not intended to be a standalone test.  It needs to
be in a separate module, because otherwise the tests in this class
(which should not all pass) would be executed as part of the
mox_test.py test suite.

See mox_test.MoxTestBaseTest for how this class is actually used.
"""

import os

import mox

class ExampleMoxTestMixin(object):
  """Mix-in class for mox test case class.

  It stubs out the same function as one of the test methods in
  the example test case.  Both tests must pass as meta class wraps
  test methods in all base classes.
  """

  def testStat(self):
    self.mox.StubOutWithMock(os, 'stat')
    os.stat(self.DIR_PATH)
    self.mox.ReplayAll()
    os.stat(self.DIR_PATH)


class ExampleMoxTest(mox.MoxTestBase, ExampleMoxTestMixin):

  DIR_PATH = '/path/to/some/directory'

  def testSuccess(self):
    self.mox.StubOutWithMock(os, 'listdir')
    os.listdir(self.DIR_PATH)
    self.mox.ReplayAll()
    os.listdir(self.DIR_PATH)

  def testExpectedNotCalled(self):
    self.mox.StubOutWithMock(os, 'listdir')
    os.listdir(self.DIR_PATH)
    self.mox.ReplayAll()

  def testUnexpectedCall(self):
    self.mox.StubOutWithMock(os, 'listdir')
    os.listdir(self.DIR_PATH)
    self.mox.ReplayAll()
    os.listdir('/path/to/some/other/directory')
    os.listdir(self.DIR_PATH)

  def testFailure(self):
    self.assertTrue(False)

  def testStatOther(self):
    self.mox.StubOutWithMock(os, 'stat')
    os.stat(self.DIR_PATH)
    self.mox.ReplayAll()
    os.stat(self.DIR_PATH)
