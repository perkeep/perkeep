include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobref.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobserver.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/schema.a\

TARG=camli/mysqlindexer
GOFILES=\
	mysqlindexer.go\
	enumerate.go\
	stat.go\
	receive.go\

include $(GOROOT)/src/Make.pkg
