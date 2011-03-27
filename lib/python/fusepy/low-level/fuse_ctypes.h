/* Necessary to prevent gccxml from complaining about
 * an undefined type */
#define __builtin_va_arg_pack_len int


#define FUSE_USE_VERSION 28
#include <fuse_lowlevel.h>
#include <attr/xattr.h>
#include <errno.h>

