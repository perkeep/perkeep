#!/usr/bin/env python
#
# Camlistore uploader client for Python.
#
# Copyright 2011 Google Inc.
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
"""Schema blob library for Camlistore."""

__author__ = 'Brett Slatkin (bslatkin@gmail.com)'

import datetime
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

import camli.schema
import simplejson


class SchemaTest(unittest.TestCase):
  """End-to-end tests for Schema blobs."""

  def testFile(self):
    schema_blob = camli.schema.decode('asdf-myblobref', """{
      "camliVersion": 1,
      "camliType": "file",
      "size": 0,
      "contentParts": [],
      "unixMtime": "2010-07-10T17:14:51.5678Z",
      "unixCtime": "2010-07-10T17:20:03Z"
    }""")
    self.assertTrue(isinstance(schema_blob, camli.schema.File))
    self.assertTrue(isinstance(schema_blob, camli.schema.FileCommon))
    self.assertTrue(isinstance(schema_blob, camli.schema.SchemaBlob))
    expected = {
      'unexpected_fields': {},
      'unix_mtime': datetime.datetime(2010, 7, 10, 17, 14, 51, 567800),
      'content_parts': [],
      'blobref': 'asdf-myblobref',
      'unix_ctime': datetime.datetime(2010, 7, 10, 17, 20, 3),
      'camli_version': 1,
      'camli_type': u'file',
      'size': 0
    }
    self.assertEquals(expected, schema_blob.__dict__)
    result = schema_blob.encode()
    result_parsed = simplejson.loads(result)
    expected = {
      'camliType': 'file',
      'camliVersion': 1,
      'unixMtime': '2010-07-10T17:14:51.567800Z',
      'unixCtime': '2010-07-10T17:20:03Z',
      'contentParts': [],
      'size': 0,
    }
    self.assertEquals(expected, result_parsed)


if __name__ == '__main__':
  unittest.main()