# Copyright 2015 The Camlistore Authors.
FROM camlistore/go
WORKDIR /usr/local/go/src
RUN CGO_ENABLED=0 GOARCH=amd64 GOOS=darwin ./make.bash --no-clean
