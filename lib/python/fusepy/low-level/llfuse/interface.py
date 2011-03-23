'''
$Id: interface.py 54 2010-02-22 02:33:10Z nikratio $

Copyright (c) 2010, Nikolaus Rath <Nikolaus@rath.org>
All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

    * Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
    * Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
    * Neither the name of the main author nor the names of other contributors may be used to endorse or promote products derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.


This module defines the interface between the FUSE C and Python API. The actual file system
is implemented as an `Operations` instance whose methods will
be called by the fuse library.

Note that all "string-like" quantities (e.g. file names, extended attribute names & values) are
represented as bytes, since POSIX doesn't require any of them to be valid unicode strings.


Exception Handling
------------------

Since Python exceptions cannot be forwarded to the FUSE kernel module,
the FUSE Python API catches all exceptions that are generated during
request processing.

If the exception is of type `FUSEError`, the appropriate errno is returned
to the kernel module and the exception is discarded.

For any other exceptions, a warning is logged and a generic error signaled
to the kernel module. Then the `handle_exc` method of the `Operations` 
instance is called, so that the file system itself has a chance to react
to the problem (e.g. by marking the file system as needing a check).

The return value and any raised exceptions of `handle_exc` are ignored.

'''

# Since we are using ctype Structures, we often have to
# access attributes that are not defined in __init__
# (since they are defined in _fields_ instead)
#pylint: disable-msg=W0212

# We need globals
#pylint: disable-msg=W0603

from __future__ import division, print_function, absolute_import

# Using .. as libfuse makes PyDev really unhappy.
from . import ctypes_api
libfuse = ctypes_api

from ctypes import c_char_p, sizeof, create_string_buffer, addressof, string_at, POINTER, c_char, cast
from functools import partial
import errno
import logging
import sys


__all__ = [ 'FUSEError', 'ENOATTR', 'ENOTSUP', 'init', 'main', 'close',
            'fuse_version' ]


# These should really be defined in the errno module, but
# unfortunately they are missing
ENOATTR = libfuse.ENOATTR
ENOTSUP = libfuse.ENOTSUP

log = logging.getLogger("fuse")

# Init globals
operations = None
fuse_ops = None
mountpoint = None
session = None
channel = None

class DiscardedRequest(Exception):
    '''Request was interrupted and reply discarded.
    
    '''

    pass

class ReplyError(Exception):
    '''Unable to send reply to fuse kernel module.

    '''

    pass

class FUSEError(Exception):
    '''Wrapped errno value to be returned to the fuse kernel module

    This exception can store only an errno. Request handlers should raise
    to return a specific errno to the fuse kernel module.
    '''

    __slots__ = [ 'errno' ]

    def __init__(self, errno_):
        super(FUSEError, self).__init__()
        self.errno = errno_

    def __str__(self):
        # errno may not have strings for all error codes
        return errno.errorcode.get(self.errno, str(self.errno))



def check_reply_result(result, func, *args):
    '''Check result of a call to a fuse_reply_* foreign function
    
    If `result` is 0, it is assumed that the call succeeded and the function does nothing.
    
    If result is `-errno.ENOENT`, this means that the request has been discarded and `DiscardedRequest`
    is raised.
    
    In all other cases,  `ReplyError` is raised.
    
    (We do not try to call `fuse_reply_err` or any other reply method as well, because the first reply
    function may have already invalidated the `req` object and it seems better to (possibly) let the
    request pend than to crash the server application.)
    '''

    if result == 0:
        return None

    elif result == -errno.ENOENT:
        raise DiscardedRequest()

    elif result > 0:
        raise ReplyError('Foreign function %s returned unexpected value %d'
                         % (func.name, result))
    elif result < 0:
        raise ReplyError('Foreign function %s returned error %s'
                         % (func.name, errno.errorcode.get(-result, str(-result))))


#
# Set return checker for common ctypes calls
#  
reply_functions = [ 'fuse_reply_err', 'fuse_reply_entry',
                   'fuse_reply_create', 'fuse_reply_readlink', 'fuse_reply_open',
                   'fuse_reply_write', 'fuse_reply_attr', 'fuse_reply_buf',
                   'fuse_reply_iov', 'fuse_reply_statfs', 'fuse_reply_xattr',
                   'fuse_reply_lock' ]
for fname in reply_functions:
    getattr(libfuse, fname).errcheck = check_reply_result

    # Name isn't stored by ctypes
    getattr(libfuse, fname).name = fname


def dict_to_entry(attr):
    '''Convert dict to fuse_entry_param'''

    entry = libfuse.fuse_entry_param()

    entry.ino = attr['st_ino']
    entry.generation = attr.pop('generation')
    entry.entry_timeout = attr.pop('entry_timeout')
    entry.attr_timeout = attr.pop('attr_timeout')

    entry.attr = dict_to_stat(attr)

    return entry

def dict_to_stat(attr):
    '''Convert dict to struct stat'''

    stat = libfuse.stat()

    # Determine correct way to store times
    if hasattr(stat, 'st_atim'): # Linux
        get_timespec_key = lambda key: key[:-1]
    elif hasattr(stat, 'st_atimespec'): # FreeBSD
        get_timespec_key = lambda key: key + 'spec'
    else:
        get_timespec_key = False

    # Raises exception if there are any unknown keys
    for (key, val) in attr.iteritems():
        if val is None: # do not set undefined items
            continue
        if get_timespec_key and key in  ('st_atime', 'st_mtime', 'st_ctime'):
            key = get_timespec_key(key)
            spec = libfuse.timespec()
            spec.tv_sec = int(val)
            spec.tv_nsec = int((val - int(val)) * 10 ** 9)
            val = spec
        setattr(stat, key, val)

    return stat


def stat_to_dict(stat):
    '''Convert ``struct stat`` to dict'''

    attr = dict()
    for (name, dummy) in libfuse.stat._fields_:
        if name.startswith('__'):
            continue

        if name in ('st_atim', 'st_mtim', 'st_ctim'):
            key = name + 'e'
            attr[key] = getattr(stat, name).tv_sec + getattr(stat, name).tv_nsec / 10 ** 9
        elif name in ('st_atimespec', 'st_mtimespec', 'st_ctimespec'):
            key = name[:-4]
            attr[key] = getattr(stat, name).tv_sec + getattr(stat, name).tv_nsec / 10 ** 9
        else:
            attr[name] = getattr(stat, name)

    return attr


def op_wrapper(func, req, *args):
    '''Catch all exceptions and call fuse_reply_err instead'''

    try:
        func(req, *args)
    except FUSEError as e:
        log.debug('op_wrapper caught FUSEError, calling fuse_reply_err(%s)',
                  errno.errorcode.get(e.errno, str(e.errno)))
        try:
            libfuse.fuse_reply_err(req, e.errno)
        except DiscardedRequest:
            pass
    except Exception as exc:
        log.exception('FUSE handler raised exception.')

        # Report error to filesystem
        if hasattr(operations, 'handle_exc'):
            try:
                operations.handle_exc(exc)
            except:
                pass

        # Send error reply, unless the error occured when replying
        if not isinstance(exc, ReplyError):
            log.debug('Calling fuse_reply_err(EIO)')
            libfuse.fuse_reply_err(req, errno.EIO)

def fuse_version():
    '''Return version of loaded fuse library'''

    return libfuse.fuse_version()


def init(operations_, mountpoint_, args):
    '''Initialize and mount FUSE file system
            
    `operations_` has to be an instance of the `Operations` class (or another
    class defining the same methods).
    
    `args` has to be a list of strings. Valid options are listed in struct fuse_opt fuse_mount_opts[]
    (mount.c:68) and struct fuse_opt fuse_ll_opts[] (fuse_lowlevel_c:1526).
    '''

    log.debug('Initializing llfuse')

    global operations
    global fuse_ops
    global mountpoint
    global session
    global channel

    # Give operations instance a chance to check and change
    # the FUSE options
    operations_.check_args(args)

    mountpoint = mountpoint_
    operations = operations_
    fuse_ops = libfuse.fuse_lowlevel_ops()
    fuse_args = make_fuse_args(args)

    # Init fuse_ops
    module = globals()
    for (name, prototype) in libfuse.fuse_lowlevel_ops._fields_:
        if hasattr(operations, name):
            method = partial(op_wrapper, module['fuse_' + name])
            setattr(fuse_ops, name, prototype(method))

    log.debug('Calling fuse_mount')
    channel = libfuse.fuse_mount(mountpoint, fuse_args)
    if not channel:
        raise RuntimeError('fuse_mount failed')
    try:
        log.debug('Calling fuse_lowlevel_new')
        session = libfuse.fuse_lowlevel_new(fuse_args, fuse_ops, sizeof(fuse_ops), None)
        if not session:
            raise RuntimeError("fuse_lowlevel_new() failed")
        try:
            log.debug('Calling fuse_set_signal_handlers')
            if libfuse.fuse_set_signal_handlers(session) == -1:
                raise RuntimeError("fuse_set_signal_handlers() failed")
            try:
                log.debug('Calling fuse_session_add_chan')
                libfuse.fuse_session_add_chan(session, channel)
                session = session
                channel = channel
                return

            except:
                log.debug('Calling fuse_remove_signal_handlers')
                libfuse.fuse_remove_signal_handlers(session)
                raise

        except:
            log.debug('Calling fuse_session_destroy')
            libfuse.fuse_session_destroy(session)
            raise
    except:
        log.debug('Calling fuse_unmount')
        libfuse.fuse_unmount(mountpoint, channel)
        raise

def make_fuse_args(args):
    '''Create fuse_args Structure for given mount options'''

    args1 = [ sys.argv[0] ]
    for opt in args:
        args1.append(b'-o')
        args1.append(opt)

    # Init fuse_args struct
    fuse_args = libfuse.fuse_args()
    fuse_args.allocated = 0
    fuse_args.argc = len(args1)
    fuse_args.argv = (POINTER(c_char) * len(args1))(*[cast(c_char_p(x), POINTER(c_char))
                                                      for x in args1])
    return fuse_args

def main(single=False):
    '''Run FUSE main loop'''

    if not session:
        raise RuntimeError('Need to call init() before main()')

    if single:
        log.debug('Calling fuse_session_loop')
        if libfuse.fuse_session_loop(session) != 0:
            raise RuntimeError("fuse_session_loop() failed")
    else:
        log.debug('Calling fuse_session_loop_mt')
        if libfuse.fuse_session_loop_mt(session) != 0:
            raise RuntimeError("fuse_session_loop_mt() failed")

def close():
    '''Unmount file system and clean up'''

    global operations
    global fuse_ops
    global mountpoint
    global session
    global channel

    log.debug('Calling fuse_session_remove_chan')
    libfuse.fuse_session_remove_chan(channel)
    log.debug('Calling fuse_remove_signal_handlers')
    libfuse.fuse_remove_signal_handlers(session)
    log.debug('Calling fuse_session_destroy')
    libfuse.fuse_session_destroy(session)
    log.debug('Calling fuse_unmount')
    libfuse.fuse_unmount(mountpoint, channel)

    operations = None
    fuse_ops = None
    mountpoint = None
    session = None
    channel = None


def fuse_lookup(req, parent_inode, name):
    '''Look up a directory entry by name and get its attributes'''

    log.debug('Handling lookup(%d, %s)', parent_inode, string_at(name))

    attr = operations.lookup(parent_inode, string_at(name))
    entry = dict_to_entry(attr)

    log.debug('Calling fuse_reply_entry')
    try:
        libfuse.fuse_reply_entry(req, entry)
    except DiscardedRequest:
        pass

def fuse_init(userdata_p, conn_info_p):
    '''Initialize Operations'''
    operations.init()

def fuse_destroy(userdata_p):
    '''Cleanup Operations'''
    operations.destroy()

def fuse_getattr(req, ino, _unused):
    '''Get attributes for `ino`'''

    log.debug('Handling getattr(%d)', ino)

    attr = operations.getattr(ino)

    attr_timeout = attr.pop('attr_timeout')
    stat = dict_to_stat(attr)

    log.debug('Calling fuse_reply_attr')
    try:
        libfuse.fuse_reply_attr(req, stat, attr_timeout)
    except DiscardedRequest:
        pass

def fuse_access(req, ino, mask):
    '''Check if calling user has `mask` rights for `ino`'''

    log.debug('Handling access(%d, %o)', ino, mask)

    # Get UID
    ctx = libfuse.fuse_req_ctx(req).contents

    # Define a function that returns a list of the GIDs
    def get_gids():
        # Get GID list if FUSE supports it
        # Weird syntax to prevent PyDev from complaining
        getgroups = getattr(libfuse, "fuse_req_getgroups")
        gid_t = getattr(libfuse, 'gid_t')
        no = 10
        buf = (gid_t * no)(range(no))
        ret = getgroups(req, no, buf)
        if ret > no:
            no = ret
            buf = (gid_t * no)(range(no))
            ret = getgroups(req, no, buf)

        return [ buf[i].value for i in range(ret) ]

    ret = operations.access(ino, mask, ctx, get_gids)

    log.debug('Calling fuse_reply_err')
    try:
        if ret:
            libfuse.fuse_reply_err(req, 0)
        else:
            libfuse.fuse_reply_err(req, errno.EPERM)
    except DiscardedRequest:
        pass


def fuse_create(req, ino_parent, name, mode, fi):
    '''Create and open a file'''

    log.debug('Handling create(%d, %s, %o)', ino_parent, string_at(name), mode)
    (fh, attr) = operations.create(ino_parent, string_at(name), mode,
                                   libfuse.fuse_req_ctx(req).contents)
    fi.contents.fh = fh
    fi.contents.keep_cache = 1
    entry = dict_to_entry(attr)

    log.debug('Calling fuse_reply_create')
    try:
        libfuse.fuse_reply_create(req, entry, fi)
    except DiscardedRequest:
        operations.release(fh)


def fuse_flush(req, ino, fi):
    '''Handle close() system call
    
    May be called multiple times for the same open file.
    '''

    log.debug('Handling flush(%d)', fi.contents.fh)
    operations.flush(fi.contents.fh)
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass


def fuse_fsync(req, ino, datasync, fi):
    '''Flush buffers for `ino`
    
    If the datasync parameter is non-zero, then only the user data
    is flushed (and not the meta data).
    '''

    log.debug('Handling fsync(%d, %s)', fi.contents.fh, datasync != 0)
    operations.fsync(fi.contents.fh, datasync != 0)
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass


def fuse_fsyncdir(req, ino, datasync, fi):
    '''Synchronize directory contents
    
    If the datasync parameter is non-zero, then only the directory contents
    are flushed (and not the meta data about the directory itself).
    '''

    log.debug('Handling fsyncdir(%d, %s)', fi.contents.fh, datasync != 0)
    operations.fsyncdir(fi.contents.fh, datasync != 0)
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass


def fuse_getxattr(req, ino, name, size):
    '''Get an extended attribute.
    '''

    log.debug('Handling getxattr(%d, %r, %d)', ino, string_at(name), size)
    val = operations.getxattr(ino, string_at(name))
    if not isinstance(val, bytes):
        raise TypeError("getxattr return value must be of type bytes")

    try:
        if size == 0:
            log.debug('Calling fuse_reply_xattr')
            libfuse.fuse_reply_xattr(req, len(val))
        elif size >= len(val):
            log.debug('Calling fuse_reply_buf')
            libfuse.fuse_reply_buf(req, val, len(val))
        else:
            raise FUSEError(errno.ERANGE)
    except DiscardedRequest:
        pass


def fuse_link(req, ino, new_parent_ino, new_name):
    '''Create a hard link'''

    log.debug('Handling fuse_link(%d, %d, %s)', ino, new_parent_ino, string_at(new_name))
    attr = operations.link(ino, new_parent_ino, string_at(new_name))
    entry = dict_to_entry(attr)

    log.debug('Calling fuse_reply_entry')
    try:
        libfuse.fuse_reply_entry(req, entry)
    except DiscardedRequest:
        pass

def fuse_listxattr(req, inode, size):
    '''List extended attributes for `inode`'''

    log.debug('Handling listxattr(%d)', inode)
    names = operations.listxattr(inode)

    if not all([ isinstance(name, bytes) for name in names]):
        raise TypeError("listxattr return value must be list of bytes")

    # Size of the \0 separated buffer 
    act_size = (len(names) - 1) + sum([ len(name) for name in names ])

    if size == 0:
        try:
            log.debug('Calling fuse_reply_xattr')
            libfuse.fuse_reply_xattr(req, len(names))
        except DiscardedRequest:
            pass

    elif act_size > size:
        raise FUSEError(errno.ERANGE)

    else:
        try:
            log.debug('Calling fuse_reply_buf')
            libfuse.fuse_reply_buf(req, b'\0'.join(names), act_size)
        except DiscardedRequest:
            pass


def fuse_mkdir(req, inode_parent, name, mode):
    '''Create directory'''

    log.debug('Handling mkdir(%d, %s, %o)', inode_parent, string_at(name), mode)
    attr = operations.mkdir(inode_parent, string_at(name), mode,
                            libfuse.fuse_req_ctx(req).contents)
    entry = dict_to_entry(attr)

    log.debug('Calling fuse_reply_entry')
    try:
        libfuse.fuse_reply_entry(req, entry)
    except DiscardedRequest:
        pass

def fuse_mknod(req, inode_parent, name, mode, rdev):
    '''Create (possibly special) file'''

    log.debug('Handling mknod(%d, %s, %o, %d)', inode_parent, string_at(name),
              mode, rdev)
    attr = operations.mknod(inode_parent, string_at(name), mode, rdev,
                            libfuse.fuse_req_ctx(req).contents)
    entry = dict_to_entry(attr)

    log.debug('Calling fuse_reply_entry')
    try:
        libfuse.fuse_reply_entry(req, entry)
    except DiscardedRequest:
        pass

def fuse_open(req, inode, fi):
    '''Open a file'''
    log.debug('Handling open(%d, %d)', inode, fi.contents.flags)
    fi.contents.fh = operations.open(inode, fi.contents.flags)
    fi.contents.keep_cache = 1

    log.debug('Calling fuse_reply_open')
    try:
        libfuse.fuse_reply_open(req, fi)
    except DiscardedRequest:
        operations.release(inode, fi.contents.fh)

def fuse_opendir(req, inode, fi):
    '''Open a directory'''

    log.debug('Handling opendir(%d)', inode)
    fi.contents.fh = operations.opendir(inode)

    log.debug('Calling fuse_reply_open')
    try:
        libfuse.fuse_reply_open(req, fi)
    except DiscardedRequest:
        operations.releasedir(fi.contents.fh)


def fuse_read(req, ino, size, off, fi):
    '''Read data from file'''

    log.debug('Handling read(ino=%d, off=%d, size=%d)', fi.contents.fh, off, size)
    data = operations.read(fi.contents.fh, off, size)

    if not isinstance(data, bytes):
        raise TypeError("read() must return bytes")

    if len(data) > size:
        raise ValueError('read() must not return more than `size` bytes')

    log.debug('Calling fuse_reply_buf')
    try:
        libfuse.fuse_reply_buf(req, data, len(data))
    except DiscardedRequest:
        pass


def fuse_readlink(req, inode):
    '''Read target of symbolic link'''

    log.debug('Handling readlink(%d)', inode)
    target = operations.readlink(inode)
    log.debug('Calling fuse_reply_readlink')
    try:
        libfuse.fuse_reply_readlink(req, target)
    except DiscardedRequest:
        pass


def fuse_readdir(req, ino, bufsize, off, fi):
    '''Read directory entries'''

    log.debug('Handling readdir(%d, %d, %d, %d)', ino, bufsize, off, fi.contents.fh)

    # Collect as much entries as we can return
    entries = list()
    size = 0
    for (name, attr) in operations.readdir(fi.contents.fh, off):
        if not isinstance(name, bytes):
            raise TypeError("readdir() must return entry names as bytes")

        stat = dict_to_stat(attr)

        entry_size = libfuse.fuse_add_direntry(req, None, 0, name, stat, 0)
        if size + entry_size > bufsize:
            break

        entries.append((name, stat))
        size += entry_size

    log.debug('Gathered %d entries, total size %d', len(entries), size)

    # If there are no entries left, return empty buffer
    if not entries:
        try:
            log.debug('Calling fuse_reply_buf')
            libfuse.fuse_reply_buf(req, None, 0)
        except DiscardedRequest:
            pass
        return

    # Create and fill buffer
    log.debug('Adding entries to buffer')
    buf = create_string_buffer(size)
    next_ = off
    addr_off = 0
    for (name, stat) in entries:
        next_ += 1
        addr_off += libfuse.fuse_add_direntry(req, cast(addressof(buf) + addr_off, POINTER(c_char)),
                                              bufsize, name, stat, next_)

    # Return buffer
    log.debug('Calling fuse_reply_buf')
    try:
        libfuse.fuse_reply_buf(req, buf, size)
    except DiscardedRequest:
        pass


def fuse_release(req, inode, fi):
    '''Release open file'''

    log.debug('Handling release(%d)', fi.contents.fh)
    operations.release(fi.contents.fh)
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass

def fuse_releasedir(req, inode, fi):
    '''Release open directory'''

    log.debug('Handling releasedir(%d)', fi.contents.fh)
    operations.releasedir(fi.contents.fh)
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass

def fuse_removexattr(req, inode, name):
    '''Remove extended attribute'''

    log.debug('Handling removexattr(%d, %s)', inode, string_at(name))
    operations.removexattr(inode, string_at(name))
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass

def fuse_rename(req, parent_inode_old, name_old, parent_inode_new, name_new):
    '''Rename a directory entry'''

    log.debug('Handling rename(%d, %r, %d, %r)', parent_inode_old, string_at(name_old),
              parent_inode_new, string_at(name_new))
    operations.rename(parent_inode_old, string_at(name_old), parent_inode_new,
                      string_at(name_new))
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass

def fuse_rmdir(req, inode_parent, name):
    '''Remove a directory'''

    log.debug('Handling rmdir(%d, %r)', inode_parent, string_at(name))
    operations.rmdir(inode_parent, string_at(name))
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass

def fuse_setattr(req, inode, stat, to_set, fi):
    '''Change directory entry attributes'''

    log.debug('Handling fuse_setattr(%d)', inode)

    # Note: We can't check if we know all possible flags,
    # because the part of to_set that is not "covered"
    # by flags seems to be undefined rather than zero.

    attr_all = stat_to_dict(stat.contents)
    attr = dict()

    if (to_set & libfuse.FUSE_SET_ATTR_MTIME) != 0:
        attr['st_mtime'] = attr_all['st_mtime']

    if (to_set & libfuse.FUSE_SET_ATTR_ATIME) != 0:
        attr['st_atime'] = attr_all['st_atime']

    if (to_set & libfuse.FUSE_SET_ATTR_MODE) != 0:
        attr['st_mode'] = attr_all['st_mode']

    if (to_set & libfuse.FUSE_SET_ATTR_UID) != 0:
        attr['st_uid'] = attr_all['st_uid']

    if (to_set & libfuse.FUSE_SET_ATTR_GID) != 0:
        attr['st_gid'] = attr_all['st_gid']

    if (to_set & libfuse.FUSE_SET_ATTR_SIZE) != 0:
        attr['st_size'] = attr_all['st_size']

    attr = operations.setattr(inode, attr)

    attr_timeout = attr.pop('attr_timeout')
    stat = dict_to_stat(attr)

    log.debug('Calling fuse_reply_attr')
    try:
        libfuse.fuse_reply_attr(req, stat, attr_timeout)
    except DiscardedRequest:
        pass

def fuse_setxattr(req, inode, name, val, size, flags):
    '''Set an extended attribute'''

    log.debug('Handling setxattr(%d, %r, %r, %d)', inode, string_at(name),
              string_at(val, size), flags)

    # Make sure we know all the flags
    if (flags & ~(libfuse.XATTR_CREATE | libfuse.XATTR_REPLACE)) != 0:
        raise ValueError('unknown flag')

    if (flags & libfuse.XATTR_CREATE) != 0:
        try:
            operations.getxattr(inode, string_at(name))
        except FUSEError as e:
            if e.errno == ENOATTR:
                pass
            raise
        else:
            raise FUSEError(errno.EEXIST)
    elif (flags & libfuse.XATTR_REPLACE) != 0:
        # Exception can be passed on if the attribute does not exist
        operations.getxattr(inode, string_at(name))

    operations.setxattr(inode, string_at(name), string_at(val, size))

    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass

def fuse_statfs(req, inode):
    '''Return filesystem statistics'''

    log.debug('Handling statfs(%d)', inode)
    attr = operations.statfs()
    statfs = libfuse.statvfs()

    for (key, val) in attr.iteritems():
        setattr(statfs, key, val)

    log.debug('Calling fuse_reply_statfs')
    try:
        libfuse.fuse_reply_statfs(req, statfs)
    except DiscardedRequest:
        pass

def fuse_symlink(req, target, parent_inode, name):
    '''Create a symbolic link'''

    log.debug('Handling symlink(%d, %r, %r)', parent_inode, string_at(name), string_at(target))
    attr = operations.symlink(parent_inode, string_at(name), string_at(target),
                              libfuse.fuse_req_ctx(req).contents)
    entry = dict_to_entry(attr)

    log.debug('Calling fuse_reply_entry')
    try:
        libfuse.fuse_reply_entry(req, entry)
    except DiscardedRequest:
        pass


def fuse_unlink(req, parent_inode, name):
    '''Delete a file'''

    log.debug('Handling unlink(%d, %r)', parent_inode, string_at(name))
    operations.unlink(parent_inode, string_at(name))
    log.debug('Calling fuse_reply_err(0)')
    try:
        libfuse.fuse_reply_err(req, 0)
    except DiscardedRequest:
        pass

def fuse_write(req, inode, buf, size, off, fi):
    '''Write into an open file handle'''

    log.debug('Handling write(fh=%d, off=%d, size=%d)', fi.contents.fh, off, size)
    written = operations.write(fi.contents.fh, off, string_at(buf, size))

    log.debug('Calling fuse_reply_write')
    try:
        libfuse.fuse_reply_write(req, written)
    except DiscardedRequest:
        pass
