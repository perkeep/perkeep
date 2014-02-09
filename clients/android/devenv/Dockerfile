# Build environment in which to build the Camlistore Android app.

FROM wasabeef/android
MAINTAINER bradfitz <brad@danga.com>

# Don't need this, since we're just using the archive URL to fetch Go.
# But it's possible we may want to switch to using hg, in which case:
# RUN yum -y install mercurial

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
ENV GRADLE_HOME /usr/local/gradle-1.9
ENV PATH $PATH:$ANDROID_HOME/tools
ENV PATH $PATH:$ANDROID_HOME/platform-tools
ENV PATH $PATH:$ANT_HOME/bin
ENV PATH $PATH:$MAVEN_HOME/bin
ENV PATH $PATH:$GRADLE_HOME/bin
ENV IN_DOCKER 1
