# Copyright 2017 The Camlistore Authors.

FROM openjdk:8-jdk

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
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev git

USER gopher
VOLUME "/home/gopher/.gradle"
ENV GOPHER /home/gopher

# Get android sdk, ndk, and rest of the stuff needed to build the android app.
WORKDIR $GOPHER
RUN mkdir android-sdk
ENV ANDROID_HOME $GOPHER/android-sdk
WORKDIR $ANDROID_HOME
RUN curl -O https://dl.google.com/android/repository/sdk-tools-linux-3859397.zip
RUN echo '444e22ce8ca0f67353bda4b85175ed3731cae3ffa695ca18119cbacef1c1bea0  sdk-tools-linux-3859397.zip' | sha256sum -c
RUN unzip sdk-tools-linux-3859397.zip
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager --update
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'platforms;android-17'
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'build-tools;26.0.0'
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'extras;android;m2repository'
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'ndk-bundle'

# Get Go stable release
WORKDIR $GOPHER
RUN curl -O https://storage.googleapis.com/golang/go1.8.3.linux-amd64.tar.gz
RUN echo '1862f4c3d3907e59b04a757cfda0ea7aa9ef39274af99a784f5be843c80c6772  go1.8.3.linux-amd64.tar.gz' | sha256sum -c
RUN tar -xzf go1.8.3.linux-amd64.tar.gz
ENV GOPATH $GOPHER
ENV GOROOT $GOPHER/go
ENV PATH $PATH:$GOROOT/bin:$GOPHER/bin

# Get gomobile
RUN go get -u golang.org/x/mobile/cmd/gomobile
WORKDIR $GOPATH/src/golang.org/x/mobile/cmd/gomobile
RUN git reset --hard 44a54e9b78442c0a8233ee2d552c61da88e053c3
RUN go install

# init gomobile
RUN gomobile init -ndk $ANDROID_HOME/ndk-bundle

CMD ["/bin/bash"]
