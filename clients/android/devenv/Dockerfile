# Build environment in which to build the Camlistore Android app.
#
# This builds on https://index.docker.io/u/wasabeef/android/

FROM wasabeef/android
MAINTAINER bradfitz <brad@danga.com>

# Don't need mercurial yet, since we're just using the archive URL to fetch Go.
# But it's possible we may want to switch to using hg, in which case:
# RUN yum -y install mercurial

# Update the GOVERS to depend on a new version of Go.
#
# The ec5195954667 version is in the Go 1.3 dev cycle, after I fixed some net/http
# bugs (that are also worked around in Camlistore, but...)
ENV GOVERS ec5195954667

RUN cd /usr/local && curl -O http://go.googlecode.com/archive/$GOVERS.zip
RUN cd /usr/local && unzip -q $GOVERS.zip
RUN cd /usr/local && mv go-$GOVERS go
RUN chmod 0755 /usr/local/go/src/make.bash
RUN echo $GOVERS > /usr/local/go/VERSION
RUN GOROOT=/usr/local/go GOARCH=arm bash -c "cd /usr/local/go/src && ./make.bash"

# Found these from: android list sdk -u -e
RUN echo y | android update sdk -u -t android-17
RUN echo y | android update sdk -u -t build-tools-19.0.1

ENV ANDROID_HOME /usr/local/android-sdk-linux
ENV ANT_HOME /usr/local/apache-ant-1.9.2
ENV PATH $PATH:$ANDROID_HOME/tools
ENV PATH $PATH:$ANDROID_HOME/platform-tools
ENV PATH $PATH:$ANT_HOME/bin
ENV IN_DOCKER 1
