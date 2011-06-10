#!/usr/bin/python2.4
#
# Unit tests for Mox.
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

import cStringIO
import unittest
import re

import mox

import mox_test_helper


class ExpectedMethodCallsErrorTest(unittest.TestCase):
  """Test creation and string conversion of ExpectedMethodCallsError."""

  def testAtLeastOneMethod(self):
    self.assertRaises(ValueError, mox.ExpectedMethodCallsError, [])

  def testOneError(self):
    method = mox.MockMethod("testMethod", [], False)
    method(1, 2).AndReturn('output')
    e = mox.ExpectedMethodCallsError([method])
    self.assertEqual(
        "Verify: Expected methods never called:\n"
        "  0.  testMethod(1, 2) -> 'output'",
        str(e))

  def testManyErrors(self):
    method1 = mox.MockMethod("testMethod", [], False)
    method1(1, 2).AndReturn('output')
    method2 = mox.MockMethod("testMethod", [], False)
    method2(a=1, b=2, c="only named")
    method3 = mox.MockMethod("testMethod2", [], False)
    method3().AndReturn(44)
    method4 = mox.MockMethod("testMethod", [], False)
    method4(1, 2).AndReturn('output')
    e = mox.ExpectedMethodCallsError([method1, method2, method3, method4])
    self.assertEqual(
        "Verify: Expected methods never called:\n"
        "  0.  testMethod(1, 2) -> 'output'\n"
        "  1.  testMethod(a=1, b=2, c='only named') -> None\n"
        "  2.  testMethod2() -> 44\n"
        "  3.  testMethod(1, 2) -> 'output'",
        str(e))


class OrTest(unittest.TestCase):
  """Test Or correctly chains Comparators."""

  def testValidOr(self):
    """Or should be True if either Comparator returns True."""
    self.assert_(mox.Or(mox.IsA(dict), mox.IsA(str)) == {})
    self.assert_(mox.Or(mox.IsA(dict), mox.IsA(str)) == 'test')
    self.assert_(mox.Or(mox.IsA(str), mox.IsA(str)) == 'test')

  def testInvalidOr(self):
    """Or should be False if both Comparators return False."""
    self.failIf(mox.Or(mox.IsA(dict), mox.IsA(str)) == 0)


class AndTest(unittest.TestCase):
  """Test And correctly chains Comparators."""

  def testValidAnd(self):
    """And should be True if both Comparators return True."""
    self.assert_(mox.And(mox.IsA(str), mox.IsA(str)) == '1')

  def testClauseOneFails(self):
    """And should be False if the first Comparator returns False."""

    self.failIf(mox.And(mox.IsA(dict), mox.IsA(str)) == '1')

  def testAdvancedUsage(self):
    """And should work with other Comparators.

    Note: this test is reliant on In and ContainsKeyValue.
    """
    test_dict = {"mock" : "obj", "testing" : "isCOOL"}
    self.assert_(mox.And(mox.In("testing"),
                           mox.ContainsKeyValue("mock", "obj")) == test_dict)

  def testAdvancedUsageFails(self):
    """Note: this test is reliant on In and ContainsKeyValue."""
    test_dict = {"mock" : "obj", "testing" : "isCOOL"}
    self.failIf(mox.And(mox.In("NOTFOUND"),
                          mox.ContainsKeyValue("mock", "obj")) == test_dict)


class SameElementsAsTest(unittest.TestCase):
  """Test SameElementsAs correctly identifies sequences with same elements."""

  def testSortedLists(self):
    """Should return True if two lists are exactly equal."""
    self.assert_(mox.SameElementsAs([1, 2.0, 'c']) == [1, 2.0, 'c'])

  def testUnsortedLists(self):
    """Should return True if two lists are unequal but have same elements."""
    self.assert_(mox.SameElementsAs([1, 2.0, 'c']) == [2.0, 'c', 1])

  def testUnhashableLists(self):
    """Should return True if two lists have the same unhashable elements."""
    self.assert_(mox.SameElementsAs([{'a': 1}, {2: 'b'}]) ==
                 [{2: 'b'}, {'a': 1}])

  def testEmptyLists(self):
    """Should return True for two empty lists."""
    self.assert_(mox.SameElementsAs([]) == [])

  def testUnequalLists(self):
    """Should return False if the lists are not equal."""
    self.failIf(mox.SameElementsAs([1, 2.0, 'c']) == [2.0, 'c'])

  def testUnequalUnhashableLists(self):
    """Should return False if two lists with unhashable elements are unequal."""
    self.failIf(mox.SameElementsAs([{'a': 1}, {2: 'b'}]) == [{2: 'b'}])


class ContainsKeyValueTest(unittest.TestCase):
  """Test ContainsKeyValue correctly identifies key/value pairs in a dict.
  """

  def testValidPair(self):
    """Should return True if the key value is in the dict."""
    self.assert_(mox.ContainsKeyValue("key", 1) == {"key": 1})

  def testInvalidValue(self):
    """Should return False if the value is not correct."""
    self.failIf(mox.ContainsKeyValue("key", 1) == {"key": 2})

  def testInvalidKey(self):
    """Should return False if they key is not in the dict."""
    self.failIf(mox.ContainsKeyValue("qux", 1) == {"key": 2})


class InTest(unittest.TestCase):
  """Test In correctly identifies a key in a list/dict"""

  def testItemInList(self):
    """Should return True if the item is in the list."""
    self.assert_(mox.In(1) == [1, 2, 3])

  def testKeyInDict(self):
    """Should return True if the item is a key in a dict."""
    self.assert_(mox.In("test") == {"test" : "module"})


class NotTest(unittest.TestCase):
  """Test Not correctly identifies False predicates."""

  def testItemInList(self):
    """Should return True if the item is NOT in the list."""
    self.assert_(mox.Not(mox.In(42)) == [1, 2, 3])

  def testKeyInDict(self):
    """Should return True if the item is NOT a key in a dict."""
    self.assert_(mox.Not(mox.In("foo")) == {"key" : 42})

  def testInvalidKeyWithNot(self):
    """Should return False if they key is NOT in the dict."""
    self.assert_(mox.Not(mox.ContainsKeyValue("qux", 1)) == {"key": 2})


class StrContainsTest(unittest.TestCase):
  """Test StrContains correctly checks for substring occurrence of a parameter.
  """

  def testValidSubstringAtStart(self):
    """Should return True if the substring is at the start of the string."""
    self.assert_(mox.StrContains("hello") == "hello world")

  def testValidSubstringInMiddle(self):
    """Should return True if the substring is in the middle of the string."""
    self.assert_(mox.StrContains("lo wo") == "hello world")

  def testValidSubstringAtEnd(self):
    """Should return True if the substring is at the end of the string."""
    self.assert_(mox.StrContains("ld") == "hello world")

  def testInvaildSubstring(self):
    """Should return False if the substring is not in the string."""
    self.failIf(mox.StrContains("AAA") == "hello world")

  def testMultipleMatches(self):
    """Should return True if there are multiple occurances of substring."""
    self.assert_(mox.StrContains("abc") == "ababcabcabcababc")


class RegexTest(unittest.TestCase):
  """Test Regex correctly matches regular expressions."""

  def testIdentifyBadSyntaxDuringInit(self):
    """The user should know immediately if a regex has bad syntax."""
    self.assertRaises(re.error, mox.Regex, '(a|b')

  def testPatternInMiddle(self):
    """Should return True if the pattern matches at the middle of the string.

    This ensures that re.search is used (instead of re.find).
    """
    self.assert_(mox.Regex(r"a\s+b") == "x y z a b c")

  def testNonMatchPattern(self):
    """Should return False if the pattern does not match the string."""
    self.failIf(mox.Regex(r"a\s+b") == "x y z")

  def testFlagsPassedCorrectly(self):
    """Should return True as we pass IGNORECASE flag."""
    self.assert_(mox.Regex(r"A", re.IGNORECASE) == "a")

  def testReprWithoutFlags(self):
    """repr should return the regular expression pattern."""
    self.assert_(repr(mox.Regex(r"a\s+b")) == "<regular expression 'a\s+b'>")

  def testReprWithFlags(self):
    """repr should return the regular expression pattern and flags."""
    self.assert_(repr(mox.Regex(r"a\s+b", flags=4)) ==
                 "<regular expression 'a\s+b', flags=4>")


class IsATest(unittest.TestCase):
  """Verify IsA correctly checks equality based upon class type, not value."""

  def testEqualityValid(self):
    """Verify that == correctly identifies objects of the same type."""
    self.assert_(mox.IsA(str) == 'test')

  def testEqualityInvalid(self):
    """Verify that == correctly identifies objects of different types."""
    self.failIf(mox.IsA(str) == 10)

  def testInequalityValid(self):
    """Verify that != identifies objects of different type."""
    self.assert_(mox.IsA(str) != 10)

  def testInequalityInvalid(self):
    """Verify that != correctly identifies objects of the same type."""
    self.failIf(mox.IsA(str) != "test")

  def testEqualityInListValid(self):
    """Verify list contents are properly compared."""
    isa_list = [mox.IsA(str), mox.IsA(str)]
    str_list = ["abc", "def"]
    self.assert_(isa_list == str_list)

  def testEquailtyInListInvalid(self):
    """Verify list contents are properly compared."""
    isa_list = [mox.IsA(str),mox.IsA(str)]
    mixed_list = ["abc", 123]
    self.failIf(isa_list == mixed_list)

  def testSpecialTypes(self):
    """Verify that IsA can handle objects like cStringIO.StringIO."""
    isA = mox.IsA(cStringIO.StringIO())
    stringIO = cStringIO.StringIO()
    self.assert_(isA == stringIO)


class IsAlmostTest(unittest.TestCase):
  """Verify IsAlmost correctly checks equality of floating point numbers."""

  def testEqualityValid(self):
    """Verify that == correctly identifies nearly equivalent floats."""
    self.assertEquals(mox.IsAlmost(1.8999999999), 1.9)

  def testEqualityInvalid(self):
    """Verify that == correctly identifies non-equivalent floats."""
    self.assertNotEquals(mox.IsAlmost(1.899), 1.9)

  def testEqualityWithPlaces(self):
    """Verify that specifying places has the desired effect."""
    self.assertNotEquals(mox.IsAlmost(1.899), 1.9)
    self.assertEquals(mox.IsAlmost(1.899, places=2), 1.9)

  def testNonNumericTypes(self):
    """Verify that IsAlmost handles non-numeric types properly."""

    self.assertNotEquals(mox.IsAlmost(1.8999999999), '1.9')
    self.assertNotEquals(mox.IsAlmost('1.8999999999'), 1.9)
    self.assertNotEquals(mox.IsAlmost('1.8999999999'), '1.9')


class MockMethodTest(unittest.TestCase):
  """Test class to verify that the MockMethod class is working correctly."""

  def setUp(self):
    self.expected_method = mox.MockMethod("testMethod", [], False)(['original'])
    self.mock_method = mox.MockMethod("testMethod", [self.expected_method],
                                        True)

  def testAndReturnNoneByDefault(self):
    """Should return None by default."""
    return_value = self.mock_method(['original'])
    self.assert_(return_value == None)

  def testAndReturnValue(self):
    """Should return a specificed return value."""
    expected_return_value = "test"
    self.expected_method.AndReturn(expected_return_value)
    return_value = self.mock_method(['original'])
    self.assert_(return_value == expected_return_value)

  def testAndRaiseException(self):
    """Should raise a specified exception."""
    expected_exception = Exception('test exception')
    self.expected_method.AndRaise(expected_exception)
    self.assertRaises(Exception, self.mock_method)

  def testWithSideEffects(self):
    """Should call state modifier."""
    local_list = ['original']
    def modifier(mutable_list):
      self.assertTrue(local_list is mutable_list)
      mutable_list[0] = 'mutation'
    self.expected_method.WithSideEffects(modifier).AndReturn(1)
    self.mock_method(local_list)
    self.assertEquals('mutation', local_list[0])

  def testEqualityNoParamsEqual(self):
    """Methods with the same name and without params should be equal."""
    expected_method = mox.MockMethod("testMethod", [], False)
    self.assertEqual(self.mock_method, expected_method)

  def testEqualityNoParamsNotEqual(self):
    """Methods with different names and without params should not be equal."""
    expected_method = mox.MockMethod("otherMethod", [], False)
    self.failIfEqual(self.mock_method, expected_method)

  def testEqualityParamsEqual(self):
    """Methods with the same name and parameters should be equal."""
    params = [1, 2, 3]
    expected_method = mox.MockMethod("testMethod", [], False)
    expected_method._params = params

    self.mock_method._params = params
    self.assertEqual(self.mock_method, expected_method)

  def testEqualityParamsNotEqual(self):
    """Methods with the same name and different params should not be equal."""
    expected_method = mox.MockMethod("testMethod", [], False)
    expected_method._params = [1, 2, 3]

    self.mock_method._params = ['a', 'b', 'c']
    self.failIfEqual(self.mock_method, expected_method)

  def testEqualityNamedParamsEqual(self):
    """Methods with the same name and same named params should be equal."""
    named_params = {"input1": "test", "input2": "params"}
    expected_method = mox.MockMethod("testMethod", [], False)
    expected_method._named_params = named_params

    self.mock_method._named_params = named_params
    self.assertEqual(self.mock_method, expected_method)

  def testEqualityNamedParamsNotEqual(self):
    """Methods with the same name and diffnamed params should not be equal."""
    expected_method = mox.MockMethod("testMethod", [], False)
    expected_method._named_params = {"input1": "test", "input2": "params"}

    self.mock_method._named_params = {"input1": "test2", "input2": "params2"}
    self.failIfEqual(self.mock_method, expected_method)

  def testEqualityWrongType(self):
    """Method should not be equal to an object of a different type."""
    self.failIfEqual(self.mock_method, "string?")

  def testObjectEquality(self):
    """Equality of objects should work without a Comparator"""
    instA = TestClass();
    instB = TestClass();

    params = [instA, ]
    expected_method = mox.MockMethod("testMethod", [], False)
    expected_method._params = params

    self.mock_method._params = [instB, ]
    self.assertEqual(self.mock_method, expected_method)

  def testStrConversion(self):
    method = mox.MockMethod("f", [], False)
    method(1, 2, "st", n1=8, n2="st2")
    self.assertEqual(str(method), ("f(1, 2, 'st', n1=8, n2='st2') -> None"))

    method = mox.MockMethod("testMethod", [], False)
    method(1, 2, "only positional")
    self.assertEqual(str(method), "testMethod(1, 2, 'only positional') -> None")

    method = mox.MockMethod("testMethod", [], False)
    method(a=1, b=2, c="only named")
    self.assertEqual(str(method),
                     "testMethod(a=1, b=2, c='only named') -> None")

    method = mox.MockMethod("testMethod", [], False)
    method()
    self.assertEqual(str(method), "testMethod() -> None")

    method = mox.MockMethod("testMethod", [], False)
    method(x="only 1 parameter")
    self.assertEqual(str(method), "testMethod(x='only 1 parameter') -> None")

    method = mox.MockMethod("testMethod", [], False)
    method().AndReturn('return_value')
    self.assertEqual(str(method), "testMethod() -> 'return_value'")

    method = mox.MockMethod("testMethod", [], False)
    method().AndReturn(('a', {1: 2}))
    self.assertEqual(str(method), "testMethod() -> ('a', {1: 2})")


class MockAnythingTest(unittest.TestCase):
  """Verify that the MockAnything class works as expected."""

  def setUp(self):
    self.mock_object = mox.MockAnything()

  def testRepr(self):
    """Calling repr on a MockAnything instance must work."""
    self.assertEqual('<MockAnything instance>', repr(self.mock_object))

  def testSetupMode(self):
    """Verify the mock will accept any call."""
    self.mock_object.NonsenseCall()
    self.assert_(len(self.mock_object._expected_calls_queue) == 1)

  def testReplayWithExpectedCall(self):
    """Verify the mock replays method calls as expected."""
    self.mock_object.ValidCall()          # setup method call
    self.mock_object._Replay()            # start replay mode
    self.mock_object.ValidCall()          # make method call

  def testReplayWithUnexpectedCall(self):
    """Unexpected method calls should raise UnexpectedMethodCallError."""
    self.mock_object.ValidCall()          # setup method call
    self.mock_object._Replay()             # start replay mode
    self.assertRaises(mox.UnexpectedMethodCallError,
                      self.mock_object.OtherValidCall)

  def testVerifyWithCompleteReplay(self):
    """Verify should not raise an exception for a valid replay."""
    self.mock_object.ValidCall()          # setup method call
    self.mock_object._Replay()             # start replay mode
    self.mock_object.ValidCall()          # make method call
    self.mock_object._Verify()

  def testVerifyWithIncompleteReplay(self):
    """Verify should raise an exception if the replay was not complete."""
    self.mock_object.ValidCall()          # setup method call
    self.mock_object._Replay()             # start replay mode
    # ValidCall() is never made
    self.assertRaises(mox.ExpectedMethodCallsError, self.mock_object._Verify)

  def testSpecialClassMethod(self):
    """Verify should not raise an exception when special methods are used."""
    self.mock_object[1].AndReturn(True)
    self.mock_object._Replay()
    returned_val = self.mock_object[1]
    self.assert_(returned_val)
    self.mock_object._Verify()

  def testNonzero(self):
    """You should be able to use the mock object in an if."""
    self.mock_object._Replay()
    if self.mock_object:
      pass

  def testNotNone(self):
    """Mock should be comparable to None."""
    self.mock_object._Replay()
    if self.mock_object is not None:
      pass

    if self.mock_object is None:
      pass

  def testEquals(self):
    """A mock should be able to compare itself to another object."""
    self.mock_object._Replay()
    self.assertEquals(self.mock_object, self.mock_object)

  def testEqualsMockFailure(self):
    """Verify equals identifies unequal objects."""
    self.mock_object.SillyCall()
    self.mock_object._Replay()
    self.assertNotEquals(self.mock_object, mox.MockAnything())

  def testEqualsInstanceFailure(self):
    """Verify equals identifies that objects are different instances."""
    self.mock_object._Replay()
    self.assertNotEquals(self.mock_object, TestClass())

  def testNotEquals(self):
    """Verify not equals works."""
    self.mock_object._Replay()
    self.assertFalse(self.mock_object != self.mock_object)

  def testNestedMockCallsRecordedSerially(self):
    """Test that nested calls work when recorded serially."""
    self.mock_object.CallInner().AndReturn(1)
    self.mock_object.CallOuter(1)
    self.mock_object._Replay()

    self.mock_object.CallOuter(self.mock_object.CallInner())

    self.mock_object._Verify()

  def testNestedMockCallsRecordedNested(self):
    """Test that nested cals work when recorded in a nested fashion."""
    self.mock_object.CallOuter(self.mock_object.CallInner().AndReturn(1))
    self.mock_object._Replay()

    self.mock_object.CallOuter(self.mock_object.CallInner())

    self.mock_object._Verify()

  def testIsCallable(self):
    """Test that MockAnything can even mock a simple callable.

    This is handy for "stubbing out" a method in a module with a mock, and
    verifying that it was called.
    """
    self.mock_object().AndReturn('mox0rd')
    self.mock_object._Replay()

    self.assertEquals('mox0rd', self.mock_object())

    self.mock_object._Verify()

  def testIsReprable(self):
    """Test that MockAnythings can be repr'd without causing a failure."""
    self.failUnless('MockAnything' in repr(self.mock_object))


class MethodCheckerTest(unittest.TestCase):
  """Tests MockMethod's use of MethodChecker method."""

  def testNoParameters(self):
    method = mox.MockMethod('NoParameters', [], False,
                            CheckCallTestClass.NoParameters)
    method()
    self.assertRaises(AttributeError, method, 1)
    self.assertRaises(AttributeError, method, 1, 2)
    self.assertRaises(AttributeError, method, a=1)
    self.assertRaises(AttributeError, method, 1, b=2)

  def testOneParameter(self):
    method = mox.MockMethod('OneParameter', [], False,
                            CheckCallTestClass.OneParameter)
    self.assertRaises(AttributeError, method)
    method(1)
    method(a=1)
    self.assertRaises(AttributeError, method, b=1)
    self.assertRaises(AttributeError, method, 1, 2)
    self.assertRaises(AttributeError, method, 1, a=2)
    self.assertRaises(AttributeError, method, 1, b=2)

  def testTwoParameters(self):
    method = mox.MockMethod('TwoParameters', [], False,
                            CheckCallTestClass.TwoParameters)
    self.assertRaises(AttributeError, method)
    self.assertRaises(AttributeError, method, 1)
    self.assertRaises(AttributeError, method, a=1)
    self.assertRaises(AttributeError, method, b=1)
    method(1, 2)
    method(1, b=2)
    method(a=1, b=2)
    method(b=2, a=1)
    self.assertRaises(AttributeError, method, b=2, c=3)
    self.assertRaises(AttributeError, method, a=1, b=2, c=3)
    self.assertRaises(AttributeError, method, 1, 2, 3)
    self.assertRaises(AttributeError, method, 1, 2, 3, 4)
    self.assertRaises(AttributeError, method, 3, a=1, b=2)

  def testOneDefaultValue(self):
    method = mox.MockMethod('OneDefaultValue', [], False,
                            CheckCallTestClass.OneDefaultValue)
    method()
    method(1)
    method(a=1)
    self.assertRaises(AttributeError, method, b=1)
    self.assertRaises(AttributeError, method, 1, 2)
    self.assertRaises(AttributeError, method, 1, a=2)
    self.assertRaises(AttributeError, method, 1, b=2)

  def testTwoDefaultValues(self):
    method = mox.MockMethod('TwoDefaultValues', [], False,
                            CheckCallTestClass.TwoDefaultValues)
    self.assertRaises(AttributeError, method)
    self.assertRaises(AttributeError, method, c=3)
    self.assertRaises(AttributeError, method, 1)
    self.assertRaises(AttributeError, method, 1, d=4)
    self.assertRaises(AttributeError, method, 1, d=4, c=3)
    method(1, 2)
    method(a=1, b=2)
    method(1, 2, 3)
    method(1, 2, 3, 4)
    method(1, 2, c=3)
    method(1, 2, c=3, d=4)
    method(1, 2, d=4, c=3)
    method(d=4, c=3, a=1, b=2)
    self.assertRaises(AttributeError, method, 1, 2, 3, 4, 5)
    self.assertRaises(AttributeError, method, 1, 2, e=9)
    self.assertRaises(AttributeError, method, a=1, b=2, e=9)

  def testArgs(self):
    method = mox.MockMethod('Args', [], False, CheckCallTestClass.Args)
    self.assertRaises(AttributeError, method)
    self.assertRaises(AttributeError, method, 1)
    method(1, 2)
    method(a=1, b=2)
    method(1, 2, 3)
    method(1, 2, 3, 4)
    self.assertRaises(AttributeError, method, 1, 2, a=3)
    self.assertRaises(AttributeError, method, 1, 2, c=3)

  def testKwargs(self):
    method = mox.MockMethod('Kwargs', [], False, CheckCallTestClass.Kwargs)
    self.assertRaises(AttributeError, method)
    method(1)
    method(1, 2)
    method(a=1, b=2)
    method(b=2, a=1)
    self.assertRaises(AttributeError, method, 1, 2, 3)
    self.assertRaises(AttributeError, method, 1, 2, a=3)
    method(1, 2, c=3)
    method(a=1, b=2, c=3)
    method(c=3, a=1, b=2)
    method(a=1, b=2, c=3, d=4)
    self.assertRaises(AttributeError, method, 1, 2, 3, 4)

  def testArgsAndKwargs(self):
    method = mox.MockMethod('ArgsAndKwargs', [], False,
                            CheckCallTestClass.ArgsAndKwargs)
    self.assertRaises(AttributeError, method)
    method(1)
    method(1, 2)
    method(1, 2, 3)
    method(a=1)
    method(1, b=2)
    self.assertRaises(AttributeError, method, 1, a=2)
    method(b=2, a=1)
    method(c=3, b=2, a=1)
    method(1, 2, c=3)


class CheckCallTestClass(object):
  def NoParameters(self):
    pass

  def OneParameter(self, a):
    pass

  def TwoParameters(self, a, b):
    pass

  def OneDefaultValue(self, a=1):
    pass

  def TwoDefaultValues(self, a, b, c=1, d=2):
    pass

  def Args(self, a, b, *args):
    pass

  def Kwargs(self, a, b=2, **kwargs):
    pass

  def ArgsAndKwargs(self, a, *args, **kwargs):
    pass


class MockObjectTest(unittest.TestCase):
  """Verify that the MockObject class works as exepcted."""

  def setUp(self):
    self.mock_object = mox.MockObject(TestClass)

  def testSetupModeWithValidCall(self):
    """Verify the mock object properly mocks a basic method call."""
    self.mock_object.ValidCall()
    self.assert_(len(self.mock_object._expected_calls_queue) == 1)

  def testSetupModeWithInvalidCall(self):
    """UnknownMethodCallError should be raised if a non-member method is called.
    """
    # Note: assertRaises does not catch exceptions thrown by MockObject's
    # __getattr__
    try:
      self.mock_object.InvalidCall()
      self.fail("No exception thrown, expected UnknownMethodCallError")
    except mox.UnknownMethodCallError:
      pass
    except Exception:
      self.fail("Wrong exception type thrown, expected UnknownMethodCallError")

  def testReplayWithInvalidCall(self):
    """UnknownMethodCallError should be raised if a non-member method is called.
    """
    self.mock_object.ValidCall()          # setup method call
    self.mock_object._Replay()             # start replay mode
    # Note: assertRaises does not catch exceptions thrown by MockObject's
    # __getattr__
    try:
      self.mock_object.InvalidCall()
      self.fail("No exception thrown, expected UnknownMethodCallError")
    except mox.UnknownMethodCallError:
      pass
    except Exception:
      self.fail("Wrong exception type thrown, expected UnknownMethodCallError")

  def testIsInstance(self):
    """Mock should be able to pass as an instance of the mocked class."""
    self.assert_(isinstance(self.mock_object, TestClass))

  def testFindValidMethods(self):
    """Mock should be able to mock all public methods."""
    self.assert_('ValidCall' in self.mock_object._known_methods)
    self.assert_('OtherValidCall' in self.mock_object._known_methods)
    self.assert_('MyClassMethod' in self.mock_object._known_methods)
    self.assert_('MyStaticMethod' in self.mock_object._known_methods)
    self.assert_('_ProtectedCall' in self.mock_object._known_methods)
    self.assert_('__PrivateCall' not in self.mock_object._known_methods)
    self.assert_('_TestClass__PrivateCall' in self.mock_object._known_methods)

  def testFindsSuperclassMethods(self):
    """Mock should be able to mock superclasses methods."""
    self.mock_object = mox.MockObject(ChildClass)
    self.assert_('ValidCall' in self.mock_object._known_methods)
    self.assert_('OtherValidCall' in self.mock_object._known_methods)
    self.assert_('MyClassMethod' in self.mock_object._known_methods)
    self.assert_('ChildValidCall' in self.mock_object._known_methods)

  def testAccessClassVariables(self):
    """Class variables should be accessible through the mock."""
    self.assert_('SOME_CLASS_VAR' in self.mock_object._known_vars)
    self.assert_('_PROTECTED_CLASS_VAR' in self.mock_object._known_vars)
    self.assertEquals('test_value', self.mock_object.SOME_CLASS_VAR)

  def testEquals(self):
    """A mock should be able to compare itself to another object."""
    self.mock_object._Replay()
    self.assertEquals(self.mock_object, self.mock_object)

  def testEqualsMockFailure(self):
    """Verify equals identifies unequal objects."""
    self.mock_object.ValidCall()
    self.mock_object._Replay()
    self.assertNotEquals(self.mock_object, mox.MockObject(TestClass))

  def testEqualsInstanceFailure(self):
    """Verify equals identifies that objects are different instances."""
    self.mock_object._Replay()
    self.assertNotEquals(self.mock_object, TestClass())

  def testNotEquals(self):
    """Verify not equals works."""
    self.mock_object._Replay()
    self.assertFalse(self.mock_object != self.mock_object)

  def testMockSetItem_ExpectedSetItem_Success(self):
    """Test that __setitem__() gets mocked in Dummy.

    In this test, _Verify() succeeds.
    """
    dummy = mox.MockObject(TestClass)
    dummy['X'] = 'Y'

    dummy._Replay()

    dummy['X'] = 'Y'

    dummy._Verify()

  def testMockSetItem_ExpectedSetItem_NoSuccess(self):
    """Test that __setitem__() gets mocked in Dummy.

    In this test, _Verify() fails.
    """
    dummy = mox.MockObject(TestClass)
    dummy['X'] = 'Y'

    dummy._Replay()

    # NOT doing dummy['X'] = 'Y'

    self.assertRaises(mox.ExpectedMethodCallsError, dummy._Verify)

  def testMockSetItem_ExpectedNoSetItem_Success(self):
    """Test that __setitem__() gets mocked in Dummy."""
    dummy = mox.MockObject(TestClass)
    # NOT doing dummy['X'] = 'Y'

    dummy._Replay()

    def call(): dummy['X'] = 'Y'
    self.assertRaises(mox.UnexpectedMethodCallError, call)

  def testMockSetItem_ExpectedNoSetItem_NoSuccess(self):
    """Test that __setitem__() gets mocked in Dummy.

    In this test, _Verify() fails.
    """
    dummy = mox.MockObject(TestClass)
    # NOT doing dummy['X'] = 'Y'

    dummy._Replay()

    # NOT doing dummy['X'] = 'Y'

    dummy._Verify()

  def testMockSetItem_ExpectedSetItem_NonmatchingParameters(self):
    """Test that __setitem__() fails if other parameters are expected."""
    dummy = mox.MockObject(TestClass)
    dummy['X'] = 'Y'

    dummy._Replay()

    def call(): dummy['wrong'] = 'Y'

    self.assertRaises(mox.UnexpectedMethodCallError, call)

    dummy._Verify()

  def testMockSetItem_WithSubClassOfNewStyleClass(self):
    class NewStyleTestClass(object):
      def __init__(self):
        self.my_dict = {}

      def __setitem__(self, key, value):
        self.my_dict[key], value

    class TestSubClass(NewStyleTestClass):
      pass

    dummy = mox.MockObject(TestSubClass)
    dummy[1] = 2
    dummy._Replay()
    dummy[1] = 2
    dummy._Verify()

  def testMockGetItem_ExpectedGetItem_Success(self):
    """Test that __getitem__() gets mocked in Dummy.

    In this test, _Verify() succeeds.
    """
    dummy = mox.MockObject(TestClass)
    dummy['X'].AndReturn('value')

    dummy._Replay()

    self.assertEqual(dummy['X'], 'value')

    dummy._Verify()

  def testMockGetItem_ExpectedGetItem_NoSuccess(self):
    """Test that __getitem__() gets mocked in Dummy.

    In this test, _Verify() fails.
    """
    dummy = mox.MockObject(TestClass)
    dummy['X'].AndReturn('value')

    dummy._Replay()

    # NOT doing dummy['X']

    self.assertRaises(mox.ExpectedMethodCallsError, dummy._Verify)

  def testMockGetItem_ExpectedNoGetItem_NoSuccess(self):
    """Test that __getitem__() gets mocked in Dummy."""
    dummy = mox.MockObject(TestClass)
    # NOT doing dummy['X']

    dummy._Replay()

    def call(): return dummy['X']
    self.assertRaises(mox.UnexpectedMethodCallError, call)

  def testMockGetItem_ExpectedGetItem_NonmatchingParameters(self):
    """Test that __getitem__() fails if other parameters are expected."""
    dummy = mox.MockObject(TestClass)
    dummy['X'].AndReturn('value')

    dummy._Replay()

    def call(): return dummy['wrong']

    self.assertRaises(mox.UnexpectedMethodCallError, call)

    dummy._Verify()

  def testMockGetItem_WithSubClassOfNewStyleClass(self):
    class NewStyleTestClass(object):
      def __getitem__(self, key):
        return {1: '1', 2: '2'}[key]

    class TestSubClass(NewStyleTestClass):
      pass

    dummy = mox.MockObject(TestSubClass)
    dummy[1].AndReturn('3')

    dummy._Replay()
    self.assertEquals('3', dummy.__getitem__(1))
    dummy._Verify()

  def testMockIter_ExpectedIter_Success(self):
    """Test that __iter__() gets mocked in Dummy.

    In this test, _Verify() succeeds.
    """
    dummy = mox.MockObject(TestClass)
    iter(dummy).AndReturn(iter(['X', 'Y']))

    dummy._Replay()

    self.assertEqual([x for x in dummy], ['X', 'Y'])

    dummy._Verify()
  def testMockContains_ExpectedContains_Success(self):
    """Test that __contains__ gets mocked in Dummy.

    In this test, _Verify() succeeds.
    """
    dummy = mox.MockObject(TestClass)
    dummy.__contains__('X').AndReturn(True)

    dummy._Replay()

    self.failUnless('X' in dummy)

    dummy._Verify()

  def testMockContains_ExpectedContains_NoSuccess(self):
    """Test that __contains__() gets mocked in Dummy.

    In this test, _Verify() fails.
    """
    dummy = mox.MockObject(TestClass)
    dummy.__contains__('X').AndReturn('True')

    dummy._Replay()

    # NOT doing 'X' in dummy

    self.assertRaises(mox.ExpectedMethodCallsError, dummy._Verify)

  def testMockContains_ExpectedContains_NonmatchingParameter(self):
    """Test that __contains__ fails if other parameters are expected."""
    dummy = mox.MockObject(TestClass)
    dummy.__contains__('X').AndReturn(True)

    dummy._Replay()

    def call(): return 'Y' in dummy

    self.assertRaises(mox.UnexpectedMethodCallError, call)

    dummy._Verify()

  def testMockIter_ExpectedIter_NoSuccess(self):
    """Test that __iter__() gets mocked in Dummy.

    In this test, _Verify() fails.
    """
    dummy = mox.MockObject(TestClass)
    iter(dummy).AndReturn(iter(['X', 'Y']))

    dummy._Replay()

    # NOT doing self.assertEqual([x for x in dummy], ['X', 'Y'])

    self.assertRaises(mox.ExpectedMethodCallsError, dummy._Verify)

  def testMockIter_ExpectedNoIter_NoSuccess(self):
    """Test that __iter__() gets mocked in Dummy."""
    dummy = mox.MockObject(TestClass)
    # NOT doing iter(dummy)

    dummy._Replay()

    def call(): return [x for x in dummy]
    self.assertRaises(mox.UnexpectedMethodCallError, call)

  def testMockIter_ExpectedGetItem_Success(self):
    """Test that __iter__() gets mocked in Dummy using getitem."""
    dummy = mox.MockObject(SubscribtableNonIterableClass)
    dummy[0].AndReturn('a')
    dummy[1].AndReturn('b')
    dummy[2].AndRaise(IndexError)

    dummy._Replay()
    self.assertEquals(['a', 'b'], [x for x in dummy])
    dummy._Verify()

  def testMockIter_ExpectedNoGetItem_NoSuccess(self):
    """Test that __iter__() gets mocked in Dummy using getitem."""
    dummy = mox.MockObject(SubscribtableNonIterableClass)
    # NOT doing dummy[index]

    dummy._Replay()
    function = lambda: [x for x in dummy]
    self.assertRaises(mox.UnexpectedMethodCallError, function)

  def testMockGetIter_WithSubClassOfNewStyleClass(self):
    class NewStyleTestClass(object):
      def __iter__(self):
        return iter([1, 2, 3])

    class TestSubClass(NewStyleTestClass):
      pass

    dummy = mox.MockObject(TestSubClass)
    iter(dummy).AndReturn(iter(['a', 'b']))
    dummy._Replay()
    self.assertEquals(['a', 'b'], [x for x in dummy])
    dummy._Verify()


class MoxTest(unittest.TestCase):
  """Verify Mox works correctly."""

  def setUp(self):
    self.mox = mox.Mox()

  def testCreateObject(self):
    """Mox should create a mock object."""
    mock_obj = self.mox.CreateMock(TestClass)

  def testVerifyObjectWithCompleteReplay(self):
    """Mox should replay and verify all objects it created."""
    mock_obj = self.mox.CreateMock(TestClass)
    mock_obj.ValidCall()
    mock_obj.ValidCallWithArgs(mox.IsA(TestClass))
    self.mox.ReplayAll()
    mock_obj.ValidCall()
    mock_obj.ValidCallWithArgs(TestClass("some_value"))
    self.mox.VerifyAll()

  def testVerifyObjectWithIncompleteReplay(self):
    """Mox should raise an exception if a mock didn't replay completely."""
    mock_obj = self.mox.CreateMock(TestClass)
    mock_obj.ValidCall()
    self.mox.ReplayAll()
    # ValidCall() is never made
    self.assertRaises(mox.ExpectedMethodCallsError, self.mox.VerifyAll)

  def testEntireWorkflow(self):
    """Test the whole work flow."""
    mock_obj = self.mox.CreateMock(TestClass)
    mock_obj.ValidCall().AndReturn("yes")
    self.mox.ReplayAll()

    ret_val = mock_obj.ValidCall()
    self.assertEquals("yes", ret_val)
    self.mox.VerifyAll()

  def testCallableObject(self):
    """Test recording calls to a callable object works."""
    mock_obj = self.mox.CreateMock(CallableClass)
    mock_obj("foo").AndReturn("qux")
    self.mox.ReplayAll()

    ret_val = mock_obj("foo")
    self.assertEquals("qux", ret_val)
    self.mox.VerifyAll()

  def testInheritedCallableObject(self):
    """Test recording calls to an object inheriting from a callable object."""
    mock_obj = self.mox.CreateMock(InheritsFromCallable)
    mock_obj("foo").AndReturn("qux")
    self.mox.ReplayAll()

    ret_val = mock_obj("foo")
    self.assertEquals("qux", ret_val)
    self.mox.VerifyAll()

  def testCallOnNonCallableObject(self):
    """Test that you cannot call a non-callable object."""
    mock_obj = self.mox.CreateMock(TestClass)
    self.assertRaises(TypeError, mock_obj)

  def testCallableObjectWithBadCall(self):
    """Test verifying calls to a callable object works."""
    mock_obj = self.mox.CreateMock(CallableClass)
    mock_obj("foo").AndReturn("qux")
    self.mox.ReplayAll()

    self.assertRaises(mox.UnexpectedMethodCallError, mock_obj, "ZOOBAZ")

  def testUnorderedGroup(self):
    """Test that using one unordered group works."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Method(1).InAnyOrder()
    mock_obj.Method(2).InAnyOrder()
    self.mox.ReplayAll()

    mock_obj.Method(2)
    mock_obj.Method(1)

    self.mox.VerifyAll()

  def testUnorderedGroupsInline(self):
    """Unordered groups should work in the context of ordered calls."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(1).InAnyOrder()
    mock_obj.Method(2).InAnyOrder()
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    mock_obj.Method(2)
    mock_obj.Method(1)
    mock_obj.Close()

    self.mox.VerifyAll()

  def testMultipleUnorderdGroups(self):
    """Multiple unoreded groups should work."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Method(1).InAnyOrder()
    mock_obj.Method(2).InAnyOrder()
    mock_obj.Foo().InAnyOrder('group2')
    mock_obj.Bar().InAnyOrder('group2')
    self.mox.ReplayAll()

    mock_obj.Method(2)
    mock_obj.Method(1)
    mock_obj.Bar()
    mock_obj.Foo()

    self.mox.VerifyAll()

  def testMultipleUnorderdGroupsOutOfOrder(self):
    """Multiple unordered groups should maintain external order"""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Method(1).InAnyOrder()
    mock_obj.Method(2).InAnyOrder()
    mock_obj.Foo().InAnyOrder('group2')
    mock_obj.Bar().InAnyOrder('group2')
    self.mox.ReplayAll()

    mock_obj.Method(2)
    self.assertRaises(mox.UnexpectedMethodCallError, mock_obj.Bar)

  def testUnorderedGroupWithReturnValue(self):
    """Unordered groups should work with return values."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(1).InAnyOrder().AndReturn(9)
    mock_obj.Method(2).InAnyOrder().AndReturn(10)
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    actual_two = mock_obj.Method(2)
    actual_one = mock_obj.Method(1)
    mock_obj.Close()

    self.assertEquals(9, actual_one)
    self.assertEquals(10, actual_two)

    self.mox.VerifyAll()

  def testUnorderedGroupWithComparator(self):
    """Unordered groups should work with comparators"""

    def VerifyOne(cmd):
      if not isinstance(cmd, str):
        self.fail('Unexpected type passed to comparator: ' + str(cmd))
      return cmd == 'test'

    def VerifyTwo(cmd):
      return True

    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Foo(['test'], mox.Func(VerifyOne), bar=1).InAnyOrder().\
        AndReturn('yes test')
    mock_obj.Foo(['test'], mox.Func(VerifyTwo), bar=1).InAnyOrder().\
        AndReturn('anything')

    self.mox.ReplayAll()

    mock_obj.Foo(['test'], 'anything', bar=1)
    mock_obj.Foo(['test'], 'test', bar=1)

    self.mox.VerifyAll()

  def testMultipleTimes(self):
    """Test if MultipleTimesGroup works."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Method(1).MultipleTimes().AndReturn(9)
    mock_obj.Method(2).AndReturn(10)
    mock_obj.Method(3).MultipleTimes().AndReturn(42)
    self.mox.ReplayAll()

    actual_one = mock_obj.Method(1)
    second_one = mock_obj.Method(1) # This tests MultipleTimes.
    actual_two = mock_obj.Method(2)
    actual_three = mock_obj.Method(3)
    mock_obj.Method(3)
    mock_obj.Method(3)

    self.mox.VerifyAll()

    self.assertEquals(9, actual_one)
    self.assertEquals(9, second_one) # Repeated calls should return same number.
    self.assertEquals(10, actual_two)
    self.assertEquals(42, actual_three)

  def testMultipleTimesUsingIsAParameter(self):
    """Test if MultipleTimesGroup works with a IsA parameter."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(mox.IsA(str)).MultipleTimes("IsA").AndReturn(9)
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    actual_one = mock_obj.Method("1")
    second_one = mock_obj.Method("2") # This tests MultipleTimes.
    mock_obj.Close()

    self.mox.VerifyAll()

    self.assertEquals(9, actual_one)
    self.assertEquals(9, second_one) # Repeated calls should return same number.

  def testMutlipleTimesUsingFunc(self):
    """Test that the Func is not evaluated more times than necessary.

    If a Func() has side effects, it can cause a passing test to fail.
    """

    self.counter = 0
    def MyFunc(actual_str):
      """Increment the counter if actual_str == 'foo'."""
      if actual_str == 'foo':
        self.counter += 1
      return True

    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(mox.Func(MyFunc)).MultipleTimes()
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    mock_obj.Method('foo')
    mock_obj.Method('foo')
    mock_obj.Method('not-foo')
    mock_obj.Close()

    self.mox.VerifyAll()

    self.assertEquals(2, self.counter)

  def testMultipleTimesThreeMethods(self):
    """Test if MultipleTimesGroup works with three or more methods."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(1).MultipleTimes().AndReturn(9)
    mock_obj.Method(2).MultipleTimes().AndReturn(8)
    mock_obj.Method(3).MultipleTimes().AndReturn(7)
    mock_obj.Method(4).AndReturn(10)
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    actual_three = mock_obj.Method(3)
    mock_obj.Method(1)
    actual_two = mock_obj.Method(2)
    mock_obj.Method(3)
    actual_one = mock_obj.Method(1)
    actual_four = mock_obj.Method(4)
    mock_obj.Close()

    self.assertEquals(9, actual_one)
    self.assertEquals(8, actual_two)
    self.assertEquals(7, actual_three)
    self.assertEquals(10, actual_four)

    self.mox.VerifyAll()

  def testMultipleTimesMissingOne(self):
    """Test if MultipleTimesGroup fails if one method is missing."""
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(1).MultipleTimes().AndReturn(9)
    mock_obj.Method(2).MultipleTimes().AndReturn(8)
    mock_obj.Method(3).MultipleTimes().AndReturn(7)
    mock_obj.Method(4).AndReturn(10)
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    mock_obj.Method(3)
    mock_obj.Method(2)
    mock_obj.Method(3)
    mock_obj.Method(3)
    mock_obj.Method(2)

    self.assertRaises(mox.UnexpectedMethodCallError, mock_obj.Method, 4)

  def testMultipleTimesTwoGroups(self):
    """Test if MultipleTimesGroup works with a group after a
    MultipleTimesGroup.
    """
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(1).MultipleTimes().AndReturn(9)
    mock_obj.Method(3).MultipleTimes("nr2").AndReturn(42)
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    actual_one = mock_obj.Method(1)
    mock_obj.Method(1)
    actual_three = mock_obj.Method(3)
    mock_obj.Method(3)
    mock_obj.Close()

    self.assertEquals(9, actual_one)
    self.assertEquals(42, actual_three)

    self.mox.VerifyAll()

  def testMultipleTimesTwoGroupsFailure(self):
    """Test if MultipleTimesGroup fails with a group after a
    MultipleTimesGroup.
    """
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.Open()
    mock_obj.Method(1).MultipleTimes().AndReturn(9)
    mock_obj.Method(3).MultipleTimes("nr2").AndReturn(42)
    mock_obj.Close()
    self.mox.ReplayAll()

    mock_obj.Open()
    actual_one = mock_obj.Method(1)
    mock_obj.Method(1)
    actual_three = mock_obj.Method(3)

    self.assertRaises(mox.UnexpectedMethodCallError, mock_obj.Method, 1)

  def testWithSideEffects(self):
    """Test side effect operations actually modify their target objects."""
    def modifier(mutable_list):
      mutable_list[0] = 'mutated'
    mock_obj = self.mox.CreateMockAnything()
    mock_obj.ConfigureInOutParameter(['original']).WithSideEffects(modifier)
    mock_obj.WorkWithParameter(['mutated'])
    self.mox.ReplayAll()

    local_list = ['original']
    mock_obj.ConfigureInOutParameter(local_list)
    mock_obj.WorkWithParameter(local_list)

    self.mox.VerifyAll()

  def testWithSideEffectsException(self):
    """Test side effect operations actually modify their target objects."""
    def modifier(mutable_list):
      mutable_list[0] = 'mutated'
    mock_obj = self.mox.CreateMockAnything()
    method = mock_obj.ConfigureInOutParameter(['original'])
    method.WithSideEffects(modifier).AndRaise(Exception('exception'))
    mock_obj.WorkWithParameter(['mutated'])
    self.mox.ReplayAll()

    local_list = ['original']
    self.failUnlessRaises(Exception,
                          mock_obj.ConfigureInOutParameter,
                          local_list)
    mock_obj.WorkWithParameter(local_list)

    self.mox.VerifyAll()

  def testStubOutMethod(self):
    """Test that a method is replaced with a MockAnything."""
    test_obj = TestClass()
    # Replace OtherValidCall with a mock.
    self.mox.StubOutWithMock(test_obj, 'OtherValidCall')
    self.assert_(isinstance(test_obj.OtherValidCall, mox.MockAnything))
    test_obj.OtherValidCall().AndReturn('foo')
    self.mox.ReplayAll()

    actual = test_obj.OtherValidCall()

    self.mox.VerifyAll()
    self.mox.UnsetStubs()
    self.assertEquals('foo', actual)
    self.failIf(isinstance(test_obj.OtherValidCall, mox.MockAnything))

  def testWarnsUserIfMockingMock(self):
    """Test that user is warned if they try to stub out a MockAnything."""
    self.mox.StubOutWithMock(TestClass, 'MyStaticMethod')
    self.assertRaises(TypeError, self.mox.StubOutWithMock, TestClass,
                      'MyStaticMethod')

  def testStubOutObject(self):
    """Test than object is replaced with a Mock."""

    class Foo(object):
      def __init__(self):
        self.obj = TestClass()

    foo = Foo()
    self.mox.StubOutWithMock(foo, "obj")
    self.assert_(isinstance(foo.obj, mox.MockObject))
    foo.obj.ValidCall()
    self.mox.ReplayAll()

    foo.obj.ValidCall()

    self.mox.VerifyAll()
    self.mox.UnsetStubs()
    self.failIf(isinstance(foo.obj, mox.MockObject))

  def testForgotReplayHelpfulMessage(self):
    """If there is an AttributeError on a MockMethod, give users a helpful msg.
    """
    foo = self.mox.CreateMockAnything()
    bar = self.mox.CreateMockAnything()
    foo.GetBar().AndReturn(bar)
    bar.ShowMeTheMoney()
    # Forgot to replay!
    try:
      foo.GetBar().ShowMeTheMoney()
    except AttributeError, e:
      self.assertEquals('MockMethod has no attribute "ShowMeTheMoney". '
          'Did you remember to put your mocks in replay mode?', str(e))


class ReplayTest(unittest.TestCase):
  """Verify Replay works properly."""

  def testReplay(self):
    """Replay should put objects into replay mode."""
    mock_obj = mox.MockObject(TestClass)
    self.assertFalse(mock_obj._replay_mode)
    mox.Replay(mock_obj)
    self.assertTrue(mock_obj._replay_mode)


class MoxTestBaseTest(unittest.TestCase):
  """Verify that all tests in a class derived from MoxTestBase are wrapped."""

  def setUp(self):
    self.mox = mox.Mox()
    self.test_mox = mox.Mox()
    self.result = unittest.TestResult()

  def tearDown(self):
    # In case one of our tests fail before UnsetStubs is called.
    self.mox.UnsetStubs()
    self.test_mox.UnsetStubs()

  def _setUpTestClass(self):
    """Replacement for setUp in the test class instance.

    Assigns a mox.Mox instance as the mox attribute of the test class instance.
    This replacement Mox instance is under our control before setUp is called
    in the test class instance.
    """
    self.test.mox = self.test_mox

  def _CreateTest(self, test_name):
    """Create a test from our example mox class.

    The created test instance is assigned to this instances test attribute.
    """
    self.test = mox_test_helper.ExampleMoxTest(test_name)
    self.mox.stubs.Set(self.test, 'setUp', self._setUpTestClass)

  def _VerifySuccess(self):
    """Run the checks to confirm test method completed successfully."""
    self.mox.StubOutWithMock(self.test_mox, 'UnsetStubs')
    self.mox.StubOutWithMock(self.test_mox, 'VerifyAll')
    self.test_mox.UnsetStubs()
    self.test_mox.VerifyAll()
    self.mox.ReplayAll()
    self.test.run(result=self.result)
    self.assertTrue(self.result.wasSuccessful())
    self.mox.UnsetStubs()
    self.mox.VerifyAll()
    self.test_mox.UnsetStubs()
    self.test_mox.VerifyAll()

  def testSuccess(self):
    """Successful test method execution test."""
    self._CreateTest('testSuccess')
    self._VerifySuccess()

  def testExpectedNotCalled(self):
    """Stubbed out method is not called."""
    self._CreateTest('testExpectedNotCalled')
    self.mox.StubOutWithMock(self.test_mox, 'UnsetStubs')
    # Dont stub out VerifyAll - that's what causes the test to fail
    self.test_mox.UnsetStubs()
    self.test_mox.VerifyAll()
    self.mox.ReplayAll()
    self.test.run(result=self.result)
    self.failIf(self.result.wasSuccessful())
    self.mox.UnsetStubs()
    self.mox.VerifyAll()
    self.test_mox.UnsetStubs()

  def testUnexpectedCall(self):
    """Stubbed out method is called with unexpected arguments."""
    self._CreateTest('testUnexpectedCall')
    self.mox.StubOutWithMock(self.test_mox, 'UnsetStubs')
    # Ensure no calls are made to VerifyAll()
    self.mox.StubOutWithMock(self.test_mox, 'VerifyAll')
    self.test_mox.UnsetStubs()
    self.mox.ReplayAll()
    self.test.run(result=self.result)
    self.failIf(self.result.wasSuccessful())
    self.mox.UnsetStubs()
    self.mox.VerifyAll()
    self.test_mox.UnsetStubs()

  def testFailure(self):
    """Failing assertion in test method."""
    self._CreateTest('testFailure')
    self.mox.StubOutWithMock(self.test_mox, 'UnsetStubs')
    # Ensure no calls are made to VerifyAll()
    self.mox.StubOutWithMock(self.test_mox, 'VerifyAll')
    self.test_mox.UnsetStubs()
    self.mox.ReplayAll()
    self.test.run(result=self.result)
    self.failIf(self.result.wasSuccessful())
    self.mox.UnsetStubs()
    self.mox.VerifyAll()
    self.test_mox.UnsetStubs()

  def testMixin(self):
    """Run test from mix-in test class, ensure it passes."""
    self._CreateTest('testStat')
    self._VerifySuccess()

  def testMixinAgain(self):
    """Run same test as above but from the current test class.

    This ensures metaclass properly wrapped test methods from all base classes.
    If unsetting of stubs doesn't happen, this will fail.
    """
    self._CreateTest('testStatOther')
    self._VerifySuccess()


class VerifyTest(unittest.TestCase):
  """Verify Verify works properly."""

  def testVerify(self):
    """Verify should be called for all objects.

    This should throw an exception because the expected behavior did not occur.
    """
    mock_obj = mox.MockObject(TestClass)
    mock_obj.ValidCall()
    mock_obj._Replay()
    self.assertRaises(mox.ExpectedMethodCallsError, mox.Verify, mock_obj)


class ResetTest(unittest.TestCase):
  """Verify Reset works properly."""

  def testReset(self):
    """Should empty all queues and put mocks in record mode."""
    mock_obj = mox.MockObject(TestClass)
    mock_obj.ValidCall()
    self.assertFalse(mock_obj._replay_mode)
    mock_obj._Replay()
    self.assertTrue(mock_obj._replay_mode)
    self.assertEquals(1, len(mock_obj._expected_calls_queue))

    mox.Reset(mock_obj)
    self.assertFalse(mock_obj._replay_mode)
    self.assertEquals(0, len(mock_obj._expected_calls_queue))


class MyTestCase(unittest.TestCase):
  """Simulate the use of a fake wrapper around Python's unittest library."""

  def setUp(self):
    super(MyTestCase, self).setUp()
    self.critical_variable = 42


class MoxTestBaseMultipleInheritanceTest(mox.MoxTestBase, MyTestCase):
  """Test that multiple inheritance can be used with MoxTestBase."""

  def setUp(self):
    super(MoxTestBaseMultipleInheritanceTest, self).setUp()

  def testMultipleInheritance(self):
    """Should be able to access members created by all parent setUp()."""
    self.assert_(isinstance(self.mox, mox.Mox))
    self.assertEquals(42, self.critical_variable)


class TestClass:
  """This class is used only for testing the mock framework"""

  SOME_CLASS_VAR = "test_value"
  _PROTECTED_CLASS_VAR = "protected value"

  def __init__(self, ivar=None):
    self.__ivar = ivar

  def __eq__(self, rhs):
    return self.__ivar == rhs

  def __ne__(self, rhs):
    return not self.__eq__(rhs)

  def ValidCall(self):
    pass

  def OtherValidCall(self):
    pass

  def ValidCallWithArgs(self, *args, **kwargs):
    pass

  @classmethod
  def MyClassMethod(cls):
    pass

  @staticmethod
  def MyStaticMethod():
    pass

  def _ProtectedCall(self):
    pass

  def __PrivateCall(self):
    pass

  def __getitem__(self, key):
    pass

  def __DoNotMock(self):
    pass

  def __getitem__(self, key):
    """Return the value for key."""
    return self.d[key]

  def __setitem__(self, key, value):
    """Set the value for key to value."""
    self.d[key] = value

  def __contains__(self, key):
     """Returns True if d contains the key."""
     return key in self.d

  def __iter__(self):
    pass

class ChildClass(TestClass):
  """This inherits from TestClass."""
  def __init__(self):
    TestClass.__init__(self)

  def ChildValidCall(self):
    pass


class CallableClass(object):
  """This class is callable, and that should be mockable!"""

  def __init__(self):
    pass

  def __call__(self, param):
    return param


class SubscribtableNonIterableClass(object):
  def __getitem__(self, index):
    raise IndexError


class InheritsFromCallable(CallableClass):
  """This class should also be mockable; it inherits from a callable class."""

  pass


if __name__ == '__main__':
  unittest.main()
