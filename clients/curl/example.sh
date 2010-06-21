#!/bin/bash

# Example client accesses to blob server using curl.
#
#


# Put -- 200 response
curl -v -L \
  -F file=@./test_data.txt \
  http://localhost:8080/put/sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Put with bad blob_ref parameter -- 400 response
curl -v -L \
  -F file=@./test_data.txt \
  http://localhost:8080/put/sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Get present -- the blob
curl -v http://localhost:8080/get/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Get missing -- 404
curl -v http://localhost:8080/get/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# Check present -- 200 with blob ref list response
curl -v http://localhost:8080/check/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538

# Check missing -- 404 with empty list response
curl -v http://localhost:8080/check/\
sha1-22a7fdd575f4c3e7caa3a55cc83db8b8a6714f0f

# List -- 200 with list of blobs (just one)
curl -v http://localhost:8080/list

# List offset -- 200 with list of no blobs
curl -v http://localhost:8080/list/\
sha1-126249fd8c18cbb5312a5705746a2af87fba9538
