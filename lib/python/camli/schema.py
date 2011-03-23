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
import re
import simplejson

__all__ = [
    'Error', 'DecodeError', 'SchemaBlob', 'FileCommon', 'File',
    'Directory', 'Symlink', 'decode']


class Error(Exception):
  """Base class for exceptions in this module."""

class DecodeError(Error):
  """Could not decode the supplied schema blob."""


# Maps 'camliType' to SchemaBlob sub-classes.
_TYPE_TO_CLASS = {}


def _camel_to_python(name):
  """Converts camelcase to Python case."""
  return re.sub(r'([a-z]+)([A-Z])', r'\1_\2', name).lower()


class _SchemaMeta(type):
  """Meta-class for schema blobs."""

  def __init__(cls, name, bases, dict):
    required_fields = set()
    optional_fields = set()
    json_to_python = {}
    python_to_json = {}
    serializers = {}

    def map_name(field):
      if field.islower():
        return field
      python_name = _camel_to_python(field)
      json_to_python[field] = python_name
      python_to_json[python_name] =  field
      return python_name

    for klz in bases + (cls,):
      if hasattr(klz, '_json_to_python'):
        json_to_python.update(klz._json_to_python)
      if hasattr(klz, '_python_to_json'):
        python_to_json.update(klz._python_to_json)

      if hasattr(klz, 'required_fields'):
        for field in klz.required_fields:
          field = map_name(field)
          assert field not in required_fields, (klz, field)
          assert field not in optional_fields, (klz, field)
          required_fields.add(field)

      if hasattr(klz, 'optional_fields'):
        for field in klz.optional_fields:
          field = map_name(field)
          assert field not in required_fields, (klz, field)
          assert field not in optional_fields, (klz, field)
          optional_fields.add(field)

      if hasattr(klz, '_serializers'):
        for field, value in klz._serializers.iteritems():
          field = map_name(field)
          assert (field in required_fields or
                  field in optional_fields), (klz, field)
          if not isinstance(value, _FieldSerializer):
            serializers[field] = value(field)
          else:
            serializers[field] = value

    setattr(cls, 'required_fields', frozenset(required_fields))
    setattr(cls, 'optional_fields', frozenset(optional_fields))
    setattr(cls, '_serializers', serializers)
    setattr(cls, '_json_to_python', json_to_python)
    setattr(cls, '_python_to_json', python_to_json)
    if hasattr(cls, 'type'):
      _TYPE_TO_CLASS[cls.type] = cls


class SchemaBlob(object):
  """Base-class for schema blobs.

  Each sub-class should have these fields:
    type: Required value of 'camliType'.
    required_fields: Set of required field names.
    optional_fields: Set of optional field names.
    _serializers: Dictionary mapping field names to the _FieldSerializer
      sub-class to use for serializing/deserializing the field's value.
  """

  __metaclass__ = _SchemaMeta

  required_fields = frozenset([
    'camliVersion',
    'camliType',
  ])
  optional_fields = frozenset([
    'camliSigner',
    'camliSig',
  ])
  _serializers = {}

  def __init__(self, blobref):
    """Initializer.

    Args:
      blobref: The blobref of the schema blob.
    """
    self.blobref = blobref
    self.unexpected_fields = {}

  @property
  def all_fields(self):
    """Returns the set of all potential fields for this blob."""
    all_fields = set()
    all_fields.update(self.required_fields)
    all_fields.update(self.optional_fields)
    all_fields.update(self.unexpected_fields)
    return all_fields

  def decode(self, blob_bytes, parsed=None):
    """Decodes a schema blob's bytes and unmarshals its fields.

    Args:
      blob_bytes: String with the bytes of the blob.
      parsed: If not None, an already parsed version of the blob bytes. When
        set, the blob_bytes argument is ignored.

    Raises:
      DecodeError if the blob_bytes are bad or the parsed blob is missing
      required fields.
    """
    for field in self.all_fields:
      if hasattr(self, field):
        delattr(self, field)

    if parsed is None:
      try:
        parsed = simplejson.loads(blob_bytes)
      except simplejson.JSONDecodeError, e:
        raise DecodeError('Could not parse JSON. %s: %s' % (e.__class__, e))

    for json_name, value in parsed.iteritems():
      name = self._json_to_python.get(json_name, json_name)
      if not (name in self.required_fields or name in self.optional_fields):
        self.unexpected_fields[name] = value
        continue
      serializer = self._serializers.get(name)
      if serializer:
        value = serializer.from_json(value)
      setattr(self, name, value)

    for name in self.required_fields:
      if not hasattr(self, name):
        raise DecodeError('Missing required field: %s' % name)

  def encode(self):
    """Encodes a schema blob's bytes and marshals its fields.

    Returns:
      A UTF-8-encoding plain string containing the encoded blob bytes.
    """
    out = {}
    for python_name in self.all_fields:
      if not hasattr(self, python_name):
        continue
      value = getattr(self, python_name)
      serializer = self._serializers.get(python_name)
      if serializer:
        value = serializer.to_json(value)
      json_name = self._python_to_json.get(python_name, python_name)
      out[json_name] = value
    return simplejson.dumps(out)

################################################################################
# Serializers for converting JSON fields to/from Python values

class _FieldSerializer(object):
  """Serializes a named field's value to and from JSON."""

  def __init__(self, name):
    """Initializer.

    Args:
      name: The name of the field.
    """
    self.name = name

  def from_json(self, value):
    """Converts the JSON format of the field to the Python type.

    Args:
      value: The JSON value.

    Returns:
      The Python value.
    """
    raise NotImplemented('Must implement from_json')

  def to_json(self, value):
    """Converts the Python field value to the JSON format of the field.

    Args:
      value: The Python value.

    Returns:
      The JSON formatted-value.
    """
    raise NotImplemented('Must implement to_json')


class _DateTimeSerializer(_FieldSerializer):
  """Formats ISO 8601 strings to/from datetime.datetime instances."""

  def from_json(self, value):
    if '.' in value:
      iso, micros = value.split('.')
      micros = int((micros[:-1] + ('0' * 6))[:6])
    else:
      iso, micros = value[:-1], 0

    when = datetime.datetime.strptime(iso, '%Y-%m-%dT%H:%M:%S')
    return when + datetime.timedelta(microseconds=micros)

  def to_json(self, value):
    return value.isoformat() + 'Z'

################################################################################
# Concrete Schema Blobs

class FileCommon(SchemaBlob):
  """Common base-class for all unix-y files."""

  required_fields = frozenset([])
  optional_fields = frozenset([
    'fileName',
    'fileNameBytes',
    'unixPermission',
    'unixOwnerId',
    'unixGroupId',
    'unixGroup',
    'unixXattrs',
    'unixMtime',
    'unixCtime',
    'unixAtime',
  ])
  _serializers = {
    'unixMtime': _DateTimeSerializer,
    'unixCtime': _DateTimeSerializer,
    'unixAtime': _DateTimeSerializer,
  }


class File(FileCommon):
  """A file."""

  type = 'file'
  required_fields = frozenset([
    'size',
    'contentParts',
  ])
  optional_fields = frozenset([
    'inodeRef',
  ])
  _serializers = {}


class Directory(FileCommon):
  """A directory."""

  type = 'directory'
  required_fields = frozenset([
    'entries',
  ])
  optional_fields = frozenset([])
  _serializers = {}


class Symlink(FileCommon):
  """A symlink."""

  type = 'symlink'
  required_fields = frozenset([])
  optional_fields = frozenset([
    'symlinkTarget',
    'symlinkTargetBytes',
  ])
  _serializers = {}


################################################################################
# Helper methods

def decode(blobref, blob_bytes):
  """Decode any schema blob, validating all required fields for its time."""
  try:
    parsed = simplejson.loads(blob_bytes)
  except simplejson.JSONDecodeError, e:
    raise DecodeError('Could not parse JSON. %s: %s' % (e.__class__, e))

  if 'camliType' not in parsed:
    raise DecodeError('Could not find "camliType" field.')

  camli_type = parsed['camliType']
  blob_class = _TYPE_TO_CLASS.get(camli_type)
  if blob_class is None:
    raise DecodeError(
        'Could not find SchemaBlob sub-class for camliType=%r' % camli_type)

  schema_blob = blob_class(blobref)
  schema_blob.decode(None, parsed=parsed)
  return schema_blob
