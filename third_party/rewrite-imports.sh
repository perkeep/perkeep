#!/bin/sh

find . -type f -name '*.go' -exec perl -pi -e 's!"code.google.com/!"camlistore/third_party/code.google.com/!' {} \;

