# Copyright 2017 The Perkeep Authors.

FROM openjdk:11-jdk

MAINTAINER camlistore <camlistore@googlegroups.com>

RUN echo "Adding gopher user and group" \
	&& groupadd --system --gid 1000 gopher \
	&& useradd --system --gid gopher --uid 1000 --shell /bin/bash --create-home gopher \
	&& mkdir /home/gopher/.gradle \
	&& chown --recursive gopher:gopher /home/gopher

# To enable running android tools such as aapt
RUN apt-get update && apt-get -y upgrade
RUN apt-get install -y lib32z1 lib32stdc++6
# For Go:
RUN apt-get -y --no-install-recommends install curl gcc make
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev git

USER gopher
VOLUME "/home/gopher/.gradle"
ENV GOPHER /home/gopher

# Get android sdk, ndk, and rest of the stuff needed to build the android app.
WORKDIR $GOPHER
RUN mkdir android-sdk
ENV ANDROID_HOME $GOPHER/android-sdk
WORKDIR $ANDROID_HOME
RUN curl -O -L https://dl.google.com/android/repository/commandlinetools-linux-8512546_latest.zip
RUN echo '2ccbda4302db862a28ada25aa7425d99dce9462046003c1714b059b5c47970d8  ./commandlinetools-linux-8512546_latest.zip' | sha256sum -c
RUN unzip ./commandlinetools-linux-8512546_latest.zip
ENV SDK_MGR "$ANDROID_HOME/cmdline-tools/bin/sdkmanager --sdk_root=$ANDROID_HOME"
RUN echo y | $SDK_MGR --update
RUN echo y | $SDK_MGR 'platforms;android-27'
RUN echo y | $SDK_MGR 'build-tools;27.0.0'
RUN echo y | $SDK_MGR 'extras;android;m2repository'
RUN echo y | $SDK_MGR 'ndk-bundle'
RUN echo y | keytool -genkeypair -dname "cn=Gopher" \
	-alias perkeep \
	-keypass gopher -keystore $GOPHER/keystore \
	-storepass gopher \
	-validity 20000

# Get Go stable release
WORKDIR $GOPHER
RUN curl -O https://storage.googleapis.com/golang/go1.18.linux-amd64.tar.gz
RUN echo 'e85278e98f57cdb150fe8409e6e5df5343ecb13cebf03a5d5ff12bd55a80264f  go1.18.linux-amd64.tar.gz' | sha256sum -c
RUN tar -xzf go1.18.linux-amd64.tar.gz
ENV GOPATH $GOPHER
ENV GOROOT $GOPHER/go
ENV PATH $PATH:$GOROOT/bin:$GOPHER/bin

# Get gomobile
RUN go install -v golang.org/x/mobile/cmd/gomobile@8578da9835fd

# init gomobile
RUN gomobile init

CMD ["/bin/bash"]
