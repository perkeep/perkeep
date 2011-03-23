# Copyright (c) 2010 Giorgos Verigakis <verigak@gmail.com>
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
from functools import partial, wraps
from inspect import getmembers, ismethod
from platform import machine, system
from stat import S_IFDIR, S_IFREG


_system = system()
_machine = machine()

class LibFUSE(CDLL):
    def __init__(self):
        if _system == 'Darwin':
            self.libiconv = CDLL(find_library('iconv'), RTLD_GLOBAL)
        super(LibFUSE, self).__init__(find_library('fuse'))
        
        self.fuse_mount.argtypes = (c_char_p, POINTER(fuse_args))
        self.fuse_mount.restype = c_void_p
        self.fuse_lowlevel_new.argtypes = (POINTER(fuse_args), POINTER(fuse_lowlevel_ops),
                                            c_size_t, c_void_p)
        self.fuse_lowlevel_new.restype = c_void_p
        self.fuse_set_signal_handlers.argtypes = (c_void_p,)
        self.fuse_session_add_chan.argtypes = (c_void_p, c_void_p)
        self.fuse_session_loop.argtypes = (c_void_p,)
        self.fuse_remove_signal_handlers.argtypes = (c_void_p,)
        self.fuse_session_remove_chan.argtypes = (c_void_p,)
        self.fuse_session_destroy.argtypes = (c_void_p,)
        self.fuse_unmount.argtypes = (c_char_p, c_void_p)
        
        self.fuse_req_ctx.restype = POINTER(fuse_ctx)
        self.fuse_req_ctx.argtypes = (fuse_req_t,)
        
        self.fuse_reply_err.argtypes = (fuse_req_t, c_int)
        self.fuse_reply_attr.argtypes = (fuse_req_t, c_void_p, c_double)
        self.fuse_reply_entry.argtypes = (fuse_req_t, c_void_p)
        self.fuse_reply_open.argtypes = (fuse_req_t, c_void_p)
        self.fuse_reply_buf.argtypes = (fuse_req_t, c_char_p, c_size_t)
        self.fuse_reply_write.argtypes = (fuse_req_t, c_size_t)
        
        self.fuse_add_direntry.argtypes = (c_void_p, c_char_p, c_size_t, c_char_p,
                                            c_stat_p, c_off_t)

class fuse_args(Structure):
    _fields_ = [('argc', c_int), ('argv', POINTER(c_char_p)), ('allocated', c_int)]

class c_timespec(Structure):
    _fields_ = [('tv_sec', c_long), ('tv_nsec', c_long)]

class c_stat(Structure):
    pass    # Platform dependent

if _system == 'Darwin':
    ENOTSUP = 45
    c_dev_t = c_int32
    c_fsblkcnt_t = c_ulong
    c_fsfilcnt_t = c_ulong
    c_gid_t = c_uint32
    c_mode_t = c_uint16
    c_off_t = c_int64
    c_pid_t = c_int32
    c_uid_t = c_uint32
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

class fuse_ctx(Structure):
    _fields_ = [('uid', c_uid_t), ('gid', c_gid_t), ('pid', c_pid_t)]

fuse_ino_t = c_ulong
fuse_req_t = c_void_p
c_stat_p = POINTER(c_stat)
fuse_file_info_p = POINTER(fuse_file_info)

FUSE_SET_ATTR = ('st_mode', 'st_uid', 'st_gid', 'st_size', 'st_atime', 'st_mtime')

class fuse_entry_param(Structure):
    _fields_ = [
        ('ino', fuse_ino_t),
        ('generation', c_ulong),
        ('attr', c_stat),
        ('attr_timeout', c_double),
        ('entry_timeout', c_double)]

class fuse_lowlevel_ops(Structure):
    _fields_ = [
        ('init', CFUNCTYPE(None, c_void_p, c_void_p)),
        ('destroy', CFUNCTYPE(None, c_void_p)),
        ('lookup', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_char_p)),
        ('forget', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_ulong)),
        ('getattr', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, fuse_file_info_p)),
        ('setattr', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_stat_p, c_int, fuse_file_info_p)),
        ('readlink', CFUNCTYPE(None, fuse_req_t, fuse_ino_t)),
        ('mknod', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_char_p, c_mode_t, c_dev_t)),
        ('mkdir', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_char_p, c_mode_t)),
        ('unlink', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_char_p)),
        ('rmdir', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_char_p)),
        ('symlink', CFUNCTYPE(None, fuse_req_t, c_char_p, fuse_ino_t, c_char_p)),
        ('rename', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_char_p, fuse_ino_t, c_char_p)),
        ('link', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, fuse_ino_t, c_char_p)),
        ('open', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, fuse_file_info_p)),
        ('read', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_size_t, c_off_t, fuse_file_info_p)),
        ('write', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_char_p, c_size_t, c_off_t,
                                fuse_file_info_p)),
        ('flush', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, fuse_file_info_p)),
        ('release', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, fuse_file_info_p)),
        ('fsync', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_int, fuse_file_info_p)),
        ('opendir', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, fuse_file_info_p)),
        ('readdir', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_size_t, c_off_t, fuse_file_info_p)),
        ('releasedir', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, fuse_file_info_p)),
        ('fsyncdir', CFUNCTYPE(None, fuse_req_t, fuse_ino_t, c_int, fuse_file_info_p))]


def struct_to_dict(p):
    try:
        x = p.contents
        return dict((key, getattr(x, key)) for key, type in x._fields_)
    except ValueError:
        return {}

def stat_to_dict(p):
    try:
        d = {}
        x = p.contents
        for key, type in x._fields_:
            if key in ('st_atimespec', 'st_mtimespec', 'st_ctimespec'):
                ts = getattr(x, key)
                key = key[:-4]      # Lose the "spec"
                d[key] = ts.tv_sec + ts.tv_nsec / 10 ** 9
            else:
                d[key] = getattr(x, key)
        return d
    except ValueError:
        return {}

def dict_to_stat(d):
    for key in ('st_atime', 'st_mtime', 'st_ctime'):
        if key in d:
            val = d[key]
            sec = int(val)
            nsec = int((val - sec) * 10 ** 9)
            d[key + 'spec'] = c_timespec(sec, nsec)
    return c_stat(**d)

def setattr_mask_to_list(mask):
    return [FUSE_SET_ATTR[i] for i in range(len(FUSE_SET_ATTR)) if mask & (1 << i)]

class FUSELL(object):
    def __init__(self, mountpoint):
        self.libfuse = LibFUSE()       
        
        fuse_ops = fuse_lowlevel_ops()
        
        for name, prototype in fuse_lowlevel_ops._fields_:
            method = getattr(self, 'fuse_' + name, None) or getattr(self, name, None)
            if method:
                setattr(fuse_ops, name, prototype(method))
        
        args = ['fuse']
        argv = fuse_args(len(args), (c_char_p * len(args))(*args), 0)
        
        # TODO: handle initialization errors
        
        chan = self.libfuse.fuse_mount(mountpoint, argv)
        assert chan
        
        session = self.libfuse.fuse_lowlevel_new(argv, byref(fuse_ops), sizeof(fuse_ops), None)
        assert session
        
        err = self.libfuse.fuse_set_signal_handlers(session)
        assert err == 0
        
        self.libfuse.fuse_session_add_chan(session, chan)
        
        err = self.libfuse.fuse_session_loop(session)
        assert err == 0
        
        err = self.libfuse.fuse_remove_signal_handlers(session)
        assert err == 0
        
        self.libfuse.fuse_session_remove_chan(chan)
        self.libfuse.fuse_session_destroy(session)
        self.libfuse.fuse_unmount(mountpoint, chan)
    
    def reply_err(self, req, err):
        return self.libfuse.fuse_reply_err(req, err)
    
    def reply_none(self, req):
        self.libfuse.fuse_reply_none(req)
    
    def reply_entry(self, req, entry):
        entry['attr'] = c_stat(**entry['attr'])
        e = fuse_entry_param(**entry)
        self.libfuse.fuse_reply_entry(req, byref(e))
    
    def reply_create(self, req, *args):
        pass    # XXX
    
    def reply_attr(self, req, attr, attr_timeout):
        st = dict_to_stat(attr)
        return self.libfuse.fuse_reply_attr(req, byref(st), c_double(attr_timeout))
    
    def reply_readlink(self, req, *args):
        pass    # XXX
    
    def reply_open(self, req, d):
        fi = fuse_file_info(**d)
        return self.libfuse.fuse_reply_open(req, byref(fi))
    
    def reply_write(self, req, count):
        return self.libfuse.fuse_reply_write(req, count)
    
    def reply_buf(self, req, buf):
        return self.libfuse.fuse_reply_buf(req, buf, len(buf))
    
    def reply_readdir(self, req, size, off, entries):
        bufsize = 0
        sized_entries = []
        for name, attr in entries:
            entsize = self.libfuse.fuse_add_direntry(req, None, 0, name, None, 0)
            sized_entries.append((name, attr, entsize))
            bufsize += entsize

        next = 0
        buf = create_string_buffer(bufsize)
        for name, attr, entsize in sized_entries:
            entbuf = cast(addressof(buf) + next, c_char_p)
            st = c_stat(**attr)
            next += entsize
            self.libfuse.fuse_add_direntry(req, entbuf, entsize, name, byref(st), next)

        if off < bufsize:
            buf = cast(addressof(buf) + off, c_char_p) if off else buf
            return self.libfuse.fuse_reply_buf(req, buf, min(bufsize - off, size))
        else:
            return self.libfuse.fuse_reply_buf(req, None, 0)
    
    
    # If you override the following methods you should reply directly
    # with the self.libfuse.fuse_reply_* methods.
    
    def fuse_getattr(self, req, ino, fi):
        self.getattr(req, ino, struct_to_dict(fi))
    
    def fuse_setattr(self, req, ino, attr, to_set, fi):
        attr_dict = stat_to_dict(attr)
        to_set_list = setattr_mask_to_list(to_set)
        fi_dict = struct_to_dict(fi)
        self.setattr(req, ino, attr_dict, to_set_list, fi_dict)
        
    def fuse_open(self, req, ino, fi):
        self.open(req, ino, struct_to_dict(fi))
    
    def fuse_read(self, req, ino, size, off, fi):
        self.read(req, ino, size, off, fi)
    
    def fuse_write(self, req, ino, buf, size, off, fi):
        buf_str = string_at(buf, size)
        fi_dict = struct_to_dict(fi)
        self.write(req, ino, buf_str, off, fi_dict)

    def fuse_flush(self, req, ino, fi):
        self.flush(req, ino, struct_to_dict(fi))
    
    def fuse_release(self, req, ino, fi):
        self.release(req, ino, struct_to_dict(fi))
    
    def fuse_fsync(self, req, ino, datasync, fi):
        self.fsyncdir(req, ino, datasync, struct_to_dict(fi))
    
    def fuse_opendir(self, req, ino, fi):
        self.opendir(req, ino, struct_to_dict(fi))
    
    def fuse_readdir(self, req, ino, size, off, fi):
        self.readdir(req, ino, size, off, struct_to_dict(fi))
    
    def fuse_releasedir(self, req, ino, fi):
        self.releasedir(req, ino, struct_to_dict(fi))
    
    def fuse_fsyncdir(self, req, ino, datasync, fi):
        self.fsyncdir(req, ino, datasync, struct_to_dict(fi))
    
    
    # Utility methods
    
    def req_ctx(self, req):
        ctx = self.libfuse.fuse_req_ctx(req)
        return struct_to_dict(ctx)
    
    
    # Methods to be overridden in subclasses.
    # Reply with the self.reply_* methods.
    
    def init(self, userdata, conn):
        """Initialize filesystem
        
        There's no reply to this method
        """
        pass

    def destroy(self, userdata):
        """Clean up filesystem
        
        There's no reply to this method
        """
        pass

    def lookup(self, req, parent, name):
        """Look up a directory entry by name and get its attributes.
        
        Valid replies:
            reply_entry
            reply_err
        """
        self.reply_err(req, ENOENT)
    
    def forget(self, req, ino, nlookup):
        """Forget about an inode
        
        Valid replies:
            reply_none
        """
        self.reply_none(req)

    def getattr(self, req, ino, fi):
        """Get file attributes
        
        Valid replies:
            reply_attr
            reply_err
        """
        if ino == 1:
            attr = {'st_ino': 1, 'st_mode': S_IFDIR | 0755, 'st_nlink': 2}
            self.reply_attr(req, attr, 1.0)
        else:
            self.reply_err(req, ENOENT)        
    
    def setattr(self, req, ino, attr, to_set, fi):
        """Set file attributes
        
        Valid replies:
            reply_attr
            reply_err
        """
        self.reply_err(req, EROFS)
        
    def readlink(self, req, ino):
        """Read symbolic link
        
        Valid replies:
            reply_readlink
            reply_err
        """
        self.reply_err(req, ENOENT)
    
    def mknod(self, req, parent, name, mode, rdev):
        """Create file node
        
        Valid replies:
            reply_entry
            reply_err
        """
        self.reply_err(req, EROFS)
    
    def mkdir(self, req, parent, name, mode):
        """Create a directory
        
        Valid replies:
            reply_entry
            reply_err
        """
        self.reply_err(req, EROFS)

    def unlink(self, req, parent, name):
        """Remove a file
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, EROFS)
    
    def rmdir(self, req, parent, name):
        """Remove a directory
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, EROFS)
    
    def symlink(self, req, link, parent, name):
        """Create a symbolic link
        
        Valid replies:
            reply_entry
            reply_err
        """
        self.reply_err(req, EROFS)
    
    def rename(self, req, parent, name, newparent, newname):
        """Rename a file
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, EROFS)
    
    def link(self, req, ino, newparent, newname):
        """Create a hard link
        
        Valid replies:
            reply_entry
            reply_err
        """
        self.reply_err(req, EROFS)
    
    def open(self, req, ino, fi):
        """Open a file
        
        Valid replies:
            reply_open
            reply_err
        """
        self.reply_open(req, fi)
    
    def read(self, req, ino, size, off, fi):
        """Read data
        
        Valid replies:
            reply_buf
            reply_err
        """
        self.reply_err(req, EIO)
        
    def write(self, req, ino, buf, off, fi):
        """Write data
        
        Valid replies:
            reply_write
            reply_err
        """
        self.reply_err(req, EROFS)
    
    def flush(self, req, ino, fi):
        """Flush method
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, 0)
    
    def release(self, req, ino, fi):
        """Release an open file
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, 0)
    
    def fsync(self, req, ino, datasync, fi):
        """Synchronize file contents
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, 0)

    def opendir(self, req, ino, fi):
        """Open a directory
        
        Valid replies:
            reply_open
            reply_err
        """
        self.reply_open(req, fi)
    
    def readdir(self, req, ino, size, off, fi):
        """Read directory
        
        Valid replies:
            reply_readdir
            reply_err
        """
        if ino == 1:
            attr = {'st_ino': 1, 'st_mode': S_IFDIR}
            entries = [('.', attr), ('..', attr)]
            self.reply_readdir(req, size, off, entries)
        else:
            self.reply_err(req, ENOENT)
    
    def releasedir(self, req, ino, fi):
        """Release an open directory
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, 0)

    def fsyncdir(self, req, ino, datasync, fi):
        """Synchronize directory contents
        
        Valid replies:
            reply_err
        """
        self.reply_err(req, 0)