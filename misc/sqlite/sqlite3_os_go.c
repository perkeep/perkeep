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
  return GoFileClose(((GoFile*) file)->fd) == 0 ? SQLITE_OK : SQLITE_ERROR;
}

static int go_file_read(sqlite3_file* file, void* dest, int iAmt, sqlite3_int64 iOfst) {
  return GoFileRead(((GoFile*) file)->fd, dest, iAmt, iOfst) == 0 ?
      SQLITE_OK :
      SQLITE_ERROR;
}

static int go_file_write(sqlite3_file* file, const void* src, int iAmt, sqlite3_int64 iOfst) {
  fprintf(stderr, "write\n");
  return 0;
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
  fprintf(stderr, "access: %s\n", zName);
  return SQLITE_OK;
}

static int go_vfs_full_pathname(sqlite3_vfs* vfs,
                                const char* zName,
                                int nOut,
                                char* zOut) {
  strncpy(zOut, zName, nOut);
  zOut[nOut - 1] = '\0';
  return SQLITE_OK;
}

static void* go_vfs_dl_open(sqlite3_vfs* vfs, const char* zFilename) {
  return NULL;
}

static void go_vfs_dl_error(sqlite3_vfs* vfs, int nByte, char *zErrMsg) {
}

static void* go_vfs_dl_sym(sqlite3_vfs* vfs,
                           void* handle,
                           const char* zSymbol) {
  return NULL;
}

static void go_vfs_dl_close(sqlite3_vfs* vfs, void* handle) {
}

static int go_vfs_randomness(sqlite3_vfs* vfs, int nByte, char *zOut) {
  return SQLITE_OK;
}

static int go_vfs_sleep(sqlite3_vfs* vfs, int microseconds) {
  return SQLITE_OK;
}

static int go_vfs_current_time(sqlite3_vfs* vfs, double* now) {
  return SQLITE_OK;
}

static int go_vfs_get_last_error(sqlite3_vfs* vfs, int foo, char* bar) {
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
#if 0
  int (*xTruncate)(sqlite3_file*, sqlite3_int64 size);
  int (*xSync)(sqlite3_file*, int flags);
  int (*xFileSize)(sqlite3_file*, sqlite3_int64 *pSize);
  int (*xLock)(sqlite3_file*, int);
  int (*xUnlock)(sqlite3_file*, int);
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
