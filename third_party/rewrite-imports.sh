#!/bin/sh

find . -type f -name '*.go' -exec perl -pi -e 's!"code.google.com/!"camlistore.org/third_party/code.google.com/!' {} \;
find . -type f -name '*.go' -exec perl -pi -e 's!"launchpad.net/!"camlistore.org/third_party/launchpad.net/!' {} \;
