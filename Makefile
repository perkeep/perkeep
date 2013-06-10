# On OS X with "brew install sqlite3", you need PKG_CONFIG_PATH=/usr/local/Cellar/sqlite/3.7.17/lib/pkgconfig/

# TODO(bradfitz): rename "all" to "raw" and "newall" to "all", once
# make.go is finished.  Then this text will remain and be accurate:
#
# The "raw" target is the old "all" way, using the "go" command
# directly. Assumes that the camlistore root is in
# $GOPATH/src/camlistore.org.
#
# The new "all" way (above) doesn't care where the directory is
# checked out, or whether you even have a GOPATH at all.
all:
	go install --ldflags="-X camlistore.org/pkg/buildinfo.GitInfo "`./misc/gitversion` `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` ./pkg/... ./server/... ./cmd/... ./third_party/...

newall:
	go run make.go

# Workaround Go bug where the $GOPATH/pkg cache doesn't know about tag changes.
# Useful when you accidentally run "make" and then "make presubmit" doesn't work.
# See https://code.google.com/p/go/issues/detail?id=4443
forcefull:
	go install -a --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmit:
	SKIP_DEP_TESTS=1 go test `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui && genfileembed ./server/camlistored/newui && genfileembed ./pkg/server

getclosure:
	perl -e 'require "misc/get_closure.pl"; get_closure_lib(); get_closure_compiler();'

UIDIR = server/camlistored/ui

NEWUIDIR = server/camlistored/newui

clean:
	rm -f $(NEWUIDIR)/all.js $(NEWUIDIR)/all.js.map

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
