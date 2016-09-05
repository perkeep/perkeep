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

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.7.linux-amd64.tar.gz
RUN echo '702ad90f705365227e902b42d91dd1a40e48ca7f67a2f4b2fd052aaa4295cd95  go1.7.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.7.linux-amd64.tar.gz

ENV ANDROID_HOME /usr/local/android-sdk-linux
ENV ANT_HOME /usr/local/apache-ant-1.9.2
ENV PATH $PATH:$ANDROID_HOME/tools
ENV PATH $PATH:$ANDROID_HOME/platform-tools
ENV PATH $PATH:$ANT_HOME/bin
ENV IN_DOCKER 1
