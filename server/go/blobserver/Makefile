include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver.a

TARG=camlistored
GOFILES=\
	camlistored.go\
	enumerate.go\
	get.go\
	localdisk.go\
	partitions.go\
	preupload.go\
	range.go\
	remove.go\
	temp_testing.go\
	upload.go\
	util.go\

include $(GOROOT)/src/Make.cmd
