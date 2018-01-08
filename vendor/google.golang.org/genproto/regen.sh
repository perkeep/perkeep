#!/bin/bash
#
# Copyright 2016 Google Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# This script rebuilds the generated code for the protocol buffers.
# To run this you will need protoc and goprotobuf installed;
# see https://github.com/golang/protobuf for instructions.
# You also need Go and Git installed.

set -e

PKG=google.golang.org/genproto
PROTO_REPO=https://github.com/google/protobuf
PROTO_SUBDIR=src/google/protobuf
API_REPO=https://github.com/googleapis/googleapis

# NOTE(cbro): Mac OS sed requires an argument be passed into -i,
# GNU sed interprets that blank argument as a filename.
if [ "Darwin" = $(uname) ]; then
  function sed-i-f {
    sed -i '' -f $@
  }
else
  function sed-i-f {
    sed -i -f $@
  }
fi

function die() {
  echo 1>&2 $*
  exit 1
}

# Sanity check that the right tools are accessible.
for tool in go git protoc protoc-gen-go; do
  q=$(which $tool) || die "didn't find $tool"
  echo 1>&2 "$tool: $q"
done

tmpdir=$(mktemp -d -t regen-cds-dir.XXXXXX)
trap 'rm -rf $tmpdir' EXIT
tmpapi=$(mktemp -d -t regen-cds-api.XXXXXX)
trap 'rm -rf $tmpapi' EXIT

echo -n 1>&2 "finding package dir... "
pkgdir=$(go list -f '{{.Dir}}' $PKG/protobuf)
echo 1>&2 $pkgdir
base=$(echo $pkgdir | sed "s,/$PKG/protobuf\$,,")
echo 1>&2 "base: $base"
cd $base

echo 1>&2 "fetching proto repos..."
git clone -q $PROTO_REPO $tmpdir &
git clone -q $API_REPO $tmpapi &
wait

import_fixes=$tmpdir/fix_imports.sed
import_msg=$tmpdir/fix_imports.txt
vanity_fixes=$tmpdir/vanity_fixes.sed

# Rename records a proto rename from $1->$2.
function rename() {
  echo >>$import_msg "Renaming $1 => $2"
  echo >>$import_fixes "s,\"$1\";,\"$2\"; // from $1,"
}

# Pass 1: copy protos from the google/protobuf repo.
for f in $(cd $PKG && find protobuf -name '*.proto'); do
  echo 1>&2 "finding latest version of $f... "
  up=google/protobuf/$(basename $f)
  cp "$tmpdir/src/$up" "$PKG/$f"
  rename "$up" "$PKG/$f"
done

# Pass 2: move the protos out of googleapis/google/{api,rpc,type}.
for g in "api" "rpc" "type"; do
  for f in $(cd $PKG && find googleapis/$g -name '*.proto'); do
    echo 1>&2 "finding latest version of $f... "
    # Note: we use move here so that the next pass doesn't see them.
    up=google/$g/$(basename $f)
    [ ! -f "$tmpapi/$up" ] && continue
    mv "$tmpapi/$up" "$PKG/$f"
    rename "$up" "$PKG/$f"
  done
done

# Pass 3: copy the rest of googleapis/google
for f in $(cd "$tmpapi/google" && find * -name '*.proto'); do
  dst=$(dirname "$PKG/googleapis/$f")
  echo 1>&2 "finding latest version of $f... "
  mkdir -p $dst
  cp "$tmpapi/google/$f" "$dst"
  rename "google/$f" "$PKG/googleapis/$f"
done

# Mappings of well-known proto types.
rename "google/protobuf/any.proto" "github.com/golang/protobuf/ptypes/any/any.proto"
rename "google/protobuf/duration.proto" "github.com/golang/protobuf/ptypes/duration/duration.proto"
rename "google/protobuf/empty.proto" "github.com/golang/protobuf/ptypes/empty/empty.proto"
rename "google/protobuf/struct.proto" "github.com/golang/protobuf/ptypes/struct/struct.proto"
rename "google/protobuf/timestamp.proto" "github.com/golang/protobuf/ptypes/timestamp/timestamp.proto"
rename "google/protobuf/wrappers.proto" "github.com/golang/protobuf/ptypes/wrappers/wrappers.proto"

# Pass 4: fix the imports in each of the protos.
sort $import_msg 1>&2
sed-i-f $import_fixes $(find $PKG -name '*.proto')

# Run protoc once per package.
for dir in $(find $PKG -name '*.proto' -exec dirname '{}' ';' | sort -u); do
  echo 1>&2 "* $dir"
  protoc --go_out=plugins=grpc:. $dir/*.proto
done

# Add import comments and fix package names.
for f in $(find $PKG -name '*.pb.go'); do
  dir=$(dirname $f)
  echo "s,^\(package .*\)\$,\\1 // import \"$dir\"," > $vanity_fixes
  sed-i-f $vanity_fixes $f
done

# Sanity check the build.
echo 1>&2 "Checking that the libraries build..."
go build -v $PKG/...

echo 1>&2 "All done!"
