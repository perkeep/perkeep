# TODO: delete this whole file and move into dock.go. Even the
# uploading should be direct from Go instead of using gsutil. See how
# golang.org/x/build/... does it and its cmd/upload etc. Also, we can
# use golang.org/x/oauth2/google's DefaultApplicationContext now too
# probably.

camlistored.tar.gz:
	docker save camlistore/server | gzip -c > $@

push: camlistored.tar.gz
	gsutil cp $< gs://camlistore-release/docker/
