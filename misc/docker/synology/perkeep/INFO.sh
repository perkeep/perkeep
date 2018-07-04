#!/bin/bash
# Copyright (c) 2000-2016 Synology Inc. All rights reserved.

source /pkgscripts/include/pkg_util.sh

package="Perkeep"
version=SET_BY_DOCKER_BUILD
displayname="Perkeep"
maintainer="Perkeep Authors <perkeep@googlegroups.com>"
arch="$(pkg_get_unified_platform)"
description="Perkeep lets you permanently keep your stuff, for life. See https://perkeep.org/doc/synology"
helpurl="https://perkeep.org/doc/synology"
[ "$(caller)" != "0 NULL" ] && return 0
pkg_dump_info
