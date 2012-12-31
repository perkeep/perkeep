#!/bin/bash

# Example client accesses to blob server using curl.

# Configuration variables here:
BSHOST=localhost:3179/bs
BSUSER=user
BSPASS=foo

# Shorter name for curl auth param:
AUTH=$BSUSER:$BSPASS

# Stat -- 200 response
curl -u $AUTH -d camliversion=1 http://$BSHOST/camli/stat

# Upload -- 200 response
curl -u $AUTH -v -L \
  -F sha1-126249fd8c18cbb5312a5705746a2af87fba9538=@./test_data.txt \
  #<the url returned by stat>

# Put with bad blob_ref parameter -- 400 response
curl -v -L \
  -F sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f=@./test_data.txt \
  #<the url returned by stat>

# Get present -- the blob
curl -u $AUTH -v http://$BSHOST/camli/sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Get missing -- 404
curl -u $AUTH -v http://$BSHOST/camli/sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Check present -- 200 with only headers
curl -u $AUTH -I http://$BSHOST/camli/sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Check missing -- 404 with empty list response
curl -I http://$BSHOST/camli/sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# List -- 200 with list of blobs (just one)
curl -v -u $AUTH http://$BSHOST/camli/enumerate-blobs?limit=1

# List offset -- 200 with list of no blobs
curl -v -u $AUTH http://$BSHOST/camli/enumerate-blobs?after=sha1-126249fd8c18cbb5312a5705746a2af87fba9538
