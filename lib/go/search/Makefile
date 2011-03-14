include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/blobref.a\
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/httputil.a\

TARG=camli/search
GOFILES=\
	search.go\
	handler.go\

include $(GOROOT)/src/Make.pkg
