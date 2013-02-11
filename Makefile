all:
	go install --ldflags="-X camlistore.org/pkg/buildinfo.GitInfo "`./misc/gitversion` `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` ./pkg/... ./server/... ./cmd/... ./third_party/...

# Workaround Go bug where the $GOPATH/pkg cache doesn't know about tag changes.
# Useful when you accidentally run "make" and then "make presubmit" doesn't work.
# See https://code.google.com/p/go/issues/detail?id=4443
forcefull:
	go install -a --tags=with_sqlite ./pkg/... ./server/... ./cmd/... ./third_party/...

presubmit:
	SKIP_DEP_TESTS=1 go test `pkg-config --libs sqlite3 1>/dev/null 2>/dev/null && echo "--tags=with_sqlite"` -short ./pkg/... ./server/camlistored ./cmd/... && echo PASS

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui && genfileembed ./pkg/server

getclosure:
	perl -e 'require "misc/get_closure.pl"; get_closure_lib(); get_closure_compiler();'

UIDIR = server/camlistored/ui

NEWUIDIR = server/camlistored/newui

minijs: $(NEWUIDIR)/all.js

$(NEWUIDIR)/all.js: $(NEWUIDIR)/blob_item.js $(NEWUIDIR)/blob_item_container.js $(NEWUIDIR)/create_item.js $(NEWUIDIR)/index.js $(NEWUIDIR)/server_connection.js $(NEWUIDIR)/server_connection.js $(NEWUIDIR)/server_type.js $(NEWUIDIR)/toolbar.js $(UIDIR)/base64.js $(UIDIR)/camli.js $(UIDIR)/Crypto.js $(UIDIR)/SHA1.js
	# This will generate non working code for now, since camli.js, SHA1.js, Crypto.js,
	# and base64.js are not explicitely declared as dependencies.
	tmp/closure-lib/closure/bin/build/closurebuilder.py\
		--root tmp/closure-lib/ \
		--root server/camlistored/ui/ \
		--root server/camlistored/newui/ \
		--namespace="camlistore.IndexPage" \
		--output_mode=compiled \
		--compiler_jar=tmp/closure-compiler/compiler.jar \
		--compiler_flags="--compilation_level=ADVANCED_OPTIMIZATIONS" \
		--compiler_flags="--jscomp_warning=checkTypes" \
		--compiler_flags="--debug" \
		--compiler_flags="--formatting=PRETTY_PRINT" \
	> $(NEWUIDIR)/all.js
