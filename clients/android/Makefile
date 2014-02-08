all:
	./check-environment.pl
	ant debug

# Dummy target to make build.pl happy
install:
	./check-environment.pl
	ant debug

dockerdebug:
	docker build -t camlistore/android devenv
	docker run -v $(GOPATH)/src/camlistore.org/clients/android:/src/camlistore -w /src/camlistore camlistore/android ant -Dsdk.dir=/usr/local/android-sdk-linux debug

dockerrelease:
	docker build -t camlistore/android devenv
	docker run -i -t -v $(GOPATH)/src/camlistore.org/clients/android:/src/camlistore -v $(HOME)/keys/android-camlistore:/keys -w /src/camlistore camlistore/android ant -Dsdk.dir=/usr/local/android-sdk-linux -Dkey.store=/keys/android-camlistore.keystore -Dkey.alias=camkey release
