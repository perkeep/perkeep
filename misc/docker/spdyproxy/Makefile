docker: Dockerfile runnginx nginx.conf
	docker build -t camlistore/spdyproxy .

runginx: runginx.go
	GOARCH=amd64 GOOS=linux go build runginx.go

push: docker
	docker push camlistore/spdyproxy
