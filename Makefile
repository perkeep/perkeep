# The normal way to build Camlistore is just "go run make.go", which
# works everywhere, even on systems without Make.  The rest of this
# Makefile is mostly historical and should hopefully disappear over
# time.
all:
	go run make.go

# On OS X with "brew install sqlite3", you need PKG_CONFIG_PATH=/usr/local/Cellar/sqlite/3.7.17/lib/pkgconfig/
full:
	go install --ldflags="-X camlistore.org/pkg/buildinfo.GitInfo "`./misc/gitversion` `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` ./pkg/... ./server/... ./cmd/... ./third_party/...


# Workaround Go bug where the $GOPATH/pkg cache doesn't know about tag changes.
# Useful when you accidentally run "make" and then "make presubmit" doesn't work.
# See https://code.google.com/p/go/issues/detail?id=4443
forcefull:
	go install -a --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmit:
	SKIP_DEP_TESTS=1 go test `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` -short ./pkg/... ./server/camlistored ./server/appengine ./cmd/... && echo PASS

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui && genfileembed ./pkg/server

UIDIR = server/camlistored/ui

NEWUIDIR = server/camlistored/newui

clean:
	rm -f $(NEWUIDIR)/all.js $(NEWUIDIR)/all.js.map

genclosuredeps: $(UIDIR)/deps.js

$(UIDIR)/deps.js: $(UIDIR)/blobinfo.js $(UIDIR)/blob_item.js $(UIDIR)/blob_item_container.js $(UIDIR)/create_item.js $(UIDIR)/filetree.js $(UIDIR)/index.js $(UIDIR)/permanode.js $(UIDIR)/pics.js $(UIDIR)/server_connection.js $(UIDIR)/server_connection.js $(UIDIR)/search.js $(UIDIR)/server_type.js $(UIDIR)/sigdebug.js $(UIDIR)/toolbar.js $(UIDIR)/base64.js $(UIDIR)/Crypto.js $(UIDIR)/SHA1.js
	go install ./pkg/misc/closure/genclosuredeps && genclosuredeps ./server/camlistored/ui \
	> $(UIDIR)/deps.js

#TODO(mpl): make it output somewhere else
minijs: $(NEWUIDIR)/all.js

$(NEWUIDIR)/all.js: $(NEWUIDIR)/blobinfo.js $(NEWUIDIR)/blob_item.js $(NEWUIDIR)/blob_item_container.js $(NEWUIDIR)/create_item.js $(NEWUIDIR)/filetree.js $(NEWUIDIR)/index.js $(NEWUIDIR)/permanode.js $(NEWUIDIR)/pics.js $(NEWUIDIR)/server_connection.js $(NEWUIDIR)/server_connection.js $(NEWUIDIR)/search.js $(NEWUIDIR)/server_type.js $(NEWUIDIR)/sigdebug.js $(NEWUIDIR)/toolbar.js $(NEWUIDIR)/base64.js $(NEWUIDIR)/Crypto.js $(NEWUIDIR)/SHA1.js
	tmp/closure-lib/closure/bin/build/closurebuilder.py\
		--root tmp/closure-lib/ \
		--root server/camlistored/newui/ \
		--namespace="camlistore.BlobPage" \
		--namespace="camlistore.DebugPage" \
		--namespace="camlistore.FiletreePage" \
		--namespace="camlistore.GalleryPage" \
		--namespace="camlistore.IndexPage" \
		--namespace="camlistore.PermanodePage" \
		--namespace="camlistore.SearchPage" \
		--output_mode=compiled \
		--compiler_jar=tmp/closure-compiler/compiler.jar \
		--compiler_flags="--compilation_level=SIMPLE_OPTIMIZATIONS" \
		--compiler_flags="--jscomp_warning=checkTypes" \
		--compiler_flags="--debug" \
		--compiler_flags="--formatting=PRETTY_PRINT" \
		--compiler_flags="--create_source_map=$(NEWUIDIR)/all.js.map" \
	> $(NEWUIDIR)/all.js
