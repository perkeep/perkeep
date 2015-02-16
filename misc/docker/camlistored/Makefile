docker: Dockerfile camlistored djpeg
	docker build -t camlistored .

.PHONY: djpeg

djpeg:
	(cd ../djpeg-static/; make djpeg; cp djpeg ../camlistored/)

.PHONY: camlistored

camlistored:
	(cd ../../../; go run make.go --docker_camlistored)

camlistored.tar.gz: docker
	docker save camlistored | gzip -c > $@

push: camlistored.tar.gz
	gsutil cp $< gs://camlistore-release/docker/
