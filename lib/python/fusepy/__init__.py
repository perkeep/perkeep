import sys
pyver = sys.version_info[0:2]
if pyver <= (2, 4):
    from fuse24 import *
elif pyver >= (3, 0):
    from fuse3 import *
else:
    from fuse import *
