docker: Dockerfile camlistored # TODO(mpl): add djpeg-squashed dependency somehow?
	docker build -t camlistored .

djpeg:
	(cd ../djpeg; make squashed)

.PHONY: camlistored

camlistored:
	(cd ../../../; go run make.go --docker_camlistored)

camlistored.tar.gz: docker
	docker save camlistored | gzip -c > $@

push: camlistored.tar.gz
	gsutil cp $< gs://camlistore-release/docker/
