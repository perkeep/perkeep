docker: Dockerfile camlistored
	docker build -t camlistore/camlistored .

.PHONY: camlistored

camlistored:
	(cd ../../../; go run make.go --docker_camlistored)

push: docker
	docker push camlistore/camlistored
