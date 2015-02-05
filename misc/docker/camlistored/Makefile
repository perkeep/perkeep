docker: Dockerfile camlistored
	docker build -t camlistored .

.PHONY: camlistored

camlistored:
	(cd ../../../; go run make.go --docker_camlistored)

camlistored.tar.gz: docker
	docker save camlistored | gzip -c > $@

push: camlistored.tar.gz
	gsutil cp $< gs://camlistore-release/docker/
