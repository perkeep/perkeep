systemd-docker.tar.gz: systemd-docker
	(cd systemd-docker && GOPATH=$(shell pwd)/systemd-docker/Godeps/_workspace go build -o image/systemd-docker)
	(cd systemd-docker/image && docker build -t camlistore/systemd-docker .)
	docker save camlistore/systemd-docker | gzip > $@

systemd-docker:
	git clone https://github.com/ibuildthecloud/systemd-docker
	# Commit 9b02cf96ec bumps the docker version to 1.12.
	(cd systemd-docker && git reset --hard 9b02cf96ec874bae449231901631b0a4b3d546c0)

clean:
	rm -rf systemd-docker
	rm systemd-docker.tar.gz
