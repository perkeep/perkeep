#include <stdlib.h>
#include <stdio.h>
#include <string.h>

#include "_cgo_export.h"

#define SKIP_SQLITE_VERSION
#include "sqlite3.h"

static sqlite3_io_methods g_file_methods;

typedef struct GoFile GoFile;
struct GoFile {
  sqlite3_io_methods const *pMethod;  /* Always the first entry */
  int fd;
};

/* File methods */

static int go_file_close(sqlite3_file* file) {
  int fd = ((GoFile*) file)->fd;
  fprintf(stderr, "go_file_close(%d)\n", fd);
  return GoFileClose(fd == 0 ? SQLITE_OK : SQLITE_ERROR);
}

static int go_file_read(sqlite3_file* file, void* dest, int iAmt, sqlite3_int64 iOfst) {
  return GoFileRead(((GoFile*) file)->fd, dest, iAmt, iOfst);
}

static int go_file_write(sqlite3_file* file, const void* src, int iAmt, sqlite3_int64 iOfst) {
  fprintf(stderr, "write\n");
  return 0;
}

static int go_file_truncate(sqlite3_file* file, sqlite3_int64 size) {
  int fd = ((GoFile*) file)->fd;
  fprintf(stderr, "TODO go_file_truncate(%d)\n", fd);
  // TODO: implement
  return SQLITE_OK;
}

static int go_file_sync(sqlite3_file* file, int flags) {
  int fd = ((GoFile*) file)->fd;
  fprintf(stderr, "TODO go_file_sync(%d)\n", fd);
  // TODO: implement
  return SQLITE_OK;
}

static int go_file_file_size(sqlite3_file* file, sqlite3_int64* pSize) {
  int fd = ((GoFile*) file)->fd;
  struct GoFileFileSize_return result = GoFileFileSize(fd);
  fprintf(stderr, "go_file_file_size(%d) = %d, %lld", fd, result.r0, result.r1);
  if (result.r0 != 0) {
    return SQLITE_ERROR;
  }

  *pSize = result.r1;
  return SQLITE_OK;
}

static int go_file_lock(sqlite3_file* file, int flags) {
  int fd = ((GoFile*) file)->fd;
  fprintf(stderr, "TODO go_file_lock(%d)\n", fd);
  // TODO: implement
  return SQLITE_OK;
}

static int go_file_unlock(sqlite3_file* file, int flags) {
  int fd = ((GoFile*) file)->fd;
  fprintf(stderr, "TODO go_file_unlock(%d)\n", fd);
  // TODO: implement
  return SQLITE_OK;
}

/* VFS methods */

static int go_vfs_open(sqlite3_vfs* vfs,
                       const char* zName,
                       sqlite3_file* file,
                       int flags,
                       int* pOutFlags) {
  fprintf(stderr, "go_vfs_open: %s\n", zName);
  GoFile* go_file = (GoFile*) file;
  memset(go_file, 0, sizeof(go_file));

  const int fd = GoVFSOpen((char*) zName, flags);
  if (fd == -1) {
    return SQLITE_ERROR;
  }

  go_file->pMethod = &g_file_methods;
  go_file->fd = fd;
  return SQLITE_OK;
}

static int go_vfs_delete(sqlite3_vfs* vfs, const char* zName, int syncDir) {
  fprintf(stderr, "delete: %s\n", zName);
  return SQLITE_OK;
}

static int go_vfs_access(sqlite3_vfs* vfs,
                         const char* zName,
                         int flags,
                         int* pResOut) {
  fprintf(stderr, "TODO access: %s\n", zName);
  return SQLITE_OK;
}

static int go_vfs_full_pathname(sqlite3_vfs* vfs,
                                const char* zName,
                                int nOut,
                                char* zOut) {
  fprintf(stderr, "TODO go_vfs_full_pathname: %s\n", zName);
  // TODO: Actually implement this.
  strncpy(zOut, zName, nOut);
  zOut[nOut - 1] = '\0';
  return SQLITE_OK;
}

static void* go_vfs_dl_open(sqlite3_vfs* vfs, const char* zFilename) {
  fprintf(stderr, "go_vfs_dl_open\n");
  return NULL;
}

static void go_vfs_dl_error(sqlite3_vfs* vfs, int nByte, char *zErrMsg) {
  fprintf(stderr, "go_vfs_dl_error\n");
}

static void* go_vfs_dl_sym(sqlite3_vfs* vfs,
                           void* handle,
                           const char* zSymbol) {
  fprintf(stderr, "go_vfs_dl_sym\n");
  return NULL;
}

static void go_vfs_dl_close(sqlite3_vfs* vfs, void* handle) {
  fprintf(stderr, "go_vfs_dl_close\n");
}

static int go_vfs_randomness(sqlite3_vfs* vfs, int nByte, char *zOut) {
  fprintf(stderr, "go_vfs_randomness\n");
  return SQLITE_OK;
}

static int go_vfs_sleep(sqlite3_vfs* vfs, int microseconds) {
  fprintf(stderr, "go_vfs_sleep\n");
  return SQLITE_OK;
}

static int go_vfs_current_time(sqlite3_vfs* vfs, double* now) {
  fprintf(stderr, "go_vfs_current_time\n");
  return SQLITE_OK;
}

static int go_vfs_get_last_error(sqlite3_vfs* vfs, int foo, char* bar) {
  fprintf(stderr, "go_vfs_get_last_error\n");
  // Unused, per sqlite3's os_unix.c.
  return SQLITE_OK;
}

int sqlite3_os_init(void) {
  static sqlite3_vfs vfs;
  memset(&vfs, 0, sizeof(vfs));
  vfs.iVersion = 1;
  vfs.szOsFile = sizeof(GoFile);
  vfs.mxPathname = 512;
  vfs.pNext = NULL;
  vfs.zName = "go";
  vfs.pAppData = NULL;
  /* Version 1 methods */
  vfs.xOpen = go_vfs_open;
  vfs.xDelete = go_vfs_delete;
  vfs.xAccess = go_vfs_access;
  vfs.xFullPathname = go_vfs_full_pathname;
  vfs.xDlOpen = go_vfs_dl_open;
  vfs.xDlError = go_vfs_dl_error;
  vfs.xDlSym = go_vfs_dl_sym;
  vfs.xDlClose = go_vfs_dl_close;
  vfs.xRandomness = go_vfs_randomness;
  vfs.xSleep = go_vfs_sleep;
  vfs.xCurrentTime = go_vfs_current_time;
  vfs.xGetLastError = go_vfs_get_last_error;
#if 0
  /*
  ** The methods above are in version 1 of the sqlite_vfs object
  ** definition.  Those that follow are added in version 2 or later
  */
  int (*xCurrentTimeInt64)(sqlite3_vfs*, sqlite3_int64*);
  /*
  ** The methods above are in versions 1 and 2 of the sqlite_vfs object.
  ** Those below are for version 3 and greater.
  */
  int (*xSetSystemCall)(sqlite3_vfs*, const char *zName, sqlite3_syscall_ptr);
  sqlite3_syscall_ptr (*xGetSystemCall)(sqlite3_vfs*, const char *zName);
  const char *(*xNextSystemCall)(sqlite3_vfs*, const char *zName);
  /*
  ** The methods above are in versions 1 through 3 of the sqlite_vfs object.
  ** New fields may be appended in figure versions.  The iVersion
  ** value will increment whenever this happens. 
  */
};
#endif
  sqlite3_vfs_register(&vfs, 1);

  memset(&g_file_methods, 0, sizeof(g_file_methods));
  g_file_methods.iVersion = 1;
  g_file_methods.xClose = go_file_close;
  g_file_methods.xRead = go_file_read;
  g_file_methods.xWrite = go_file_write;
  g_file_methods.xTruncate = go_file_truncate;
  g_file_methods.xSync = go_file_sync;
  g_file_methods.xFileSize = go_file_file_size;
  g_file_methods.xLock = go_file_lock;
  g_file_methods.xUnlock = go_file_unlock;
#if 0
  int (*xCheckReservedLock)(sqlite3_file*, int *pResOut);
  int (*xFileControl)(sqlite3_file*, int op, void *pArg);
  int (*xSectorSize)(sqlite3_file*);
  int (*xDeviceCharacteristics)(sqlite3_file*);
  /* Methods above are valid for version 1 */
  int (*xShmMap)(sqlite3_file*, int iPg, int pgsz, int, void volatile**);
  int (*xShmLock)(sqlite3_file*, int offset, int n, int flags);
  void (*xShmBarrier)(sqlite3_file*);
  int (*xShmUnmap)(sqlite3_file*, int deleteFlag);
  /* Methods above are valid for version 2 */
  /* Additional methods may be added in future releases */
#endif

  return SQLITE_OK;
}

int sqlite3_os_end(void) { return SQLITE_OK; }
