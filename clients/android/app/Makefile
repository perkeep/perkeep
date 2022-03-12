REPOROOT=$(shell git rev-parse --show-toplevel)
GOBIN=$(shell go env GOPATH)/bin

BINNAME=libpkput.so
LIBDIR=libs

ARMLIB=$(LIBDIR)/armeabi-v7a
ARMLIB64=$(LIBDIR)/arm64-v8a
X86LIB64=$(LIBDIR)/x86_64

ARMPKPUT=$(ARMLIB)/$(BINNAME)
ARMPKPUT64=$(ARMLIB64)/$(BINNAME)
X86PKPUT64=$(X86LIB64)/$(BINNAME)

all: $(ARMPKPUT) $(ARMPKPUT64) $(X86PKPUT64)

clean:
	rm -rf $(LIBDIR)

$(ARMLIB):
	mkdir -p $(ARMLIB)

$(ARMLIB64):
	mkdir -p $(ARMLIB64)

$(X86LIB64):
	mkdir -p $(X86LIB64)

$(ARMPKPUT): $(ARMLIB)
	cd $(REPOROOT) && go run make.go --os=linux --arch=arm --targets=perkeep.org/cmd/pk-put
	cp $(GOBIN)/linux_arm/pk-put $(ARMPKPUT)

$(ARMPKPUT64): $(ARMLIB64)
	cd $(REPOROOT) && go run make.go --os=linux --arch=arm64 --targets=perkeep.org/cmd/pk-put
	cp $(GOBIN)/linux_arm64/pk-put $(ARMPKPUT64)

$(X86PKPUT64): $(X86LIB64)
	cd $(REPOROOT) && go run make.go --os=linux --arch=amd64 --targets=perkeep.org/cmd/pk-put
	cp $(GOBIN)/pk-put $(X86PKPUT64)
