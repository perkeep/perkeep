# Copyright 2017 The Perkeep Authors.

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
RUN curl -O https://dl.google.com/android/repository/sdk-tools-linux-4333796.zip
RUN echo '92ffee5a1d98d856634e8b71132e8a95d96c83a63fde1099be3d86df3106def9  sdk-tools-linux-4333796.zip' | sha256sum -c
RUN unzip sdk-tools-linux-4333796.zip
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager --update
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'platforms;android-27'
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'build-tools;28.0.2'
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'extras;android;m2repository'
RUN echo y | $ANDROID_HOME/tools/bin/sdkmanager 'ndk-bundle'

# Get Go stable release
WORKDIR $GOPHER
RUN curl -O https://storage.googleapis.com/golang/go1.11.linux-amd64.tar.gz
RUN echo 'b3fcf280ff86558e0559e185b601c9eade0fd24c900b4c63cd14d1d38613e499  go1.11.linux-amd64.tar.gz' | sha256sum -c
RUN tar -xzf go1.11.linux-amd64.tar.gz
ENV GOPATH $GOPHER
ENV GOROOT $GOPHER/go
ENV PATH $PATH:$GOROOT/bin:$GOPHER/bin

# Get gomobile
RUN go get -u golang.org/x/mobile/cmd/gomobile
WORKDIR $GOPATH/src/golang.org/x/mobile/cmd/gomobile
RUN git reset --hard 3e0bab5405d63a8f5dd9d9764a24c8e5ac4997fa
RUN go install

CMD ["/bin/bash"]
