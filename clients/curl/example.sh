#!/bin/bash

# Example client accesses to blob server using curl.

BSHOST=localhost:8080
BSPASS=foo

# Preupload -- 200 response
curl -u user:$BSPASS -d camliversion=1 http://$BSHOST/camli/preupload

# Upload -- 200 response
curl -v -L \
  -F sha1-126249fd8c18cbb5312a5705746a2af87fba9538=@./test_data.txt \
  #<the url returned by preupload>

# Put with bad blob_ref parameter -- 400 response
curl -v -L \
  -F sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f=@./test_data.txt \
  #<the url returned by preupload>

# Get present -- the blob
curl -u user:$BSPASS -v http://$BSHOST/camli/sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Get missing -- 404
curl -u user:$BSPASS -v http://$BSHOST/camli/sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Check present -- 200 with only headers
curl -u user:$BSPASS -I http://$BSHOST/camli/sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Check missing -- 404 with empty list response
curl -I http://$BSHOST/camli/sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# List -- 200 with list of blobs (just one)
curl -v -u user:$BSPASS http://$BSHOST/camli/enumerate-blobs?limit=1

# List offset -- 200 with list of no blobs
curl -v -u user:$BSPASS http://$BSHOST/camli/enumerate-blobs?after=sha1-126249fd8c18cbb5312a5705746a2af87fba9538
