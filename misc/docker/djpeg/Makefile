docker: Dockerfile
	docker build -t djpeg .

squashed: # TODO(mpl): make it depend on above docker target somehow?
	docker save djpeg | sudo docker-squash -t djpeg-squashed -verbose | docker load
