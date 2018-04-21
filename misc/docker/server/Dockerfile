# Copyright 2015 The Perkeep Authors.
#
# NOTE: this will not build directly with "docker build". This file is just a
# template used by misc/docker/dock.go
#

FROM scratch
MAINTAINER Perkeep Contributors <camlistore@googlegroups.com>
ADD djpeg /usr/bin/djpeg
# Because one of the default zoneinfo dirs on linux. See zoneDirs in time/zoneinfo_unix.go
ADD zoneinfo /usr/share/zoneinfo
ADD perkeep.org /perkeep

EXPOSE 80 443
ENTRYPOINT ["/perkeep/bin/perkeepd"]
