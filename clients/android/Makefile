all:
	./check-environment.pl
	ant debug

# Dummy target to make build.pl happy
install:
	./check-environment.pl
	ant debug

env:
	docker build -t camlistore/android devenv

dockerdebug:
	docker run -v $(GOPATH)/src/camlistore.org:/src/camlistore.org camlistore/android /src/camlistore.org/clients/android/build-in-docker.pl debug

dockerrelease:
	docker run -i -t -v $(GOPATH)/src/camlistore.org:/src/camlistore.org -v $(HOME)/keys/android-camlistore:/keys camlistore/android /src/camlistore.org/clients/android/build-in-docker.pl release
