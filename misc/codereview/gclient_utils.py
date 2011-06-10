# Copyright (c) 2010 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.

"""Generic utils."""

import errno
import logging
import os
import Queue
import re
import stat
import subprocess
import sys
import threading
import time


def hack_subprocess():
  """subprocess functions may throw exceptions when used in multiple threads.

  See http://bugs.python.org/issue1731717 for more information.
  """
  subprocess._cleanup = lambda: None


class Error(Exception):
  """gclient exception class."""
  pass


class CheckCallError(OSError, Error):
  """CheckCall() returned non-0."""
  def __init__(self, command, cwd, returncode, stdout, stderr=None):
    OSError.__init__(self, command, cwd, returncode, stdout, stderr)
    Error.__init__(self, command)
    self.command = command
    self.cwd = cwd
    self.returncode = returncode
    self.stdout = stdout
    self.stderr = stderr

  def __str__(self):
    out = ' '.join(self.command)
    if self.cwd:
      out += ' in ' + self.cwd
    if self.returncode is not None:
      out += ' returned %d' % self.returncode
    if self.stdout is not None:
      out += '\nstdout: %s\n' % self.stdout
    if self.stderr is not None:
      out += '\nstderr: %s\n' % self.stderr
    return out


def Popen(args, **kwargs):
  """Calls subprocess.Popen() with hacks to work around certain behaviors.

  Ensure English outpout for svn and make it work reliably on Windows.
  """
  logging.debug(u'%s, cwd=%s' % (u' '.join(args), kwargs.get('cwd', '')))
  if not 'env' in kwargs:
    # It's easier to parse the stdout if it is always in English.
    kwargs['env'] = os.environ.copy()
    kwargs['env']['LANGUAGE'] = 'en'
  if not 'shell' in kwargs:
    # *Sigh*:  Windows needs shell=True, or else it won't search %PATH% for the
    # executable, but shell=True makes subprocess on Linux fail when it's called
    # with a list because it only tries to execute the first item in the list.
    kwargs['shell'] = (sys.platform=='win32')
  try:
    return subprocess.Popen(args, **kwargs)
  except OSError, e:
    if e.errno == errno.EAGAIN and sys.platform == 'cygwin':
      raise Error(
          'Visit '
          'http://code.google.com/p/chromium/wiki/CygwinDllRemappingFailure to '
          'learn how to fix this error; you need to rebase your cygwin dlls')
    raise


def CheckCall(command, print_error=True, **kwargs):
  """Similar subprocess.check_call() but redirects stdout and
  returns (stdout, stderr).

  Works on python 2.4
  """
  try:
    stderr = None
    if not print_error:
      stderr = subprocess.PIPE
    process = Popen(command, stdout=subprocess.PIPE, stderr=stderr, **kwargs)
    std_out, std_err = process.communicate()
  except OSError, e:
    raise CheckCallError(command, kwargs.get('cwd', None), e.errno, None)
  if process.returncode:
    raise CheckCallError(command, kwargs.get('cwd', None), process.returncode,
        std_out, std_err)
  return std_out, std_err


def SplitUrlRevision(url):
  """Splits url and returns a two-tuple: url, rev"""
  if url.startswith('ssh:'):
    # Make sure ssh://user-name@example.com/~/test.git@stable works
    regex = r'(ssh://(?:[-\w]+@)?[-\w:\.]+/[-~\w\./]+)(?:@(.+))?'
    components = re.search(regex, url).groups()
  else:
    components = url.split('@', 1)
    if len(components) == 1:
      components += [None]
  return tuple(components)


def SyntaxErrorToError(filename, e):
  """Raises a gclient_utils.Error exception with the human readable message"""
  try:
    # Try to construct a human readable error message
    if filename:
      error_message = 'There is a syntax error in %s\n' % filename
    else:
      error_message = 'There is a syntax error\n'
    error_message += 'Line #%s, character %s: "%s"' % (
        e.lineno, e.offset, re.sub(r'[\r\n]*$', '', e.text))
  except:
    # Something went wrong, re-raise the original exception
    raise e
  else:
    raise Error(error_message)


class PrintableObject(object):
  def __str__(self):
    output = ''
    for i in dir(self):
      if i.startswith('__'):
        continue
      output += '%s = %s\n' % (i, str(getattr(self, i, '')))
    return output


def FileRead(filename, mode='rU'):
  content = None
  f = open(filename, mode)
  try:
    content = f.read()
  finally:
    f.close()
  return content


def FileWrite(filename, content, mode='w'):
  f = open(filename, mode)
  try:
    f.write(content)
  finally:
    f.close()


def rmtree(path):
  """shutil.rmtree() on steroids.

  Recursively removes a directory, even if it's marked read-only.

  shutil.rmtree() doesn't work on Windows if any of the files or directories
  are read-only, which svn repositories and some .svn files are.  We need to
  be able to force the files to be writable (i.e., deletable) as we traverse
  the tree.

  Even with all this, Windows still sometimes fails to delete a file, citing
  a permission error (maybe something to do with antivirus scans or disk
  indexing).  The best suggestion any of the user forums had was to wait a
  bit and try again, so we do that too.  It's hand-waving, but sometimes it
  works. :/

  On POSIX systems, things are a little bit simpler.  The modes of the files
  to be deleted doesn't matter, only the modes of the directories containing
  them are significant.  As the directory tree is traversed, each directory
  has its mode set appropriately before descending into it.  This should
  result in the entire tree being removed, with the possible exception of
  *path itself, because nothing attempts to change the mode of its parent.
  Doing so would be hazardous, as it's not a directory slated for removal.
  In the ordinary case, this is not a problem: for our purposes, the user
  will never lack write permission on *path's parent.
  """
  if not os.path.exists(path):
    return

  if os.path.islink(path) or not os.path.isdir(path):
    raise Error('Called rmtree(%s) in non-directory' % path)

  if sys.platform == 'win32':
    # Some people don't have the APIs installed. In that case we'll do without.
    win32api = None
    win32con = None
    try:
      # Unable to import 'XX'
      # pylint: disable=F0401
      import win32api, win32con
    except ImportError:
      pass
  else:
    # On POSIX systems, we need the x-bit set on the directory to access it,
    # the r-bit to see its contents, and the w-bit to remove files from it.
    # The actual modes of the files within the directory is irrelevant.
    os.chmod(path, stat.S_IRUSR | stat.S_IWUSR | stat.S_IXUSR)

  def remove(func, subpath):
    if sys.platform == 'win32':
      os.chmod(subpath, stat.S_IWRITE)
      if win32api and win32con:
        win32api.SetFileAttributes(subpath, win32con.FILE_ATTRIBUTE_NORMAL)
    try:
      func(subpath)
    except OSError, e:
      if e.errno != errno.EACCES or sys.platform != 'win32':
        raise
      # Failed to delete, try again after a 100ms sleep.
      time.sleep(0.1)
      func(subpath)

  for fn in os.listdir(path):
    # If fullpath is a symbolic link that points to a directory, isdir will
    # be True, but we don't want to descend into that as a directory, we just
    # want to remove the link.  Check islink and treat links as ordinary files
    # would be treated regardless of what they reference.
    fullpath = os.path.join(path, fn)
    if os.path.islink(fullpath) or not os.path.isdir(fullpath):
      remove(os.remove, fullpath)
    else:
      # Recurse.
      rmtree(fullpath)

  remove(os.rmdir, path)

# TODO(maruel): Rename the references.
RemoveDirectory = rmtree


def CheckCallAndFilterAndHeader(args, always=False, **kwargs):
  """Adds 'header' support to CheckCallAndFilter.

  If |always| is True, a message indicating what is being done
  is printed to stdout all the time even if not output is generated. Otherwise
  the message header is printed only if the call generated any ouput.
  """
  stdout = kwargs.get('stdout', None) or sys.stdout
  if always:
    stdout.write('\n________ running \'%s\' in \'%s\'\n'
        % (' '.join(args), kwargs.get('cwd', '.')))
  else:
    filter_fn = kwargs.get('filter_fn', None)
    def filter_msg(line):
      if line is None:
        stdout.write('\n________ running \'%s\' in \'%s\'\n'
            % (' '.join(args), kwargs.get('cwd', '.')))
      elif filter_fn:
        filter_fn(line)
    kwargs['filter_fn'] = filter_msg
    kwargs['call_filter_on_first_line'] = True
  # Obviously.
  kwargs['print_stdout'] = True
  return CheckCallAndFilter(args, **kwargs)


def SoftClone(obj):
  """Clones an object. copy.copy() doesn't work on 'file' objects."""
  if obj.__class__.__name__ == 'SoftCloned':
    return obj
  class SoftCloned(object):
    pass
  new_obj = SoftCloned()
  for member in dir(obj):
    if member.startswith('_'):
      continue
    setattr(new_obj, member, getattr(obj, member))
  return new_obj


def MakeFileAutoFlush(fileobj, delay=10):
  """Creates a file object clone to automatically flush after N seconds."""
  if hasattr(fileobj, 'last_flushed_at'):
    # Already patched. Just update delay.
    fileobj.delay = delay
    return fileobj

  # Attribute 'XXX' defined outside __init__
  # pylint: disable=W0201
  new_fileobj = SoftClone(fileobj)
  if not hasattr(new_fileobj, 'lock'):
    new_fileobj.lock = threading.Lock()
  new_fileobj.last_flushed_at = time.time()
  new_fileobj.delay = delay
  new_fileobj.old_auto_flush_write = new_fileobj.write
  # Silence pylint.
  new_fileobj.flush = fileobj.flush

  def auto_flush_write(out):
    new_fileobj.old_auto_flush_write(out)
    should_flush = False
    new_fileobj.lock.acquire()
    try:
      if (new_fileobj.delay and
          (time.time() - new_fileobj.last_flushed_at) > new_fileobj.delay):
        should_flush = True
        new_fileobj.last_flushed_at = time.time()
    finally:
      new_fileobj.lock.release()
    if should_flush:
      new_fileobj.flush()

  new_fileobj.write = auto_flush_write
  return new_fileobj


def MakeFileAnnotated(fileobj):
  """Creates a file object clone to automatically prepends every line in worker
  threads with a NN> prefix."""
  if hasattr(fileobj, 'output_buffers'):
    # Already patched.
    return fileobj

  # Attribute 'XXX' defined outside __init__
  # pylint: disable=W0201
  new_fileobj = SoftClone(fileobj)
  if not hasattr(new_fileobj, 'lock'):
    new_fileobj.lock = threading.Lock()
  new_fileobj.output_buffers = {}
  new_fileobj.old_annotated_write = new_fileobj.write

  def annotated_write(out):
    index = getattr(threading.currentThread(), 'index', None)
    if index is None:
      # Undexed threads aren't buffered.
      new_fileobj.old_annotated_write(out)
      return

    new_fileobj.lock.acquire()
    try:
      # Use a dummy array to hold the string so the code can be lockless.
      # Strings are immutable, requiring to keep a lock for the whole dictionary
      # otherwise. Using an array is faster than using a dummy object.
      if not index in new_fileobj.output_buffers:
        obj = new_fileobj.output_buffers[index] = ['']
      else:
        obj = new_fileobj.output_buffers[index]
    finally:
      new_fileobj.lock.release()

    # Continue lockless.
    obj[0] += out
    while '\n' in obj[0]:
      line, remaining = obj[0].split('\n', 1)
      new_fileobj.old_annotated_write('%d>%s\n' % (index, line))
      obj[0] = remaining

  def full_flush():
    """Flush buffered output."""
    orphans = []
    new_fileobj.lock.acquire()
    try:
      # Detect threads no longer existing.
      indexes = (getattr(t, 'index', None) for t in threading.enumerate())
      indexes = filter(None, indexes)
      for index in new_fileobj.output_buffers:
        if not index in indexes:
          orphans.append((index, new_fileobj.output_buffers[index][0]))
      for orphan in orphans:
        del new_fileobj.output_buffers[orphan[0]]
    finally:
      new_fileobj.lock.release()

    # Don't keep the lock while writting. Will append \n when it shouldn't.
    for orphan in orphans:
      new_fileobj.old_annotated_write('%d>%s\n' % (orphan[0], orphan[1]))

  new_fileobj.write = annotated_write
  new_fileobj.full_flush = full_flush
  return new_fileobj


def CheckCallAndFilter(args, stdout=None, filter_fn=None,
                       print_stdout=None, call_filter_on_first_line=False,
                       **kwargs):
  """Runs a command and calls back a filter function if needed.

  Accepts all subprocess.Popen() parameters plus:
    print_stdout: If True, the command's stdout is forwarded to stdout.
    filter_fn: A function taking a single string argument called with each line
               of the subprocess's output. Each line has the trailing newline
               character trimmed.
    stdout: Can be any bufferable output.

  stderr is always redirected to stdout.
  """
  assert print_stdout or filter_fn
  stdout = stdout or sys.stdout
  filter_fn = filter_fn or (lambda x: None)
  assert not 'stderr' in kwargs
  kid = Popen(args, bufsize=0,
              stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
              **kwargs)

  # Do a flush of stdout before we begin reading from the subprocess's stdout
  stdout.flush()

  # Also, we need to forward stdout to prevent weird re-ordering of output.
  # This has to be done on a per byte basis to make sure it is not buffered:
  # normally buffering is done for each line, but if svn requests input, no
  # end-of-line character is output after the prompt and it would not show up.
  in_byte = kid.stdout.read(1)
  if in_byte:
    if call_filter_on_first_line:
      filter_fn(None)
    in_line = ''
    while in_byte:
      if in_byte != '\r':
        if print_stdout:
          stdout.write(in_byte)
        if in_byte != '\n':
          in_line += in_byte
        else:
          filter_fn(in_line)
          in_line = ''
      in_byte = kid.stdout.read(1)
    # Flush the rest of buffered output. This is only an issue with
    # stdout/stderr not ending with a \n.
    if len(in_line):
      filter_fn(in_line)
  rv = kid.wait()
  if rv:
    raise CheckCallError(args, kwargs.get('cwd', None), rv, None)
  return 0


def FindGclientRoot(from_dir, filename='.gclient'):
  """Tries to find the gclient root."""
  real_from_dir = os.path.realpath(from_dir)
  path = real_from_dir
  while not os.path.exists(os.path.join(path, filename)):
    split_path = os.path.split(path)
    if not split_path[1]:
      return None
    path = split_path[0]

  # If we did not find the file in the current directory, make sure we are in a
  # sub directory that is controlled by this configuration.
  if path != real_from_dir:
    entries_filename = os.path.join(path, filename + '_entries')
    if not os.path.exists(entries_filename):
      # If .gclient_entries does not exist, a previous call to gclient sync
      # might have failed. In that case, we cannot verify that the .gclient
      # is the one we want to use. In order to not to cause too much trouble,
      # just issue a warning and return the path anyway.
      print >> sys.stderr, ("%s file in parent directory %s might not be the "
          "file you want to use" % (filename, path))
      return path
    scope = {}
    try:
      exec(FileRead(entries_filename), scope)
    except SyntaxError, e:
      SyntaxErrorToError(filename, e)
    all_directories = scope['entries'].keys()
    path_to_check = real_from_dir[len(path)+1:]
    while path_to_check:
      if path_to_check in all_directories:
        return path
      path_to_check = os.path.dirname(path_to_check)
    return None

  logging.info('Found gclient root at ' + path)
  return path


def PathDifference(root, subpath):
  """Returns the difference subpath minus root."""
  root = os.path.realpath(root)
  subpath = os.path.realpath(subpath)
  if not subpath.startswith(root):
    return None
  # If the root does not have a trailing \ or /, we add it so the returned
  # path starts immediately after the seperator regardless of whether it is
  # provided.
  root = os.path.join(root, '')
  return subpath[len(root):]


def FindFileUpwards(filename, path=None):
  """Search upwards from the a directory (default: current) to find a file."""
  if not path:
    path = os.getcwd()
  path = os.path.realpath(path)
  while True:
    file_path = os.path.join(path, filename)
    if os.path.isfile(file_path):
      return file_path
    (new_path, _) = os.path.split(path)
    if new_path == path:
      return None
    path = new_path


def GetGClientRootAndEntries(path=None):
  """Returns the gclient root and the dict of entries."""
  config_file = '.gclient_entries'
  config_path = FindFileUpwards(config_file, path)

  if not config_path:
    print "Can't find %s" % config_file
    return None

  env = {}
  execfile(config_path, env)
  config_dir = os.path.dirname(config_path)
  return config_dir, env['entries']


class WorkItem(object):
  """One work item."""
  # A list of string, each being a WorkItem name.
  requirements = []
  # A unique string representing this work item.
  name = None

  def run(self, work_queue):
    """work_queue is passed as keyword argument so it should be
    the last parameters of the function when you override it."""
    pass


class ExecutionQueue(object):
  """Runs a set of WorkItem that have interdependencies and were WorkItem are
  added as they are processed.

  In gclient's case, Dependencies sometime needs to be run out of order due to
  From() keyword. This class manages that all the required dependencies are run
  before running each one.

  Methods of this class are thread safe.
  """
  def __init__(self, jobs, progress):
    """jobs specifies the number of concurrent tasks to allow. progress is a
    Progress instance."""
    hack_subprocess()
    # Set when a thread is done or a new item is enqueued.
    self.ready_cond = threading.Condition()
    # Maximum number of concurrent tasks.
    self.jobs = jobs
    # List of WorkItem, for gclient, these are Dependency instances.
    self.queued = []
    # List of strings representing each Dependency.name that was run.
    self.ran = []
    # List of items currently running.
    self.running = []
    # Exceptions thrown if any.
    self.exceptions = Queue.Queue()
    # Progress status
    self.progress = progress
    if self.progress:
      self.progress.update(0)

  def enqueue(self, d):
    """Enqueue one Dependency to be executed later once its requirements are
    satisfied.
    """
    assert isinstance(d, WorkItem)
    self.ready_cond.acquire()
    try:
      self.queued.append(d)
      total = len(self.queued) + len(self.ran) + len(self.running)
      logging.debug('enqueued(%s)' % d.name)
      if self.progress:
        self.progress._total = total + 1
        self.progress.update(0)
      self.ready_cond.notifyAll()
    finally:
      self.ready_cond.release()

  def flush(self, *args, **kwargs):
    """Runs all enqueued items until all are executed."""
    kwargs['work_queue'] = self
    self.ready_cond.acquire()
    try:
      while True:
        # Check for task to run first, then wait.
        while True:
          if not self.exceptions.empty():
            # Systematically flush the queue when an exception logged.
            self.queued = []
          self._flush_terminated_threads()
          if (not self.queued and not self.running or
              self.jobs == len(self.running)):
            # No more worker threads or can't queue anything.
            break

          # Check for new tasks to start.
          for i in xrange(len(self.queued)):
            # Verify its requirements.
            for r in self.queued[i].requirements:
              if not r in self.ran:
                # Requirement not met.
                break
            else:
              # Start one work item: all its requirements are satisfied.
              self._run_one_task(self.queued.pop(i), args, kwargs)
              break
          else:
            # Couldn't find an item that could run. Break out the outher loop.
            break

        if not self.queued and not self.running:
          # We're done.
          break
        # We need to poll here otherwise Ctrl-C isn't processed.
        self.ready_cond.wait(10)
        # Something happened: self.enqueue() or a thread terminated. Loop again.
    finally:
      self.ready_cond.release()

    assert not self.running, 'Now guaranteed to be single-threaded'
    if not self.exceptions.empty():
      # To get back the stack location correctly, the raise a, b, c form must be
      # used, passing a tuple as the first argument doesn't work.
      e = self.exceptions.get()
      raise e[0], e[1], e[2]
    if self.progress:
      self.progress.end()

  def _flush_terminated_threads(self):
    """Flush threads that have terminated."""
    running = self.running
    self.running = []
    for t in running:
      if t.isAlive():
        self.running.append(t)
      else:
        t.join()
        sys.stdout.full_flush()  # pylint: disable=E1101
        if self.progress:
          self.progress.update(1, t.item.name)
        assert not t.item.name in self.ran
        if not t.item.name in self.ran:
          self.ran.append(t.item.name)

  def _run_one_task(self, task_item, args, kwargs):
    if self.jobs > 1:
      # Start the thread.
      index = len(self.ran) + len(self.running) + 1
      new_thread = self._Worker(task_item, index, args, kwargs)
      self.running.append(new_thread)
      new_thread.start()
    else:
      # Run the 'thread' inside the main thread. Don't try to catch any
      # exception.
      task_item.run(*args, **kwargs)
      self.ran.append(task_item.name)
      if self.progress:
        self.progress.update(1, ', '.join(t.item.name for t in self.running))

  class _Worker(threading.Thread):
    """One thread to execute one WorkItem."""
    def __init__(self, item, index, args, kwargs):
      threading.Thread.__init__(self, name=item.name or 'Worker')
      logging.info(item.name)
      self.item = item
      self.index = index
      self.args = args
      self.kwargs = kwargs

    def run(self):
      """Runs in its own thread."""
      logging.debug('running(%s)' % self.item.name)
      work_queue = self.kwargs['work_queue']
      try:
        self.item.run(*self.args, **self.kwargs)
      except Exception:
        # Catch exception location.
        logging.info('Caught exception in thread %s' % self.item.name)
        logging.info(str(sys.exc_info()))
        work_queue.exceptions.put(sys.exc_info())
      logging.info('Task %s done' % self.item.name)

      work_queue.ready_cond.acquire()
      try:
        work_queue.ready_cond.notifyAll()
      finally:
        work_queue.ready_cond.release()
