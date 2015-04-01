# Copyright 2015 The Camlistore Authors.
#
# NOTE: this will not build directly with "docker build". This file is just a
# template used by misc/docker/dock.go
#

FROM scratch
MAINTAINER Camlistore Contributors <camlistore@googlegroups.com>
ADD djpeg /usr/bin/djpeg
ADD camlistore.org /camlistore

EXPOSE 80 443
ENTRYPOINT ["/camlistore/bin/camlistored"]
