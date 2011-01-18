include $(GOROOT)/src/Make.inc

PREREQ=$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/schema.a \
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/client.a \
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/camli/jsonsign.a \
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/crypto/openpgp/packet.a \
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/crypto/openpgp/error.a \
	$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/crypto/openpgp/armor.a

TARG=camput
GOFILES=\
	camput.go\
	init.go\

include $(GOROOT)/src/Make.cmd

