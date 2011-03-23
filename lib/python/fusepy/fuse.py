# Copyright (c) 2008 Giorgos Verigakis <verigak@gmail.com>
# 
# Permission to use, copy, modify, and distribute this software for any
# purpose with or without fee is hereby granted, provided that the above
# copyright notice and this permission notice appear in all copies.
# 
# THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
# WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
# MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
# ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
# WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
# ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
# OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

from __future__ import division

from ctypes import *
from ctypes.util import find_library
from errno import *
from functools import partial
from os import strerror
from platform import machine, system
from stat import S_IFDIR
from traceback import print_exc


class c_timespec(Structure):
    _fields_ = [('tv_sec', c_long), ('tv_nsec', c_long)]

class c_utimbuf(Structure):
    _fields_ = [('actime', c_timespec), ('modtime', c_timespec)]

class c_stat(Structure):
    pass    # Platform dependent

_system = system()
if _system in ('Darwin', 'FreeBSD'):
    _libiconv = CDLL(find_library("iconv"), RTLD_GLOBAL)     # libfuse dependency
    ENOTSUP = 45
    c_dev_t = c_int32
    c_fsblkcnt_t = c_ulong
    c_fsfilcnt_t = c_ulong
    c_gid_t = c_uint32
    c_mode_t = c_uint16
    c_off_t = c_int64
    c_pid_t = c_int32
    c_uid_t = c_uint32
    setxattr_t = CFUNCTYPE(c_int, c_char_p, c_char_p, POINTER(c_byte),
        c_size_t, c_int, c_uint32)
    getxattr_t = CFUNCTYPE(c_int, c_char_p, c_char_p, POINTER(c_byte),
        c_size_t, c_uint32)
    c_stat._fields_ = [
        ('st_dev', c_dev_t),
        ('st_ino', c_uint32),
        ('st_mode', c_mode_t),
        ('st_nlink', c_uint16),
        ('st_uid', c_uid_t),
        ('st_gid', c_gid_t),
        ('st_rdev', c_dev_t),
        ('st_atimespec', c_timespec),
        ('st_mtimespec', c_timespec),
        ('st_ctimespec', c_timespec),
        ('st_size', c_off_t),
        ('st_blocks', c_int64),
        ('st_blksize', c_int32)]
elif _system == 'Linux':
    ENOTSUP = 95
    c_dev_t = c_ulonglong
    c_fsblkcnt_t = c_ulonglong
    c_fsfilcnt_t = c_ulonglong
    c_gid_t = c_uint
    c_mode_t = c_uint
    c_off_t = c_longlong
    c_pid_t = c_int
    c_uid_t = c_uint
    setxattr_t = CFUNCTYPE(c_int, c_char_p, c_char_p, POINTER(c_byte), c_size_t, c_int)
    getxattr_t = CFUNCTYPE(c_int, c_char_p, c_char_p, POINTER(c_byte), c_size_t)
    
    _machine = machine()
    if _machine == 'x86_64':
        c_stat._fields_ = [
            ('st_dev', c_dev_t),
            ('st_ino', c_ulong),
            ('st_nlink', c_ulong),
            ('st_mode', c_mode_t),
            ('st_uid', c_uid_t),
            ('st_gid', c_gid_t),
            ('__pad0', c_int),
            ('st_rdev', c_dev_t),
            ('st_size', c_off_t),
            ('st_blksize', c_long),
            ('st_blocks', c_long),
            ('st_atimespec', c_timespec),
            ('st_mtimespec', c_timespec),
            ('st_ctimespec', c_timespec)]
    elif _machine == 'ppc':
        c_stat._fields_ = [
            ('st_dev', c_dev_t),
            ('st_ino', c_ulonglong),
            ('st_mode', c_mode_t),
            ('st_nlink', c_uint),
            ('st_uid', c_uid_t),
            ('st_gid', c_gid_t),
            ('st_rdev', c_dev_t),
            ('__pad2', c_ushort),
            ('st_size', c_off_t),
            ('st_blksize', c_long),
            ('st_blocks', c_longlong),
            ('st_atimespec', c_timespec),
            ('st_mtimespec', c_timespec),
            ('st_ctimespec', c_timespec)]
    else:
        # i686, use as fallback for everything else
        c_stat._fields_ = [
            ('st_dev', c_dev_t),
            ('__pad1', c_ushort),
            ('__st_ino', c_ulong),
            ('st_mode', c_mode_t),
            ('st_nlink', c_uint),
            ('st_uid', c_uid_t),
            ('st_gid', c_gid_t),
            ('st_rdev', c_dev_t),
            ('__pad2', c_ushort),
            ('st_size', c_off_t),
            ('st_blksize', c_long),
            ('st_blocks', c_longlong),
            ('st_atimespec', c_timespec),
            ('st_mtimespec', c_timespec),
            ('st_ctimespec', c_timespec),
            ('st_ino', c_ulonglong)]
else:
    raise NotImplementedError('%s is not supported.' % _system)


class c_statvfs(Structure):
    _fields_ = [
        ('f_bsize', c_ulong),
        ('f_frsize', c_ulong),
        ('f_blocks', c_fsblkcnt_t),
        ('f_bfree', c_fsblkcnt_t),
        ('f_bavail', c_fsblkcnt_t),
        ('f_files', c_fsfilcnt_t),
        ('f_ffree', c_fsfilcnt_t),
        ('f_favail', c_fsfilcnt_t)]

if _system == 'FreeBSD':
    c_fsblkcnt_t = c_uint64
    c_fsfilcnt_t = c_uint64
    setxattr_t = CFUNCTYPE(c_int, c_char_p, c_char_p, POINTER(c_byte), c_size_t, c_int)
    getxattr_t = CFUNCTYPE(c_int, c_char_p, c_char_p, POINTER(c_byte), c_size_t)
    class c_statvfs(Structure):
        _fields_ = [
            ('f_bavail', c_fsblkcnt_t),
            ('f_bfree', c_fsblkcnt_t),
            ('f_blocks', c_fsblkcnt_t),
            ('f_favail', c_fsfilcnt_t),
            ('f_ffree', c_fsfilcnt_t),
            ('f_files', c_fsfilcnt_t),
            ('f_bsize', c_ulong),
            ('f_flag', c_ulong),
            ('f_frsize', c_ulong)]

class fuse_file_info(Structure):
    _fields_ = [
        ('flags', c_int),
        ('fh_old', c_ulong),
        ('writepage', c_int),
        ('direct_io', c_uint, 1),
        ('keep_cache', c_uint, 1),
        ('flush', c_uint, 1),
        ('padding', c_uint, 29),
        ('fh', c_uint64),
        ('lock_owner', c_uint64)]

class fuse_context(Structure):
    _fields_ = [
        ('fuse', c_voidp),
        ('uid', c_uid_t),
        ('gid', c_gid_t),
        ('pid', c_pid_t),
        ('private_data', c_voidp)]

class fuse_operations(Structure):
    _fields_ = [
        ('getattr', CFUNCTYPE(c_int, c_char_p, POINTER(c_stat))),
        ('readlink', CFUNCTYPE(c_int, c_char_p, POINTER(c_byte), c_size_t)),
        ('getdir', c_voidp),    # Deprecated, use readdir
        ('mknod', CFUNCTYPE(c_int, c_char_p, c_mode_t, c_dev_t)),
        ('mkdir', CFUNCTYPE(c_int, c_char_p, c_mode_t)),
        ('unlink', CFUNCTYPE(c_int, c_char_p)),
        ('rmdir', CFUNCTYPE(c_int, c_char_p)),
        ('symlink', CFUNCTYPE(c_int, c_char_p, c_char_p)),
        ('rename', CFUNCTYPE(c_int, c_char_p, c_char_p)),
        ('link', CFUNCTYPE(c_int, c_char_p, c_char_p)),
        ('chmod', CFUNCTYPE(c_int, c_char_p, c_mode_t)),
        ('chown', CFUNCTYPE(c_int, c_char_p, c_uid_t, c_gid_t)),
        ('truncate', CFUNCTYPE(c_int, c_char_p, c_off_t)),
        ('utime', c_voidp),     # Deprecated, use utimens
        ('open', CFUNCTYPE(c_int, c_char_p, POINTER(fuse_file_info))),
        ('read', CFUNCTYPE(c_int, c_char_p, POINTER(c_byte), c_size_t, c_off_t,
            POINTER(fuse_file_info))),
        ('write', CFUNCTYPE(c_int, c_char_p, POINTER(c_byte), c_size_t, c_off_t,
            POINTER(fuse_file_info))),
        ('statfs', CFUNCTYPE(c_int, c_char_p, POINTER(c_statvfs))),
        ('flush', CFUNCTYPE(c_int, c_char_p, POINTER(fuse_file_info))),
        ('release', CFUNCTYPE(c_int, c_char_p, POINTER(fuse_file_info))),
        ('fsync', CFUNCTYPE(c_int, c_char_p, c_int, POINTER(fuse_file_info))),
        ('setxattr', setxattr_t),
        ('getxattr', getxattr_t),
        ('listxattr', CFUNCTYPE(c_int, c_char_p, POINTER(c_byte), c_size_t)),
        ('removexattr', CFUNCTYPE(c_int, c_char_p, c_char_p)),
        ('opendir', CFUNCTYPE(c_int, c_char_p, POINTER(fuse_file_info))),
        ('readdir', CFUNCTYPE(c_int, c_char_p, c_voidp, CFUNCTYPE(c_int, c_voidp,
            c_char_p, POINTER(c_stat), c_off_t), c_off_t, POINTER(fuse_file_info))),
        ('releasedir', CFUNCTYPE(c_int, c_char_p, POINTER(fuse_file_info))),
        ('fsyncdir', CFUNCTYPE(c_int, c_char_p, c_int, POINTER(fuse_file_info))),
        ('init', CFUNCTYPE(c_voidp, c_voidp)),
        ('destroy', CFUNCTYPE(c_voidp, c_voidp)),
        ('access', CFUNCTYPE(c_int, c_char_p, c_int)),
        ('create', CFUNCTYPE(c_int, c_char_p, c_mode_t, POINTER(fuse_file_info))),
        ('ftruncate', CFUNCTYPE(c_int, c_char_p, c_off_t, POINTER(fuse_file_info))),
        ('fgetattr', CFUNCTYPE(c_int, c_char_p, POINTER(c_stat),
            POINTER(fuse_file_info))),
        ('lock', CFUNCTYPE(c_int, c_char_p, POINTER(fuse_file_info), c_int, c_voidp)),
        ('utimens', CFUNCTYPE(c_int, c_char_p, POINTER(c_utimbuf))),
        ('bmap', CFUNCTYPE(c_int, c_char_p, c_size_t, POINTER(c_ulonglong)))]


def time_of_timespec(ts):
    return ts.tv_sec + ts.tv_nsec / 10 ** 9

def set_st_attrs(st, attrs):
    for key, val in attrs.items():
        if key in ('st_atime', 'st_mtime', 'st_ctime'):
            timespec = getattr(st, key + 'spec')
            timespec.tv_sec = int(val)
            timespec.tv_nsec = int((val - timespec.tv_sec) * 10 ** 9)
        elif hasattr(st, key):
            setattr(st, key, val)


_libfuse_path = find_library('fuse')
if not _libfuse_path:
    raise EnvironmentError('Unable to find libfuse')
_libfuse = CDLL(_libfuse_path)
_libfuse.fuse_get_context.restype = POINTER(fuse_context)


def fuse_get_context():
    """Returns a (uid, gid, pid) tuple"""
    ctxp = _libfuse.fuse_get_context()
    ctx = ctxp.contents
    return ctx.uid, ctx.gid, ctx.pid


class FuseOSError(OSError):
    def __init__(self, errno):
        super(FuseOSError, self).__init__(errno, strerror(errno))


class FUSE(object):
    """This class is the lower level interface and should not be subclassed
       under normal use. Its methods are called by fuse.
       Assumes API version 2.6 or later."""
    
    def __init__(self, operations, mountpoint, raw_fi=False, **kwargs):
        """Setting raw_fi to True will cause FUSE to pass the fuse_file_info
           class as is to Operations, instead of just the fh field.
           This gives you access to direct_io, keep_cache, etc."""
        
        self.operations = operations
        self.raw_fi = raw_fi
        args = ['fuse']
        if kwargs.pop('foreground', False):
            args.append('-f')
        if kwargs.pop('debug', False):
            args.append('-d')
        if kwargs.pop('nothreads', False):
            args.append('-s')
        kwargs.setdefault('fsname', operations.__class__.__name__)
        args.append('-o')
        args.append(','.join(key if val == True else '%s=%s' % (key, val)
            for key, val in kwargs.items()))
        args.append(mountpoint)
        argv = (c_char_p * len(args))(*args)
        
        fuse_ops = fuse_operations()
        for name, prototype in fuse_operations._fields_:
            if prototype != c_voidp and getattr(operations, name, None):
                op = partial(self._wrapper_, getattr(self, name))
                setattr(fuse_ops, name, prototype(op))
        err = _libfuse.fuse_main_real(len(args), argv, pointer(fuse_ops),
            sizeof(fuse_ops), None)            
        del self.operations     # Invoke the destructor
        if err:
            raise RuntimeError(err)
    
    def _wrapper_(self, func, *args, **kwargs):
        """Decorator for the methods that follow"""
        try:
            return func(*args, **kwargs) or 0
        except OSError, e:
            return -(e.errno or EFAULT)
        except:
            print_exc()
            return -EFAULT
    
    def getattr(self, path, buf):
        return self.fgetattr(path, buf, None)
    
    def readlink(self, path, buf, bufsize):
        ret = self.operations('readlink', path)
        data = create_string_buffer(ret[:bufsize - 1])
        memmove(buf, data, len(data))
        return 0
    
    def mknod(self, path, mode, dev):
        return self.operations('mknod', path, mode, dev)
    
    def mkdir(self, path, mode):
        return self.operations('mkdir', path, mode)
    
    def unlink(self, path):
        return self.operations('unlink', path)
    
    def rmdir(self, path):
        return self.operations('rmdir', path)
    
    def symlink(self, source, target):
        return self.operations('symlink', target, source)
    
    def rename(self, old, new):
        return self.operations('rename', old, new)
    
    def link(self, source, target):
        return self.operations('link', target, source)
    
    def chmod(self, path, mode):
        return self.operations('chmod', path, mode)
    
    def chown(self, path, uid, gid):
        # Check if any of the arguments is a -1 that has overflowed
        if c_uid_t(uid + 1).value == 0:
            uid = -1
        if c_gid_t(gid + 1).value == 0:
            gid = -1
        return self.operations('chown', path, uid, gid)
    
    def truncate(self, path, length):
        return self.operations('truncate', path, length)
    
    def open(self, path, fip):
        fi = fip.contents
        if self.raw_fi:
            return self.operations('open', path, fi)
        else:
            fi.fh = self.operations('open', path, fi.flags)
            return 0
    
    def read(self, path, buf, size, offset, fip):
        fh = fip.contents if self.raw_fi else fip.contents.fh
        ret = self.operations('read', path, size, offset, fh)
        if not ret:
            return 0
        data = create_string_buffer(ret[:size], size)
        memmove(buf, data, size)
        return size
    
    def write(self, path, buf, size, offset, fip):
        data = string_at(buf, size)
        fh = fip.contents if self.raw_fi else fip.contents.fh
        return self.operations('write', path, data, offset, fh)
    
    def statfs(self, path, buf):
        stv = buf.contents
        attrs = self.operations('statfs', path)
        for key, val in attrs.items():
            if hasattr(stv, key):
                setattr(stv, key, val)
        return 0
    
    def flush(self, path, fip):
        fh = fip.contents if self.raw_fi else fip.contents.fh
        return self.operations('flush', path, fh)
    
    def release(self, path, fip):
        fh = fip.contents if self.raw_fi else fip.contents.fh
        return self.operations('release', path, fh)
    
    def fsync(self, path, datasync, fip):
        fh = fip.contents if self.raw_fi else fip.contents.fh
        return self.operations('fsync', path, datasync, fh)
    
    def setxattr(self, path, name, value, size, options, *args):
        data = string_at(value, size)
        return self.operations('setxattr', path, name, data, options, *args)
    
    def getxattr(self, path, name, value, size, *args):
        ret = self.operations('getxattr', path, name, *args)
        retsize = len(ret)
        buf = create_string_buffer(ret, retsize)    # Does not add trailing 0
        if bool(value):
            if retsize > size:
                return -ERANGE
            memmove(value, buf, retsize)
        return retsize
    
    def listxattr(self, path, namebuf, size):
        ret = self.operations('listxattr', path)
        buf = create_string_buffer('\x00'.join(ret)) if ret else ''
        bufsize = len(buf)
        if bool(namebuf):
            if bufsize > size:
                return -ERANGE
            memmove(namebuf, buf, bufsize)
        return bufsize
    
    def removexattr(self, path, name):
        return self.operations('removexattr', path, name)
    
    def opendir(self, path, fip):
        # Ignore raw_fi
        fip.contents.fh = self.operations('opendir', path)
        return 0
    
    def readdir(self, path, buf, filler, offset, fip):
        # Ignore raw_fi
        for item in self.operations('readdir', path, fip.contents.fh):
            if isinstance(item, str):
                name, st, offset = item, None, 0
            else:
                name, attrs, offset = item
                if attrs:
                    st = c_stat()
                    set_st_attrs(st, attrs)
                else:
                    st = None
            if filler(buf, name, st, offset) != 0:
                break
        return 0
    
    def releasedir(self, path, fip):
        # Ignore raw_fi
        return self.operations('releasedir', path, fip.contents.fh)
    
    def fsyncdir(self, path, datasync, fip):
        # Ignore raw_fi
        return self.operations('fsyncdir', path, datasync, fip.contents.fh)
    
    def init(self, conn):
        return self.operations('init', '/')
    
    def destroy(self, private_data):
        return self.operations('destroy', '/')
    
    def access(self, path, amode):
        return self.operations('access', path, amode)
    
    def create(self, path, mode, fip):
        fi = fip.contents
        if self.raw_fi:
            return self.operations('create', path, mode, fi)
        else:
            fi.fh = self.operations('create', path, mode)
            return 0
    
    def ftruncate(self, path, length, fip):
        fh = fip.contents if self.raw_fi else fip.contents.fh
        return self.operations('truncate', path, length, fh)
    
    def fgetattr(self, path, buf, fip):
        memset(buf, 0, sizeof(c_stat))
        st = buf.contents
        fh = fip and (fip.contents if self.raw_fi else fip.contents.fh)
        attrs = self.operations('getattr', path, fh)
        set_st_attrs(st, attrs)
        return 0
    
    def lock(self, path, fip, cmd, lock):
        fh = fip.contents if self.raw_fi else fip.contents.fh
        return self.operations('lock', path, fh, cmd, lock)
    
    def utimens(self, path, buf):
        if buf:
            atime = time_of_timespec(buf.contents.actime)
            mtime = time_of_timespec(buf.contents.modtime)
            times = (atime, mtime)
        else:
            times = None
        return self.operations('utimens', path, times)
    
    def bmap(self, path, blocksize, idx):
        return self.operations('bmap', path, blocksize, idx)


class Operations(object):
    """This class should be subclassed and passed as an argument to FUSE on
       initialization. All operations should raise a FuseOSError exception
       on error.
       
       When in doubt of what an operation should do, check the FUSE header
       file or the corresponding system call man page."""
    
    def __call__(self, op, *args):
        if not hasattr(self, op):
            raise FuseOSError(EFAULT)
        return getattr(self, op)(*args)
        
    def access(self, path, amode):
        return 0
    
    bmap = None
    
    def chmod(self, path, mode):
        raise FuseOSError(EROFS)
    
    def chown(self, path, uid, gid):
        raise FuseOSError(EROFS)
    
    def create(self, path, mode, fi=None):
        """When raw_fi is False (default case), fi is None and create should
           return a numerical file handle.
           When raw_fi is True the file handle should be set directly by create
           and return 0."""
        raise FuseOSError(EROFS)
    
    def destroy(self, path):
        """Called on filesystem destruction. Path is always /"""
        pass
    
    def flush(self, path, fh):
        return 0
    
    def fsync(self, path, datasync, fh):
        return 0
    
    def fsyncdir(self, path, datasync, fh):
        return 0
    
    def getattr(self, path, fh=None):
        """Returns a dictionary with keys identical to the stat C structure
           of stat(2).
           st_atime, st_mtime and st_ctime should be floats.
           NOTE: There is an incombatibility between Linux and Mac OS X concerning
           st_nlink of directories. Mac OS X counts all files inside the directory,
           while Linux counts only the subdirectories."""
        
        if path != '/':
            raise FuseOSError(ENOENT)
        return dict(st_mode=(S_IFDIR | 0755), st_nlink=2)
    
    def getxattr(self, path, name, position=0):
        raise FuseOSError(ENOTSUP)
    
    def init(self, path):
        """Called on filesystem initialization. Path is always /
           Use it instead of __init__ if you start threads on initialization."""
        pass
    
    def link(self, target, source):
        raise FuseOSError(EROFS)
    
    def listxattr(self, path):
        return []
        
    lock = None
    
    def mkdir(self, path, mode):
        raise FuseOSError(EROFS)
    
    def mknod(self, path, mode, dev):
        raise FuseOSError(EROFS)
    
    def open(self, path, flags):
        """When raw_fi is False (default case), open should return a numerical
           file handle.
           When raw_fi is True the signature of open becomes:
               open(self, path, fi)
           and the file handle should be set directly."""
        return 0
    
    def opendir(self, path):
        """Returns a numerical file handle."""
        return 0
    
    def read(self, path, size, offset, fh):
        """Returns a string containing the data requested."""
        raise FuseOSError(EIO)
    
    def readdir(self, path, fh):
        """Can return either a list of names, or a list of (name, attrs, offset)
           tuples. attrs is a dict as in getattr."""
        return ['.', '..']
    
    def readlink(self, path):
        raise FuseOSError(ENOENT)
    
    def release(self, path, fh):
        return 0
    
    def releasedir(self, path, fh):
        return 0
    
    def removexattr(self, path, name):
        raise FuseOSError(ENOTSUP)
    
    def rename(self, old, new):
        raise FuseOSError(EROFS)
    
    def rmdir(self, path):
        raise FuseOSError(EROFS)
    
    def setxattr(self, path, name, value, options, position=0):
        raise FuseOSError(ENOTSUP)
    
    def statfs(self, path):
        """Returns a dictionary with keys identical to the statvfs C structure
           of statvfs(3).
           On Mac OS X f_bsize and f_frsize must be a power of 2 (minimum 512)."""
        return {}
    
    def symlink(self, target, source):
        raise FuseOSError(EROFS)
    
    def truncate(self, path, length, fh=None):
        raise FuseOSError(EROFS)
    
    def unlink(self, path):
        raise FuseOSError(EROFS)
    
    def utimens(self, path, times=None):
        """Times is a (atime, mtime) tuple. If None use current time."""
        return 0
    
    def write(self, path, data, offset, fh):
        raise FuseOSError(EROFS)


class LoggingMixIn:
    def __call__(self, op, path, *args):
        print '->', op, path, repr(args)
        ret = '[Unhandled Exception]'
        try:
            ret = getattr(self, op)(path, *args)
            return ret
        except OSError, e:
            ret = str(e)
            raise
        finally:
            print '<-', op, repr(ret)
