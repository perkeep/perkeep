container:
	docker build -t camlistore/demoblobserver .

upload:
	go get golang.org/x/build/cmd/upload
	docker save camlistore/demoblobserver | gzip | upload --public  --project=camlistore-website camlistore-website-resource/docker-demoblobserver.tar.gz

