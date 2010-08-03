#!/bin/bash

# Example client accesses to blob server using curl.
#
#


# Preupload -- 200 response
curl -v \
  http://localhost:8080/camli/preupload

# Upload -- 200 response
curl -v -L \
  -F sha1-126249fd8c18cbb5312a5705746a2af87fba9538=@./test_data.txt \
  #<the url returned by preupload>

# Put with bad blob_ref parameter -- 400 response
curl -v -L \
  -F sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f=@./test_data.txt \
  #<the url returned by preupload>

# Get present -- the blob
curl -v http://localhost:8080/camli/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Get missing -- 404
curl -v http://localhost:8080/camli/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Check present -- 200 with only headers
curl -I http://localhost:8080/camli/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Check missing -- 404 with empty list response
curl -I http://localhost:8080/camli/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# List -- 200 with list of blobs (just one)
curl -v http://localhost:8080/camli/enumerate-blobs&limit=1

# List offset -- 200 with list of no blobs
curl -v http://localhost:8080/camli/enumerate-blobs?after=\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538
