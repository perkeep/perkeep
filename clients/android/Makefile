all:
	# to run if you already have a functional android development
	# environment, and you don't need the one in docker.
	./check-environment.pl
	./gradlew assembleRelease

env:
	docker build -t camlistore/android devenv

dockerdev:
	docker run --rm -v $(GOPATH)/src/camlistore.org:/home/gopher/src/camlistore.org -v $(HOME)/.gradle:/home/gopher/.gradle -v $(HOME)/.android:/home/gopher/.android -w /home/gopher/src/camlistore.org/clients/android --name camlidroid -i -t camlistore/android /bin/bash

dockerdebug:
	docker run --rm -v $(GOPATH)/src/camlistore.org:/home/gopher/src/camlistore.org -v $(HOME)/.gradle:/home/gopher/.gradle -v $(HOME)/.android:/home/gopher/.android -w /home/gopher/src/camlistore.org/clients/android --name camlidroid -i -t camlistore/android go run build-in-docker.go

dockerrelease:
	docker run --rm -v $(GOPATH)/src/camlistore.org:/home/gopher/src/camlistore.org -v $(HOME)/.gradle:/home/gopher/.gradle -v $(HOME)/.android:/home/gopher/.android -w /home/gopher/src/camlistore.org/clients/android --name camlidroid -i -t camlistore/android go run build-in-docker.go -release=true

# just for documentation, as make is not actually installed in the docker image.
debug:
	# when within the env dev (i.e. after make dockerdev)
	./gradlew assembleDebug

installdebug:
	adb install -r app/build/outputs/apk/app-debug.apk

install:
	# TODO(bradfitz): step to sign app/build/outputs/apk/app-release-unsigned.apk
	# adb install -r app/build/outputs/apk/app-release.apk

test:
	# TODO(mpl): can't run the instrumentation tests within docker, since
	# can't connect to the phone from in there.
