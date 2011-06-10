# coding=utf8
# Copyright (c) 2011 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.
"""Collection of subprocess wrapper functions.

In theory you shouldn't need anything else in subprocess, or this module failed.
"""

from __future__ import with_statement
import errno
import logging
import os
import subprocess
import sys
import tempfile
import time
import threading

# Constants forwarded from subprocess.
PIPE = subprocess.PIPE
STDOUT = subprocess.STDOUT
# Sends stdout or stderr to os.devnull.
VOID = '/dev/null'
# Error code when a process was killed because it timed out.
TIMED_OUT = -2001

# Globals.
# Set to True if you somehow need to disable this hack.
SUBPROCESS_CLEANUP_HACKED = False


class CalledProcessError(subprocess.CalledProcessError):
  """Augment the standard exception with more data."""
  def __init__(self, returncode, cmd, cwd, stdout, stderr):
    super(CalledProcessError, self).__init__(returncode, cmd)
    self.stdout = stdout
    self.stderr = stderr
    self.cwd = cwd

  def __str__(self):
    out = 'Command %s returned non-zero exit status %s' % (
        ' '.join(self.cmd), self.returncode)
    if self.cwd:
      out += ' in ' + self.cwd
    return '\n'.join(filter(None, (out, self.stdout, self.stderr)))


class CygwinRebaseError(CalledProcessError):
  """Occurs when cygwin's fork() emulation fails due to rebased dll."""


## Utility functions


def kill_pid(pid):
  """Kills a process by its process id."""
  try:
    # Unable to import 'module'
    # pylint: disable=E1101,F0401
    import signal
    return os.kill(pid, signal.SIGKILL)
  except ImportError:
    pass


def kill_win(process):
  """Kills a process with its windows handle.

  Has no effect on other platforms.
  """
  try:
    # Unable to import 'module'
    # pylint: disable=F0401
    import win32process
    # Access to a protected member _handle of a client class
    # pylint: disable=W0212
    return win32process.TerminateProcess(process._handle, -1)
  except ImportError:
    pass


def add_kill():
  """Adds kill() method to subprocess.Popen for python <2.6"""
  if hasattr(subprocess.Popen, 'kill'):
    return

  if sys.platform == 'win32':
    subprocess.Popen.kill = kill_win
  else:
    subprocess.Popen.kill = lambda process: kill_pid(process.pid)


def hack_subprocess():
  """subprocess functions may throw exceptions when used in multiple threads.

  See http://bugs.python.org/issue1731717 for more information.
  """
  global SUBPROCESS_CLEANUP_HACKED
  if not SUBPROCESS_CLEANUP_HACKED and threading.activeCount() != 1:
    # Only hack if there is ever multiple threads.
    # There is no point to leak with only one thread.
    subprocess._cleanup = lambda: None
    SUBPROCESS_CLEANUP_HACKED = True


def get_english_env(env):
  """Forces LANG and/or LANGUAGE to be English.

  Forces encoding to utf-8 for subprocesses.

  Returns None if it is unnecessary.
  """
  if sys.platform == 'win32':
    return None
  env = env or os.environ

  # Test if it is necessary at all.
  is_english = lambda name: env.get(name, 'en').startswith('en')

  if is_english('LANG') and is_english('LANGUAGE'):
    return None

  # Requires modifications.
  env = env.copy()
  def fix_lang(name):
    if not is_english(name):
      env[name] = 'en_US.UTF-8'
  fix_lang('LANG')
  fix_lang('LANGUAGE')
  return env


def Popen(args, **kwargs):
  """Wraps subprocess.Popen().

  Returns a subprocess.Popen object.

  - Forces English output since it's easier to parse the stdout if it is always
    in English.
  - Sets shell=True on windows by default. You can override this by forcing
    shell parameter to a value.
  - Adds support for VOID to not buffer when not needed.

  Note: Popen() can throw OSError when cwd or args[0] doesn't exist.
  """
  # Make sure we hack subprocess if necessary.
  hack_subprocess()
  add_kill()

  env = get_english_env(kwargs.get('env'))
  if env:
    kwargs['env'] = env
  if kwargs.get('shell') is None:
    # *Sigh*:  Windows needs shell=True, or else it won't search %PATH% for the
    # executable, but shell=True makes subprocess on Linux fail when it's called
    # with a list because it only tries to execute the first item in the list.
    kwargs['shell'] = bool(sys.platform=='win32')

  tmp_str = ' '.join(args)
  if kwargs.get('cwd', None):
    tmp_str += ';  cwd=%s' % kwargs['cwd']
  logging.debug(tmp_str)

  def fix(stream):
    if kwargs.get(stream) in (VOID, os.devnull):
      # Replaces VOID with handle to /dev/null.
      # Create a temporary file to workaround python's deadlock.
      # http://docs.python.org/library/subprocess.html#subprocess.Popen.wait
      # When the pipe fills up, it will deadlock this process. Using a real file
      # works around that issue.
      kwargs[stream] = open(os.devnull, 'w')

  fix('stdout')
  fix('stderr')

  try:
    return subprocess.Popen(args, **kwargs)
  except OSError, e:
    if e.errno == errno.EAGAIN and sys.platform == 'cygwin':
      # Convert fork() emulation failure into a CygwinRebaseError().
      raise CygwinRebaseError(
          e.errno,
          args,
          kwargs.get('cwd'),
          None,
          'Visit '
          'http://code.google.com/p/chromium/wiki/CygwinDllRemappingFailure to '
          'learn how to fix this error; you need to rebase your cygwin dlls')
    # Popen() can throw OSError when cwd or args[0] doesn't exist. Let it go
    # through
    raise


def call(args, timeout=None, **kwargs):
  """Wraps subprocess.Popen().communicate().

  Returns ((stdout, stderr), returncode).

  - The process will be killed after |timeout| seconds and returncode set to
    TIMED_OUT.
  - Automatically passes stdin content as input so do not specify stdin=PIPE.
  """
  stdin = kwargs.pop('stdin', None)
  if stdin is not None:
    assert stdin != PIPE
    # When stdin is passed as an argument, use it as the actual input data and
    # set the Popen() parameter accordingly.
    kwargs['stdin'] = PIPE

  if not timeout:
    # Normal workflow.
    proc = Popen(args, **kwargs)
    if stdin is not None:
      return proc.communicate(stdin), proc.returncode
    else:
      return proc.communicate(), proc.returncode

  # Create a temporary file to workaround python's deadlock.
  # http://docs.python.org/library/subprocess.html#subprocess.Popen.wait
  # When the pipe fills up, it will deadlock this process. Using a real file
  # works around that issue.
  with tempfile.TemporaryFile() as buff:
    start = time.time()
    kwargs['stdout'] = buff
    proc = Popen(args, **kwargs)
    if stdin is not None:
      proc.stdin.write(stdin)
    while proc.returncode is None:
      proc.poll()
      if timeout and (time.time() - start) > timeout:
        proc.kill()
        proc.wait()
        # It's -9 on linux and 1 on Windows. Standardize to TIMED_OUT.
        proc.returncode = TIMED_OUT
      time.sleep(0.001)
    # Now that the process died, reset the cursor and read the file.
    buff.seek(0)
    out = [buff.read(), None]
  return out, proc.returncode


def check_call(args, **kwargs):
  """Improved version of subprocess.check_call().

  Returns (stdout, stderr), unlike subprocess.check_call().
  """
  out, returncode = call(args, **kwargs)
  if returncode:
    raise CalledProcessError(
        returncode, args, kwargs.get('cwd'), out[0], out[1])
  return out


def capture(args, **kwargs):
  """Captures stdout of a process call and returns it.

  Returns stdout.

  - Discards returncode.
  - Discards stderr. By default sets stderr=STDOUT.
  """
  if kwargs.get('stdout') is None:
    kwargs['stdout'] = PIPE
  if kwargs.get('stderr') is None:
    kwargs['stderr'] = STDOUT
  return call(args, **kwargs)[0][0]


def check_output(args, **kwargs):
  """Captures stdout of a process call and returns it.

  Returns stdout.

  - Discards stderr. By default sets stderr=STDOUT.
  - Throws if return code is not 0.
  - Works even prior to python 2.7.
  """
  if kwargs.get('stdout') is None:
    kwargs['stdout'] = PIPE
  if kwargs.get('stderr') is None:
    kwargs['stderr'] = STDOUT
  return check_call(args, **kwargs)[0]
