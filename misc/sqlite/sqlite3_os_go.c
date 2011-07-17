#include <stdlib.h>
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

static int go_file_close(sqlite3_file* file) {
  return GoFileClose(((GoFile*) file)->fd) == 0 ? SQLITE_OK : SQLITE_ERROR;
}

static int go_vfs_open(sqlite3_vfs* vfs,
                       const char* zName,
                       sqlite3_file* file,
                       int flags,
                       int* pOutFlags) {
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

int sqlite3_os_init(void) {
  static sqlite3_vfs vfs;
  memset(&vfs, 0, sizeof(vfs));
  vfs.iVersion = 3;
  vfs.szOsFile = sizeof(GoFile);
  vfs.mxPathname = 512;
  vfs.pNext = NULL;
  vfs.zName = "go";
  vfs.pAppData = NULL;
  vfs.xOpen = go_vfs_open;
#if 0
  int (*xDelete)(sqlite3_vfs*, const char *zName, int syncDir);
  int (*xAccess)(sqlite3_vfs*, const char *zName, int flags, int *pResOut);
  int (*xFullPathname)(sqlite3_vfs*, const char *zName, int nOut, char *zOut);
  void *(*xDlOpen)(sqlite3_vfs*, const char *zFilename);
  void (*xDlError)(sqlite3_vfs*, int nByte, char *zErrMsg);
  void (*(*xDlSym)(sqlite3_vfs*,void*, const char *zSymbol))(void);
  void (*xDlClose)(sqlite3_vfs*, void*);
  int (*xRandomness)(sqlite3_vfs*, int nByte, char *zOut);
  int (*xSleep)(sqlite3_vfs*, int microseconds);
  int (*xCurrentTime)(sqlite3_vfs*, double*);
  int (*xGetLastError)(sqlite3_vfs*, int, char *);
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
#if 0
  int (*xRead)(sqlite3_file*, void*, int iAmt, sqlite3_int64 iOfst);
  int (*xWrite)(sqlite3_file*, const void*, int iAmt, sqlite3_int64 iOfst);
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
