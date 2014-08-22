# Build environment in which to build the Camlistore Android app.
#
# This extends the Dockerfile from https://index.docker.io/u/wasabeef/android/

FROM wasabeef/android
MAINTAINER bradfitz <brad@danga.com>

# Found these from: android list sdk -u -e
RUN android list sdk -u -e | grep build-tools- | perl -npe 's/.+"(.+)"/$1/' > /tmp/build-tools-version
RUN perl -e 'die "No Android build tools version found." unless -s "/tmp/build-tools-version"'
RUN echo y | android update sdk -u -t $(cat /tmp/build-tools-version)
RUN echo y | android update sdk -u -t android-17

# Don't need mercurial yet, since we're just using the archive URL to fetch Go.
# But it's possible we may want to switch to using hg, in which case:
# RUN yum -y install mercurial

# Update the GOVERS to depend on a new version of Go.
#
# The 073fc578434b version is Go 1.3.1 (2014-02-21),
# to satisfy the dependency for Go 1.3 in the Docker build of
# camput.
ENV GOVERS 073fc578434b

RUN cd /usr/local && curl -O http://go.googlecode.com/archive/$GOVERS.zip
RUN cd /usr/local && unzip -q $GOVERS.zip
RUN cd /usr/local && mv go-$GOVERS go
RUN chmod 0755 /usr/local/go/src/make.bash
RUN echo $GOVERS > /usr/local/go/VERSION
RUN GOROOT=/usr/local/go GOARCH=arm bash -c "cd /usr/local/go/src && ./make.bash"


ENV ANDROID_HOME /usr/local/android-sdk-linux
ENV ANT_HOME /usr/local/apache-ant-1.9.2
ENV PATH $PATH:$ANDROID_HOME/tools
ENV PATH $PATH:$ANDROID_HOME/platform-tools
ENV PATH $PATH:$ANT_HOME/bin
ENV IN_DOCKER 1
