#!/bin/sh

find . -type f -name '*.go' -exec perl -pi -e 's!"code.google.com/!"camlistore.org/third_party/code.google.com/!' {} \;
find . -type f -name '*.go' -exec perl -pi -e 's!"launchpad.net/!"camlistore.org/third_party/launchpad.net/!' {} \;
find . -type f -name '*.go' -exec perl -pi -e 's!"github.com/!"camlistore.org/third_party/github.com/!' {} \;
find . -type f -name '*.go' -exec perl -pi -e 's!"labix.org/!"camlistore.org/third_party/labix.org/!' {} \;

