Once:
$ gcloud auth configure-docker

On any change:

$ docker build --tag=gcr.io/perkeep-containers/thumbnail:latest .
$ docker push gcr.io/perkeep-containers/thumbnail:latest

TODO: version it probably.
