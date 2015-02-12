djpeg: docker
	docker run --rm -it -v $(CURDIR):/out djpeg-static cp /src/libjpeg-turbo-1.4.0/djpeg /out/djpeg

docker: Dockerfile
	docker build -t djpeg-static .
