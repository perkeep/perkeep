'''
$Id: operations.py 47 2010-01-29 17:11:23Z nikratio $

Copyright (c) 2010, Nikolaus Rath <Nikolaus@rath.org>
All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

    * Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
    * Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
    * Neither the name of the main author nor the names of other contributors may be used to endorse or promote products derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
''' 

from __future__ import division, print_function, absolute_import

from .interface import FUSEError
import errno
  
class Operations(object):
    '''
    This is a dummy class that just documents the possible methods that
    a file system may declare.
    '''
    
    # This is a dummy class, so all the methods could of course
    # be functions
    #pylint: disable-msg=R0201
    
    def handle_exc(self, exc):
        '''Handle exceptions that occured during request processing. 
        
        This method returns nothing and does not raise any exceptions itself.
        '''
        
        pass
    
    def init(self):
        '''Initialize operations
        
        This function has to be called before any request has been received,
        but after the mountpoint has been set up and the process has
        daemonized.
        '''
        
        pass
    
    def destroy(self):
        '''Clean up operations.
        
        This method has to be called after the last request has been
        received, when the file system is about to be unmounted.
        '''
        
        pass
    
    def check_args(self, fuse_args):
        '''Review FUSE arguments
        
        This method checks if the FUSE options `fuse_args` are compatible
        with the way that the file system operations are implemented.
        It raises an exception if incompatible options are encountered and
        silently adds required options if they are missing.
        '''
        
        pass
    
    def readdir(self, fh, off):
        '''Read directory entries
        
        This method returns an iterator over the contents of directory `fh`,
        starting at entry `off`. The iterator yields tuples of the form
        ``(name, attr)``, where ``attr` is a dict with keys corresponding to
        the elements of ``struct stat``.
         
        Iteration may be stopped as soon as enough elements have been
        retrieved and does not have to be continued until `StopIteration`
        is raised.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
        
    def read(self, fh, off, size):
        '''Read `size` bytes from `fh` at position `off`
        
        Unless the file has been opened in direct_io mode or EOF is reached,
        this function  returns exactly `size` bytes. 
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def link(self, inode, new_parent_inode, new_name):
        '''Create a hard link.
    
        Returns a dict with the attributes of the newly created directory
        entry. The keys are the same as for `lookup`.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def open(self, inode, flags):
        '''Open a file.
        
        Returns an (integer) file handle. `flags` is a bitwise or of the open flags
        described in open(2) and defined in the `os` module (with the exception of 
        ``O_CREAT``, ``O_EXCL``, ``O_NOCTTY`` and ``O_TRUNC``)
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def opendir(self, inode):
        '''Open a directory.
        
        Returns an (integer) file handle. 
        '''
        
        raise FUSEError(errno.ENOSYS)

    
    def mkdir(self, parent_inode, name, mode, ctx):
        '''Create a directory
    
        `ctx` must be a context object that contains pid, uid and 
        primary gid of the requesting process.
        
        Returns a dict with the attributes of the newly created directory
        entry. The keys are the same as for `lookup`.
        '''
        
        raise FUSEError(errno.ENOSYS)

    def mknod(self, parent_inode, name, mode, rdev, ctx):
        '''Create (possibly special) file
    
        `ctx` must be a context object that contains pid, uid and 
        primary gid of the requesting process.
        
        Returns a dict with the attributes of the newly created directory
        entry. The keys are the same as for `lookup`.
        '''
        
        raise FUSEError(errno.ENOSYS)

    
    def lookup(self, parent_inode, name):
        '''Look up a directory entry by name and get its attributes.
    
        Returns a dict with keys corresponding to the elements in 
        ``struct stat`` and the following additional keys:
        
        :generation: The inode generation number
        :attr_timeout: Validity timeout (in seconds) for the attributes
        :entry_timeout: Validity timeout (in seconds) for the name 
        
        Note also that the ``st_Xtime`` entries support floating point numbers 
        to allow for nano second resolution.
        
        The returned dict can be modified at will by the caller without
        influencing the internal state of the file system.
        
        If the entry does not exist, raises `FUSEError(errno.ENOENT)`.
        '''
        
        raise FUSEError(errno.ENOSYS)

    def listxattr(self, inode):
        '''Get list of extended attribute names'''
        
        raise FUSEError(errno.ENOSYS)
    
    def getattr(self, inode):
        '''Get attributes for `inode`
    
        Returns a dict with keys corresponding to the elements in 
        ``struct stat`` and the following additional keys:
        
        :attr_timeout: Validity timeout (in seconds) for the attributes
        
        The returned dict can be modified at will by the caller without
        influencing the internal state of the file system.
        
        Note that the ``st_Xtime`` entries support floating point numbers 
        to allow for nano second resolution.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def getxattr(self, inode, name):
        '''Return extended attribute value
        
        If the attribute does not exist, raises `FUSEError(ENOATTR)`
        '''
        
        raise FUSEError(errno.ENOSYS)
 
    def access(self, inode, mode, ctx, get_sup_gids):
        '''Check if requesting process has `mode` rights on `inode`. 
        
        Returns a boolean value. `get_sup_gids` must be a function that
        returns a list of the supplementary group ids of the requester. 
        
        `ctx` must be a context object that contains pid, uid and 
        primary gid of the requesting process.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def create(self, inode_parent, name, mode, ctx):
        '''Create a file and open it
                
        `ctx` must be a context object that contains pid, uid and 
        primary gid of the requesting process.
        
        Returns a tuple of the form ``(fh, attr)``. `fh` is
        integer file handle that is used to identify the open file and
        `attr` is a dict similar to the one returned by `lookup`.
        '''
        
        raise FUSEError(errno.ENOSYS)

    def flush(self, fh):
        '''Handle close() syscall.
        
        May be called multiple times for the same open file (e.g. if the file handle
        has been duplicated).
                                                             
        If the filesystem supports file locking operations, all locks belonging
        to the file handle's owner are cleared. 
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def fsync(self, fh, datasync):
        '''Flush buffers for file `fh`
        
        If `datasync` is true, only the user data is flushed (and no meta data). 
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    
    def fsyncdir(self, fh, datasync):  
        '''Flush buffers for directory `fh`
        
        If the `datasync` is true, then only the directory contents
        are flushed (and not the meta data about the directory itself).
        '''
        
        raise FUSEError(errno.ENOSYS)
        
    def readlink(self, inode):
        '''Return target of symbolic link'''
        
        raise FUSEError(errno.ENOSYS)
    
    def release(self, fh):
        '''Release open file
        
        This method must be called exactly once for each `open` call.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def releasedir(self, fh):
        '''Release open directory
        
        This method must be called exactly once for each `opendir` call.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def removexattr(self, inode, name):
        '''Remove extended attribute
        
        If the attribute does not exist, raises FUSEError(ENOATTR)
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def rename(self, inode_parent_old, name_old, inode_parent_new, name_new):
        '''Rename a directory entry'''
        
        raise FUSEError(errno.ENOSYS)
    
    def rmdir(self, inode_parent, name):
        '''Remove a directory'''
        
        raise FUSEError(errno.ENOSYS)
    
    def setattr(self, inode, attr):
        '''Change directory entry attributes
        
        `attr` must be a dict with keys corresponding to the attributes of 
        ``struct stat``. `attr` may also include a new value for ``st_size`` which
        means that the file should be truncated or extended.
        
        Returns a dict with the new attributs of the directory entry,
        similar to the one returned by `getattr()`
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def setxattr(self, inode, name, value):
        '''Set an extended attribute.
        
        The attribute may or may not exist already.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def statfs(self):
        '''Get file system statistics
        
        Returns a `dict` with keys corresponding to the attributes of 
        ``struct statfs``.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def symlink(self, inode_parent, name, target, ctx):
        '''Create a symbolic link
        
        `ctx` must be a context object that contains pid, uid and 
        primary gid of the requesting process.
        
        Returns a dict with the attributes of the newly created directory
        entry. The keys are the same as for `lookup`.
        '''
        
        raise FUSEError(errno.ENOSYS)
    
    def unlink(self, parent_inode, name):
        '''Remove a (possibly special) file'''
        
        raise FUSEError(errno.ENOSYS)
    
    def write(self, fh, off, data):
        '''Write data into an open file
        
        Returns the number of bytes written.
        Unless the file was opened in ``direct_io`` mode, this is always equal to
        `len(data)`. 
        '''
        
        raise FUSEError(errno.ENOSYS)
    
