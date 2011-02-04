include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver/handlers.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver/localdisk.a\

TARG=camlistored
GOFILES=\
	camlistored.go\
	partitions.go\

include $(GOROOT)/src/Make.cmd
