include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver/handlers.a\

TARG=camlistored
GOFILES=\
	camlistored.go\
	get.go\
	localdisk.go\
	partitions.go\
	preupload.go\
	range.go\
	temp_testing.go\
	upload.go\
	util.go\

include $(GOROOT)/src/Make.cmd
