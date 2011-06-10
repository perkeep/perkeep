Mox is an open source mock object framework for Python, inspired by
the Java library EasyMock.

To install:

  $ python setup.py install

To run Mox's internal tests:

  $ python mox_test.py

Basic usage:

  import unittest
  import mox

  class PersonTest(mox.MoxTestBase):

    def testUsingMox(self):
      # Create a mock Person
      mock_person = self.mox.CreateMock(Person)

      test_person = ...
      test_primary_key = ...
      unknown_person = ...

      # Expect InsertPerson to be called with test_person; return
      # test_primary_key at that point
      mock_person.InsertPerson(test_person).AndReturn(test_primary_key)

      # Raise an exception when this is called
      mock_person.DeletePerson(unknown_person).AndRaise(UnknownPersonError())

      # Switch from record mode to replay mode
      self.mox.ReplayAll()

      # Run the test
      ret_pk = mock_person.InsertPerson(test_person)
      self.assertEquals(test_primary_key, ret_pk)
      self.assertRaises(UnknownPersonError, mock_person, unknown_person)

For more documentation, see:

  http://code.google.com/p/pymox/wiki/MoxDocumentation

For more information, see:

  http://code.google.com/p/pymox/

Our user and developer discussion group is:

  http://groups.google.com/group/mox-discuss

Mox is Copyright 2008 Google Inc, and licensed under the Apache
License, Version 2.0; see the file COPYING for details.  If you would
like to help us improve Mox, join the group.
