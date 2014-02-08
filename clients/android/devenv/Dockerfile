# Build environment in which to build the Camlistore Android app.

FROM wasabeef/android
MAINTAINER bradfitz <brad@danga.com>

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
