fakes3:
	docker build -t camlistore/fakes3 .

upload: fakes3
	docker save camlistore/fakes3 | gzip > fakes3.tar.gz
	gsutil cp fakes3.tar.gz gs://camlistore-docker/

clean:
	rm fakes3.tar.gz
