docker: Dockerfile camlistored
	docker build -t camlistore/camlistored .

camlistored:
	CGO_ENABLED=0 GOOS=linux go build -a -tags netgo -ldflags '-w' -o camlistored camlistore.org/server/camlistored

push: docker
	docker push camlistore/camlistored
