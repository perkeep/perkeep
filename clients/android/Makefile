all:
	# to run if you already have a functional android development
	# environment, and you don't need the one in docker.
	./check-environment.pl
	./gradlew assembleRelease

env:
	mkdir -p $(HOME)/.gradle
	mkdir -p $(HOME)/.android
	docker build -t perkeep/android devenv

dockerdev:
	docker run --rm -v $(GOPATH)/src/perkeep.org:/home/gopher/src/perkeep.org -v $(HOME)/.gradle:/home/gopher/.gradle -v $(HOME)/.android:/home/gopher/.android -v $(HOME)/keys:/keys -w /home/gopher/src/perkeep.org/clients/android --name camlidroid -i -t perkeep/android /bin/bash

dockerdebug:
	docker run --rm -v $(GOPATH)/src/perkeep.org:/home/gopher/src/perkeep.org -v $(HOME)/.gradle:/home/gopher/.gradle -v $(HOME)/.android:/home/gopher/.android -w /home/gopher/src/perkeep.org/clients/android --name camlidroid -i -t perkeep/android go run build-in-docker.go

dockerrelease:
	docker run --rm -v $(GOPATH)/src/perkeep.org:/home/gopher/src/perkeep.org -v $(HOME)/.gradle:/home/gopher/.gradle -v $(HOME)/.android:/home/gopher/.android -v $(HOME)/keys:/keys -w /home/gopher/src/perkeep.org/clients/android --name camlidroid -i -t perkeep/android go run build-in-docker.go -release=true

dockersignrelease:
	docker run --rm -v $(GOPATH)/src/perkeep.org:/home/gopher/src/perkeep.org -v $(HOME)/.gradle:/home/gopher/.gradle -v $(HOME)/.android:/home/gopher/.android -v $(HOME)/keys:/keys -w /home/gopher/src/perkeep.org/clients/android --name camlidroid -i -t perkeep/android /home/gopher/android-sdk/build-tools/27.0.0/apksigner sign --ks /keys/perkeep-android.keystore --out ./app/build/outputs/apk/release/app-release.apk ./app/build/outputs/apk/release/app-release-unsigned.apk

# just for documentation, as make is not actually installed in the docker image.
debug:
	# when within the env dev (i.e. after make dockerdev)
	./gradlew assembleDebug

installdebug:
	adb install -r app/build/outputs/apk/debug/app-debug.apk

install:
	adb install -r app/build/outputs/apk/release/app-release.apk

test:
	# TODO(mpl): can't run the instrumentation tests within docker, since
	# can't connect to the phone from in there.
